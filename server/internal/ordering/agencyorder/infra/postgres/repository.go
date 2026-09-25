package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct{ database *sharedpostgres.Database }

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

// RetireExpiredSessionCreationKey는 만료된 세션의 creation key를 세션 고유
// 값으로 치환해 (user, key) 유니크 제약에서 회수한다. state 조건을 SQL에
// 넣어 만료가 아닌 세션의 key를 절대 회수하지 못하게 한다.
func (r *Repository) RetireExpiredSessionCreationKey(ctx context.Context, userID, sessionID string) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE agency_order_sheet_sessions
		SET creation_key_hash='retired:'||id, updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND state='EXPIRED'
	`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("retire expired OrderSheet key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("retire expired OrderSheet key: %w", err)
	}
	if affected != 1 {
		return agencydomain.ErrStateInvalid
	}
	return nil
}

func (r *Repository) FindSessionByCreationKey(ctx context.Context, userID, keyHash string) (agencydomain.OrderSheetSession, string, bool, error) {
	var payload []byte
	var requestHash string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT snapshot, creation_request_hash
		FROM agency_order_sheet_sessions
		WHERE user_id=$1 AND creation_key_hash=$2
	`, userID, keyHash).Scan(&payload, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.OrderSheetSession{}, "", false, nil
	}
	if err != nil {
		return agencydomain.OrderSheetSession{}, "", false, fmt.Errorf("find OrderSheet by key: %w", err)
	}
	session, err := decodeSession(payload, userID)
	return session, requestHash, true, err
}

func (r *Repository) CreateSession(ctx context.Context, session agencydomain.OrderSheetSession, keyHash, requestHash string) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,block_reason,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$12)
	`, session.ID, session.UserID, session.SourceCart.CartID, session.SourceCart.CartVersion,
		session.SourceCart.SnapshotHash, session.State, session.BlockReason, session.Version,
		keyHash, requestHash, payload, session.CreatedAt, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create OrderSheet: %w", err)
	}
	return nil
}

func (r *Repository) GetSession(ctx context.Context, userID, sessionID string) (agencydomain.OrderSheetSession, error) {
	var payload []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT snapshot FROM agency_order_sheet_sessions WHERE id=$1 AND user_id=$2
	`, sessionID, userID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.OrderSheetSession{}, agencydomain.ErrNotFound
	}
	if err != nil {
		return agencydomain.OrderSheetSession{}, fmt.Errorf("get OrderSheet: %w", err)
	}
	return decodeSession(payload, userID)
}

func (r *Repository) SaveSession(ctx context.Context, session agencydomain.OrderSheetSession, expectedVersion int64) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE agency_order_sheet_sessions
		SET state=$1, block_reason=NULLIF($2,''), version=$3, snapshot=$4, updated_at=$5
		WHERE id=$6 AND user_id=$7 AND version=$8 AND state <> 'CONSUMED'
	`, session.State, session.BlockReason, session.Version, payload, time.Now().UTC(),
		session.ID, session.UserID, expectedVersion)
	if err != nil {
		return fmt.Errorf("save OrderSheet: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return agencydomain.ErrVersionConflict
	}
	return nil
}

func (r *Repository) IssueAtomic(ctx context.Context, session agencydomain.OrderSheetSession, expectedVersion int64,
	order agencydomain.AgencyOrder, instruction agencydomain.PaymentInstruction) error {
	if err := order.ValidateExecutionProfile(); err != nil {
		return err
	}
	if err := instruction.ValidateExecutionProfile(order); err != nil {
		return err
	}
	if err := order.ProcurementAuthorization.ValidateForOrder(order); err != nil {
		return err
	}
	sessionPayload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	orderPayload, err := json.Marshal(order)
	if err != nil {
		return err
	}
	authorizationPayload, err := json.Marshal(order.ProcurementAuthorization)
	if err != nil {
		return err
	}
	instructionPayload, err := json.Marshal(instruction)
	if err != nil {
		return err
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		result, updateErr := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE agency_order_sheet_sessions
			SET state='CONSUMED',version=$1,snapshot=$2,updated_at=$3,consumed_at=$3,
				cleanup_state='PENDING',cleanup_available_at=$3,
				cleanup_lease_until=NULL,cleanup_last_error_code=NULL
			WHERE id=$4 AND user_id=$5 AND version=$6 AND state='READY'
		`, session.Version, sessionPayload, order.IssuedAt, session.ID, session.UserID, expectedVersion)
		if updateErr != nil {
			return updateErr
		}
		updated, updateErr := result.RowsAffected()
		if updateErr != nil {
			return updateErr
		}
		if updated != 1 {
			return agencydomain.ErrVersionConflict
		}
		_, insertErr := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_orders(
				id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
				source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,idempotency_key_hash,
				status,customer_payable_minor,currency,payment_rail,provider_environment,
				asset,economic_effect,merchant_execution_mode,execution_profile_hash,
				snapshot,issued_at,expires_at
			) VALUES(
				$1,$2,$3,$4,$5,$6,$7,$8,$9,'ISSUED',$10,'USD',$11,$12,$13,$14,$15,$16,
				$17,$18,$19
			)
		`, order.ID, order.UserID, session.ID, order.SourceCart.CartID, order.SourceCart.CartVersion,
			order.SourceCart.SnapshotHash, order.ShippingAddress.SnapshotRef, order.SnapshotHash,
			order.IssuanceEvidence.IdempotencyKeyHash, order.CustomerPayableTotal.AmountMinor,
			order.ExecutionProfile.PaymentRail, order.ExecutionProfile.ProviderEnvironment,
			order.ExecutionProfile.Asset, order.ExecutionProfile.EconomicEffect,
			order.ExecutionProfile.MerchantExecutionMode, order.ExecutionProfileHash,
			orderPayload, order.IssuedAt, order.ExpiresAt)
		if insertErr != nil {
			return fmt.Errorf("insert AgencyOrder: %w", insertErr)
		}
		_, insertErr = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_procurement_authorizations(
				agency_order_id,user_id,order_sheet_session_id,authorization_kind,
				authorization_hash,execution_profile_hash,source_cart_snapshot_hash,
				displayed_snapshot_hash,order_snapshot_hash,locale,copy_version,
				accepted_at,payload,created_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$12)
		`, order.ID, order.UserID, session.ID, order.ProcurementAuthorization.Kind,
			order.ProcurementAuthorization.AuthorizationHash,
			order.ProcurementAuthorization.ExecutionProfileHash,
			order.ProcurementAuthorization.SourceCartSnapshotHash,
			order.ProcurementAuthorization.DisplayedSnapshotHash,
			order.SnapshotHash,
			order.ProcurementAuthorization.CustomerApproval.Locale,
			order.ProcurementAuthorization.CustomerApproval.CopyVersion,
			order.ProcurementAuthorization.AcceptedAt, authorizationPayload)
		if insertErr != nil {
			return fmt.Errorf("insert ProcurementAuthorization: %w", insertErr)
		}
		// process 행 생성은 OrderProcessor가 order.issued 이벤트 소비로
		// 수행한다(ADR-0056 §1 — 발행 transaction의 직접 INSERT 폐지).
		// Procurement creates operational MerchantOrders after customer funding
		// is ready. Issuance fixes one economic allocation per checkout now;
		// capture, cancel and refund consume this row without unit math.
		allocations, allocationErr := agencydomain.MerchantOrderAllocations(order)
		if allocationErr != nil {
			return allocationErr
		}
		for _, allocation := range allocations {
			if _, insertErr = r.database.Queryer(tx).ExecContext(tx, `
				INSERT INTO agency_order_mo_allocations(
					id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
					pass_through_minor,fee_variable_minor,fee_fixed_minor,
					fee_total_minor,customer_gross_minor,currency,
					fee_policy_version,allocation_hash,execution_profile_hash,created_at
				) VALUES(
					md5(($1::uuid)::text||':mo-allocation:'||($2::integer)::text)::uuid,
					$1,$2,$3,$4,$5,$6,$7,$8,$9,'USD',$10,$11,$12,$13
				)
			`, order.ID, allocation.CheckoutOrdinal, allocation.MerchantID,
				allocation.ShopDomain, allocation.PassThroughAmount.AmountMinor,
				allocation.FeeVariableAmount.AmountMinor,
				allocation.FeeFixedAmount.AmountMinor,
				allocation.FeeTotalAmount.AmountMinor,
				allocation.CustomerGrossAmount.AmountMinor,
				allocation.FeePolicyVersion, allocation.AllocationHash,
				allocation.ExecutionProfileHash, order.IssuedAt); insertErr != nil {
				return fmt.Errorf("insert AgencyOrder MO allocation: %w", insertErr)
			}
		}
		_, insertErr = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_payment_instructions(
				id,agency_order_id,user_id,agency_order_snapshot_hash,amount_minor,currency,
				rail,provider_environment,asset,economic_effect,merchant_execution_mode,
				execution_profile_hash,state,payload,expires_at,created_at
			) VALUES($1,$2,$3,$4,$5,'USD',$6,$7,$8,$9,$10,$11,'PENDING',$12,$13,$14)
		`, instruction.ID, order.ID, order.UserID, order.SnapshotHash,
			instruction.CustomerPayableTotal.AmountMinor,
			order.ExecutionProfile.PaymentRail, order.ExecutionProfile.ProviderEnvironment,
			order.ExecutionProfile.Asset, order.ExecutionProfile.EconomicEffect,
			order.ExecutionProfile.MerchantExecutionMode, order.ExecutionProfileHash,
			instructionPayload, instruction.ExpiresAt, instruction.CreatedAt)
		if insertErr != nil {
			return fmt.Errorf("insert PaymentInstruction: %w", insertErr)
		}
		// PaymentInstructionIssued outbox는 order.issued 이벤트가 대체했다
		// (ADR-0056 컷오버 B — 소비자 없는 write-only 잔재 폐기).
		_, insertErr = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_audits(
				agency_order_id,user_id,action,order_snapshot_hash,displayed_snapshot_hash,
				disclosure_version,idempotency_key_hash,created_at
			) VALUES($1,$2,'ISSUED',$3,$4,$5,$6,$7)
		`, order.ID, order.UserID, order.SnapshotHash,
			order.IssuanceEvidence.DisplayedSnapshotHash, order.IssuanceEvidence.DisclosureVersion,
			order.IssuanceEvidence.IdempotencyKeyHash, order.IssuedAt)
		if insertErr != nil {
			return insertErr
		}
		return emitOrderIssued(tx, r.database.Queryer(tx),
			order.ID, order.UserID, order.IssuedAt, order.IssuedAt)
	})
}

func (r *Repository) FindOrderByIdempotencyKey(ctx context.Context, userID, keyHash string) (agencydomain.AgencyOrder, bool, error) {
	var payload []byte
	var profile agencydomain.OrderExecutionProfile
	var profileHash string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT snapshot,payment_rail,provider_environment,asset,economic_effect,
		       merchant_execution_mode,execution_profile_hash
		FROM agency_orders WHERE user_id=$1 AND idempotency_key_hash=$2
	`, userID, keyHash).Scan(&payload, &profile.PaymentRail, &profile.ProviderEnvironment,
		&profile.Asset, &profile.EconomicEffect, &profile.MerchantExecutionMode, &profileHash)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.AgencyOrder{}, false, nil
	}
	if err != nil {
		return agencydomain.AgencyOrder{}, false, err
	}
	var order agencydomain.AgencyOrder
	if err := json.Unmarshal(payload, &order); err != nil {
		return agencydomain.AgencyOrder{}, false, err
	}
	order.ExecutionProfile = profile
	order.ExecutionProfileHash = profileHash
	return order, true, nil
}

func (r *Repository) FindOrderBySessionID(ctx context.Context, userID, sessionID string) (agencydomain.AgencyOrder, bool, error) {
	var payload []byte
	var profile agencydomain.OrderExecutionProfile
	var profileHash string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT snapshot,payment_rail,provider_environment,asset,economic_effect,
		       merchant_execution_mode,execution_profile_hash
		FROM agency_orders WHERE user_id=$1 AND order_sheet_session_id=$2
	`, userID, sessionID).Scan(&payload, &profile.PaymentRail, &profile.ProviderEnvironment,
		&profile.Asset, &profile.EconomicEffect, &profile.MerchantExecutionMode, &profileHash)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.AgencyOrder{}, false, nil
	}
	if err != nil {
		return agencydomain.AgencyOrder{}, false, fmt.Errorf("find AgencyOrder by OrderSheet: %w", err)
	}
	var order agencydomain.AgencyOrder
	if err := json.Unmarshal(payload, &order); err != nil {
		return agencydomain.AgencyOrder{}, false, err
	}
	order.ExecutionProfile = profile
	order.ExecutionProfileHash = profileHash
	return order, true, nil
}

func (r *Repository) GetOrder(ctx context.Context, userID, orderID string) (agencydomain.AgencyOrder, agencydomain.PaymentInstruction, error) {
	var orderPayload, instructionPayload []byte
	var profile agencydomain.OrderExecutionProfile
	var profileHash, instructionProfileHash string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT o.snapshot, i.payload,
		       o.payment_rail,o.provider_environment,o.asset,o.economic_effect,
		       o.merchant_execution_mode,o.execution_profile_hash,
		       i.execution_profile_hash
		FROM agency_orders o
		JOIN agency_order_payment_instructions i ON i.agency_order_id=o.id
		WHERE o.id=$1 AND o.user_id=$2
	`, orderID, userID).Scan(&orderPayload, &instructionPayload,
		&profile.PaymentRail, &profile.ProviderEnvironment, &profile.Asset,
		&profile.EconomicEffect, &profile.MerchantExecutionMode, &profileHash,
		&instructionProfileHash)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, agencydomain.ErrNotFound
	}
	if err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	}
	var order agencydomain.AgencyOrder
	var instruction agencydomain.PaymentInstruction
	if err := json.Unmarshal(orderPayload, &order); err != nil {
		return order, instruction, err
	}
	if err := json.Unmarshal(instructionPayload, &instruction); err != nil {
		return order, instruction, err
	}
	order.ExecutionProfile = profile
	order.ExecutionProfileHash = profileHash
	instruction.ExecutionProfileHash = instructionProfileHash
	return order, instruction, nil
}

func decodeSession(payload []byte, userID string) (agencydomain.OrderSheetSession, error) {
	var session agencydomain.OrderSheetSession
	if err := json.Unmarshal(payload, &session); err != nil {
		return session, fmt.Errorf("decode OrderSheet: %w", err)
	}
	session.UserID = userID
	return session, nil
}

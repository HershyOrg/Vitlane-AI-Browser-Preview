package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	paymentpostgres "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

type storedAuthorizationPayload struct {
	Authorization settlementdomain.PaymentAuthorization `json:"authorization"`
	Domain        settlementdomain.AuthorizationDomain  `json:"domain"`
	Signature     string                                `json:"signature"`
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) GetAuthorizationContext(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementapp.AuthorizationContext, error) {
	value, err := r.getAgencyOrderAuthorizationContext(ctx, userID, agencyOrderID)
	if errors.Is(err, sql.ErrNoRows) {
		return settlementapp.AuthorizationContext{}, settlementdomain.ErrAuthorizationInvalid
	}
	if err != nil {
		return settlementapp.AuthorizationContext{}, fmt.Errorf("load AgencyOrder authorization context: %w", err)
	}
	return value, nil
}

func (r *Repository) getAgencyOrderAuthorizationContext(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementapp.AuthorizationContext, error) {
	var value settlementapp.AuthorizationContext
	// consent의 amount_base_units는 소비자 총액(pass-through + fee)이다.
	// pass-through는 발행 시 고정된 주문 snapshot에서 읽는다(ADR-0050).
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT o.id, o.snapshot_hash, o.snapshot_hash, 'USER_APPROVED', c.merchant_id,
		       o.snapshot_hash, o.snapshot_hash, i.expires_at,
		       i.amount_minor::text, i.amount_minor::text,
		       i.currency, i.currency, c.amount_base_units::text,
		       ((o.snapshot#>>'{passThroughTotal,amountMinor}')::numeric*10000)::text,
		       (c.amount_base_units
		        - (o.snapshot#>>'{passThroughTotal,amountMinor}')::numeric*10000)::text,
		       c.token_address, c.settlement_address, c.chain_id,
		       c.merchant_registry_version, c.fee_bps, c.fee_recipient,
		       c.principal_recipient,
		       m.principal_recipient, m.registry_version, m.active AND m.payment_enabled,
		       o.snapshot_hash, c.wallet_id, c.wallet_ownership_proof_id,
		       w.account_id, w.address, w.chain_id, proof.method,
		       proof.message_hash, proof.verified_at, proof.valid_until,
		       policy.policy_id, policy.policy_version
		FROM agency_orders o
		JOIN agency_order_payment_instructions i ON i.agency_order_id=o.id
		JOIN agency_order_payment_consents c ON c.agency_order_id=o.id
		JOIN merchant_registry_entries m ON m.merchant_id=c.merchant_id
		JOIN wallets w ON w.id=c.wallet_id AND w.user_id=o.user_id
		JOIN wallet_ownership_proofs proof
		  ON proof.id=c.wallet_ownership_proof_id
		 AND proof.wallet_id=w.id AND proof.user_id=o.user_id
		JOIN user_policy_acceptances policy
		  ON policy.user_id=o.user_id
		 AND policy.policy_id='PHASE5_TEST_SETTLEMENT'
		 AND policy.policy_version='2026-07-24'
		WHERE o.id=$1 AND o.user_id=$2 AND o.status='ISSUED'
		  AND i.state IN ('PENDING','CONSUMED')
		  AND w.registration_status='REGISTERED'
		  AND w.current_ownership_proof_id=proof.id
		  AND proof.revoked_at IS NULL
	`, agencyOrderID, userID).Scan(
		&value.AgencyOrderID, &value.AgencyOrderHash, &value.ConsentAgencyOrderHash,
		&value.AgencyOrderStatus, &value.MerchantID,
		&value.InstructionHash, &value.ConsentInstructionHash, &value.InstructionExpiresAt,
		&value.ConsentAmount, &value.InstructionAmount,
		&value.ConsentCurrency, &value.InstructionCurrency, &value.AmountBaseUnits,
		&value.PassThroughBaseUnits, &value.FeeBaseUnits,
		&value.TokenAddress, &value.SettlementAddress, &value.ChainID,
		&value.MerchantRegistryVersion, &value.FeeBps, &value.FeeRecipient,
		&value.PrincipalRecipient,
		&value.CurrentMerchantPrincipalRecipient,
		&value.CurrentMerchantRegistryVersion,
		&value.CurrentMerchantActive,
		&value.ConsentHash, &value.PayerWalletID,
		&value.WalletOwnershipProofID, &value.PayerAccountID,
		&value.PayerAddress, &value.PayerChainID, &value.OwnershipProofMethod,
		&value.OwnershipMessageHash, &value.OwnershipVerifiedAt,
		&value.OwnershipValidUntil,
		&value.TestPolicyID, &value.TestPolicyVersion,
	)
	return value, err
}

func (r *Repository) GetAuthorization(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.AuthorizationRecord, error) {
	var record settlementdomain.AuthorizationRecord
	var payload []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT sa.id, sa.agency_order_id, sa.authorization_payload,
		       sa.signer_address, sa.typed_data_hash, sa.created_at
		FROM settlement_authorizations sa
		JOIN agency_orders o ON o.id=sa.agency_order_id
		WHERE sa.agency_order_id=$1 AND o.user_id=$2
	`, agencyOrderID, userID).Scan(
		&record.ID, &record.AgencyOrderID, &payload,
		&record.Signer, &record.TypedDataHash,
		&record.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationNotFound
	}
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, fmt.Errorf("get authorization: %w", err)
	}
	var stored storedAuthorizationPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return settlementdomain.AuthorizationRecord{}, fmt.Errorf("decode authorization: %w", err)
	}
	record.Authorization = stored.Authorization
	record.Domain = stored.Domain
	record.Signature = stored.Signature
	return record, nil
}

func (r *Repository) CreateAuthorization(
	ctx context.Context,
	record settlementdomain.AuthorizationRecord,
	payment settlementdomain.Payment,
) error {
	payload, err := json.Marshal(storedAuthorizationPayload{
		Authorization: record.Authorization,
		Domain:        record.Domain,
		Signature:     record.Signature,
	})
	if err != nil {
		return err
	}
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		_, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_authorizations(
				id, agency_order_id, order_hash, payer, nonce, pay_deadline,
				refund_after, signer_address, typed_data_hash, authorization_payload, created_at
			) VALUES ($1,$2,$3,$4,$5,to_timestamp($6),to_timestamp($7),$8,$9,$10,$11)
		`, record.ID, record.AgencyOrderID, record.Authorization.OrderHash,
			record.Authorization.Payer, record.Authorization.Nonce,
			record.Authorization.PayDeadline, record.Authorization.RefundAfter,
			record.Signer, record.TypedDataHash, payload, record.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert settlement authorization: %w", err)
		}
		_, err = r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_payments(
				id, agency_order_id, order_hash, chain_id, settlement_address, payer,
				amount_base_units, state, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, payment.ID, payment.AgencyOrderID, payment.OrderHash, payment.ChainID,
			payment.Settlement, payment.Payer, payment.AmountBaseUnits,
			payment.State, payment.CreatedAt, payment.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert settlement payment: %w", err)
		}
		return nil
	})
}

func (r *Repository) RecordSubmittedTransaction(
	ctx context.Context,
	userID, agencyOrderID, txHash string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var paymentID string
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			UPDATE settlement_payments sp
			SET pay_tx_hash=$1, state='PAYMENT_SUBMITTED',
			    last_reason_code=NULL, observation_exhausted_at=NULL, updated_at=$2
			FROM settlement_authorizations sa
			WHERE sp.agency_order_id=$3
			  AND sa.agency_order_id=sp.agency_order_id
			  AND EXISTS (
			    SELECT 1 FROM agency_orders o
			    JOIN agency_order_payment_instructions i ON i.agency_order_id=o.id
			    WHERE o.id=sp.agency_order_id AND o.user_id=$4
			      AND o.status='ISSUED' AND i.state='CONSUMED'
			  )
			  AND sa.pay_deadline > $2
			  AND sp.state IN ('AUTHORIZED','AWAITING_ALLOWANCE','SUBMISSION_UNKNOWN')
			RETURNING sp.id
		`, txHash, now, agencyOrderID, userID).Scan(&paymentID)
		if errors.Is(err, sql.ErrNoRows) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if err != nil {
			return fmt.Errorf("record pay tx: %w", err)
		}
		if _, err = r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO chain_transactions(
				chain_id, tx_hash, settlement_payment_id, purpose, state,
				next_observation_at, submitted_at, updated_at
			)
			SELECT chain_id, $1, id, 'PAY', 'SUBMITTED', $2, $2, $2
			FROM settlement_payments WHERE id=$3
			ON CONFLICT (chain_id, tx_hash) DO NOTHING
		`, txHash, now, paymentID); err != nil {
			return err
		}
		return emitSettlementEvent(txContext, r.database.Queryer(txContext), paymentID, now)
	})
}

func (r *Repository) RecordWalletTransaction(
	ctx context.Context,
	userID, agencyOrderID, purpose, txHash string,
	now time.Time,
) error {
	column := "claim_tx_hash"
	if purpose == "APPROVE" {
		column = "approve_tx_hash"
	}
	query := fmt.Sprintf(`
		UPDATE settlement_payments sp SET %s=$1, updated_at=$2
		WHERE sp.agency_order_id=$3
		  AND EXISTS (SELECT 1 FROM agency_orders o
		              WHERE o.id=sp.agency_order_id AND o.user_id=$4)
		  AND (%s IS NULL OR %s=$1)
		  AND sp.state IN ('AUTHORIZED','AWAITING_ALLOWANCE','PAYMENT_SUBMITTED','SAFE','FINALIZED')
	`, column, column, column)
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var paymentID string
		var chainID uint64
		err := r.database.Queryer(txContext).QueryRowContext(
			txContext, query+" RETURNING sp.id, sp.chain_id",
			txHash, now, agencyOrderID, userID,
		).Scan(&paymentID, &chainID)
		if errors.Is(err, sql.ErrNoRows) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if err != nil {
			return fmt.Errorf("record %s tx: %w", purpose, err)
		}
		_, err = r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO chain_transactions(
				chain_id, tx_hash, settlement_payment_id, purpose, state,
				next_observation_at, submitted_at, updated_at
			) VALUES ($1,$2,$3,$4,'SUBMITTED',$5,$5,$5)
			ON CONFLICT (chain_id, tx_hash) DO NOTHING
		`, chainID, txHash, paymentID, purpose, now)
		return err
	})
}

func (r *Repository) GetRefundIntent(
	ctx context.Context,
	userID, agencyOrderID string,
	now time.Time,
) (settlementdomain.RefundIntent, error) {
	var intent settlementdomain.RefundIntent
	var state settlementdomain.PaymentStatus
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT sp.agency_order_id, sp.chain_id, sp.order_hash,
		       sp.settlement_address, sp.payer,
		       sp.amount_base_units::text,
		       COALESCE(sa.authorization_payload#>>'{authorization,passThroughAmount}',''),
		       COALESCE(sa.authorization_payload#>>'{authorization,feeAmount}',''),
		       sa.refund_after, sp.state
		FROM settlement_payments sp
		JOIN agency_orders o ON o.id=sp.agency_order_id
		JOIN settlement_authorizations sa
		  ON sa.agency_order_id=sp.agency_order_id
		WHERE sp.agency_order_id=$1 AND o.user_id=$2
	`, agencyOrderID, userID).Scan(
		&intent.AgencyOrderID, &intent.ChainID, &intent.OrderHash,
		&intent.Settlement, &intent.Payer, &intent.AmountBaseUnits,
		&intent.PassThroughBaseUnits, &intent.FeeBaseUnits,
		&intent.RefundAfter, &state,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return settlementdomain.RefundIntent{}, settlementdomain.ErrPaymentNotFound
	}
	if err != nil {
		return settlementdomain.RefundIntent{}, fmt.Errorf("get refund intent: %w", err)
	}
	if state != settlementdomain.PaymentFinalized || now.Before(intent.RefundAfter) {
		return settlementdomain.RefundIntent{}, settlementdomain.ErrPaymentStateInvalid
	}
	intent.Eligible = true
	return intent, nil
}

func (r *Repository) RecordRefundTransaction(
	ctx context.Context,
	userID, agencyOrderID, txHash string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var paymentID string
		var chainID uint64
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			UPDATE settlement_payments sp
			SET refund_tx_hash=$1, state='REFUND_PENDING', updated_at=$2
			FROM settlement_authorizations sa
			WHERE sa.agency_order_id=sp.agency_order_id
			  AND sp.agency_order_id=$3
			  AND EXISTS (SELECT 1 FROM agency_orders o
			              WHERE o.id=sp.agency_order_id AND o.user_id=$4)
			  AND sp.state='FINALIZED'
			  AND sp.complete_tx_hash IS NULL AND sa.refund_after <= $2
			RETURNING sp.id, sp.chain_id
		`, txHash, now, agencyOrderID, userID).Scan(&paymentID, &chainID)
		if errors.Is(err, sql.ErrNoRows) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if err != nil {
			return fmt.Errorf("record refund tx: %w", err)
		}
		_, err = r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO chain_transactions(
				chain_id, tx_hash, settlement_payment_id, purpose, state,
				next_observation_at, submitted_at, updated_at
			) VALUES ($1,$2,$3,'REFUND','SUBMITTED',$4,$4,$4)
		`, chainID, txHash, paymentID, now)
		if err != nil {
			return err
		}
		// stage 전이는 Manager가 settlement REFUND_PENDING 이벤트 소비로
		// 결정한다(ADR-0056 — 종전 process 직접 UPDATE 폐지).
		return emitSettlementEvent(txContext, r.database.Queryer(txContext), paymentID, now)
	})
}

func (r *Repository) GetPayment(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.Payment, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT sp.id, sp.agency_order_id, sp.order_hash, sp.chain_id,
		       sp.settlement_address, sp.payer, sp.amount_base_units::text,
		       COALESCE(sp.claim_tx_hash,''), COALESCE(sp.approve_tx_hash,''),
		       COALESCE(sp.pay_tx_hash,''), COALESCE(sp.complete_tx_hash,''),
		       COALESCE(sp.refund_tx_hash,''), sp.state, sp.safe_block,
		       sp.finalized_block, COALESCE(sp.last_reason_code,''),
		       sp.observation_exhausted_at, sp.created_at, sp.updated_at
		FROM settlement_payments sp
		JOIN agency_orders o ON o.id=sp.agency_order_id
		WHERE sp.agency_order_id=$1 AND o.user_id=$2
	`, agencyOrderID, userID)
	return scanPayment(row)
}

func scanPayment(row sharedpostgres.Row) (settlementdomain.Payment, error) {
	var payment settlementdomain.Payment
	err := row.Scan(
		&payment.ID, &payment.AgencyOrderID, &payment.OrderHash, &payment.ChainID,
		&payment.Settlement, &payment.Payer, &payment.AmountBaseUnits,
		&payment.ClaimTxHash, &payment.ApproveTxHash,
		&payment.PayTxHash, &payment.CompleteTxHash, &payment.RefundTxHash,
		&payment.State, &payment.SafeBlock, &payment.FinalizedBlock,
		&payment.LastReasonCode, &payment.ObservationExhaustedAt,
		&payment.CreatedAt, &payment.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return settlementdomain.Payment{}, settlementdomain.ErrPaymentNotFound
	}
	if err != nil {
		return settlementdomain.Payment{}, fmt.Errorf("scan settlement payment: %w", err)
	}
	return payment, nil
}

func (r *Repository) ListReconcileItems(
	ctx context.Context,
	limit int,
	now time.Time,
) ([]settlementapp.ReconcileItem, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT sp.id, sp.agency_order_id, sp.order_hash, sp.chain_id,
		       sp.settlement_address, sp.payer, sp.amount_base_units::text,
		       COALESCE(sp.claim_tx_hash,''), COALESCE(sp.approve_tx_hash,''),
		       COALESCE(sp.pay_tx_hash,''), COALESCE(sp.complete_tx_hash,''),
		       COALESCE(sp.refund_tx_hash,''), sp.state, sp.safe_block,
		       sp.finalized_block, COALESCE(sp.last_reason_code,''),
		       sp.observation_exhausted_at, sp.created_at, sp.updated_at,
		       ct.tx_hash, ct.purpose, ct.state, ct.block_number,
		       COALESCE(ct.block_hash,''), ct.submitted_at,
		       ct.observation_attempt_count
		FROM chain_transactions ct
		JOIN settlement_payments sp ON sp.id=ct.settlement_payment_id
		WHERE ct.state IN ('SUBMITTED','SAFE')
		  AND ct.next_observation_at <= $1
		ORDER BY ct.next_observation_at, ct.submitted_at, ct.chain_id, ct.tx_hash
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list reconcile items: %w", err)
	}
	defer rows.Close()
	items := []settlementapp.ReconcileItem{}
	for rows.Next() {
		var item settlementapp.ReconcileItem
		if err := rows.Scan(
			&item.Payment.ID, &item.Payment.AgencyOrderID, &item.Payment.OrderHash,
			&item.Payment.ChainID, &item.Payment.Settlement, &item.Payment.Payer,
			&item.Payment.AmountBaseUnits, &item.Payment.ClaimTxHash,
			&item.Payment.ApproveTxHash, &item.Payment.PayTxHash,
			&item.Payment.CompleteTxHash, &item.Payment.RefundTxHash,
			&item.Payment.State, &item.Payment.SafeBlock, &item.Payment.FinalizedBlock,
			&item.Payment.LastReasonCode, &item.Payment.ObservationExhaustedAt,
			&item.Payment.CreatedAt, &item.Payment.UpdatedAt,
			&item.TxHash, &item.Purpose, &item.TxState,
			&item.TxBlock, &item.TxBlockHash, &item.TxSubmittedAt,
			&item.ObservationAttempt,
		); err != nil {
			return nil, fmt.Errorf("scan reconcile item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ScheduleTransactionObservation(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	next time.Time,
	reason string,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE chain_transactions
		SET observation_attempt_count=observation_attempt_count+1,
		    last_observed_at=$1, next_observation_at=$2,
		    last_reason_code=NULLIF($3,''), updated_at=$1
		WHERE chain_id=$4 AND tx_hash=$5 AND state IN ('SUBMITTED','SAFE')
	`, now, next, reason, item.Payment.ChainID, item.TxHash)
	return err
}

func (r *Repository) MarkTransactionObservationUnknown(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var paymentID, fromState string
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT settlement_payment_id, state
			FROM chain_transactions
			WHERE chain_id=$1 AND tx_hash=$2 AND state IN ('SUBMITTED','SAFE')
			FOR UPDATE
		`, item.Payment.ChainID, item.TxHash).Scan(&paymentID, &fromState)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_reconcile_audit(
				settlement_payment_id, chain_id, tx_hash, from_state, to_state,
				reason_code, evidence_kind, observed_at
			) VALUES ($1,$2,$3,$4,'OBSERVATION_UNKNOWN',$5,
			          'OBSERVATION_POLICY',$6)
		`, paymentID, item.Payment.ChainID, item.TxHash, fromState, reason, now); err != nil {
			return err
		}
		_, err = r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE chain_transactions
			SET state='OBSERVATION_UNKNOWN', last_observed_at=$1,
			    next_observation_at=NULL, observation_exhausted_at=$1,
			    last_reason_code=$2, updated_at=$1
			WHERE chain_id=$3 AND tx_hash=$4 AND state IN ('SUBMITTED','SAFE')
		`, now, reason, item.Payment.ChainID, item.TxHash)
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposePay {
			if _, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments
				SET state='SUBMISSION_UNKNOWN', last_reason_code=$1,
				    observation_exhausted_at=$2, updated_at=$2
				WHERE id=$3 AND state IN ('PAYMENT_SUBMITTED','SAFE')
			`, reason, now, item.Payment.ID); err != nil {
				return err
			}
			return emitSettlementEvent(txContext, r.database.Queryer(txContext), item.Payment.ID, now)
		}
		if item.Purpose == settlementapp.PurposeComplete ||
			item.Purpose == settlementapp.PurposeRefund ||
			item.Purpose == settlementapp.PurposeRefundPartial {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_command_outbox
				SET state='CONFLICT', last_error_code=$1,
				    reconciled_at=$2, updated_at=$2
				WHERE settlement_payment_id=$3 AND purpose=$4
				  AND lower(tx_hash)=lower($5)
				  AND state IN ('SIGNED','BROADCAST')
			`, reason, now, item.Payment.ID, item.Purpose, item.TxHash)
		}
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposeRefundPartial {
			return r.transitionMOCompensationOutcome(
				txContext, item.Payment.ID, item.TxHash,
				"OUTCOME_UNKNOWN", "RELEASE_UNKNOWN", now,
			)
		}
		return nil
	})
}

func (r *Repository) ListSubmissionClosureCandidates(
	ctx context.Context,
	finalizedTimestamp uint64,
	limit int,
) ([]settlementapp.SubmissionClosureCandidate, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT sp.id, sp.agency_order_id, sp.order_hash, sp.chain_id,
		       sp.settlement_address, sp.payer, sp.amount_base_units::text,
		       COALESCE(sp.claim_tx_hash,''), COALESCE(sp.approve_tx_hash,''),
		       COALESCE(sp.pay_tx_hash,''), COALESCE(sp.complete_tx_hash,''),
		       COALESCE(sp.refund_tx_hash,''), sp.state, sp.safe_block,
		       sp.finalized_block, COALESCE(sp.last_reason_code,''),
		       sp.observation_exhausted_at, sp.created_at, sp.updated_at,
		       sa.pay_deadline
		FROM settlement_payments sp
		JOIN settlement_authorizations sa ON sa.agency_order_id=sp.agency_order_id
		LEFT JOIN agency_order_payment_instructions i ON i.agency_order_id=sp.agency_order_id
		WHERE i.state='CONSUMED'
		  AND sp.state IN (
		      'AUTHORIZED', 'AWAITING_ALLOWANCE', 'PAYMENT_SUBMITTED',
		      'SUBMISSION_UNKNOWN'
		  )
		  AND sa.pay_deadline <= to_timestamp($1)
		ORDER BY sa.pay_deadline, sp.id
		LIMIT $2
	`, finalizedTimestamp, limit)
	if err != nil {
		return nil, fmt.Errorf("list submission closure candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]settlementapp.SubmissionClosureCandidate, 0)
	for rows.Next() {
		var candidate settlementapp.SubmissionClosureCandidate
		if err := rows.Scan(
			&candidate.Payment.ID, &candidate.Payment.AgencyOrderID,
			&candidate.Payment.OrderHash, &candidate.Payment.ChainID,
			&candidate.Payment.Settlement, &candidate.Payment.Payer,
			&candidate.Payment.AmountBaseUnits, &candidate.Payment.ClaimTxHash,
			&candidate.Payment.ApproveTxHash, &candidate.Payment.PayTxHash,
			&candidate.Payment.CompleteTxHash, &candidate.Payment.RefundTxHash,
			&candidate.Payment.State, &candidate.Payment.SafeBlock,
			&candidate.Payment.FinalizedBlock, &candidate.Payment.LastReasonCode,
			&candidate.Payment.ObservationExhaustedAt,
			&candidate.Payment.CreatedAt, &candidate.Payment.UpdatedAt,
			&candidate.PayDeadline,
		); err != nil {
			return nil, fmt.Errorf("scan submission closure candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (r *Repository) MarkPaymentSubmissionUnknown(
	ctx context.Context,
	candidate settlementapp.SubmissionClosureCandidate,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		payTransactionLocked := false
		if strings.TrimSpace(candidate.Payment.PayTxHash) != "" {
			var transactionState string
			err := r.database.Queryer(txContext).QueryRowContext(txContext, `
				SELECT state FROM chain_transactions
				WHERE chain_id=$1 AND lower(tx_hash)=lower($2)
				FOR UPDATE
			`, candidate.Payment.ChainID, candidate.Payment.PayTxHash).Scan(&transactionState)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				if transactionState != "SUBMITTED" && transactionState != "SAFE" &&
					transactionState != "OBSERVATION_UNKNOWN" {
					return nil
				}
				payTransactionLocked = true
			}
		}
		var fromState string
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT state FROM settlement_payments WHERE id=$1 FOR UPDATE
		`, candidate.Payment.ID).Scan(&fromState)
		if err != nil {
			return err
		}
		if fromState != "AUTHORIZED" && fromState != "AWAITING_ALLOWANCE" &&
			fromState != "PAYMENT_SUBMITTED" && fromState != "SUBMISSION_UNKNOWN" {
			return nil
		}
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_reconcile_audit(
				settlement_payment_id, chain_id, tx_hash, from_state, to_state,
				reason_code, evidence_kind, observed_at
			) VALUES ($1,$2,NULLIF($3,''),$4,'SUBMISSION_UNKNOWN',$5,
			          'FINALIZED_CONTRACT_STATE',$6)
		`, candidate.Payment.ID, candidate.Payment.ChainID,
			candidate.Payment.PayTxHash, fromState, reason, now); err != nil {
			return err
		}
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE settlement_payments
			SET state='SUBMISSION_UNKNOWN', last_reason_code=$1,
			    observation_exhausted_at=$2, updated_at=$2
			WHERE id=$3 AND state IN (
			    'AUTHORIZED','AWAITING_ALLOWANCE','PAYMENT_SUBMITTED',
			    'SUBMISSION_UNKNOWN'
			)
		`, reason, now, candidate.Payment.ID); err != nil {
			return err
		}
		if err := emitSettlementEvent(txContext, r.database.Queryer(txContext),
			candidate.Payment.ID, now); err != nil {
			return err
		}
		if payTransactionLocked {
			if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE chain_transactions
				SET state='OBSERVATION_UNKNOWN', last_reason_code=$1,
				    observation_exhausted_at=$2, next_observation_at=NULL,
				    updated_at=$2
				WHERE chain_id=$3 AND lower(tx_hash)=lower($4)
				  AND state IN ('SUBMITTED','SAFE','OBSERVATION_UNKNOWN')
			`, reason, now, candidate.Payment.ChainID,
				candidate.Payment.PayTxHash); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) MarkTransactionSafe(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	observation settlementapp.TransactionObservation,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		_, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE chain_transactions SET state='SAFE', block_number=$1,
			       block_hash=$2, gas_used=NULLIF($6,'')::numeric,
			       effective_gas_price=NULLIF($7,'')::numeric,
			       from_address=COALESCE(NULLIF($8,''), from_address),
			       last_reason_code=NULL, updated_at=$3
			WHERE chain_id=$4 AND tx_hash=$5 AND state='SUBMITTED'
		`, observation.BlockNumber, observation.BlockHash, now,
			item.Payment.ChainID, item.TxHash, observation.GasUsed,
			observation.EffectiveGasPrice, observation.FromAddress)
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposePay {
			if _, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='SAFE', safe_block=$1, updated_at=$2
				WHERE id=$3 AND state='PAYMENT_SUBMITTED'
			`, observation.BlockNumber, now, item.Payment.ID); err != nil {
				return err
			}
			return emitSettlementEvent(txContext, r.database.Queryer(txContext), item.Payment.ID, now)
		}
		return err
	})
}

func (r *Repository) MarkAuxTransactionFinalized(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	observation settlementapp.TransactionObservation,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE chain_transactions SET state='FINALIZED', block_number=$1,
		       block_hash=$2, gas_used=NULLIF($3,'')::numeric,
		       effective_gas_price=NULLIF($4,'')::numeric,
		       from_address=COALESCE(NULLIF($5,''), from_address),
		       next_observation_at=NULL,
		       last_reason_code=NULL, updated_at=$6
		WHERE chain_id=$7 AND tx_hash=$8 AND state IN ('SUBMITTED','SAFE')
	`, observation.BlockNumber, observation.BlockHash, observation.GasUsed,
		observation.EffectiveGasPrice, observation.FromAddress, now,
		item.Payment.ChainID, item.TxHash)
	return err
}

func (r *Repository) MarkTransactionFailed(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var paymentID, fromState string
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT settlement_payment_id, state
			FROM chain_transactions
			WHERE chain_id=$1 AND tx_hash=$2 AND state IN ('SUBMITTED','SAFE')
			FOR UPDATE
		`, item.Payment.ChainID, item.TxHash).Scan(&paymentID, &fromState)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_reconcile_audit(
				settlement_payment_id, chain_id, tx_hash, from_state, to_state,
				reason_code, evidence_kind, observed_at
			) VALUES ($1,$2,$3,$4,'FAILED',$5,'CHAIN_RECEIPT',$6)
		`, paymentID, item.Payment.ChainID, item.TxHash, fromState, reason, now); err != nil {
			return err
		}
		_, err = r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE chain_transactions
			SET state='FAILED', last_reason_code=$1, next_observation_at=NULL,
			    last_observed_at=$2, updated_at=$2
			WHERE chain_id=$3 AND tx_hash=$4 AND state IN ('SUBMITTED','SAFE')
		`, reason, now, item.Payment.ChainID, item.TxHash)
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposePay {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments
				SET state='FAILED', last_reason_code=$1, updated_at=$2 WHERE id=$3;
			`, reason, now, item.Payment.ID)
		} else if item.Purpose == settlementapp.PurposeComplete {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='FINALIZED', complete_tx_hash=NULL,
				       updated_at=$1 WHERE id=$2
			`, now, item.Payment.ID)
		} else if item.Purpose == settlementapp.PurposeRefund {
			// ATTENTION 전이는 아래 refund_conflict 이벤트를 Manager가 소비해
			// 결정한다(ADR-0056 — 종전 process 직접 UPDATE 폐지).
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='FINALIZED', refund_tx_hash=NULL,
				       updated_at=$1 WHERE id=$2
			`, now, item.Payment.ID)
		}
		if err == nil && (item.Purpose == settlementapp.PurposeComplete ||
			item.Purpose == settlementapp.PurposeRefund ||
			item.Purpose == settlementapp.PurposeRefundPartial) {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_command_outbox
				SET state='CONFLICT', last_error_code=$1,
				    reconciled_at=$2, updated_at=$2
				WHERE settlement_payment_id=$3 AND purpose=$4
				  AND lower(tx_hash)=lower($5)
				  AND state IN ('SIGNED','BROADCAST')
			`, reason, now, item.Payment.ID, item.Purpose, item.TxHash)
		}
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposeRefundPartial {
			if err := r.transitionMOCompensationOutcome(
				txContext, item.Payment.ID, item.TxHash,
				"FAILED", "FAILED", now,
			); err != nil {
				return err
			}
			return emitSettlementRefundConflict(txContext,
				r.database.Queryer(txContext), item.Payment.ID,
				item.Payment.AgencyOrderID, reason, now)
		}
		if err := emitSettlementEvent(txContext,
			r.database.Queryer(txContext), item.Payment.ID, now); err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposeRefund {
			// 환불 finalize 충돌은 이벤트로만 알 수 있는 전이다 — process의
			// ATTENTION 재판정은 이 이벤트 소비가 결정한다(ADR-0056).
			return emitSettlementRefundConflict(txContext,
				r.database.Queryer(txContext), item.Payment.ID,
				item.Payment.AgencyOrderID, reason, now)
		}
		return nil
	})
}

func (r *Repository) MarkTransactionReorged(
	ctx context.Context,
	item settlementapp.ReconcileItem,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		_, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE chain_transactions SET state='SUBMITTED', block_number=NULL,
			       block_hash=NULL, next_observation_at=$1,
			       observation_attempt_count=0, last_reason_code=NULL, updated_at=$1
			WHERE chain_id=$2 AND tx_hash=$3 AND state='SAFE'
		`, now, item.Payment.ChainID, item.TxHash)
		if err != nil {
			return err
		}
		if item.Purpose == settlementapp.PurposePay {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='PAYMENT_SUBMITTED', safe_block=NULL,
				       updated_at=$1 WHERE id=$2 AND state='SAFE'
			`, now, item.Payment.ID)
		} else if item.Purpose == settlementapp.PurposeComplete {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='FINALIZED', complete_tx_hash=NULL,
				       updated_at=$1 WHERE id=$2 AND state='COMPLETION_SUBMITTED'
			`, now, item.Payment.ID)
		} else if item.Purpose == settlementapp.PurposeRefund {
			_, err = r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE settlement_payments SET state='FINALIZED', refund_tx_hash=NULL,
				       updated_at=$1 WHERE id=$2 AND state='REFUND_PENDING'
			`, now, item.Payment.ID)
		}
		if err != nil {
			return err
		}
		return emitSettlementEvent(txContext, r.database.Queryer(txContext), item.Payment.ID, now)
	})
}

func (r *Repository) GetFinalizedCursor(
	ctx context.Context,
	chainID uint64,
	contractAddress string,
) (uint64, bool, error) {
	var cursor uint64
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT finalized_block FROM chain_cursors
		WHERE chain_id=$1 AND contract_address=$2
	`, chainID, strings.ToLower(contractAddress)).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get finalized cursor: %w", err)
	}
	return cursor, true, nil
}

func (r *Repository) ProjectFinalizedEvents(
	ctx context.Context,
	chainID uint64,
	contractAddress string,
	events []settlementapp.FinalizedEvent,
	finalizedBlock uint64,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		for _, event := range events {
			_, err := r.database.Queryer(txContext).ExecContext(txContext, `
				INSERT INTO chain_events(
					chain_id, tx_hash, log_index, block_number, block_hash,
					event_name, order_hash, observed_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
			`, chainID, event.TxHash, event.LogIndex, event.BlockNumber,
				event.BlockHash, event.EventName, event.OrderHash, now)
			if err != nil {
				return fmt.Errorf("insert finalized event: %w", err)
			}
			// Projection is intentionally replayed even when the unique log row
			// already exists. A prior overlap pass may have observed the log before
			// its offchain payment row was committed; the state upserts below are
			// idempotent and let a later overlap pass converge that delayed row.
			if err := r.projectFinalizedEvent(txContext, chainID, event, now); err != nil {
				return err
			}
		}
		_, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO chain_cursors(chain_id, contract_address, finalized_block, updated_at)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (chain_id, contract_address) DO UPDATE SET
				finalized_block=GREATEST(chain_cursors.finalized_block, EXCLUDED.finalized_block),
				updated_at=EXCLUDED.updated_at
		`, chainID, strings.ToLower(contractAddress), finalizedBlock, now)
		return err
	})
}

func (r *Repository) projectFinalizedEvent(
	ctx context.Context,
	chainID uint64,
	event settlementapp.FinalizedEvent,
	now time.Time,
) error {
	var paymentID string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id
		FROM settlement_payments
		WHERE order_hash=$1 AND chain_id=$2
	`, event.OrderHash, chainID).Scan(&paymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	purpose, paymentState, txColumn := "PAY", "FINALIZED", "pay_tx_hash"
	paymentPredicate := "state NOT IN ('COMPLETION_SUBMITTED','COMPLETED','REFUND_PENDING','REFUNDED')"
	switch event.EventName {
	case "PaymentCompleted":
		purpose, paymentState, txColumn = "COMPLETE", "COMPLETED", "complete_tx_hash"
		paymentPredicate = "state <> 'REFUNDED'"
	case "PaymentRefunded":
		purpose, paymentState, txColumn = "REFUND", "REFUNDED", "refund_tx_hash"
		paymentPredicate = "state <> 'COMPLETED'"
	case "PaymentEscrowed":
	default:
		return nil
	}
	// Settlement v2의 MO 보상도 PaymentRefunded를 낸다. 이 tx가
	// REFUND_PARTIAL Effect이면 주문 전체 REFUNDED 전이 대신 정확히 연결된
	// whole-MO compensation만 닫는다.
	if event.EventName == "PaymentRefunded" {
		var moCompensationID string
		partialErr := r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT mo_compensation_id::text
			FROM settlement_command_outbox
			WHERE settlement_payment_id=$1 AND purpose='REFUND_PARTIAL'
			  AND lower(tx_hash)=lower($2)
		`, paymentID, event.TxHash).Scan(&moCompensationID)
		if partialErr != nil && !errors.Is(partialErr, sql.ErrNoRows) {
			return partialErr
		}
		if moCompensationID != "" {
			return r.finalizeMOCompensation(
				ctx, chainID, paymentID, moCompensationID, event, now,
			)
		}
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO chain_transactions(
			chain_id, tx_hash, settlement_payment_id, purpose, state,
			block_number, block_hash, gas_used, effective_gas_price, from_address,
			submitted_at, updated_at
		) VALUES ($1,$2,$3,$4,'FINALIZED',$5,$6,NULLIF($7,'')::numeric,
			NULLIF($8,'')::numeric,NULLIF($9,''),$10,$10)
		ON CONFLICT (chain_id, tx_hash) DO UPDATE SET
			settlement_payment_id=EXCLUDED.settlement_payment_id,
			purpose=EXCLUDED.purpose, state='FINALIZED',
			block_number=EXCLUDED.block_number, block_hash=EXCLUDED.block_hash,
			gas_used=COALESCE(EXCLUDED.gas_used, chain_transactions.gas_used),
			effective_gas_price=COALESCE(
				EXCLUDED.effective_gas_price, chain_transactions.effective_gas_price
			),
			from_address=COALESCE(chain_transactions.from_address, EXCLUDED.from_address),
			next_observation_at=NULL, last_reason_code=NULL,
			updated_at=EXCLUDED.updated_at
	`, chainID, event.TxHash, paymentID, purpose, event.BlockNumber, event.BlockHash,
		event.GasUsed, event.EffectiveGasPrice, event.FromAddress, now)
	if err != nil {
		return err
	}
	var currentPaymentState string
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT state FROM settlement_payments WHERE id=$1 FOR UPDATE
	`, paymentID).Scan(&currentPaymentState); err != nil {
		return err
	}
	if currentPaymentState == "SUBMISSION_UNKNOWN" || currentPaymentState == "FAILED" {
		if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
			INSERT INTO settlement_reconcile_audit(
				settlement_payment_id, chain_id, tx_hash, from_state, to_state,
				reason_code, evidence_kind, observed_at
			) VALUES ($1,$2,$3,$4,$5,'LATE_CANONICAL_EVENT',
			          'CANONICAL_EVENT',$6)
		`, paymentID, chainID, event.TxHash, currentPaymentState,
			paymentState, now); err != nil {
			return err
		}
	}
	query := fmt.Sprintf(`
		UPDATE settlement_payments SET state=$1, finalized_block=$2,
		       %s=COALESCE(%s,$3), last_reason_code=NULL,
		       observation_exhausted_at=NULL, updated_at=$4
		WHERE id=$5 AND %s
	`, txColumn, txColumn, paymentPredicate)
	if _, err := r.database.Queryer(ctx).ExecContext(
		ctx, query, paymentState, event.BlockNumber, event.TxHash, now, paymentID,
	); err != nil {
		return err
	}
	if purpose == string(settlementapp.CommandComplete) {
		if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
			UPDATE settlement_command_outbox
			SET state='FINALIZED', reconciled_at=$1, updated_at=$1
			WHERE settlement_payment_id=$2 AND purpose=$3
			  AND lower(tx_hash)=lower($4)
			  AND state IN ('SIGNED','BROADCAST','CONFLICT')
		`, now, paymentID, purpose, event.TxHash); err != nil {
			return err
		}
	}
	return emitSettlementEvent(ctx, r.database.Queryer(ctx), paymentID, now)
}

// finalizeMOCompensation applies canonical REFUND_PARTIAL finality to exactly
// one immutable MO allocation. The order-level settlement payment remains an
// independent aggregate.
func (r *Repository) finalizeMOCompensation(
	ctx context.Context,
	chainID uint64,
	paymentID, moCompensationID string,
	event settlementapp.FinalizedEvent,
	now time.Time,
) error {
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO chain_transactions(
			chain_id, tx_hash, settlement_payment_id, purpose, state,
			block_number, block_hash, gas_used, effective_gas_price, from_address,
			submitted_at, updated_at
		) VALUES ($1,$2,$3,'REFUND_PARTIAL','FINALIZED',$4,$5,NULLIF($6,'')::numeric,
			NULLIF($7,'')::numeric,NULLIF($8,''),$9,$9)
		ON CONFLICT (chain_id, tx_hash) DO UPDATE SET
			settlement_payment_id=EXCLUDED.settlement_payment_id,
			purpose=EXCLUDED.purpose, state='FINALIZED',
			block_number=EXCLUDED.block_number, block_hash=EXCLUDED.block_hash,
			gas_used=COALESCE(EXCLUDED.gas_used, chain_transactions.gas_used),
			effective_gas_price=COALESCE(
				EXCLUDED.effective_gas_price, chain_transactions.effective_gas_price
			),
			from_address=COALESCE(chain_transactions.from_address, EXCLUDED.from_address),
			next_observation_at=NULL, last_reason_code=NULL,
			updated_at=EXCLUDED.updated_at
	`, chainID, event.TxHash, paymentID, event.BlockNumber, event.BlockHash,
		event.GasUsed, event.EffectiveGasPrice, event.FromAddress, now); err != nil {
		return err
	}
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE settlement_command_outbox
		SET state='FINALIZED', reconciled_at=$1, updated_at=$1
		WHERE settlement_payment_id=$2 AND purpose='REFUND_PARTIAL'
		  AND lower(tx_hash)=lower($3)
		  AND state IN ('SIGNED','BROADCAST','CONFLICT')
	`, now, paymentID, event.TxHash); err != nil {
		return err
	}
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE payment_mo_compensations
		SET state='SUCCEEDED', provider_resource_id=lower($1),
		    version=version+1, completed_at=$2, updated_at=$2
		WHERE id=$3 AND state IN (
		    'APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN','FAILED'
		)
	`, event.TxHash, now, moCompensationID); err != nil {
		return err
	}
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE payment_mo_funding_positions funding
		SET state='RELEASED', released_at=$1,
		    version=funding.version+1, updated_at=$1
		FROM payment_mo_compensations compensation
		WHERE compensation.id=$2
		  AND funding.id=compensation.funding_position_id
		  AND funding.state IN (
		      'AVAILABLE','ACTIVE','RELEASE_PENDING','RELEASE_UNKNOWN','FAILED'
		  )
	`, now, moCompensationID); err != nil {
		return err
	}
	if err := paymentpostgres.EmitMOFundingEventForCompensation(
		ctx, r.database.Queryer(ctx), moCompensationID, now,
	); err != nil {
		return err
	}
	return paymentpostgres.EmitMOCompensationEvent(
		ctx, r.database.Queryer(ctx), moCompensationID, now,
	)
}

// transitionMOCompensationOutcome records non-successful chain outcomes for
// the exact outbox owner. Observation exhaustion remains OUTCOME_UNKNOWN;
// a definitive reverted/mismatched receipt is FAILED. Neither is presented as
// a customer refund or as released funding.
func (r *Repository) transitionMOCompensationOutcome(
	ctx context.Context,
	paymentID, txHash, compensationState, fundingState string,
	now time.Time,
) error {
	if (compensationState != "OUTCOME_UNKNOWN" || fundingState != "RELEASE_UNKNOWN") &&
		(compensationState != "FAILED" || fundingState != "FAILED") {
		return fmt.Errorf("invalid MO compensation outcome %s/%s",
			compensationState, fundingState)
	}
	var moCompensationID string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		UPDATE payment_mo_compensations compensation
		SET state=$1,
		    provider_resource_id=COALESCE(provider_resource_id, lower($2)),
		    version=version+1,
		    completed_at=CASE WHEN $1='FAILED' THEN $3::timestamptz ELSE NULL END,
		    updated_at=$3::timestamptz
		FROM settlement_command_outbox command
		WHERE command.mo_compensation_id=compensation.id
		  AND command.settlement_payment_id=$4
		  AND command.purpose='REFUND_PARTIAL'
		  AND lower(command.tx_hash)=lower($2)
		  AND compensation.state IN (
		      'APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN'
		  )
		  AND compensation.state<>$1
		RETURNING compensation.id::text
	`, compensationState, txHash, now, paymentID).Scan(&moCompensationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("record MO compensation outcome: %w", err)
	}
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE payment_mo_funding_positions funding
		SET state=$1, released_at=NULL,
		    version=funding.version+1, updated_at=$2
		FROM payment_mo_compensations compensation
		WHERE compensation.id=$3
		  AND funding.id=compensation.funding_position_id
		  AND funding.state IN (
		      'AVAILABLE','ACTIVE','RELEASE_PENDING','RELEASE_UNKNOWN'
		  )
		  AND funding.state<>$1
	`, fundingState, now, moCompensationID); err != nil {
		return fmt.Errorf("record MO funding outcome: %w", err)
	}
	if err := paymentpostgres.EmitMOFundingEventForCompensation(
		ctx, r.database.Queryer(ctx), moCompensationID, now,
	); err != nil {
		return err
	}
	return paymentpostgres.EmitMOCompensationEvent(
		ctx, r.database.Queryer(ctx), moCompensationID, now,
	)
}

func (r *Repository) EnsureCommands(
	ctx context.Context,
	finalizerAddress, refunderAddress string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		// COMPLETE는 terminal MerchantOrder와 whole-MO compensation projection을
		// 직접 검사한다. 실패/취소 MO는 자기 immutable allocation의 compensation이
		// SUCCEEDED여야 하며, PLACED unit은 물리 coverage가 끝나야 한다.
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO settlement_command_outbox(
				settlement_payment_id, chain_id, purpose, signer_address,
				order_hash, fulfillment_hash, state, planned_at, updated_at
			)
			SELECT payment.id, payment.chain_id, 'COMPLETE', lower($1), payment.order_hash,
			       '0x' || encode(sha256(convert_to(
			           string_agg(mo.result_hash, ':' ORDER BY mo.checkout_ordinal)
			               FILTER (WHERE mo.state='PLACED'),
			           'UTF8'
			       )), 'hex'),
			       'PLANNED', $2::timestamptz, $2::timestamptz
			FROM settlement_payments payment
			JOIN payment_customer_payments joined
			  ON joined.agency_order_id=payment.agency_order_id
			 AND joined.state='CAPTURED'
			 AND joined.rail='GIWA'
			 AND joined.provider_environment='TESTNET'
			JOIN merchant_orders mo
			  ON mo.agency_order_id=payment.agency_order_id
			WHERE payment.state='FINALIZED'
			  AND payment.agency_order_id IS NOT NULL
			  AND payment.complete_tx_hash IS NULL
			  AND payment.refund_tx_hash IS NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM payment_mo_compensations compensation
			      WHERE compensation.customer_payment_id=joined.id
			        AND compensation.state<>'SUCCEEDED'
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM merchant_orders terminal_mo
			      LEFT JOIN payment_mo_compensations compensation
			        ON compensation.allocation_id=terminal_mo.allocation_id
			       AND compensation.agency_order_id=terminal_mo.agency_order_id
			       AND compensation.state='SUCCEEDED'
			      WHERE terminal_mo.agency_order_id=payment.agency_order_id
			        AND terminal_mo.state IN ('FAILED','CANCELLED')
			        AND compensation.id IS NULL
			  )
			  AND NOT EXISTS (
			      -- 배송 coverage guard(ADR-0053): PLACED unit은 수령·해소 또는
			      -- 취소에 의한 기대 폐기 전에는 escrow를 방출하지 않는다.
			      SELECT 1 FROM merchant_order_units unit
			      JOIN merchant_orders placed ON placed.id=unit.merchant_order_id
			      LEFT JOIN logistics_expected_units expected
			        ON expected.merchant_order_unit_id=unit.id
			      WHERE unit.agency_order_id=payment.agency_order_id
			        AND placed.state='PLACED'
			        AND (expected.id IS NULL OR expected.fulfillment NOT IN (
			             'DELIVERED_EXPECTED','RESOLVED','NONCONFORMING_RESOLVED',
			             'SUPERSEDED_BY_CANCELLATION','NO_PLACEMENT'))
			  )
			GROUP BY payment.id
			HAVING bool_and(mo.state IN ('PLACED','FAILED','CANCELLED'))
			   AND count(*) FILTER (WHERE mo.state='PLACED') > 0
			   AND bool_and(mo.result_hash IS NOT NULL)
			       FILTER (WHERE mo.state='PLACED')
			   -- PLACED MO가 전부 whole-MO 보상으로 환불됐으면(배송 예외·환불 심사)
			   -- 방출할 escrow가 없다 — contract는 REFUNDED라 COMPLETE가
			   -- PaymentNotEscrowed로 영구 revert한다. 환불되지 않은 PLACED MO가
			   -- 하나라도 있어야 잔여 pass-through 방출을 계획한다.
			   AND count(*) FILTER (WHERE mo.state='PLACED' AND NOT EXISTS (
			       SELECT 1 FROM payment_mo_compensations refunded
			       WHERE refunded.allocation_id=mo.allocation_id
			         AND refunded.agency_order_id=mo.agency_order_id
			         AND refunded.state='SUCCEEDED'
			   )) > 0
			ON CONFLICT (settlement_payment_id, purpose)
			WHERE purpose='COMPLETE' DO NOTHING
		`, finalizerAddress, now); err != nil {
			return fmt.Errorf("plan joined AgencyOrder completion commands: %w", err)
		}
		// Payment가 승인한 TVIT_REFUND 한 건을 immutable MO allocation 전체의
		// pass-through+fee로 계획한다. minor USD를 6-decimal tVITUSD base units로
		// 정확히 변환하며, compensation identity에서 결정한 refund_key로 contract
		// replay도 막는다.
		// Lock approved compensations before inserting a retry attempt. This
		// serializes a retained old transaction's late finality with explicit
		// rearm, so an already-SUCCEEDED compensation can never be moved back to
		// EXECUTION_PENDING by the planner.
		lockedRows, err := r.database.Queryer(txContext).QueryContext(txContext, `
				SELECT id::text
				FROM payment_mo_compensations
				WHERE rail='GIWA' AND provider_environment='TESTNET'
				  AND action='TVIT_REFUND' AND state='APPROVED'
				ORDER BY id
				FOR UPDATE
			`)
		if err != nil {
			return fmt.Errorf("lock GIWA MO compensations for planning: %w", err)
		}
		for lockedRows.Next() {
			var ignoredID string
			if err := lockedRows.Scan(&ignoredID); err != nil {
				lockedRows.Close()
				return err
			}
		}
		lockedRows.Close()
		if err := lockedRows.Err(); err != nil {
			return err
		}
		plannedRows, err := r.database.Queryer(txContext).QueryContext(txContext, `
			WITH planned AS (
				INSERT INTO settlement_command_outbox(
					settlement_payment_id, chain_id, purpose, signer_address,
					order_hash, fulfillment_hash, state,
					mo_compensation_id, pass_through_part, fee_part, refund_key,
					planned_at, updated_at
				)
				SELECT sp.id, sp.chain_id, 'REFUND_PARTIAL', lower($1),
				       sp.order_hash, NULL, 'PLANNED',
				       compensation.id,
				       allocation.pass_through_minor * 10000,
				       allocation.fee_total_minor * 10000,
				       '0x' || encode(sha256(convert_to(
				           sp.order_hash || ':' || compensation.id::text || ':'
				               || allocation.allocation_hash
				               || ':VITLANE_SETTLEMENT_V2_MO_COMPENSATION',
				           'UTF8'
				       )), 'hex'),
				       $2::timestamptz, $2::timestamptz
				FROM payment_mo_compensations compensation
				JOIN payment_mo_funding_positions funding
				  ON funding.id=compensation.funding_position_id
				 AND funding.allocation_id=compensation.allocation_id
				 AND funding.agency_order_id=compensation.agency_order_id
				 AND funding.customer_payment_id=compensation.customer_payment_id
				 AND funding.rail='GIWA'
				 AND funding.provider_environment='TESTNET'
				 AND funding.state IN ('AVAILABLE','ACTIVE','RELEASE_PENDING')
				JOIN agency_order_mo_allocations allocation
				  ON allocation.id=compensation.allocation_id
				 AND allocation.agency_order_id=compensation.agency_order_id
				 AND allocation.customer_gross_minor=compensation.amount_minor
				 AND allocation.currency=compensation.currency
				JOIN payment_funds_receipts receipt
				  ON receipt.customer_payment_id=compensation.customer_payment_id
				 AND receipt.agency_order_id=compensation.agency_order_id
				 AND receipt.kind='GIWA_FINALIZED_PAY'
				 AND receipt.provider_environment='TESTNET'
				 AND receipt.accepted
				JOIN settlement_payments sp
				  ON sp.agency_order_id=compensation.agency_order_id
				 AND sp.order_hash=receipt.order_hash
				 AND sp.state IN ('FINALIZED','COMPLETION_SUBMITTED','COMPLETED')
				WHERE compensation.rail='GIWA'
				  AND compensation.provider_environment='TESTNET'
				  AND compensation.action='TVIT_REFUND'
				  AND compensation.state='APPROVED'
				ON CONFLICT DO NOTHING
				RETURNING mo_compensation_id
			)
			UPDATE payment_mo_compensations
				SET state='EXECUTION_PENDING', version=version+1,
				    updated_at=$2::timestamptz
				WHERE id IN (SELECT mo_compensation_id FROM planned)
				  AND state='APPROVED'
			RETURNING id::text
		`, refunderAddress, now)
		if err != nil {
			return fmt.Errorf("plan GIWA MO compensation commands: %w", err)
		}
		compensationIDs := make([]string, 0)
		for plannedRows.Next() {
			var id string
			if err := plannedRows.Scan(&id); err != nil {
				plannedRows.Close()
				return err
			}
			compensationIDs = append(compensationIDs, id)
		}
		plannedRows.Close()
		if err := plannedRows.Err(); err != nil {
			return err
		}
		for _, id := range compensationIDs {
			if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
				UPDATE payment_mo_funding_positions funding
				SET state='RELEASE_PENDING',
				    version=funding.version+1, updated_at=$1
				FROM payment_mo_compensations compensation
				WHERE compensation.id=$2
				  AND funding.id=compensation.funding_position_id
				  AND funding.state IN ('AVAILABLE','ACTIVE')
			`, now, id); err != nil {
				return fmt.Errorf("mark GIWA MO funding release pending: %w", err)
			}
			if err := paymentpostgres.EmitMOFundingEventForCompensation(txContext,
				r.database.Queryer(txContext), id, now); err != nil {
				return err
			}
			if err := paymentpostgres.EmitMOCompensationEvent(txContext,
				r.database.Queryer(txContext), id, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) ListCommandWork(
	ctx context.Context,
	limit int,
	dueAt time.Time,
) ([]settlementapp.SettlementCommand, error) {
	// COMPLETE와 whole-MO compensation이 소유한 REFUND_PARTIAL만 worker에
	// 노출한다. REFUND_PARTIAL은 contract vocabulary이며 slice owner는 없다.
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT sco.id, sco.settlement_payment_id, sco.chain_id, sco.purpose,
		       sco.signer_address, sco.order_hash,
		       COALESCE(sco.fulfillment_hash,''), sco.state,
		       sco.signer_nonce, COALESCE(sco.tx_hash,''),
		       sco.raw_transaction,
		       COALESCE(sa.authorization_payload#>>'{authorization,passThroughAmount}',''),
		       COALESCE(sa.authorization_payload#>>'{authorization,feeAmount}',''),
		       COALESCE(sco.mo_compensation_id::text,''),
		       COALESCE(sco.refund_key,''),
		       COALESCE(sco.pass_through_part::text,''),
		       COALESCE(sco.fee_part::text,'')
		FROM settlement_command_outbox sco
		JOIN settlement_payments sp ON sp.id=sco.settlement_payment_id
		LEFT JOIN settlement_authorizations sa ON sa.agency_order_id=sp.agency_order_id
		LEFT JOIN chain_transactions chain_tx
		  ON chain_tx.chain_id=sco.chain_id
		 AND chain_tx.tx_hash=sco.tx_hash
		LEFT JOIN payment_mo_compensations compensation
		  ON compensation.id=sco.mo_compensation_id
		WHERE sco.state IN ('PLANNED','NONCE_RESERVED','SIGNED','BROADCAST')
		  AND sco.purpose IN ('COMPLETE','REFUND_PARTIAL')
		  AND (sco.purpose<>'REFUND_PARTIAL' OR compensation.state IN (
		      'APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN'
		  ))
		  AND (
		      sco.state <> 'BROADCAST'
		      OR (chain_tx.state IN ('SUBMITTED','SAFE') AND
		          chain_tx.next_observation_at <= $2)
		  )
		ORDER BY sco.planned_at, sco.purpose
		LIMIT $1
	`, limit, dueAt)
	if err != nil {
		return nil, fmt.Errorf("list settlement command outbox: %w", err)
	}
	defer rows.Close()
	commands := []settlementapp.SettlementCommand{}
	for rows.Next() {
		command, scanErr := scanCommand(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		commands = append(commands, command)
	}
	return commands, rows.Err()
}

func (r *Repository) ReserveCommandNonce(
	ctx context.Context,
	command settlementapp.SettlementCommand,
	pendingNonce uint64,
	now time.Time,
) (settlementapp.SettlementCommand, error) {
	if pendingNonce > math.MaxInt64 {
		return settlementapp.SettlementCommand{}, fmt.Errorf("pending signer nonce exceeds PostgreSQL BIGINT")
	}
	var reserved settlementapp.SettlementCommand
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		lockKey := fmt.Sprintf(
			"settlement-command:%d:%s",
			command.ChainID, strings.ToLower(command.SignerAddress),
		)
		if _, lockErr := r.database.Queryer(txContext).ExecContext(txContext, `
			SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
		`, lockKey); lockErr != nil {
			return fmt.Errorf("lock signer nonce allocator: %w", lockErr)
		}
		var state settlementapp.CommandState
		var signerAddress string
		if lockErr := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT state, signer_address
			FROM settlement_command_outbox
			WHERE id=$1
			FOR UPDATE
		`, command.CommandID).Scan(&state, &signerAddress); lockErr != nil {
			return lockErr
		}
		if state != settlementapp.CommandPlanned ||
			!strings.EqualFold(signerAddress, command.SignerAddress) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		var maximum sql.NullInt64
		if maxErr := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT MAX(signer_nonce)
			FROM settlement_command_outbox
			WHERE chain_id=$1 AND lower(signer_address)=lower($2)
			  AND signer_nonce IS NOT NULL
		`, command.ChainID, command.SignerAddress).Scan(&maximum); maxErr != nil {
			return maxErr
		}
		nextNonce := int64(pendingNonce)
		if maximum.Valid && maximum.Int64 >= nextNonce {
			if maximum.Int64 == math.MaxInt64 {
				return fmt.Errorf("signer nonce allocator exhausted")
			}
			nextNonce = maximum.Int64 + 1
		}
		row := r.database.Queryer(txContext).QueryRowContext(txContext, `
			UPDATE settlement_command_outbox
			SET state='NONCE_RESERVED', signer_nonce=$1, updated_at=$2
			WHERE id=$3 AND state='PLANNED'
			RETURNING id, settlement_payment_id, chain_id, purpose, signer_address,
			          order_hash, COALESCE(fulfillment_hash,''), state,
			          signer_nonce, COALESCE(tx_hash,''), raw_transaction, '', '',
			          COALESCE(mo_compensation_id::text,''), COALESCE(refund_key,''),
			          COALESCE(pass_through_part::text,''), COALESCE(fee_part::text,'')
		`, nextNonce, now, command.CommandID)
		var scanErr error
		reserved, scanErr = scanCommand(row)
		if reserved.Purpose != settlementapp.CommandRefundPartial {
			// 전액 환불 gross는 outbox가 아니라 authorization payload에서
			// 오므로 입력 Effect의 값을 그대로 유지한다.
			reserved.PassThroughAmount = command.PassThroughAmount
			reserved.FeeAmount = command.FeeAmount
		}
		return scanErr
	})
	if err != nil {
		return settlementapp.SettlementCommand{}, fmt.Errorf("reserve signer nonce: %w", err)
	}
	return reserved, nil
}

func (r *Repository) RecordCommandSigned(
	ctx context.Context,
	command settlementapp.SettlementCommand,
	txHash string,
	rawTransaction []byte,
	now time.Time,
) error {
	if strings.TrimSpace(txHash) == "" || len(rawTransaction) == 0 {
		return fmt.Errorf("signed transaction hash and bytes are required")
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE settlement_command_outbox
		SET state='SIGNED', tx_hash=lower($1), raw_transaction=$2,
		    signed_at=$3, updated_at=$3
		WHERE id=$4 AND state='NONCE_RESERVED' AND signer_nonce=$5
	`, txHash, rawTransaction, now, command.CommandID, command.SignerNonce)
	if err != nil {
		return fmt.Errorf("record signed settlement command: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return settlementdomain.ErrPaymentStateInvalid
	}
	return nil
}

func (r *Repository) RecordCommandBroadcast(
	ctx context.Context,
	command settlementapp.SettlementCommand,
	attempted bool,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var chainID uint64
		err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT chain_id
			FROM settlement_payments
			WHERE id=$1
		`, command.PaymentID).Scan(&chainID)
		if errors.Is(err, sql.ErrNoRows) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if err != nil {
			return fmt.Errorf("load settlement command payment: %w", err)
		}

		// Keep the same chain_transactions -> settlement_payments lock order as
		// finalized-event projection and transaction reconciliation. A command
		// can be observed as finalized while this recovery write is running, so
		// taking these locks in the opposite order creates a deterministic
		// deadlock at the finality boundary.
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			INSERT INTO chain_transactions(
				chain_id, tx_hash, settlement_payment_id, purpose, state,
				from_address, next_observation_at, submitted_at, updated_at
			) VALUES ($1,lower($2),$3,$4,'SUBMITTED',NULLIF(lower($6),''),$5,$5,$5)
			ON CONFLICT (chain_id, tx_hash) DO UPDATE SET
				settlement_payment_id=EXCLUDED.settlement_payment_id,
				purpose=EXCLUDED.purpose,
				state=CASE WHEN chain_transactions.state='FINALIZED'
				           THEN 'FINALIZED' ELSE chain_transactions.state END,
				from_address=COALESCE(
					chain_transactions.from_address, EXCLUDED.from_address
				),
				updated_at=EXCLUDED.updated_at
		`, chainID, command.TxHash, command.PaymentID, command.Purpose, now,
			command.SignerAddress); err != nil {
			return fmt.Errorf("record settlement command transaction: %w", err)
		}

		var agencyOrderID, paymentState string
		switch command.Purpose {
		case settlementapp.CommandComplete:
			err = r.database.Queryer(txContext).QueryRowContext(txContext, `
				UPDATE settlement_payments
				SET complete_tx_hash=COALESCE(complete_tx_hash, lower($1)),
				    state=CASE WHEN state='FINALIZED'
				               THEN 'COMPLETION_SUBMITTED' ELSE state END,
				    updated_at=$2
				WHERE id=$3 AND refund_tx_hash IS NULL
				  AND state IN ('FINALIZED','COMPLETION_SUBMITTED','COMPLETED')
				  AND (complete_tx_hash IS NULL OR lower(complete_tx_hash)=lower($1))
				RETURNING chain_id, agency_order_id, state
			`, command.TxHash, now, command.PaymentID).Scan(
				&chainID, &agencyOrderID, &paymentState,
			)
		case settlementapp.CommandRefundPartial:
			// 부분환불은 payment의 전액 상태기계를 건드리지 않는다 — 누적은
			// contract 카운터와 payment 모듈 원장이 소유하고, finality가
			// 환불 행만 닫는다.
			err = r.database.Queryer(txContext).QueryRowContext(txContext, `
				SELECT sp.chain_id, sp.agency_order_id, sp.state
				FROM settlement_payments sp
				WHERE sp.id=$1
			`, command.PaymentID).Scan(
				&chainID, &agencyOrderID, &paymentState,
			)
		default:
			return fmt.Errorf("unsupported settlement command purpose %q", command.Purpose)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if err != nil {
			return fmt.Errorf("record settlement command payment state: %w", err)
		}

		outboxState := settlementapp.CommandBroadcast
		// 부분환불의 payment 상태는 이 Effect의 종결과 무관하다(이미 COMPLETED일
		// 수 있다) — outbox FINALIZED는 오직 자기 tx의 finality 이벤트가 건다.
		if command.Purpose == settlementapp.CommandComplete && paymentState == "COMPLETED" {
			outboxState = settlementapp.CommandFinalized
		}
		result, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE settlement_command_outbox
			SET state=CASE WHEN state='FINALIZED' THEN 'FINALIZED' ELSE $1 END,
			    attempt_count=CASE WHEN $2 THEN attempt_count+1
			                       ELSE GREATEST(attempt_count,1) END,
			    broadcast_at=COALESCE(broadcast_at,$3),
			    reconciled_at=CASE WHEN $1='FINALIZED' THEN $3 ELSE reconciled_at END,
			    updated_at=$3
			WHERE id=$4
			  AND state IN ('SIGNED','BROADCAST','FINALIZED')
			  AND lower(tx_hash)=lower($5)
		`, outboxState, attempted, now, command.CommandID, command.TxHash)
		if err != nil {
			return fmt.Errorf("mark settlement command broadcast: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return settlementdomain.ErrPaymentStateInvalid
		}
		return emitSettlementEvent(txContext, r.database.Queryer(txContext), command.PaymentID, now)
	})
}

func (r *Repository) RecordCommandConflict(
	ctx context.Context,
	command settlementapp.SettlementCommand,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		result, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE settlement_command_outbox
			SET state='CONFLICT', last_error_code=$1,
			    reconciled_at=$2, updated_at=$2
			WHERE id=$3
			  AND state IN ('NONCE_RESERVED','SIGNED','BROADCAST')
		`, reason, now, command.CommandID)
		if err != nil {
			return fmt.Errorf("record settlement command conflict: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return settlementdomain.ErrPaymentStateInvalid
		}
		if command.Purpose != settlementapp.CommandRefundPartial ||
			strings.TrimSpace(command.TxHash) == "" {
			return nil
		}
		if err := r.transitionMOCompensationOutcome(
			txContext, command.PaymentID, command.TxHash,
			"OUTCOME_UNKNOWN", "RELEASE_UNKNOWN", now,
		); err != nil {
			return err
		}
		var agencyOrderID string
		if err := r.database.Queryer(txContext).QueryRowContext(txContext, `
			SELECT agency_order_id::text FROM settlement_payments WHERE id=$1
		`, command.PaymentID).Scan(&agencyOrderID); err != nil {
			return err
		}
		return emitSettlementRefundConflict(txContext,
			r.database.Queryer(txContext), command.PaymentID,
			agencyOrderID, reason, now)
	})
}

type commandScanner interface {
	Scan(...any) error
}

func scanCommand(scanner commandScanner) (settlementapp.SettlementCommand, error) {
	var command settlementapp.SettlementCommand
	var nonce sql.NullInt64
	var passPart, feePart string
	if err := scanner.Scan(
		&command.CommandID, &command.PaymentID, &command.ChainID, &command.Purpose,
		&command.SignerAddress, &command.OrderHash, &command.FulfillmentHash,
		&command.State, &nonce, &command.TxHash, &command.RawTransaction,
		&command.PassThroughAmount, &command.FeeAmount,
		&command.MOCompensationID, &command.RefundKey, &passPart, &feePart,
	); err != nil {
		return settlementapp.SettlementCommand{}, fmt.Errorf("scan settlement command: %w", err)
	}
	if nonce.Valid {
		if nonce.Int64 < 0 {
			return settlementapp.SettlementCommand{}, fmt.Errorf("negative signer nonce")
		}
		command.SignerNonce = uint64(nonce.Int64)
	}
	// REFUND_PARTIAL의 금액은 authorization payload가 아니라 outbox의 immutable
	// MO allocation parts가 소유한다.
	if command.Purpose == settlementapp.CommandRefundPartial {
		command.PassThroughAmount = passPart
		command.FeeAmount = feePart
	}
	return command, nil
}

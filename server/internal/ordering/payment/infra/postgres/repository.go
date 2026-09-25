// Package postgres는 Payment Bounded Context의 영속성을 소유한다.
// AgencyOrder가 소유한 instruction/unit 테이블은 기존 Settlement direct-consumer
// 패턴대로 읽기 전용으로 소비한다(ADR-0050 §1 — 기존 구조 합류).
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct{ database *sharedpostgres.Database }

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

// paymentLive는 아직 죽지 않은 결제 상태 집합이다. open-unique index와 일치한다.
const paymentLive = `('CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN')`
const paymentOpen = `('CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN',
	'AUTHORIZED','PARTIALLY_CAPTURED','CAPTURED')`

func uniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

func (r *Repository) GetPayableInstruction(
	ctx context.Context,
	userID, agencyOrderID string,
) (domain.PayableInstruction, error) {
	var value domain.PayableInstruction
	var currency string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT i.agency_order_id::text, i.user_id::text, i.rail,
		       i.provider_environment, i.asset, i.economic_effect,
		       i.merchant_execution_mode, i.execution_profile_hash,
		       i.agency_order_snapshot_hash, i.amount_minor, i.currency, i.expires_at
		FROM agency_order_payment_instructions i
		JOIN agency_orders orders
		  ON orders.id=i.agency_order_id
		 AND orders.payment_rail=i.rail
		 AND orders.provider_environment=i.provider_environment
		 AND orders.asset=i.asset
		 AND orders.economic_effect=i.economic_effect
		 AND orders.merchant_execution_mode=i.merchant_execution_mode
		 AND orders.execution_profile_hash=i.execution_profile_hash
		WHERE i.agency_order_id=$1 AND i.user_id=$2
	`, strings.TrimSpace(agencyOrderID), strings.TrimSpace(userID)).Scan(
		&value.AgencyOrderID, &value.UserID, &value.Rail, &value.ProviderEnvironment,
		&value.Asset, &value.EconomicEffect, &value.MerchantExecutionMode,
		&value.ExecutionProfileHash,
		&value.SnapshotHash, &value.CustomerPayableMinor, &currency, &value.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PayableInstruction{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PayableInstruction{}, fmt.Errorf("get payable instruction: %w", err)
	}
	value.Currency = strings.TrimSpace(currency)
	return value, nil
}

const paymentColumns = `
	payment.id::text, payment.agency_order_id::text, payment.user_id::text,
	payment.rail, payment.provider_environment, payment.asset, payment.economic_effect,
	payment.merchant_execution_mode, payment.execution_profile_hash,
	payment.amount_minor, payment.currency, payment.state,
	COALESCE(payment.last_reason_code,''),
	payment.version, payment.created_at, payment.updated_at`

const attemptColumns = `
	attempt.id::text, attempt.customer_payment_id::text, attempt.sequence, attempt.state,
	COALESCE(attempt.paypal_order_id,''), COALESCE(attempt.approval_url,''),
	attempt.return_nonce, COALESCE(attempt.last_reason_code,''), attempt.version,
	attempt.created_at, attempt.updated_at`

func scanPaymentAttempt(scanner interface{ Scan(...any) error }) (
	domain.CustomerPayment, domain.PayPalAttempt, error,
) {
	var payment domain.CustomerPayment
	var attempt domain.PayPalAttempt
	err := scanner.Scan(
		&payment.ID, &payment.AgencyOrderID, &payment.UserID,
		&payment.Rail, &payment.ProviderEnvironment, &payment.Asset, &payment.EconomicEffect,
		&payment.MerchantExecutionMode, &payment.ExecutionProfileHash,
		&payment.AmountMinor, &payment.Currency, &payment.State,
		&payment.LastReasonCode,
		&payment.Version, &payment.CreatedAt, &payment.UpdatedAt,
		&attempt.ID, &attempt.CustomerPaymentID, &attempt.Sequence, &attempt.State,
		&attempt.PayPalOrderID, &attempt.ApprovalURL,
		&attempt.ReturnNonce, &attempt.LastReasonCode, &attempt.Version,
		&attempt.CreatedAt, &attempt.UpdatedAt,
	)
	payment.Currency = strings.TrimSpace(payment.Currency)
	return payment, attempt, err
}

const paymentAttemptJoin = `
	FROM payment_customer_payments payment
	JOIN payment_paypal_attempts attempt
	  ON attempt.customer_payment_id=payment.id
	 AND attempt.sequence=(
	     SELECT max(inner_attempt.sequence) FROM payment_paypal_attempts inner_attempt
	     WHERE inner_attempt.customer_payment_id=payment.id
	 )`

func (r *Repository) GetOpenPayment(
	ctx context.Context,
	userID, agencyOrderID string,
) (domain.CustomerPayment, domain.PayPalAttempt, bool, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+paymentColumns+`, `+attemptColumns+paymentAttemptJoin+`
		WHERE payment.agency_order_id=$1 AND payment.user_id=$2
		  AND payment.state IN `+paymentOpen+`
	`, strings.TrimSpace(agencyOrderID), strings.TrimSpace(userID))
	payment, attempt, err := scanPaymentAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false, nil
	}
	if err != nil {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false,
			fmt.Errorf("get open payment: %w", err)
	}
	return payment, attempt, true, nil
}

func (r *Repository) CreatePaymentWithAttempt(
	ctx context.Context,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
	operation domain.ExternalOperation,
) error {
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var instructionState string
		if err := q.QueryRowContext(tx, `
			SELECT state
			FROM agency_order_payment_instructions
			WHERE agency_order_id=$1 AND user_id=$2
			FOR UPDATE
		`, payment.AgencyOrderID, payment.UserID).Scan(&instructionState); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrInstructionNotConsumable
			}
			return err
		}
		if instructionState != "PENDING" {
			return domain.ErrInstructionNotConsumable
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_customer_payments(
				id,agency_order_id,user_id,rail,provider_environment,asset,
				economic_effect,merchant_execution_mode,execution_profile_hash,
				amount_minor,currency,state,last_reason_code,
				version,created_at,updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL,1,$13,$13)
		`, payment.ID, payment.AgencyOrderID, payment.UserID, payment.Rail,
			payment.ProviderEnvironment, payment.Asset, payment.EconomicEffect,
			payment.MerchantExecutionMode, payment.ExecutionProfileHash,
			payment.AmountMinor, payment.Currency, string(payment.State),
			payment.CreatedAt); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_paypal_attempts(
				id,customer_payment_id,sequence,state,return_nonce,version,
				created_at,updated_at
			) VALUES($1,$2,$3,$4,$5,1,$6,$6)
		`, attempt.ID, attempt.CustomerPaymentID, attempt.Sequence,
			string(attempt.State), attempt.ReturnNonce, attempt.CreatedAt); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_external_operations(
				id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
				created_at,updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)
		`, operation.ID, string(operation.Purpose), operation.OwnerKind, operation.OwnerID,
			operation.IdempotencyKey, operation.RequestHash, string(operation.State),
			payment.CreatedAt); err != nil {
			return err
		}
		return EmitCustomerPaymentEvent(tx, q, payment.ID, payment.CreatedAt)
	})
	if uniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

func (r *Repository) GetAttemptByNonce(
	ctx context.Context,
	nonce string,
) (domain.CustomerPayment, domain.PayPalAttempt, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+paymentColumns+`, `+attemptColumns+`
		FROM payment_paypal_attempts attempt
		JOIN payment_customer_payments payment ON payment.id=attempt.customer_payment_id
		WHERE attempt.return_nonce=$1
	`, strings.TrimSpace(nonce))
	payment, attempt, err := scanPaymentAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, fmt.Errorf("get attempt by nonce: %w", err)
	}
	return payment, attempt, nil
}

func (r *Repository) FindByPayPalOrder(
	ctx context.Context,
	paypalOrderID string,
) (domain.CustomerPayment, domain.PayPalAttempt, bool, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+paymentColumns+`, `+attemptColumns+`
		FROM payment_paypal_attempts attempt
		JOIN payment_customer_payments payment ON payment.id=attempt.customer_payment_id
		WHERE attempt.paypal_order_id=$1
	`, strings.TrimSpace(paypalOrderID))
	payment, attempt, err := scanPaymentAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false, nil
	}
	if err != nil {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false,
			fmt.Errorf("find by paypal order: %w", err)
	}
	return payment, attempt, true, nil
}

func (r *Repository) MarkOperationSent(
	ctx context.Context,
	operation domain.ExternalOperation,
	attemptID string,
	attemptState domain.AttemptState,
	paymentID string,
	paymentState domain.PaymentState,
	firstSentAt, idempotencyDeadline time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		result, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SENT',first_sent_at=COALESCE(first_sent_at,$6),
			    idempotency_deadline=COALESCE(idempotency_deadline,$7),updated_at=$6
			WHERE purpose=$1 AND owner_kind=$2 AND owner_id=$3
			  AND idempotency_key=$4 AND request_hash=$5
			  AND (
			      state IN ('PREPARED','UNKNOWN')
			      OR (state='SENT' AND updated_at<=$6::timestamptz-INTERVAL '2 minutes')
			  )
			  AND (idempotency_deadline IS NULL OR idempotency_deadline>$6)
		`, string(operation.Purpose), operation.OwnerKind, operation.OwnerID,
			operation.IdempotencyKey, operation.RequestHash,
			firstSentAt, idempotencyDeadline)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			var deadline sql.NullTime
			var resourceID sql.NullString
			if queryErr := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT idempotency_deadline,provider_resource_id
				FROM payment_external_operations
				WHERE purpose=$1 AND owner_kind=$2 AND owner_id=$3
				  AND idempotency_key=$4 AND request_hash=$5
			`, string(operation.Purpose), operation.OwnerKind, operation.OwnerID,
				operation.IdempotencyKey, operation.RequestHash).Scan(
				&deadline, &resourceID,
			); queryErr == nil && deadline.Valid && !firstSentAt.Before(deadline.Time) &&
				(!resourceID.Valid || strings.TrimSpace(resourceID.String) == "") {
				return domain.ErrIdempotencyExpired
			}
			return domain.ErrConflict
		}
		return r.updateAttemptPayment(tx, attemptID, attemptState, paymentID, paymentState, "", firstSentAt)
	})
}

func (r *Repository) RecordOrderCreated(
	ctx context.Context,
	attemptID, paymentID, paypalOrderID, approvalURL string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SUCCEEDED', provider_resource_id=$2, resolved_at=$3, updated_at=$3
			WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id=$1
			  AND purpose='PAYPAL_ORDER_CREATE' AND state IN ('PREPARED','SENT','UNKNOWN')
		`, attemptID, paypalOrderID, now); err != nil {
			return err
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE payment_paypal_attempts
			SET state='PAYER_ACTION_REQUIRED', paypal_order_id=$2,
			    approval_url=CASE WHEN $3<>'' THEN $3 ELSE approval_url END,
			    version=version+1, updated_at=$4
			WHERE id=$1
			  AND (paypal_order_id IS NULL OR paypal_order_id=$2)
			  AND state IN ('ORDER_PREPARED','ORDER_CREATE_SUBMITTED','ORDER_CREATE_UNKNOWN',
			                'PAYER_ACTION_REQUIRED')
		`, attemptID, paypalOrderID, approvalURL, now); err != nil {
			return err
		}
		return r.updatePayment(tx, paymentID, domain.PaymentActionRequired, "", now)
	})
}

func (r *Repository) RecordAttemptOutcome(
	ctx context.Context,
	attemptID string,
	attemptState domain.AttemptState,
	paymentID string,
	paymentState domain.PaymentState,
	purpose domain.OperationPurpose,
	operationState domain.OperationState,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if purpose != "" && operationState != "" {
			if _, err := r.database.Queryer(tx).ExecContext(tx, `
				UPDATE payment_external_operations
				SET state=$3, last_reason_code=$4,
				    resolved_at=CASE WHEN $3 IN ('SUCCEEDED','FAILED','CANCELLED')
				                THEN $5 ELSE resolved_at END,
				    updated_at=$5
				WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id=$1 AND purpose=$2
				  AND state IN ('PREPARED','SENT','UNKNOWN')
			`, attemptID, string(purpose), string(operationState), reason, now); err != nil {
				return err
			}
		}
		return r.updateAttemptPayment(tx, attemptID, attemptState, paymentID, paymentState, reason, now)
	})
}

func (r *Repository) MarkPayerApproved(
	ctx context.Context,
	attemptID, paymentID string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE payment_paypal_attempts
			SET state='PAYER_APPROVED', version=version+1, updated_at=$2
			WHERE id=$1 AND state IN
				('PAYER_ACTION_REQUIRED','CANCELLED_BY_USER')
		`, attemptID, now); err != nil {
			return err
		}
		return r.updatePayment(tx, paymentID, domain.PaymentProcessing, "", now)
	})
}

func (r *Repository) ListReconcileDue(
	ctx context.Context,
	limit int,
	now time.Time,
) ([]paymentapp.ReconcileItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+paymentColumns+`, `+attemptColumns+paymentAttemptJoin+`
			WHERE payment.state IN (
				'CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN'
			)
			  AND payment.updated_at <= $2::timestamptz - INTERVAL '60 seconds'
		ORDER BY payment.updated_at
		LIMIT $1
	`, limit, now)
	if err != nil {
		return nil, fmt.Errorf("list reconcile due: %w", err)
	}
	defer rows.Close()
	items := make([]paymentapp.ReconcileItem, 0)
	for rows.Next() {
		payment, attempt, err := scanPaymentAttempt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, paymentapp.ReconcileItem{Payment: payment, Attempt: attempt})
	}
	return items, rows.Err()
}

func (r *Repository) ListPaymentReconciliations(
	ctx context.Context,
	limit int,
) ([]paymentapp.PaymentReconciliationItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT payment.id::text, payment.agency_order_id::text,
		       attempt.id::text, COALESCE(attempt.paypal_order_id,''),
		       payment.provider_environment,
		       payment.amount_minor, payment.currency, payment.state, attempt.state,
		       COALESCE(payment.last_reason_code, attempt.last_reason_code, ''),
		       payment.updated_at
		FROM payment_customer_payments payment
		JOIN payment_paypal_attempts attempt
		  ON attempt.customer_payment_id=payment.id
		WHERE payment.state='OUTCOME_UNKNOWN'
		ORDER BY payment.updated_at DESC, payment.id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list payment reconciliations: %w", err)
	}
	defer rows.Close()
	items := make([]paymentapp.PaymentReconciliationItem, 0)
	for rows.Next() {
		var item paymentapp.PaymentReconciliationItem
		if err := rows.Scan(
			&item.PaymentID, &item.AgencyOrderID, &item.PayPalAttemptID,
			&item.PayPalOrderID, &item.ProviderEnvironment,
			&item.AmountMinor, &item.Currency, &item.PaymentState,
			&item.AttemptState, &item.ReasonCode, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) CountPaymentReconciliations(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM payment_customer_payments
		WHERE state='OUTCOME_UNKNOWN'
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count payment reconciliations: %w", err)
	}
	return count, nil
}

// GetSucceededPaymentID는 주문의 확정 수납 payment id다 — process Effect
// (FAILURE trigger)의 실행 입력(ADR-0056: 종전 ListTerminalCandidates
// cross-context 스캔의 대체).
func (r *Repository) GetBinding(
	ctx context.Context,
	environment string,
) (domain.AccountBinding, bool, error) {
	var value domain.AccountBinding
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT environment, merchant_id, client_id_fingerprint, webhook_id,
		       verified_by, evidence_note, verified_at
		FROM paypal_account_bindings
		WHERE environment=$1
	`, strings.TrimSpace(environment)).Scan(
		&value.Environment, &value.MerchantID, &value.ClientIDFingerprint,
		&value.WebhookID, &value.VerifiedBy, &value.EvidenceNote, &value.VerifiedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountBinding{}, false, nil
	}
	if err != nil {
		return domain.AccountBinding{}, false, fmt.Errorf("get paypal binding: %w", err)
	}
	return value, true, nil
}

func (r *Repository) UpsertBinding(
	ctx context.Context,
	binding domain.AccountBinding,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO paypal_account_bindings(
			environment,merchant_id,client_id_fingerprint,webhook_id,
			verified_by,evidence_note,verified_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT (environment) DO UPDATE SET
			merchant_id=EXCLUDED.merchant_id,
			client_id_fingerprint=EXCLUDED.client_id_fingerprint,
			webhook_id=EXCLUDED.webhook_id,
			verified_by=EXCLUDED.verified_by,
			evidence_note=EXCLUDED.evidence_note,
			verified_at=EXCLUDED.verified_at,
			updated_at=EXCLUDED.updated_at
	`, binding.Environment, binding.MerchantID, binding.ClientIDFingerprint,
		binding.WebhookID, binding.VerifiedBy, binding.EvidenceNote,
		binding.VerifiedAt, now)
	if err != nil {
		return fmt.Errorf("upsert paypal binding: %w", err)
	}
	return nil
}

func (r *Repository) InsertWebhookEvent(
	ctx context.Context,
	event domain.WebhookEvent,
) (bool, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO payment_paypal_webhook_inbox(
			id,environment,webhook_id,event_id,event_type,transmission_id,
			resource_kind,resource_id,received_at
		) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8)
		ON CONFLICT (environment, webhook_id, event_id) DO NOTHING
	`, event.Environment, event.WebhookID, event.EventID, event.EventType,
		event.TransmissionID, event.ResourceKind, event.ResourceID, event.ReceivedAt)
	if err != nil {
		return false, fmt.Errorf("insert webhook event: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

func (r *Repository) MarkWebhookProcessed(
	ctx context.Context,
	environment, webhookID, eventID string,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE payment_paypal_webhook_inbox
		SET processed_at=$4
		WHERE environment=$1 AND webhook_id=$2 AND event_id=$3 AND processed_at IS NULL
	`, environment, webhookID, eventID, now)
	if err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	return nil
}

// updateAttemptPayment는 이미 terminal인 row를 덮지 않는 공통 전이다.
func (r *Repository) updateAttemptPayment(
	tx context.Context,
	attemptID string,
	attemptState domain.AttemptState,
	paymentID string,
	paymentState domain.PaymentState,
	reason string,
	now time.Time,
) error {
	if attemptState != "" {
		// CANCELLED_BY_USER는 재개 가능하므로 terminal 가드에 넣지 않는다.
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE payment_paypal_attempts
			SET state=$2, last_reason_code=NULLIF($3,''), version=version+1, updated_at=$4
			WHERE id=$1 AND state NOT IN (
				'CANCELLED_BEFORE_CREATE','ORDER_CREATE_FAILED',
				'EXPIRED','APPROVAL_REVERSED','SUPERSEDED_BEFORE_AUTHORIZE',
				'ABANDONED_BEFORE_AUTHORIZE','AUTHORIZE_COMPLETED',
				'AUTHORIZE_DECLINED','AUTHORIZE_FAILED'
			)
		`, attemptID, string(attemptState), reason, now); err != nil {
			return err
		}
	}
	return r.updatePayment(tx, paymentID, paymentState, reason, now)
}

func (r *Repository) updatePayment(
	tx context.Context,
	paymentID string,
	paymentState domain.PaymentState,
	reason string,
	now time.Time,
) error {
	if paymentState == "" {
		return nil
	}
	if _, err := r.database.Queryer(tx).ExecContext(tx, `
		UPDATE payment_customer_payments
		SET state=$2, last_reason_code=NULLIF($3,''), version=version+1, updated_at=$4
		WHERE id=$1 AND state IN `+paymentLive+`
	`, paymentID, string(paymentState), reason, now); err != nil {
		return err
	}
	return EmitCustomerPaymentEvent(tx, r.database.Queryer(tx), paymentID, now)
}

func requestKeyHash(key string, amount int64) string {
	// 짧은 결정적 hash — canonical request는 (key, amount)로 유일하다.
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d", key, amount))
	return hex.EncodeToString(sum[:])
}

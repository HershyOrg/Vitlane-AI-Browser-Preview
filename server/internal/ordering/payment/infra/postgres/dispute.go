package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

const disputeCaseColumns = `
	dispute.id::text, dispute.environment, dispute.dispute_id,
	dispute.agency_order_id::text, dispute.customer_payment_id::text,
	dispute.mo_cash_receipt_id::text, dispute.capture_id, dispute.state,
	dispute.provider_status, dispute.outcome, dispute.reason,
	dispute.lifecycle_stage, dispute.seller_response_due_at,
	dispute.latest_event_id, dispute.latest_event_type, dispute.opened_at,
	dispute.last_observed_at, dispute.resolved_at, dispute.version,
	dispute.created_at, dispute.updated_at`

func scanDisputeCase(scanner interface{ Scan(...any) error }) (domain.PayPalDisputeCase, error) {
	var item domain.PayPalDisputeCase
	var sellerDue, resolvedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.Environment, &item.DisputeID,
		&item.AgencyOrderID, &item.CustomerPaymentID, &item.MOCashReceiptID,
		&item.CaptureID, &item.State, &item.ProviderStatus, &item.Outcome,
		&item.Reason, &item.LifecycleStage, &sellerDue,
		&item.LatestEventID, &item.LatestEventType, &item.OpenedAt,
		&item.LastObservedAt, &resolvedAt, &item.Version,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if sellerDue.Valid {
		item.SellerResponseDueAt = &sellerDue.Time
	}
	if resolvedAt.Valid {
		item.ResolvedAt = &resolvedAt.Time
	}
	return item, err
}

func (r *Repository) ProcessDisputeWebhook(
	ctx context.Context,
	event domain.WebhookEvent,
	observation domain.DisputeObservation,
	caseID string,
) (domain.PayPalDisputeCase, bool, error) {
	if event.Environment != observation.Environment || event.EventID != observation.EventID ||
		event.EventType != observation.EventType || strings.TrimSpace(caseID) == "" {
		return domain.PayPalDisputeCase{}, false, domain.ErrDisputeInvalid
	}
	var item domain.PayPalDisputeCase
	var applied bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		result, err := q.ExecContext(tx, `
			INSERT INTO payment_paypal_webhook_inbox(
				id,environment,webhook_id,event_id,event_type,transmission_id,
				resource_kind,resource_id,received_at
			) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8)
			ON CONFLICT (environment, webhook_id, event_id) DO NOTHING
		`, event.Environment, event.WebhookID, event.EventID, event.EventType,
			event.TransmissionID, event.ResourceKind, event.ResourceID, event.ReceivedAt)
		if err != nil {
			return fmt.Errorf("insert dispute webhook inbox: %w", err)
		}
		inserted, _ := result.RowsAffected()
		if inserted == 0 {
			var processedAt sql.NullTime
			if err := q.QueryRowContext(tx, `
				SELECT processed_at FROM payment_paypal_webhook_inbox
				WHERE environment=$1 AND webhook_id=$2 AND event_id=$3
				FOR UPDATE
			`, event.Environment, event.WebhookID, event.EventID).Scan(&processedAt); err != nil {
				return fmt.Errorf("lock duplicate dispute webhook: %w", err)
			}
			if processedAt.Valid {
				var findErr error
				item, findErr = r.getPayPalDisputeByProviderID(
					tx, event.Environment, observation.DisputeID, false,
				)
				if findErr != nil {
					return findErr
				}
				applied = false
				return nil
			}
		}

		current, err := r.getPayPalDisputeByProviderID(
			tx, observation.Environment, observation.DisputeID, true,
		)
		switch {
		case err == nil:
			if observation.CaptureID != "" && observation.CaptureID != current.CaptureID {
				return domain.ErrInstructionMismatch
			}
			if _, err := q.ExecContext(tx,
				`SELECT 1 FROM payment_mo_cash_receipts WHERE id=$1 FOR UPDATE`,
				current.MOCashReceiptID,
			); err != nil {
				return fmt.Errorf("lock dispute receipt: %w", err)
			}
			item, err = r.applyDisputeObservation(tx, current, observation, event.ReceivedAt)
			if err != nil {
				return err
			}
			if err := EmitDisputeEvent(tx, q, item.ID, "", event.ReceivedAt); err != nil {
				return err
			}
		case errors.Is(err, domain.ErrDisputeNotFound):
			if observation.CaptureID == "" {
				var otherEnvironment string
				otherErr := q.QueryRowContext(tx, `
					SELECT environment FROM payment_paypal_dispute_cases
					WHERE dispute_id=$1 AND environment<>$2 LIMIT 1
				`, observation.DisputeID, observation.Environment).Scan(&otherEnvironment)
				if otherErr == nil {
					return domain.ErrInstructionMismatch
				}
				if !errors.Is(otherErr, sql.ErrNoRows) {
					return fmt.Errorf("check cross-environment dispute id: %w", otherErr)
				}
				return domain.ErrDisputeNotFound
			}
			var receiptID, paymentID, agencyOrderID string
			findErr := q.QueryRowContext(tx, `
				SELECT receipt.id::text, receipt.customer_payment_id::text,
				       receipt.agency_order_id::text
				FROM payment_mo_cash_receipts receipt
				JOIN payment_customer_payments payment
				  ON payment.id=receipt.customer_payment_id
				 AND payment.agency_order_id=receipt.agency_order_id
				 AND payment.provider_environment=receipt.provider_environment
				WHERE receipt.provider_environment=$1 AND receipt.provider_capture_id=$2
				  AND receipt.kind='PAYPAL_CAPTURE'
				  AND payment.rail='PAYPAL'
				FOR UPDATE OF receipt
			`, observation.Environment, observation.CaptureID).Scan(
				&receiptID, &paymentID, &agencyOrderID,
			)
			if errors.Is(findErr, sql.ErrNoRows) {
				var otherEnvironment string
				otherErr := q.QueryRowContext(tx, `
					SELECT provider_environment FROM payment_mo_cash_receipts
					WHERE provider_capture_id=$1 AND provider_environment<>$2
					LIMIT 1
				`, observation.CaptureID, observation.Environment).Scan(&otherEnvironment)
				if otherErr == nil {
					return domain.ErrInstructionMismatch
				}
				if !errors.Is(otherErr, sql.ErrNoRows) {
					return fmt.Errorf("check cross-environment dispute capture: %w", otherErr)
				}
				return domain.ErrDisputeNotFound
			}
			if findErr != nil {
				return fmt.Errorf("resolve dispute capture: %w", findErr)
			}
			state := observation.State()
			var resolvedAt any
			if state == domain.DisputeResolved {
				resolvedAt = observation.ObservedAt
			}
			if _, err := q.ExecContext(tx, `
				INSERT INTO payment_paypal_dispute_cases(
					id,environment,dispute_id,agency_order_id,customer_payment_id,
					mo_cash_receipt_id,capture_id,state,provider_status,outcome,reason,
					lifecycle_stage,seller_response_due_at,latest_event_id,
					latest_event_type,opened_at,last_observed_at,resolved_at,
					version,created_at,updated_at
				) VALUES(
					$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
					$16,$16,$17,1,$18,$18
				)
			`, caseID, observation.Environment, observation.DisputeID,
				agencyOrderID, paymentID, receiptID, observation.CaptureID,
				string(state), string(observation.ProviderStatus), string(observation.Outcome),
				observation.Reason, string(observation.LifecycleStage),
				observation.SellerResponseDueAt, observation.EventID, observation.EventType,
				observation.ObservedAt, resolvedAt, event.ReceivedAt); err != nil {
				if uniqueViolation(err) {
					return domain.ErrConflict
				}
				return fmt.Errorf("insert PayPal dispute case: %w", err)
			}
			item, err = r.getPayPalDisputeByProviderID(
				tx, observation.Environment, observation.DisputeID, false,
			)
			if err != nil {
				return err
			}
			if err := EmitDisputeEvent(tx, q, item.ID, "", event.ReceivedAt); err != nil {
				return err
			}
		default:
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_paypal_webhook_inbox SET processed_at=$4
			WHERE environment=$1 AND webhook_id=$2 AND event_id=$3
		`, event.Environment, event.WebhookID, event.EventID, event.ReceivedAt); err != nil {
			return fmt.Errorf("mark dispute webhook processed: %w", err)
		}
		applied = true
		return nil
	})
	return item, applied, err
}

func (r *Repository) applyDisputeObservation(
	ctx context.Context,
	current domain.PayPalDisputeCase,
	observation domain.DisputeObservation,
	receivedAt time.Time,
) (domain.PayPalDisputeCase, error) {
	if observation.ObservedAt.Before(current.LastObservedAt) {
		return current, nil
	}
	nextState := observation.State()
	nextStatus := observation.ProviderStatus
	nextOutcome := observation.Outcome
	resolvedAt := current.ResolvedAt
	if current.State == domain.DisputeResolved && nextState != domain.DisputeResolved {
		nextState = domain.DisputeResolved
		nextStatus = domain.DisputeStatusResolved
		nextOutcome = current.Outcome
	}
	if current.State == domain.DisputeResolved && nextState == domain.DisputeResolved &&
		current.Outcome != domain.DisputeOutcomeNone &&
		nextOutcome == domain.DisputeOutcomeNone {
		nextOutcome = current.Outcome
	}
	if nextState == domain.DisputeResolved {
		resolved := observation.ObservedAt
		if resolvedAt == nil {
			resolvedAt = &resolved
		}
	} else {
		nextOutcome = domain.DisputeOutcomeNone
		resolvedAt = nil
	}
	reason := observation.Reason
	if reason == "OTHER" && current.Reason != "OTHER" {
		reason = current.Reason
	}
	stage := observation.LifecycleStage
	if stage == domain.DisputeStageUnknown && current.LifecycleStage != domain.DisputeStageUnknown {
		stage = current.LifecycleStage
	}
	sellerDue := observation.SellerResponseDueAt
	if sellerDue == nil {
		sellerDue = current.SellerResponseDueAt
	}
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		UPDATE payment_paypal_dispute_cases dispute SET
			state=$2, provider_status=$3, outcome=$4, reason=$5,
			lifecycle_stage=$6, seller_response_due_at=$7,
			latest_event_id=$8, latest_event_type=$9, last_observed_at=$10,
			resolved_at=$11, version=version+1, updated_at=$12
		WHERE id=$1
		RETURNING `+disputeCaseColumns,
		current.ID, string(nextState), string(nextStatus), string(nextOutcome), reason,
		string(stage), sellerDue, observation.EventID, observation.EventType,
		observation.ObservedAt, resolvedAt, receivedAt,
	)
	item, err := scanDisputeCase(row)
	if err != nil {
		return domain.PayPalDisputeCase{}, fmt.Errorf("update PayPal dispute case: %w", err)
	}
	return item, nil
}

func (r *Repository) getPayPalDisputeByProviderID(
	ctx context.Context,
	environment, disputeID string,
	forUpdate bool,
) (domain.PayPalDisputeCase, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+disputeCaseColumns+`
		FROM payment_paypal_dispute_cases dispute
		WHERE dispute.environment=$1 AND dispute.dispute_id=$2`+suffix,
		environment, disputeID,
	)
	item, err := scanDisputeCase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PayPalDisputeCase{}, domain.ErrDisputeNotFound
	}
	if err != nil {
		return domain.PayPalDisputeCase{}, fmt.Errorf("get PayPal dispute by provider id: %w", err)
	}
	return item, nil
}

func (r *Repository) AssertMOCompensationAllowed(
	ctx context.Context,
	moCashReceiptID string,
) error {
	var kind string
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT kind FROM payment_mo_cash_receipts WHERE id=$1 FOR UPDATE
	`, strings.TrimSpace(moCashReceiptID)).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock refund receipt: %w", err)
	}
	if kind != "PAYPAL_CAPTURE" {
		return domain.ErrInstructionMismatch
	}
	var blocked bool
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM payment_paypal_dispute_cases dispute
			WHERE dispute.mo_cash_receipt_id=$1 AND (
				dispute.state='OPEN' OR dispute.outcome IN (
					'NONE','RESOLVED_BUYER_FAVOUR','RESOLVED_WITH_PAYOUT','ACCEPTED'
				)
			)
		)
	`, strings.TrimSpace(moCashReceiptID)).Scan(&blocked); err != nil {
		return fmt.Errorf("check refund-blocking dispute: %w", err)
	}
	if blocked {
		return domain.ErrRefundBlockedByDispute
	}
	return nil
}

func (r *Repository) ListPayPalDisputes(
	ctx context.Context,
	filter paymentapp.DisputeQueueFilter,
) ([]domain.PayPalDisputeCase, error) {
	stateClause := "AND dispute.state=$2"
	args := []any{filter.Environment, filter.State, filter.Limit}
	if filter.State == "ALL" {
		stateClause = ""
		args = []any{filter.Environment, filter.Limit}
	}
	limitPlaceholder := "$3"
	if filter.State == "ALL" {
		limitPlaceholder = "$2"
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+disputeCaseColumns+`
		FROM payment_paypal_dispute_cases dispute
		WHERE dispute.environment=$1 `+stateClause+`
		ORDER BY
			CASE WHEN dispute.state='OPEN' THEN 0 ELSE 1 END,
			dispute.seller_response_due_at ASC NULLS LAST,
			dispute.last_observed_at DESC
		LIMIT `+limitPlaceholder,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list PayPal disputes: %w", err)
	}
	defer rows.Close()
	items := make([]domain.PayPalDisputeCase, 0)
	for rows.Next() {
		item, err := scanDisputeCase(rows)
		if err != nil {
			return nil, fmt.Errorf("scan PayPal dispute: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetPayPalDispute(
	ctx context.Context,
	environment, caseID string,
) (domain.PayPalDisputeCase, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+disputeCaseColumns+`
		FROM payment_paypal_dispute_cases dispute
		WHERE dispute.environment=$1 AND dispute.id=$2
	`, strings.TrimSpace(environment), strings.TrimSpace(caseID))
	item, err := scanDisputeCase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PayPalDisputeCase{}, domain.ErrDisputeNotFound
	}
	if err != nil {
		return domain.PayPalDisputeCase{}, fmt.Errorf("get PayPal dispute: %w", err)
	}
	return item, nil
}

const disputeActionColumns = `
	action.id::text, action.dispute_case_id::text, action.action_kind,
	action.external_reference, action.public_rationale, action.internal_note,
	action.actor_user_id::text, action.observed_provider_status,
	action.observed_outcome, action.evidence_source, action.evidence_hash,
	action.observed_at, action.idempotency_key_hash, action.request_hash,
	action.created_at`

func scanDisputeAction(scanner interface{ Scan(...any) error }) (
	domain.PayPalDisputeManualAction,
	error,
) {
	var item domain.PayPalDisputeManualAction
	var status, outcome sql.NullString
	err := scanner.Scan(
		&item.ID, &item.DisputeCaseID, &item.ActionKind,
		&item.ExternalReference, &item.PublicRationale, &item.InternalNote,
		&item.ActorUserID, &status, &outcome, &item.EvidenceSource,
		&item.EvidenceHash, &item.ObservedAt, &item.IdempotencyKeyHash,
		&item.RequestHash, &item.CreatedAt,
	)
	if status.Valid {
		item.ObservedProviderStatus = domain.DisputeProviderStatus(status.String)
	}
	if outcome.Valid {
		item.ObservedOutcome = domain.DisputeOutcome(outcome.String)
	}
	return item, err
}

func (r *Repository) ListPayPalDisputeActions(
	ctx context.Context,
	environment, caseID string,
) ([]domain.PayPalDisputeManualAction, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+disputeActionColumns+`
		FROM payment_paypal_dispute_manual_actions action
		JOIN payment_paypal_dispute_cases dispute
		  ON dispute.id=action.dispute_case_id
		 AND dispute.environment=$1
		WHERE action.dispute_case_id=$2
		ORDER BY action.observed_at, action.created_at, action.id
	`, strings.TrimSpace(environment), strings.TrimSpace(caseID))
	if err != nil {
		return nil, fmt.Errorf("list PayPal dispute actions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.PayPalDisputeManualAction, 0)
	for rows.Next() {
		item, err := scanDisputeAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan PayPal dispute action: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) RecordPayPalDisputeAction(
	ctx context.Context,
	environment string,
	action domain.PayPalDisputeManualAction,
	expectedVersion int64,
) (domain.PayPalDisputeCase, domain.PayPalDisputeManualAction, bool, error) {
	var item domain.PayPalDisputeCase
	var recorded domain.PayPalDisputeManualAction
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		row := q.QueryRowContext(tx, `
			SELECT `+disputeCaseColumns+`
			FROM payment_paypal_dispute_cases dispute
			WHERE dispute.environment=$1 AND dispute.id=$2
			FOR UPDATE
		`, strings.TrimSpace(environment), action.DisputeCaseID)
		var err error
		item, err = scanDisputeCase(row)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrDisputeNotFound
		}
		if err != nil {
			return fmt.Errorf("lock PayPal dispute: %w", err)
		}
		existingRow := q.QueryRowContext(tx, `
			SELECT `+disputeActionColumns+`
			FROM payment_paypal_dispute_manual_actions action
			WHERE action.dispute_case_id=$1 AND action.idempotency_key_hash=$2
		`, action.DisputeCaseID, action.IdempotencyKeyHash)
		existing, existingErr := scanDisputeAction(existingRow)
		if existingErr == nil {
			if existing.RequestHash != action.RequestHash {
				return domain.ErrConflict
			}
			recorded, replay = existing, true
			return nil
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return fmt.Errorf("read PayPal dispute action replay: %w", existingErr)
		}
		if item.Version != expectedVersion {
			return domain.ErrConflict
		}
		if _, err := q.ExecContext(tx,
			`SELECT 1 FROM payment_mo_cash_receipts WHERE id=$1 FOR UPDATE`,
			item.MOCashReceiptID,
		); err != nil {
			return fmt.Errorf("lock manual dispute receipt: %w", err)
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_paypal_dispute_manual_actions(
				id,dispute_case_id,action_kind,external_reference,public_rationale,
				internal_note,actor_user_id,observed_provider_status,observed_outcome,
				evidence_source,evidence_hash,observed_at,idempotency_key_hash,
				request_hash,created_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14,$15)
		`, action.ID, action.DisputeCaseID, string(action.ActionKind),
			action.ExternalReference, action.PublicRationale, action.InternalNote,
			action.ActorUserID, string(action.ObservedProviderStatus),
			string(action.ObservedOutcome), string(action.EvidenceSource),
			action.EvidenceHash, action.ObservedAt, action.IdempotencyKeyHash,
			action.RequestHash, action.CreatedAt); err != nil {
			if uniqueViolation(err) {
				return domain.ErrConflict
			}
			return fmt.Errorf("insert PayPal dispute action: %w", err)
		}
		nextState := item.State
		nextStatus := item.ProviderStatus
		nextOutcome := item.Outcome
		resolvedAt := item.ResolvedAt
		lastObservedAt := item.LastObservedAt
		if action.ObservedProviderStatus != "" &&
			!action.ObservedAt.Before(item.LastObservedAt) {
			lastObservedAt = action.ObservedAt
			if item.State != domain.DisputeResolved {
				nextStatus = action.ObservedProviderStatus
				if action.ObservedProviderStatus == domain.DisputeStatusResolved {
					nextState = domain.DisputeResolved
					nextOutcome = action.ObservedOutcome
					if nextOutcome == "" {
						nextOutcome = domain.DisputeOutcomeNone
					}
					resolved := action.ObservedAt
					resolvedAt = &resolved
				}
			} else if action.ObservedProviderStatus == domain.DisputeStatusResolved &&
				action.ObservedOutcome != "" {
				nextOutcome = action.ObservedOutcome
			}
		}
		updatedRow := q.QueryRowContext(tx, `
			UPDATE payment_paypal_dispute_cases dispute SET
				state=$2,provider_status=$3,outcome=$4,last_observed_at=$5,
				resolved_at=$6,version=version+1,updated_at=$7
			WHERE dispute.id=$1 AND dispute.version=$8
			  AND dispute.environment=$9
			RETURNING `+disputeCaseColumns,
			item.ID, string(nextState), string(nextStatus), string(nextOutcome),
			lastObservedAt, resolvedAt, action.CreatedAt, expectedVersion,
			strings.TrimSpace(environment),
		)
		item, err = scanDisputeCase(updatedRow)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("advance PayPal dispute after manual action: %w", err)
		}
		if err := EmitDisputeEvent(tx, q, item.ID, action.ID, action.CreatedAt); err != nil {
			return err
		}
		recorded, replay = action, false
		return nil
	})
	return item, recorded, replay, err
}

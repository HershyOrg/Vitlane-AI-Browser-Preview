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

const payPalRefundAdoptionColumns = `
	adoption.id::text,adoption.compensation_id::text,adoption.operation_id::text,
	adoption.provider_environment,adoption.provider_refund_id,
	adoption.provider_status,adoption.outcome_state,adoption.amount_minor,
	adoption.currency,adoption.parent_capture_id,adoption.invoice_id,
	adoption.operation_first_sent_at,adoption.operation_idempotency_deadline,
	adoption.operator_user_id::text,adoption.public_rationale,
	adoption.evidence_source,adoption.evidence_hash,adoption.observed_at,
	adoption.request_hash,adoption.created_at`

func scanPayPalRefundAdoption(scanner interface{ Scan(...any) error }) (
	domain.PayPalRefundAdoption,
	error,
) {
	var adoption domain.PayPalRefundAdoption
	err := scanner.Scan(
		&adoption.ID, &adoption.CompensationID, &adoption.OperationID,
		&adoption.ProviderEnvironment, &adoption.ProviderRefundID,
		&adoption.ProviderStatus, &adoption.OutcomeState, &adoption.AmountMinor,
		&adoption.Currency, &adoption.ParentCaptureID, &adoption.InvoiceID,
		&adoption.OperationFirstSentAt, &adoption.OperationIdempotencyDeadline,
		&adoption.OperatorUserID, &adoption.PublicRationale,
		&adoption.EvidenceSource, &adoption.EvidenceHash, &adoption.ObservedAt,
		&adoption.RequestHash, &adoption.CreatedAt,
	)
	return adoption, err
}

func (r *Repository) PreparePayPalMORefundAdoption(
	ctx context.Context,
	compensationID, providerRefundID string,
	now time.Time,
) (paymentapp.PayPalMORefundAdoptionPlan, error) {
	compensationID = strings.TrimSpace(compensationID)
	providerRefundID = strings.TrimSpace(providerRefundID)
	if compensationID == "" || providerRefundID == "" {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			domain.ErrPayPalRefundAdoptionInvalid
	}
	q := r.database.Queryer(ctx)
	compensation, err := scanMOCompensation(q.QueryRowContext(ctx, `
		SELECT `+moCompensationColumns+`
		FROM payment_mo_compensations compensation
		WHERE compensation.id=$1
	`, compensationID))
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			domain.ErrPayPalRefundAdoptionMissing
	}
	if err != nil {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			fmt.Errorf("read PayPal refund adoption compensation: %w", err)
	}
	if compensation.Rail != "PAYPAL" || compensation.Action != domain.MOCompensationRefund ||
		(compensation.ProviderEnvironment != "SANDBOX" &&
			compensation.ProviderEnvironment != "LIVE") {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			domain.ErrPayPalRefundAdoptionMissing
	}

	var execution paymentapp.MOCompensationExecution
	execution.Compensation = compensation
	var firstSentAt, deadline sql.NullTime
	err = q.QueryRowContext(ctx, `
		SELECT funding.state,receipt.id::text,receipt.provider_capture_id,
		       operation.id::text,operation.state,operation.idempotency_key,
		       COALESCE(operation.provider_resource_id,''),
		       operation.first_sent_at,operation.idempotency_deadline
		FROM payment_mo_funding_positions funding
		JOIN payment_mo_cash_receipts receipt
		  ON receipt.funding_position_id=funding.id
		JOIN LATERAL (
		    SELECT candidate.id,candidate.state,candidate.idempotency_key,
		           candidate.provider_resource_id,candidate.first_sent_at,
		           candidate.idempotency_deadline
		    FROM payment_external_operations candidate
		    WHERE candidate.owner_kind='MO_COMPENSATION'
		      AND candidate.owner_id=$1
		      AND candidate.purpose='PAYPAL_MO_REFUND'
		    ORDER BY candidate.created_at DESC,candidate.id DESC
		    LIMIT 1
		) operation ON true
		WHERE funding.id=$2
	`, compensation.ID, compensation.FundingPositionID).Scan(
		&execution.FundingState, &execution.MOCashReceiptID,
		&execution.ProviderCaptureID, &execution.OperationID,
		&execution.OperationState, &execution.OperationIdempotencyKey,
		&execution.OperationResourceID, &firstSentAt, &deadline,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			domain.ErrPayPalRefundAdoptionMissing
	}
	if err != nil {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			fmt.Errorf("read PayPal refund adoption operation: %w", err)
	}
	plan := paymentapp.PayPalMORefundAdoptionPlan{Execution: execution}
	if firstSentAt.Valid {
		plan.OperationFirstSentAt = firstSentAt.Time
	}
	if deadline.Valid {
		plan.IdempotencyDeadline = deadline.Time
	}
	existing, existingErr := scanPayPalRefundAdoption(q.QueryRowContext(ctx, `
		SELECT `+payPalRefundAdoptionColumns+`
		FROM payment_paypal_refund_adoptions adoption
		WHERE adoption.operation_id=$1
	`, execution.OperationID))
	if existingErr == nil {
		if existing.ProviderRefundID != providerRefundID ||
			execution.OperationResourceID != providerRefundID ||
			compensation.ProviderResourceID != providerRefundID {
			return paymentapp.PayPalMORefundAdoptionPlan{},
				domain.ErrPayPalRefundAdoptionMismatch
		}
		plan.ExistingAdoption = &existing
		return plan, nil
	}
	if !errors.Is(existingErr, sql.ErrNoRows) {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			fmt.Errorf("read PayPal refund adoption replay: %w", existingErr)
	}
	if compensation.ProviderResourceID != "" || execution.OperationResourceID != "" ||
		(compensation.State != domain.MOCompensationExecutionPending &&
			compensation.State != domain.MOCompensationOutcomeUnknown) ||
		(execution.OperationState != domain.OperationSent &&
			execution.OperationState != domain.OperationUnknown) ||
		!firstSentAt.Valid || !deadline.Valid || deadline.Time.After(now) {
		return paymentapp.PayPalMORefundAdoptionPlan{},
			domain.ErrPayPalRefundAdoptionMissing
	}
	return plan, nil
}

func (r *Repository) RecordPayPalMORefundAdoption(
	ctx context.Context,
	plan paymentapp.PayPalMORefundAdoptionPlan,
	adoption domain.PayPalRefundAdoption,
	compensationState domain.MOCompensationState,
	operationState domain.OperationState,
	reason string,
	now time.Time,
) (domain.MOCompensation, domain.PayPalRefundAdoption, bool, error) {
	var compensation domain.MOCompensation
	var recorded domain.PayPalRefundAdoption
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		current, err := scanMOCompensation(q.QueryRowContext(tx, `
			SELECT `+moCompensationColumns+`
			FROM payment_mo_compensations compensation
			WHERE compensation.id=$1
			FOR UPDATE
		`, adoption.CompensationID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrPayPalRefundAdoptionMissing
		}
		if err != nil {
			return fmt.Errorf("lock PayPal refund adoption compensation: %w", err)
		}
		var currentOperationState domain.OperationState
		var currentOperationKey, currentResourceID string
		var currentFirstSentAt, currentDeadline sql.NullTime
		err = q.QueryRowContext(tx, `
			SELECT operation.state,operation.idempotency_key,
			       COALESCE(operation.provider_resource_id,''),
			       operation.first_sent_at,operation.idempotency_deadline
			FROM payment_external_operations operation
			WHERE operation.id=$1
			  AND operation.owner_kind='MO_COMPENSATION'
			  AND operation.owner_id=$2
			  AND operation.purpose='PAYPAL_MO_REFUND'
			  AND operation.id=(
			      SELECT candidate.id FROM payment_external_operations candidate
			      WHERE candidate.owner_kind='MO_COMPENSATION'
			        AND candidate.owner_id=$2
			        AND candidate.purpose='PAYPAL_MO_REFUND'
			      ORDER BY candidate.created_at DESC,candidate.id DESC LIMIT 1
			  )
			FOR UPDATE
		`, adoption.OperationID, adoption.CompensationID).Scan(
			&currentOperationState, &currentOperationKey, &currentResourceID,
			&currentFirstSentAt, &currentDeadline,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrPayPalRefundAdoptionMissing
		}
		if err != nil {
			return fmt.Errorf("lock PayPal refund adoption operation: %w", err)
		}
		var currentCaptureID string
		if err := q.QueryRowContext(tx, `
			SELECT receipt.provider_capture_id
			FROM payment_mo_cash_receipts receipt
			WHERE receipt.id=$1 AND receipt.funding_position_id=$2
			FOR UPDATE
		`, plan.Execution.MOCashReceiptID, current.FundingPositionID).Scan(
			&currentCaptureID,
		); err != nil {
			return fmt.Errorf("lock PayPal refund adoption receipt: %w", err)
		}
		if current.ID != plan.Execution.Compensation.ID ||
			current.Rail != "PAYPAL" || current.Action != domain.MOCompensationRefund ||
			current.ProviderEnvironment != adoption.ProviderEnvironment ||
			current.AmountMinor != adoption.AmountMinor || current.Currency != adoption.Currency ||
			currentCaptureID != adoption.ParentCaptureID ||
			currentCaptureID != plan.Execution.ProviderCaptureID ||
			currentOperationKey != adoption.InvoiceID ||
			currentOperationKey != plan.Execution.OperationIdempotencyKey ||
			!currentFirstSentAt.Valid || !currentDeadline.Valid ||
			!currentFirstSentAt.Time.Equal(adoption.OperationFirstSentAt) ||
			!currentFirstSentAt.Time.Equal(plan.OperationFirstSentAt) ||
			!currentDeadline.Time.Equal(adoption.OperationIdempotencyDeadline) ||
			!currentDeadline.Time.Equal(plan.IdempotencyDeadline) {
			return domain.ErrPayPalRefundAdoptionMismatch
		}

		existing, existingErr := scanPayPalRefundAdoption(q.QueryRowContext(tx, `
			SELECT `+payPalRefundAdoptionColumns+`
			FROM payment_paypal_refund_adoptions adoption
			WHERE adoption.operation_id=$1
		`, adoption.OperationID))
		if existingErr == nil {
			if existing.ProviderRefundID != adoption.ProviderRefundID ||
				existing.RequestHash != adoption.RequestHash ||
				currentResourceID != adoption.ProviderRefundID ||
				current.ProviderResourceID != adoption.ProviderRefundID {
				return domain.ErrConflict
			}
			recorded, replay = existing, true
			if current.State == domain.MOCompensationSucceeded ||
				current.State == domain.MOCompensationFailed ||
				(current.State == compensationState && currentOperationState == operationState) {
				compensation = current
				return nil
			}
		} else if !errors.Is(existingErr, sql.ErrNoRows) {
			return fmt.Errorf("read locked PayPal refund adoption replay: %w", existingErr)
		} else {
			if current.ProviderResourceID != "" || currentResourceID != "" ||
				(current.State != domain.MOCompensationExecutionPending &&
					current.State != domain.MOCompensationOutcomeUnknown) ||
				(currentOperationState != domain.OperationSent &&
					currentOperationState != domain.OperationUnknown) ||
				!currentDeadline.Valid || currentDeadline.Time.After(now) {
				return domain.ErrPayPalRefundAdoptionMissing
			}
			if _, err := q.ExecContext(tx, `
				INSERT INTO payment_paypal_refund_adoptions(
					id,compensation_id,operation_id,operation_owner_kind,
					provider_environment,provider_refund_id,provider_status,
					outcome_state,amount_minor,currency,parent_capture_id,invoice_id,
					operation_first_sent_at,operation_idempotency_deadline,
					operator_user_id,public_rationale,evidence_source,evidence_hash,
					observed_at,request_hash,created_at
				) VALUES($1,$2,$3,'MO_COMPENSATION',$4,$5,$6,$7,$8,$9,$10,$11,
				         $12,$13,$14,$15,$16,$17,$18,$19,$20)
			`, adoption.ID, adoption.CompensationID, adoption.OperationID,
				adoption.ProviderEnvironment, adoption.ProviderRefundID,
				adoption.ProviderStatus, adoption.OutcomeState, adoption.AmountMinor,
				adoption.Currency, adoption.ParentCaptureID, adoption.InvoiceID,
				adoption.OperationFirstSentAt, adoption.OperationIdempotencyDeadline,
				adoption.OperatorUserID, adoption.PublicRationale,
				adoption.EvidenceSource, adoption.EvidenceHash, adoption.ObservedAt,
				adoption.RequestHash, adoption.CreatedAt); err != nil {
				if uniqueViolation(err) {
					return domain.ErrConflict
				}
				return fmt.Errorf("insert PayPal refund adoption: %w", err)
			}
			recorded, replay = adoption, false
		}

		execution := plan.Execution
		execution.Compensation = current
		execution.OperationState = currentOperationState
		execution.OperationIdempotencyKey = currentOperationKey
		execution.OperationResourceID = currentResourceID
		execution.ProviderCaptureID = currentCaptureID
		compensation, err = r.RecordMOCompensationOutcome(
			tx, execution, compensationState, operationState,
			adoption.ProviderRefundID, reason, now,
		)
		return err
	})
	return compensation, recorded, replay, err
}

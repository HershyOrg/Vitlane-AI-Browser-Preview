package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type moCompensationBase struct {
	MerchantOrderID         string
	AllocationID            string
	AgencyOrderID           string
	PositionID              string
	CustomerPaymentID       string
	Rail                    string
	Environment             string
	AmountMinor             int64
	Currency                string
	ExecutionProfileHash    string
	FundingState            domain.MOFundingState
	AuthorizationID         string
	ProviderAuthorizationID string
	MOCashReceiptID         string
	ProviderCaptureID       string
}

func scanMOCompensation(scanner interface{ Scan(...any) error }) (
	domain.MOCompensation,
	error,
) {
	var value domain.MOCompensation
	var completedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.AllocationID, &value.FundingPositionID,
		&value.AgencyOrderID, &value.CustomerPaymentID, &value.Rail,
		&value.ProviderEnvironment, &value.Action, &value.Cause, &value.State,
		&value.AmountMinor, &value.Currency, &value.ExecutionProfileHash,
		&value.ProviderResourceID, &value.IdempotencyKey, &value.Version,
		&value.ApprovedAt, &completedAt, &value.CreatedAt, &value.UpdatedAt,
	)
	if completedAt.Valid {
		value.CompletedAt = &completedAt.Time
	}
	return value, err
}

const moCompensationColumns = `
	compensation.id::text,compensation.allocation_id::text,
	compensation.funding_position_id::text,compensation.agency_order_id::text,
	compensation.customer_payment_id::text,compensation.rail,
	compensation.provider_environment,compensation.action,compensation.cause,
	compensation.state,compensation.amount_minor,compensation.currency,
	compensation.execution_profile_hash,
	COALESCE(compensation.provider_resource_id,''),compensation.idempotency_key,
	compensation.version,compensation.approved_at,compensation.completed_at,
	compensation.created_at,compensation.updated_at`

func (r *Repository) PrepareMOCompensation(
	ctx context.Context,
	request paymentapp.MOCompensationRequest,
	compensationID, operationID string,
	now time.Time,
) (paymentapp.MOCompensationExecution, bool, error) {
	var execution paymentapp.MOCompensationExecution
	replay := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var base moCompensationBase
		if err := q.QueryRowContext(tx, `
			SELECT fp.merchant_order_id::text,fp.allocation_id::text,fp.agency_order_id::text,
			       fp.id::text,fp.customer_payment_id::text,
			       fp.rail,fp.provider_environment,fp.amount_minor,
			       fp.currency,fp.execution_profile_hash,fp.state,
			       COALESCE(pa.id::text,''),
			       COALESCE(pa.paypal_authorization_id,''),
			       COALESCE(cash_receipt.id::text,''),
			       COALESCE(cash_receipt.provider_capture_id,'')
			FROM payment_mo_funding_positions fp
			LEFT JOIN payment_paypal_authorizations pa
			  ON pa.id=fp.paypal_authorization_id
			LEFT JOIN payment_mo_cash_receipts cash_receipt
			  ON cash_receipt.funding_position_id=fp.id
			WHERE fp.merchant_order_id=$1 AND fp.allocation_id=$2 AND fp.agency_order_id=$3
			FOR UPDATE OF fp
		`, request.MerchantOrderID, request.AllocationID, request.AgencyOrderID).Scan(
			&base.MerchantOrderID, &base.AllocationID, &base.AgencyOrderID,
			&base.PositionID, &base.CustomerPaymentID, &base.Rail,
			&base.Environment, &base.AmountMinor, &base.Currency,
			&base.ExecutionProfileHash, &base.FundingState,
			&base.AuthorizationID, &base.ProviderAuthorizationID,
			&base.MOCashReceiptID, &base.ProviderCaptureID,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrCompensationNotAvailable
			}
			return err
		}
		if base.Rail == "PAYPAL" {
			if err := q.QueryRowContext(tx, `
				SELECT paypal_authorization_id
				FROM payment_paypal_authorizations
				WHERE id=$1
				FOR UPDATE
			`, base.AuthorizationID).Scan(&base.ProviderAuthorizationID); err != nil {
				return err
			}
			if base.ProviderAuthorizationID == "" {
				return domain.ErrCompensationNotAvailable
			}
		}

		current, err := scanMOCompensation(q.QueryRowContext(tx, `
			SELECT `+moCompensationColumns+`
			FROM payment_mo_compensations compensation
			WHERE compensation.allocation_id=$1
			FOR UPDATE
		`, base.AllocationID))
		if err == nil {
			if current.AgencyOrderID != request.AgencyOrderID ||
				current.AllocationID != request.AllocationID ||
				current.Cause != request.Cause ||
				current.IdempotencyKey != request.IdempotencyKey {
				return domain.ErrConflict
			}
			// A definitive GIWA transaction revert closes one chain attempt, not
			// the approved whole-MO customer obligation. An explicit replay of the
			// same immutable command rearms the same compensation identity; the
			// GIWA outbox preserves the failed transaction audit and allocates a
			// fresh signer nonce for the next attempt.
			if current.Action == domain.MOCompensationTVitRefund &&
				current.State == domain.MOCompensationFailed {
				result, updateErr := q.ExecContext(tx, `
					UPDATE payment_mo_compensations
					SET state='APPROVED',provider_resource_id=NULL,completed_at=NULL,
					    version=version+1,updated_at=$2
					WHERE id=$1 AND state='FAILED'
				`, current.ID, now)
				if updateErr != nil {
					return updateErr
				}
				affected, updateErr := result.RowsAffected()
				if updateErr != nil {
					return updateErr
				}
				if affected != 1 {
					return domain.ErrConflict
				}
				fundingResult, updateErr := q.ExecContext(tx, `
					UPDATE payment_mo_funding_positions
					SET state='RELEASE_PENDING',released_at=NULL,
					    version=version+1,updated_at=$2
					WHERE id=$1 AND state='FAILED'
				`, current.FundingPositionID, now)
				if updateErr != nil {
					return updateErr
				}
				fundingAffected, updateErr := fundingResult.RowsAffected()
				if updateErr != nil {
					return updateErr
				}
				if fundingAffected != 1 {
					return domain.ErrConflict
				}
				if updateErr = EmitMOFundingEvent(ctx, q, current.FundingPositionID, now); updateErr != nil {
					return updateErr
				}
				current, updateErr = scanMOCompensation(q.QueryRowContext(tx, `
					SELECT `+moCompensationColumns+`
					FROM payment_mo_compensations compensation
					WHERE compensation.id=$1
				`, current.ID))
				if updateErr != nil {
					return updateErr
				}
				base.FundingState = domain.MOFundingReleasePending
				if updateErr = EmitMOCompensationEvent(ctx, q, current.ID, now); updateErr != nil {
					return updateErr
				}
			}
			// A terminal PayPal refund resource cannot be retried with its old
			// PayPal-Request-Id: PayPal would replay that failed resource. Keep the
			// immutable compensation command, but explicitly rearm it with a fresh,
			// append-only external-operation generation.
			if current.Action == domain.MOCompensationRefund &&
				current.State == domain.MOCompensationFailed {
				result, updateErr := q.ExecContext(tx, `
					UPDATE payment_mo_compensations
					SET state='APPROVED',provider_resource_id=NULL,completed_at=NULL,
					    version=version+1,updated_at=$2
					WHERE id=$1 AND state='FAILED'
				`, current.ID, now)
				if updateErr != nil {
					return updateErr
				}
				affected, updateErr := result.RowsAffected()
				if updateErr != nil {
					return updateErr
				}
				if affected != 1 {
					return domain.ErrConflict
				}
				fundingResult, updateErr := q.ExecContext(tx, `
					UPDATE payment_mo_funding_positions
					SET state='RELEASE_PENDING',released_at=NULL,
					    version=version+1,updated_at=$2
					WHERE id=$1 AND state='FAILED'
				`, current.FundingPositionID, now)
				if updateErr != nil {
					return updateErr
				}
				fundingAffected, updateErr := fundingResult.RowsAffected()
				if updateErr != nil {
					return updateErr
				}
				if fundingAffected != 1 {
					return domain.ErrConflict
				}
				if updateErr = EmitMOFundingEvent(ctx, q, current.FundingPositionID, now); updateErr != nil {
					return updateErr
				}
				var generation int
				if updateErr = q.QueryRowContext(tx, `
					SELECT count(*)+1
					FROM payment_external_operations
					WHERE owner_kind='MO_COMPENSATION' AND owner_id=$1
					  AND purpose='PAYPAL_MO_REFUND'
				`, current.ID).Scan(&generation); updateErr != nil {
					return updateErr
				}
				operationKey := fmt.Sprintf(
					"paypal:mo-refund:%s:%d", current.ID, generation,
				)
				if _, updateErr = q.ExecContext(tx, `
					INSERT INTO payment_external_operations(
						id,purpose,owner_kind,owner_id,idempotency_key,request_hash,
						state,created_at,updated_at
					) VALUES($1,'PAYPAL_MO_REFUND','MO_COMPENSATION',$2,$3,$4,
					         'PREPARED',$5,$5)
				`, operationID, current.ID, operationKey,
					requestKeyHash(
						string(domain.OperationPayPalMORefund)+"|"+operationKey+"|"+
							base.ProviderAuthorizationID+"|"+base.ProviderCaptureID,
						base.AmountMinor,
					), now); updateErr != nil {
					return updateErr
				}
				current, updateErr = scanMOCompensation(q.QueryRowContext(tx, `
					SELECT `+moCompensationColumns+`
					FROM payment_mo_compensations compensation
					WHERE compensation.id=$1
				`, current.ID))
				if updateErr != nil {
					return updateErr
				}
				base.FundingState = domain.MOFundingReleasePending
				if updateErr = EmitMOCompensationEvent(ctx, q, current.ID, now); updateErr != nil {
					return updateErr
				}
			}
			execution.Compensation = current
			execution.FundingState = base.FundingState
			execution.ProviderAuthorizationID = base.ProviderAuthorizationID
			execution.MOCashReceiptID = base.MOCashReceiptID
			execution.ProviderCaptureID = base.ProviderCaptureID
			replay = true
			if err := loadMOCompensationOperation(ctx, q, &execution); err != nil {
				return err
			}
			if current.Action == domain.MOCompensationRefund &&
				current.State != domain.MOCompensationSucceeded &&
				(execution.OperationState == "" ||
					execution.OperationState == domain.OperationPrepared) {
				if err := r.AssertMOCompensationAllowed(tx, base.MOCashReceiptID); err != nil {
					return err
				}
			}
			if current.Action == domain.MOCompensationVoid &&
				current.State != domain.MOCompensationSucceeded {
				if err := assertNoOpenPayPalReauthorization(
					tx, q, base.AuthorizationID,
				); err != nil {
					return err
				}
				execution.ProviderVoidRequired, err = lockAuthorizationForMORelease(
					ctx, q, base.AuthorizationID, base.PositionID,
				)
			}
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		action := domain.MOCompensationTVitRefund
		switch base.FundingState {
		case domain.MOFundingAvailable, domain.MOFundingFailed:
			if base.Rail == "PAYPAL" {
				action = domain.MOCompensationVoid
			}
		case domain.MOFundingActive:
			if base.Rail == "PAYPAL" {
				action = domain.MOCompensationRefund
			}
		case domain.MOFundingActivationPending, domain.MOFundingActivationUnknown,
			domain.MOFundingReleasePending, domain.MOFundingReleaseUnknown:
			return domain.ErrCompensationOutcomeUnknown
		default:
			return domain.ErrCompensationNotAvailable
		}
		if base.Rail == "PAYPAL" && (base.AuthorizationID == "" ||
			base.ProviderAuthorizationID == "") {
			return domain.ErrCompensationNotAvailable
		}
		if action == domain.MOCompensationRefund && base.ProviderCaptureID == "" {
			return domain.ErrCompensationNotAvailable
		}
		if action == domain.MOCompensationRefund {
			if err := r.AssertMOCompensationAllowed(tx, base.MOCashReceiptID); err != nil {
				return err
			}
		}
		if base.Rail != "PAYPAL" && base.Rail != "GIWA" {
			return domain.ErrCompensationNotAvailable
		}

		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_mo_compensations(
				id,allocation_id,funding_position_id,agency_order_id,
				customer_payment_id,rail,provider_environment,action,cause,state,
				amount_minor,currency,execution_profile_hash,idempotency_key,
				approved_at,created_at,updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'APPROVED',$10,$11,$12,$13,$14,$14,$14)
		`, compensationID, base.AllocationID, base.PositionID, base.AgencyOrderID,
			base.CustomerPaymentID, base.Rail, base.Environment, action,
			request.Cause, base.AmountMinor, base.Currency,
			base.ExecutionProfileHash, request.IdempotencyKey, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state='RELEASE_PENDING',version=version+1,updated_at=$2
			WHERE id=$1 AND state IN ('AVAILABLE','ACTIVE','FAILED')
		`, base.PositionID, now); err != nil {
			return err
		}
		base.FundingState = domain.MOFundingReleasePending
		if err := EmitMOFundingEvent(ctx, q, base.PositionID, now); err != nil {
			return err
		}

		execution.Compensation = domain.MOCompensation{
			ID: compensationID, AllocationID: base.AllocationID,
			FundingPositionID: base.PositionID, AgencyOrderID: base.AgencyOrderID,
			CustomerPaymentID: base.CustomerPaymentID, Rail: base.Rail,
			ProviderEnvironment: base.Environment, Action: action, Cause: request.Cause,
			State: domain.MOCompensationApproved, AmountMinor: base.AmountMinor,
			Currency: base.Currency, ExecutionProfileHash: base.ExecutionProfileHash,
			IdempotencyKey: request.IdempotencyKey, Version: 1,
			ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
		}
		execution.FundingState = base.FundingState
		execution.ProviderAuthorizationID = base.ProviderAuthorizationID
		execution.MOCashReceiptID = base.MOCashReceiptID
		execution.ProviderCaptureID = base.ProviderCaptureID

		if action == domain.MOCompensationVoid {
			if err := assertNoOpenPayPalReauthorization(
				tx, q, base.AuthorizationID,
			); err != nil {
				return err
			}
			execution.ProviderVoidRequired, err = lockAuthorizationForMORelease(
				ctx, q, base.AuthorizationID, base.PositionID,
			)
			if err != nil {
				return err
			}
		}
		if action == domain.MOCompensationRefund || execution.ProviderVoidRequired {
			purpose := domain.OperationPayPalMORefund
			if action == domain.MOCompensationVoid {
				purpose = domain.OperationPayPalAuthVoid
			}
			execution.OperationID = operationID
			execution.OperationState = domain.OperationPrepared
			operationKey := "paypal:mo-refund:" + compensationID + ":1"
			if action == domain.MOCompensationVoid {
				operationKey = "paypal:auth-void:" + compensationID + ":1"
			}
			execution.OperationIdempotencyKey = operationKey
			if _, err := q.ExecContext(tx, `
				INSERT INTO payment_external_operations(
					id,purpose,owner_kind,owner_id,idempotency_key,request_hash,
					state,created_at,updated_at
				) VALUES($1,$2,'MO_COMPENSATION',$3,$4,$5,'PREPARED',$6,$6)
			`, operationID, purpose, compensationID, operationKey,
				requestKeyHash(
					string(purpose)+"|"+operationKey+"|"+
						base.ProviderAuthorizationID+"|"+base.ProviderCaptureID,
					base.AmountMinor,
				), now); err != nil {
				return err
			}
		}
		return EmitMOCompensationEvent(ctx, q, compensationID, now)
	})
	if err != nil {
		return paymentapp.MOCompensationExecution{}, false,
			fmt.Errorf("prepare MO compensation: %w", err)
	}
	return execution, replay, nil
}

func assertNoOpenPayPalReauthorization(
	ctx context.Context,
	q PaymentQueryer,
	authorizationID string,
) error {
	var open bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM payment_external_operations
			WHERE owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$1
			  AND purpose='PAYPAL_REAUTHORIZE'
			  AND state IN ('PREPARED','SENT','UNKNOWN')
		)
	`, authorizationID).Scan(&open); err != nil {
		return err
	}
	if open {
		return domain.ErrCompensationOutcomeUnknown
	}
	return nil
}

// WithMOCompensationEffectLock serializes the direct PayPal refund send with
// signed dispute ingestion on the exact partial-capture receipt. The bounded
// provider POST runs while the receipt row is locked; after it returns, a
// dispute may be recorded but no competing refund can have started first.
func (r *Repository) WithMOCompensationEffectLock(
	ctx context.Context,
	execution paymentapp.MOCompensationExecution,
	fn func(context.Context) error,
) error {
	if execution.Compensation.Action != domain.MOCompensationRefund ||
		execution.MOCashReceiptID == "" || execution.ProviderCaptureID == "" {
		return domain.ErrCompensationNotAvailable
	}
	// The receipt lock deliberately spans the bounded provider write. Use the
	// admin timeout class (2m) so the transaction outlives Payment's 30s HTTP
	// timeout; the normal 8s/15s classes would cancel legitimate refunds early.
	effectContext := runtimepolicy.WithWorkClass(ctx, runtimepolicy.Admin)
	return r.database.WithinTransaction(effectContext, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var locked bool
		err := q.QueryRowContext(tx, `
			SELECT TRUE FROM payment_mo_cash_receipts receipt
			WHERE receipt.id=$1 AND receipt.funding_position_id=$2
			  AND receipt.provider_capture_id=$3
			FOR UPDATE
		`, execution.MOCashReceiptID, execution.Compensation.FundingPositionID,
			execution.ProviderCaptureID).Scan(&locked)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrCompensationNotAvailable
		}
		if err != nil {
			return err
		}
		if err := r.AssertMOCompensationAllowed(tx, execution.MOCashReceiptID); err != nil {
			return err
		}
		return fn(tx)
	})
}

// lockAuthorizationForMORelease serializes release planning with both capture
// planning and every sibling release. An in-flight sibling makes the residual
// hold unknowable, so the command retries only after it becomes ACTIVE or
// RELEASED. Once stable, only AVAILABLE siblings can still be captured.
func lockAuthorizationForMORelease(
	ctx context.Context,
	q PaymentQueryer,
	authorizationID, currentPositionID string,
) (bool, error) {
	var providerAuthorizationID string
	var authorizationState domain.PayPalAuthorizationState
	if err := q.QueryRowContext(ctx, `
		SELECT paypal_authorization_id,state
		FROM payment_paypal_authorizations
		WHERE id=$1
		FOR UPDATE
	`, authorizationID).Scan(&providerAuthorizationID, &authorizationState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, domain.ErrCompensationNotAvailable
		}
		return false, err
	}
	if providerAuthorizationID == "" {
		return false, domain.ErrCompensationNotAvailable
	}
	switch authorizationState {
	case domain.AuthorizationCaptured, domain.AuthorizationVoided,
		domain.AuthorizationExpired, domain.AuthorizationDenied,
		domain.AuthorizationFailed:
		// No residual provider hold can remain. A FAILED/uncaptured position is
		// still closed by a durable local whole-MO compensation fact.
		return false, nil
	case domain.AuthorizationAuthorized, domain.AuthorizationPartiallyCaptured:
		// Continue below and decide whether this release owns the residual void.
	default:
		return false, domain.ErrCompensationOutcomeUnknown
	}
	var unresolved int
	if err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_mo_funding_positions
		WHERE paypal_authorization_id=$1 AND id<>$2
		  AND state IN (
		      'ACTIVATION_PENDING','ACTIVATION_UNKNOWN',
		      'RELEASE_PENDING','RELEASE_UNKNOWN'
		  )
	`, authorizationID, currentPositionID).Scan(&unresolved); err != nil {
		return false, err
	}
	if unresolved != 0 {
		return false, domain.ErrCompensationOutcomeUnknown
	}
	var remainingAvailable int
	if err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_mo_funding_positions
		WHERE paypal_authorization_id=$1 AND id<>$2
		  AND state='AVAILABLE'
	`, authorizationID, currentPositionID).Scan(&remainingAvailable); err != nil {
		return false, err
	}
	return remainingAvailable == 0, nil
}

func loadMOCompensationOperation(
	ctx context.Context,
	q PaymentQueryer,
	execution *paymentapp.MOCompensationExecution,
) error {
	err := q.QueryRowContext(ctx, `
		SELECT id::text,state,idempotency_key,COALESCE(provider_resource_id,'')
		FROM payment_external_operations
		WHERE owner_kind='MO_COMPENSATION' AND owner_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 1
	`, execution.Compensation.ID).Scan(
		&execution.OperationID, &execution.OperationState,
		&execution.OperationIdempotencyKey,
		&execution.OperationResourceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (r *Repository) MarkMOCompensationSent(
	ctx context.Context,
	execution paymentapp.MOCompensationExecution,
	firstSentAt, deadline time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		result, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SENT',first_sent_at=COALESCE(first_sent_at,$3),
			    idempotency_deadline=COALESCE(idempotency_deadline,$4),updated_at=$3
			WHERE id=$2 AND owner_kind='MO_COMPENSATION' AND owner_id=$1
			  AND (
			      state IN ('PREPARED','UNKNOWN')
			      OR (
			          state='SENT' AND provider_resource_id IS NULL
			          AND updated_at<=$3::timestamptz-INTERVAL '2 minutes'
			      )
			  )
			  AND provider_resource_id IS NULL
			  AND (idempotency_deadline IS NULL OR idempotency_deadline>$3)
		`, execution.Compensation.ID, execution.OperationID, firstSentAt, deadline)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return domain.ErrConflict
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_compensations
			SET state='EXECUTION_PENDING',version=version+1,updated_at=$2
			WHERE id=$1 AND state IN ('APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN')
		`, execution.Compensation.ID, firstSentAt); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state='RELEASE_PENDING',version=version+1,updated_at=$2
			WHERE id=$1 AND state IN ('RELEASE_PENDING','RELEASE_UNKNOWN')
		`, execution.Compensation.FundingPositionID, firstSentAt); err != nil {
			return err
		}
		if err := EmitMOFundingEvent(
			ctx, q, execution.Compensation.FundingPositionID, firstSentAt,
		); err != nil {
			return err
		}
		return EmitMOCompensationEvent(
			ctx, q, execution.Compensation.ID, firstSentAt,
		)
	})
}

func (r *Repository) RecordMOCompensationOutcome(
	ctx context.Context,
	execution paymentapp.MOCompensationExecution,
	state domain.MOCompensationState,
	operationState domain.OperationState,
	providerResourceID, reason string,
	now time.Time,
) (domain.MOCompensation, error) {
	var current domain.MOCompensation
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var storedState domain.MOCompensationState
		if err := q.QueryRowContext(tx, `
			SELECT state FROM payment_mo_compensations WHERE id=$1 FOR UPDATE
		`, execution.Compensation.ID).Scan(&storedState); err != nil {
			return err
		}
		if storedState == domain.MOCompensationSucceeded {
			current, _ = scanMOCompensation(q.QueryRowContext(tx, `
				SELECT `+moCompensationColumns+` FROM payment_mo_compensations compensation
				WHERE compensation.id=$1
			`, execution.Compensation.ID))
			return nil
		}
		if state != domain.MOCompensationSucceeded &&
			state != domain.MOCompensationOutcomeUnknown &&
			state != domain.MOCompensationFailed {
			return domain.ErrInvalid
		}
		if execution.OperationID != "" {
			var resolvedAt any
			if operationState == domain.OperationSucceeded ||
				operationState == domain.OperationFailed {
				resolvedAt = now
			}
			result, err := q.ExecContext(tx, `
				UPDATE payment_external_operations
				SET state=$2,provider_resource_id=COALESCE(NULLIF($3,''),provider_resource_id),
				    last_reason_code=NULLIF($4,''),resolved_at=$5,updated_at=$6
				WHERE id=$1 AND owner_kind='MO_COMPENSATION'
				  AND state IN ('PREPARED','SENT','UNKNOWN')
			`, execution.OperationID, operationState, providerResourceID,
				reason, resolvedAt, now)
			if err != nil {
				return err
			}
			affected, _ := result.RowsAffected()
			if affected != 1 {
				return domain.ErrConflict
			}
		}
		var completedAt any
		var releasedAt any
		fundingState := domain.MOFundingReleaseUnknown
		if state == domain.MOCompensationSucceeded {
			completedAt = now
			releasedAt = now
			fundingState = domain.MOFundingReleased
		} else if state == domain.MOCompensationFailed {
			completedAt = now
			fundingState = domain.MOFundingFailed
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_compensations
			SET state=$2,provider_resource_id=COALESCE(NULLIF($3,''),provider_resource_id),
			    completed_at=$4,version=version+1,updated_at=$5
			WHERE id=$1 AND state IN ('APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN')
		`, execution.Compensation.ID, state, providerResourceID, completedAt, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state=$2,released_at=$3,version=version+1,updated_at=$4
			WHERE id=$1 AND state IN ('RELEASE_PENDING','RELEASE_UNKNOWN')
		`, execution.Compensation.FundingPositionID, fundingState, releasedAt, now); err != nil {
			return err
		}
		if err := EmitMOFundingEvent(
			ctx, q, execution.Compensation.FundingPositionID, now,
		); err != nil {
			return err
		}
		if state == domain.MOCompensationSucceeded &&
			execution.Compensation.Action == domain.MOCompensationVoid &&
			providerResourceID != "" {
			authorizationState := domain.AuthorizationVoided
			switch reason {
			case "AUTHORIZATION_EXPIRED":
				authorizationState = domain.AuthorizationExpired
			case "AUTHORIZATION_DENIED":
				authorizationState = domain.AuthorizationDenied
			}
			if _, err := q.ExecContext(tx, `
				UPDATE payment_paypal_authorizations
				SET state=$3,terminal_at=$2,version=version+1,updated_at=$2
				WHERE customer_payment_id=$1
				  AND state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
			`, execution.Compensation.CustomerPaymentID, now, authorizationState); err != nil {
				return err
			}
		}
		if state == domain.MOCompensationSucceeded {
			if _, err := q.ExecContext(tx, `
				UPDATE payment_customer_payments payment
				SET state='CLOSED',version=version+1,updated_at=$2
				WHERE payment.id=$1
				  AND NOT EXISTS (
				      SELECT 1 FROM payment_mo_funding_positions fp
				      WHERE fp.customer_payment_id=payment.id
				        AND fp.state<>'RELEASED'
				  )
			`, execution.Compensation.CustomerPaymentID, now); err != nil {
				return err
			}
		}
		var err error
		current, err = scanMOCompensation(q.QueryRowContext(tx, `
			SELECT `+moCompensationColumns+` FROM payment_mo_compensations compensation
			WHERE compensation.id=$1
		`, execution.Compensation.ID))
		if err != nil {
			return err
		}
		return EmitMOCompensationEvent(ctx, q, execution.Compensation.ID, now)
	})
	return current, err
}

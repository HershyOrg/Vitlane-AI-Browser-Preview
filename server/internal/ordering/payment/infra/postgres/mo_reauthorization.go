package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	payPalHonorPeriod         = 72 * time.Hour
	payPalAuthorizationWindow = 29 * 24 * time.Hour
	payPalProviderSendLease   = 2 * time.Minute
)

type reauthorizationQueryer interface {
	QueryContext(context.Context, string, ...any) (sharedpostgres.Rows, error)
}

func lockAuthorizationFundingPositions(
	ctx context.Context,
	q reauthorizationQueryer,
	authorizationID, targetPositionID string,
) (domain.MOFundingState, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id::text,state
		FROM payment_mo_funding_positions
		WHERE paypal_authorization_id=$1
		ORDER BY id
		FOR UPDATE
	`, authorizationID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	targetState := domain.MOFundingState("")
	for rows.Next() {
		var positionID string
		var state domain.MOFundingState
		if err := rows.Scan(&positionID, &state); err != nil {
			return "", err
		}
		if positionID == targetPositionID {
			targetState = state
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if targetState == "" {
		return "", domain.ErrFundingNotAvailable
	}
	return targetState, nil
}

func lockRemainingCapturableMinor(
	ctx context.Context,
	q reauthorizationQueryer,
	authorizationID, targetPositionID, currency string,
	authorizedAmountMinor int64,
) (int64, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id::text,state,amount_minor,currency
		FROM payment_mo_funding_positions
		WHERE paypal_authorization_id=$1
		ORDER BY id
		FOR UPDATE
	`, authorizationID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var totalMinor, remainingMinor int64
	targetFound := false
	for rows.Next() {
		var positionID, state, positionCurrency string
		var amountMinor int64
		if err := rows.Scan(
			&positionID, &state, &amountMinor, &positionCurrency,
		); err != nil {
			return 0, err
		}
		if amountMinor <= 0 || positionCurrency != currency ||
			totalMinor > math.MaxInt64-amountMinor {
			return 0, domain.ErrInstructionMismatch
		}
		totalMinor += amountMinor
		if positionID == targetPositionID {
			targetFound = true
			if state != string(domain.MOFundingAvailable) {
				return 0, domain.ErrFundingNotAvailable
			}
		}
		switch domain.MOFundingState(state) {
		case domain.MOFundingAvailable:
			if remainingMinor > math.MaxInt64-amountMinor {
				return 0, domain.ErrInstructionMismatch
			}
			remainingMinor += amountMinor
		case domain.MOFundingActive, domain.MOFundingReleased, domain.MOFundingFailed:
			// Already captured or permanently excluded from future Capture.
		case domain.MOFundingActivationPending, domain.MOFundingActivationUnknown,
			domain.MOFundingReleasePending, domain.MOFundingReleaseUnknown:
			return 0, domain.ErrFundingOutcomeUnknown
		default:
			return 0, domain.ErrInstructionMismatch
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if !targetFound || remainingMinor <= 0 || totalMinor != authorizedAmountMinor {
		return 0, domain.ErrInstructionMismatch
	}
	return remainingMinor, nil
}

func moReauthorizationRequestHash(plan paymentapp.MOReauthorizationPlan) string {
	return requestKeyHash(
		"PAYPAL_REAUTHORIZE|"+plan.OperationIdempotencyKey+"|"+
			plan.PreviousProviderAuthorizationID+"|"+plan.PayPalOrderID+"|"+
			plan.PayeeMerchantID,
		plan.RemainingCapturableMinor,
	)
}

func (r *Repository) PrepareMOReauthorization(
	ctx context.Context,
	merchantOrderID, operationID string,
	now time.Time,
) (paymentapp.MOReauthorizationPlan, error) {
	var plan paymentapp.MOReauthorizationPlan
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var rail, positionState string
		var authorizationState sql.NullString
		var authorizationID, environment, providerAuthorizationID, currency sql.NullString
		var paypalOrderID, payeeMerchantID sql.NullString
		var preliminaryAuthorizedAmount sql.NullInt64
		var originalAuthorizedAt, honorRefreshedAt sql.NullTime
		var count sql.NullInt64
		if err := q.QueryRowContext(tx, `
			SELECT fp.id::text,fp.rail,fp.state,
			       pa.id::text,pa.provider_environment,
			       pa.paypal_authorization_id,pa.amount_minor,pa.currency,
			       pa.state,pa.authorized_at,
			       pa.honor_refreshed_at,
			       pa.reauthorization_count
			FROM payment_mo_funding_positions fp
			LEFT JOIN payment_paypal_authorizations pa
			  ON pa.id=fp.paypal_authorization_id
			WHERE fp.merchant_order_id=$1
		`, merchantOrderID).Scan(
			&plan.TargetPositionID, &rail, &positionState,
			&authorizationID, &environment, &providerAuthorizationID,
			&preliminaryAuthorizedAmount, &currency, &authorizationState,
			&originalAuthorizedAt,
			&honorRefreshedAt, &count,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrFundingNotAvailable
			}
			return err
		}
		if positionState == string(domain.MOFundingFailed) {
			plan.Expired = true
			return nil
		}
		if rail == "GIWA" || positionState == string(domain.MOFundingActive) ||
			positionState == string(domain.MOFundingActivationPending) ||
			positionState == string(domain.MOFundingActivationUnknown) {
			return nil
		}
		if rail != "PAYPAL" ||
			!authorizationID.Valid || !environment.Valid ||
			!providerAuthorizationID.Valid || !preliminaryAuthorizedAmount.Valid ||
			!currency.Valid ||
			!originalAuthorizedAt.Valid || !honorRefreshedAt.Valid ||
			!authorizationState.Valid {
			return domain.ErrFundingNotAvailable
		}
		plan.AuthorizationID = authorizationID.String
		plan.Currency = currency.String
		plan.AuthorizedAmountMinor = preliminaryAuthorizedAmount.Int64
		// An authorization may already have expired before this MO is opened.
		// Persist the exact target as FAILED in the same successful transaction;
		// returning an error here would roll that projection back and strand the
		// Procurement effect lock in FUNDING_PENDING.
		if authorizationState.String == string(domain.AuthorizationExpired) {
			lockedPositionState, err := lockAuthorizationFundingPositions(
				tx, q, plan.AuthorizationID, plan.TargetPositionID,
			)
			if err != nil {
				return err
			}
			var lockedAuthorizationState string
			if err := q.QueryRowContext(tx, `
				SELECT state FROM payment_paypal_authorizations
				WHERE id=$1 FOR UPDATE
			`, plan.AuthorizationID).Scan(&lockedAuthorizationState); err != nil {
				return err
			}
			if lockedAuthorizationState != string(domain.AuthorizationExpired) {
				return domain.ErrConflict
			}
			switch lockedPositionState {
			case domain.MOFundingAvailable:
			case domain.MOFundingFailed:
				// Idempotent replay of the persisted expiry projection.
			default:
				return domain.ErrFundingOutcomeUnknown
			}
			if _, err := FailAvailableFundingPositions(
				tx, q, plan.AuthorizationID, now,
			); err != nil {
				return err
			}
			plan.Expired = true
			return nil
		}
		if positionState != string(domain.MOFundingAvailable) ||
			(authorizationState.String != string(domain.AuthorizationAuthorized) &&
				authorizationState.String != string(domain.AuthorizationPartiallyCaptured)) {
			return domain.ErrFundingNotAvailable
		}
		remainingMinor, err := lockRemainingCapturableMinor(
			tx, q, plan.AuthorizationID, plan.TargetPositionID, plan.Currency,
			plan.AuthorizedAmountMinor,
		)
		if err != nil {
			return err
		}
		var authorizedAmountMinor int64
		if err := q.QueryRowContext(tx, `
			SELECT provider_environment,paypal_order_id,payee_merchant_id,
			       paypal_authorization_id,amount_minor,currency,state,
			       authorized_at,honor_refreshed_at,reauthorization_count
			FROM payment_paypal_authorizations WHERE id=$1 FOR UPDATE
		`, authorizationID.String).Scan(
			&environment, &paypalOrderID, &payeeMerchantID, &providerAuthorizationID,
			&authorizedAmountMinor, &currency,
			&authorizationState, &originalAuthorizedAt, &honorRefreshedAt,
			&count,
		); err != nil {
			return err
		}
		if !environment.Valid || !paypalOrderID.Valid || !payeeMerchantID.Valid ||
			!providerAuthorizationID.Valid || !currency.Valid ||
			!originalAuthorizedAt.Valid || !honorRefreshedAt.Valid ||
			(!authorizationState.Valid ||
				(authorizationState.String != string(domain.AuthorizationAuthorized) &&
					authorizationState.String != string(domain.AuthorizationPartiallyCaptured))) {
			return domain.ErrFundingNotAvailable
		}
		if authorizedAmountMinor != plan.AuthorizedAmountMinor {
			return domain.ErrInstructionMismatch
		}
		plan.AuthorizationID = authorizationID.String
		plan.ProviderEnvironment = environment.String
		plan.PayPalOrderID = paypalOrderID.String
		plan.PayeeMerchantID = payeeMerchantID.String
		plan.PreviousProviderAuthorizationID = providerAuthorizationID.String
		plan.Currency = currency.String
		plan.OriginalAuthorizedAt = originalAuthorizedAt.Time
		plan.HonorRefreshedAt = honorRefreshedAt.Time
		plan.ReauthorizationCount = int(count.Int64)
		if now.Before(plan.HonorRefreshedAt.Add(payPalHonorPeriod)) {
			return nil
		}
		if !now.Before(plan.OriginalAuthorizedAt.Add(payPalAuthorizationWindow)) {
			result, updateErr := q.ExecContext(tx, `
				UPDATE payment_paypal_authorizations
				SET state='EXPIRED',terminal_at=$2,version=version+1,updated_at=$2
				WHERE id=$1 AND state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
			`, plan.AuthorizationID, now)
			if updateErr != nil {
				return updateErr
			}
			if affected, updateErr := result.RowsAffected(); updateErr != nil {
				return updateErr
			} else if affected != 1 {
				return domain.ErrConflict
			}
			failed, updateErr := FailAvailableFundingPositions(
				tx, q, plan.AuthorizationID, now,
			)
			if updateErr != nil {
				return updateErr
			}
			if failed < 1 {
				return domain.ErrConflict
			}
			plan.Expired = true
			return nil
		}
		plan.RemainingCapturableMinor = remainingMinor
		plan.Required = true

		var firstSentAt, deadline sql.NullTime
		var operationUpdatedAt time.Time
		var storedRequestHash string
		err = q.QueryRowContext(tx, `
			SELECT id::text,state,idempotency_key,request_hash,
			       COALESCE(provider_resource_id,''),first_sent_at,idempotency_deadline,
			       updated_at
			FROM payment_external_operations
			WHERE owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$1
			  AND purpose='PAYPAL_REAUTHORIZE'
			  AND state IN ('PREPARED','SENT','UNKNOWN')
			ORDER BY created_at DESC LIMIT 1
		`, plan.AuthorizationID).Scan(
			&plan.OperationID, &plan.OperationState,
			&plan.OperationIdempotencyKey, &storedRequestHash,
			&plan.OperationResourceID,
			&firstSentAt, &deadline, &operationUpdatedAt,
		)
		if err == nil {
			if storedRequestHash != moReauthorizationRequestHash(plan) {
				return domain.ErrConflict
			}
			if plan.OperationState == domain.OperationSent &&
				!now.Before(operationUpdatedAt.Add(payPalProviderSendLease)) &&
				(!deadline.Valid || now.Before(deadline.Time)) {
				result, updateErr := q.ExecContext(tx, `
					UPDATE payment_external_operations
					SET state='UNKNOWN',last_reason_code='STALE_SEND_RECOVERY',updated_at=$2
					WHERE id=$1 AND state='SENT' AND updated_at=$3
				`, plan.OperationID, now, operationUpdatedAt)
				if updateErr != nil {
					return updateErr
				}
				if affected, updateErr := result.RowsAffected(); updateErr != nil {
					return updateErr
				} else if affected != 1 {
					return domain.ErrConflict
				}
				plan.OperationState = domain.OperationUnknown
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var failedExists bool
		if err := q.QueryRowContext(tx, `
			SELECT EXISTS(
				SELECT 1 FROM payment_external_operations
				WHERE owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$1
				  AND purpose='PAYPAL_REAUTHORIZE' AND state='FAILED'
				  AND idempotency_key=$2
			)
		`, plan.AuthorizationID, fmt.Sprintf(
			"paypal:reauthorize:%s:%d", plan.AuthorizationID,
			plan.ReauthorizationCount+1,
		)).Scan(&failedExists); err != nil {
			return err
		}
		if failedExists {
			return domain.ErrFundingNotAvailable
		}
		plan.OperationID = operationID
		plan.OperationState = domain.OperationPrepared
		plan.OperationIdempotencyKey = fmt.Sprintf(
			"paypal:reauthorize:%s:%d", plan.AuthorizationID,
			plan.ReauthorizationCount+1,
		)
		_, err = q.ExecContext(tx, `
			INSERT INTO payment_external_operations(
				id,purpose,owner_kind,owner_id,idempotency_key,request_hash,
				state,created_at,updated_at
			) VALUES($1,'PAYPAL_REAUTHORIZE','PAYPAL_AUTHORIZATION',$2,$3,$4,
			         'PREPARED',$5,$5)
		`, plan.OperationID, plan.AuthorizationID,
			plan.OperationIdempotencyKey,
			moReauthorizationRequestHash(plan), now)
		return err
	})
	if err != nil {
		return paymentapp.MOReauthorizationPlan{},
			fmt.Errorf("prepare PayPal MO reauthorization: %w", err)
	}
	return plan, nil
}

func (r *Repository) MarkMOReauthorizationSent(
	ctx context.Context,
	plan paymentapp.MOReauthorizationPlan,
	firstSentAt, deadline time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		remainingMinor, err := lockRemainingCapturableMinor(
			tx, q, plan.AuthorizationID, plan.TargetPositionID, plan.Currency,
			plan.AuthorizedAmountMinor,
		)
		if err != nil {
			return err
		}
		if remainingMinor != plan.RemainingCapturableMinor {
			return domain.ErrConflict
		}
		var providerAuthorizationID, paypalOrderID, payeeMerchantID, currency string
		var authorizedAmountMinor int64
		var reauthorizationCount int
		if err := q.QueryRowContext(tx, `
			SELECT paypal_authorization_id,paypal_order_id,payee_merchant_id,
			       amount_minor,currency,reauthorization_count
			FROM payment_paypal_authorizations
			WHERE id=$1
			FOR UPDATE
		`, plan.AuthorizationID).Scan(
			&providerAuthorizationID, &paypalOrderID, &payeeMerchantID,
			&authorizedAmountMinor, &currency,
			&reauthorizationCount,
		); err != nil {
			return err
		}
		if providerAuthorizationID != plan.PreviousProviderAuthorizationID ||
			paypalOrderID != plan.PayPalOrderID ||
			payeeMerchantID != plan.PayeeMerchantID ||
			authorizedAmountMinor != plan.AuthorizedAmountMinor ||
			currency != plan.Currency ||
			reauthorizationCount != plan.ReauthorizationCount {
			return domain.ErrConflict
		}
		result, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SENT',first_sent_at=COALESCE(first_sent_at,$3),
			    idempotency_deadline=COALESCE(idempotency_deadline,$4),updated_at=$3
			WHERE id=$1 AND owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$2
			  AND purpose='PAYPAL_REAUTHORIZE'
			  AND idempotency_key=$5 AND request_hash=$6
			  AND state IN ('PREPARED','UNKNOWN')
			  AND (idempotency_deadline IS NULL OR idempotency_deadline>$3)
		`, plan.OperationID, plan.AuthorizationID, firstSentAt, deadline,
			plan.OperationIdempotencyKey, moReauthorizationRequestHash(plan))
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.ErrConflict
		}
		return nil
	})
}

func (r *Repository) RecordMOReauthorizationOutcome(
	ctx context.Context,
	plan paymentapp.MOReauthorizationPlan,
	operationState domain.OperationState,
	providerAuthorizationID string,
	amountMinor int64,
	currency string,
	refreshedAt time.Time,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if operationState == domain.OperationFailed {
			targetState, err := lockAuthorizationFundingPositions(
				tx, q, plan.AuthorizationID, plan.TargetPositionID,
			)
			if err != nil {
				return err
			}
			if targetState != domain.MOFundingAvailable &&
				targetState != domain.MOFundingFailed {
				return domain.ErrFundingOutcomeUnknown
			}
		}
		var currentProviderID, paypalOrderID, payeeMerchantID, environment, storedCurrency string
		var authorizedAmountMinor int64
		var originalAuthorizedAt time.Time
		var count int
		if err := q.QueryRowContext(tx, `
			SELECT paypal_authorization_id,paypal_order_id,payee_merchant_id,
			       provider_environment,amount_minor,currency,
			       authorized_at,reauthorization_count
			FROM payment_paypal_authorizations WHERE id=$1 FOR UPDATE
		`, plan.AuthorizationID).Scan(
			&currentProviderID, &paypalOrderID, &payeeMerchantID, &environment,
			&authorizedAmountMinor, &storedCurrency,
			&originalAuthorizedAt, &count,
		); err != nil {
			return err
		}
		var storedState domain.OperationState
		var storedRequestHash string
		if err := q.QueryRowContext(tx, `
			SELECT state,request_hash FROM payment_external_operations
			WHERE id=$1 AND owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$2
			  AND purpose='PAYPAL_REAUTHORIZE'
			FOR UPDATE
		`, plan.OperationID, plan.AuthorizationID).Scan(
			&storedState, &storedRequestHash,
		); err != nil {
			return err
		}
		if storedRequestHash != moReauthorizationRequestHash(plan) {
			return domain.ErrConflict
		}
		if storedState == domain.OperationSucceeded {
			if currentProviderID == providerAuthorizationID {
				return nil
			}
			return domain.ErrConflict
		}
		if currentProviderID != plan.PreviousProviderAuthorizationID ||
			paypalOrderID != plan.PayPalOrderID ||
			payeeMerchantID != plan.PayeeMerchantID ||
			authorizedAmountMinor != plan.AuthorizedAmountMinor ||
			storedCurrency != plan.Currency || count != plan.ReauthorizationCount {
			return domain.ErrConflict
		}
		if operationState == domain.OperationUnknown ||
			operationState == domain.OperationFailed {
			var resolvedAt any
			if operationState == domain.OperationFailed {
				resolvedAt = now
			}
			result, err := q.ExecContext(tx, `
				UPDATE payment_external_operations
				SET state=$2,
				    provider_resource_id=COALESCE(NULLIF($3,''),provider_resource_id),
				    last_reason_code=NULLIF($4,''),resolved_at=$5,updated_at=$6
				WHERE id=$1 AND state IN ('PREPARED','SENT','UNKNOWN')
			`, plan.OperationID, operationState, providerAuthorizationID,
				reason, resolvedAt, now)
			if err != nil {
				return err
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return domain.ErrConflict
			}
			if operationState == domain.OperationFailed {
				// A zero count is the idempotent failure replay after the exact
				// fan-out was persisted.
				if _, err = FailAvailableFundingPositions(
					tx, q, plan.AuthorizationID, now,
				); err != nil {
					return err
				}
			}
			return nil
		}
		if operationState != domain.OperationSucceeded ||
			providerAuthorizationID == "" ||
			providerAuthorizationID == plan.PreviousProviderAuthorizationID ||
			amountMinor != plan.RemainingCapturableMinor || currency != plan.Currency ||
			refreshedAt.IsZero() || refreshedAt.Before(originalAuthorizedAt) ||
			!refreshedAt.Before(originalAuthorizedAt.Add(payPalAuthorizationWindow)) {
			return domain.ErrInstructionMismatch
		}
		result, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SUCCEEDED',provider_resource_id=$2,last_reason_code=NULL,
			    resolved_at=$3,updated_at=$3
			WHERE id=$1 AND state IN ('PREPARED','SENT','UNKNOWN')
		`, plan.OperationID, providerAuthorizationID, now)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.ErrConflict
		}
		result, err = q.ExecContext(tx, `
			UPDATE payment_paypal_authorizations
			SET paypal_authorization_id=$2,honor_refreshed_at=$3,
			    reauthorization_count=reauthorization_count+1,
			    version=version+1,updated_at=$4
			WHERE id=$1 AND paypal_authorization_id=$5
			  AND reauthorization_count=$6
		`, plan.AuthorizationID, providerAuthorizationID, refreshedAt, now,
			plan.PreviousProviderAuthorizationID,
			plan.ReauthorizationCount)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.ErrConflict
		}
		_, err = q.ExecContext(tx, `
			INSERT INTO payment_paypal_reauthorizations(
				id,paypal_authorization_id,operation_id,provider_environment,
				previous_provider_authorization_id,provider_authorization_id,
				amount_minor,currency,occurred_at,created_at
			) VALUES(md5($1::text||':reauthorization')::uuid,$2,$1::uuid,$3,$4,$5,$6,$7,$8,$9)
		`, plan.OperationID, plan.AuthorizationID, environment,
			plan.PreviousProviderAuthorizationID, providerAuthorizationID,
			amountMinor, currency, refreshedAt, now)
		return err
	})
}

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

func (r *Repository) PrepareAuthorizationOperation(
	ctx context.Context,
	attemptID string,
	operation domain.ExternalOperation,
	now time.Time,
) (domain.ExternalOperation, bool, error) {
	created := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var existing domain.ExternalOperation
		var firstSentAt, deadline sql.NullTime
		var updatedAt time.Time
		err := q.QueryRowContext(tx, `
			SELECT id::text,idempotency_key,request_hash,state,
			       COALESCE(provider_resource_id,''),first_sent_at,
			       idempotency_deadline,updated_at
			FROM payment_external_operations
			WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id=$1
			  AND purpose='PAYPAL_AUTHORIZE'
			ORDER BY created_at DESC LIMIT 1
		`, attemptID).Scan(
			&existing.ID, &existing.IdempotencyKey, &existing.RequestHash,
			&existing.State, &existing.ProviderResourceID, &firstSentAt, &deadline,
			&updatedAt,
		)
		if err == nil {
			existing.Purpose = domain.OperationPayPalAuthorize
			existing.OwnerKind, existing.OwnerID = "PAYPAL_ATTEMPT", attemptID
			if firstSentAt.Valid {
				existing.FirstSentAt = &firstSentAt.Time
			}
			if deadline.Valid {
				existing.IdempotencyDeadline = &deadline.Time
			}
			if existing.IdempotencyKey != operation.IdempotencyKey ||
				existing.RequestHash != operation.RequestHash {
				return domain.ErrConflict
			}
			if existing.State == domain.OperationSent &&
				!now.Before(updatedAt.Add(payPalProviderSendLease)) &&
				(!deadline.Valid || now.Before(deadline.Time)) {
				result, updateErr := q.ExecContext(tx, `
					UPDATE payment_external_operations
					SET state='UNKNOWN',last_reason_code='STALE_SEND_RECOVERY',updated_at=$2
					WHERE id=$1 AND state='SENT' AND updated_at=$3
				`, existing.ID, now, updatedAt)
				if updateErr != nil {
					return updateErr
				}
				if affected, updateErr := result.RowsAffected(); updateErr != nil {
					return updateErr
				} else if affected != 1 {
					return domain.ErrConflict
				}
				existing.State = domain.OperationUnknown
			}
			operation = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_external_operations(
				id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
				created_at,updated_at
			) VALUES($1,'PAYPAL_AUTHORIZE','PAYPAL_ATTEMPT',$2,$3,$4,'PREPARED',NOW(),NOW())
		`, operation.ID, attemptID, operation.IdempotencyKey, operation.RequestHash); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return domain.ExternalOperation{}, false, fmt.Errorf("prepare authorization operation: %w", err)
	}
	return operation, created, nil
}

// RecordAuthorizationCompleted atomically adopts the verified full-order
// PayPal authorization and creates one AVAILABLE funding position per immutable
// MO allocation. It creates no capture receipt.
func (r *Repository) RecordAuthorizationCompleted(
	ctx context.Context,
	attemptID, paymentID string,
	authorization domain.PayPalAuthorization,
	now time.Time,
) (bool, error) {
	applied := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var agencyOrderID, environment, rail, currency, executionProfileHash string
		var amountMinor int64
		if err := q.QueryRowContext(tx, `
			SELECT agency_order_id::text,provider_environment,rail,currency,amount_minor,
			       execution_profile_hash
			FROM payment_customer_payments WHERE id=$1 FOR UPDATE
		`, paymentID).Scan(
			&agencyOrderID, &environment, &rail, &currency, &amountMinor,
			&executionProfileHash,
		); err != nil {
			return err
		}
		if rail != "PAYPAL" || authorization.CustomerPaymentID != paymentID ||
			authorization.AgencyOrderID != agencyOrderID ||
			authorization.PayPalAttemptID != attemptID ||
			authorization.ProviderEnvironment != environment ||
			authorization.AmountMinor != amountMinor || authorization.Currency != currency ||
			authorization.PayPalAuthorizationID == "" || authorization.PayPalOrderID == "" ||
			authorization.PayeeMerchantID == "" {
			return domain.ErrInstructionMismatch
		}
		result, err := q.ExecContext(tx, `
			UPDATE payment_customer_payments
			SET state='AUTHORIZED',last_reason_code=NULL,version=version+1,updated_at=$2
			WHERE id=$1 AND state IN ('CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN')
		`, paymentID, now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			var providerID string
			if err := q.QueryRowContext(tx, `
				SELECT paypal_authorization_id FROM payment_paypal_authorizations
				WHERE customer_payment_id=$1
			`, paymentID).Scan(&providerID); err == nil &&
				providerID == authorization.PayPalAuthorizationID {
				return nil
			}
			return domain.ErrConflict
		}
		if affected != 1 {
			return domain.ErrConflict
		}
		applied = true
		if _, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SUCCEEDED',provider_resource_id=$2,resolved_at=$3,updated_at=$3
			WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id=$1
			  AND purpose='PAYPAL_AUTHORIZE'
			  AND state IN ('PREPARED','SENT','UNKNOWN','SUCCEEDED')
			  AND (provider_resource_id IS NULL OR provider_resource_id=$2)
		`, attemptID, authorization.PayPalAuthorizationID, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_paypal_attempts
			SET state='AUTHORIZE_COMPLETED',version=version+1,updated_at=$2
			WHERE id=$1 AND state<>'AUTHORIZE_COMPLETED'
		`, attemptID, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_paypal_authorizations(
				id,customer_payment_id,agency_order_id,paypal_attempt_id,
				rail,provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
				amount_minor,currency,execution_profile_hash,state,version,
				authorized_at,honor_refreshed_at,created_at,updated_at
			) VALUES($1,$2,$3,$4,'PAYPAL',$5,$6,$7,$8,$9,$10,$11,'AUTHORIZED',1,$12,$12,$13,$13)
		`, authorization.ID, paymentID, agencyOrderID, attemptID, environment,
			authorization.PayPalOrderID, authorization.PayeeMerchantID,
			authorization.PayPalAuthorizationID, authorization.AmountMinor,
			authorization.Currency, executionProfileHash,
			authorization.AuthorizedAt, now); err != nil {
			return err
		}
		result, err = q.ExecContext(tx, `
			INSERT INTO payment_mo_funding_positions(
				id,allocation_id,agency_order_id,customer_payment_id,
				paypal_authorization_id,rail,source,provider_environment,
				amount_minor,currency,execution_profile_hash,state,version,
				available_at,created_at,updated_at
			)
			SELECT md5(allocation.id::text||':funding')::uuid,allocation.id,
			       allocation.agency_order_id,$1,$2,'PAYPAL','PAYPAL_AUTHORIZATION',$3,
			       allocation.customer_gross_minor,'USD',allocation.execution_profile_hash,
			       'AVAILABLE',1,$4,$4,$4
			FROM agency_order_mo_allocations allocation
			WHERE allocation.agency_order_id=$5
		`, paymentID, authorization.ID, environment, now, agencyOrderID)
		if err != nil {
			return err
		}
		if err := EmitMOFundingEventsForPayment(tx, q, paymentID, now); err != nil {
			return err
		}
		positionCount, err := result.RowsAffected()
		if err != nil {
			return err
		}
		var allocationCount int64
		var allocationGross int64
		if err := q.QueryRowContext(tx, `
			SELECT count(*),COALESCE(sum(customer_gross_minor),0)
			FROM agency_order_mo_allocations WHERE agency_order_id=$1
		`, agencyOrderID).Scan(&allocationCount, &allocationGross); err != nil {
			return err
		}
		if positionCount != allocationCount || allocationCount == 0 ||
			allocationGross != amountMinor {
			return domain.ErrInstructionMismatch
		}
		return EmitCustomerFundingReadyEvent(tx, q, paymentID, now)
	})
	return applied, err
}

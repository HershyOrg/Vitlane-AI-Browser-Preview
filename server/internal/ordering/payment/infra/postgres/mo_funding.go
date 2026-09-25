package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

const moFundingActivationSelect = `
	SELECT fp.merchant_order_id::text,fp.id::text,fp.allocation_id::text,
	       fp.agency_order_id::text,fp.customer_payment_id::text,
	       COALESCE(pa.id::text,''),
	       COALESCE(pa.paypal_authorization_id,''),
	       COALESCE(pa.state,''),
	       COALESCE(pa.paypal_order_id,''),
	       fp.rail,fp.provider_environment,fp.amount_minor,
	       fp.currency,fp.execution_profile_hash,fp.state,
	       COALESCE(current_op.id::text,''),COALESCE(current_op.state,''),
	       COALESCE(current_op.idempotency_key,''),
	       COALESCE(current_op.request_hash,''),
	       COALESCE(current_op.provider_resource_id,'')
	FROM payment_mo_funding_positions fp
	LEFT JOIN payment_paypal_authorizations pa
	  ON pa.id=fp.paypal_authorization_id
	LEFT JOIN LATERAL (
		SELECT op.id,op.state,op.idempotency_key,op.request_hash,
		       op.provider_resource_id
		FROM payment_external_operations op
		WHERE op.owner_kind='MO_FUNDING_POSITION' AND op.owner_id=fp.id
		  AND op.purpose='PAYPAL_MO_CAPTURE'
		ORDER BY op.created_at DESC LIMIT 1
	) current_op ON TRUE`

func scanMOFundingActivation(scanner interface{ Scan(...any) error }) (
	paymentapp.MOFundingActivation,
	error,
) {
	var value paymentapp.MOFundingActivation
	err := scanner.Scan(
		&value.MerchantOrderID, &value.PositionID, &value.AllocationID,
		&value.AgencyOrderID, &value.CustomerPaymentID, &value.AuthorizationID,
		&value.ProviderAuthorizationID, &value.AuthorizationState,
		&value.PayPalOrderID, &value.Rail,
		&value.ProviderEnvironment, &value.AmountMinor, &value.Currency,
		&value.ExecutionProfileHash, &value.State, &value.OperationID,
		&value.OperationState, &value.OperationIdempotencyKey,
		&value.OperationRequestHash,
		&value.ProviderCaptureID,
	)
	value.InvoiceID = "VIT-MO-" + value.AllocationID
	return value, err
}

func (r *Repository) PrepareMOFundingActivation(
	ctx context.Context,
	merchantOrderID string,
	operation domain.ExternalOperation,
	now time.Time,
) (paymentapp.MOFundingActivation, bool, error) {
	var activation paymentapp.MOFundingActivation
	replay := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var err error
		activation, err = scanMOFundingActivation(q.QueryRowContext(tx,
			moFundingActivationSelect+`
			WHERE fp.merchant_order_id=$1
			FOR UPDATE OF fp
		`, merchantOrderID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrFundingNotAvailable
		}
		if err != nil {
			return err
		}
		if activation.State == domain.MOFundingFailed || activation.State == domain.MOFundingActive {
			return nil
		}
		storedOperationKey := activation.OperationIdempotencyKey
		storedRequestHash := activation.OperationRequestHash
		if activation.OperationState == domain.OperationSent && activation.OperationID != "" {
			result, updateErr := q.ExecContext(tx, `
				UPDATE payment_external_operations
				SET state='UNKNOWN',last_reason_code='STALE_SEND_RECOVERY',updated_at=$2
				WHERE id=$1 AND state='SENT'
				  AND updated_at<=$3
				  AND (idempotency_deadline IS NULL OR idempotency_deadline>$2)
			`, activation.OperationID, now, now.Add(-payPalProviderSendLease))
			if updateErr != nil {
				return updateErr
			}
			if affected, updateErr := result.RowsAffected(); updateErr != nil {
				return updateErr
			} else if affected == 1 {
				activation.OperationState = domain.OperationUnknown
			}
		}
		if activation.Rail == "PAYPAL" {
			if err := q.QueryRowContext(tx, `
				SELECT state,paypal_authorization_id,paypal_order_id
				FROM payment_paypal_authorizations
				WHERE id=$1
				FOR UPDATE
			`, activation.AuthorizationID).Scan(
				&activation.AuthorizationState,
				&activation.ProviderAuthorizationID,
				&activation.PayPalOrderID,
			); err != nil {
				return err
			}
			if activation.ProviderAuthorizationID == "" ||
				(activation.AuthorizationState != domain.AuthorizationAuthorized &&
					activation.AuthorizationState != domain.AuthorizationPartiallyCaptured) {
				return domain.ErrFundingNotAvailable
			}
			var reauthorizationOpen bool
			if err := q.QueryRowContext(tx, `
				SELECT EXISTS(
					SELECT 1 FROM payment_external_operations
					WHERE owner_kind='PAYPAL_AUTHORIZATION' AND owner_id=$1
					  AND purpose='PAYPAL_REAUTHORIZE'
					  AND state IN ('PREPARED','SENT','UNKNOWN')
				)
			`, activation.AuthorizationID).Scan(&reauthorizationOpen); err != nil {
				return err
			}
			if reauthorizationOpen {
				return domain.ErrFundingOutcomeUnknown
			}
			var otherUnresolvedEffects int
			if err := q.QueryRowContext(tx, `
				SELECT count(*) FROM payment_mo_funding_positions
				WHERE paypal_authorization_id=$1 AND id<>$2
				  AND state IN (
				      'ACTIVATION_PENDING','ACTIVATION_UNKNOWN',
				      'RELEASE_PENDING','RELEASE_UNKNOWN'
				  )
			`, activation.AuthorizationID, activation.PositionID).Scan(
				&otherUnresolvedEffects,
			); err != nil {
				return err
			}
			if otherUnresolvedEffects != 0 {
				return domain.ErrFundingOutcomeUnknown
			}
			var remaining int
			if err := q.QueryRowContext(tx, `
				SELECT count(*) FROM payment_mo_funding_positions
				WHERE paypal_authorization_id=$1 AND id<>$2
				  AND state='AVAILABLE'
			`, activation.AuthorizationID, activation.PositionID).Scan(&remaining); err != nil {
				return err
			}
			activation.FinalCapture = remaining == 0
			activation.OperationIdempotencyKey =
				"paypal:mo-capture:" + activation.PositionID + ":v1"
			activation.OperationRequestHash = requestKeyHash(
				fmt.Sprintf("%s|%s|%s|%t",
					activation.OperationIdempotencyKey,
					activation.ProviderAuthorizationID,
					activation.InvoiceID,
					activation.FinalCapture,
				),
				activation.AmountMinor,
			)
		}
		switch activation.State {
		case domain.MOFundingActive:
			replay = true
			return nil
		case domain.MOFundingActivationPending, domain.MOFundingActivationUnknown:
			if activation.OperationID == "" {
				return domain.ErrConflict
			}
			if activation.Rail == "PAYPAL" &&
				(storedOperationKey != activation.OperationIdempotencyKey ||
					storedRequestHash != activation.OperationRequestHash) {
				return domain.ErrConflict
			}
			replay = true
		case domain.MOFundingAvailable:
			if activation.Rail == "GIWA" {
				if _, err := q.ExecContext(tx, `
					UPDATE payment_mo_funding_positions
					SET state='ACTIVE',activated_at=$2,version=version+1,updated_at=$2
					WHERE id=$1 AND state='AVAILABLE'
				`, activation.PositionID, now); err != nil {
					return err
				}
				activation.State = domain.MOFundingActive
				return EmitMOFundingEvent(tx, q, activation.PositionID, now)
			}
			if activation.Rail != "PAYPAL" {
				return domain.ErrFundingNotAvailable
			}
			activation.OperationID = operation.ID
			activation.OperationState = domain.OperationPrepared
			if _, err := q.ExecContext(tx, `
				INSERT INTO payment_external_operations(
					id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
					created_at,updated_at
				) VALUES($1,'PAYPAL_MO_CAPTURE','MO_FUNDING_POSITION',$2,$3,$4,'PREPARED',$5,$5)
			`, activation.OperationID, activation.PositionID,
				activation.OperationIdempotencyKey,
				activation.OperationRequestHash, now); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, `
				UPDATE payment_mo_funding_positions
				SET state='ACTIVATION_PENDING',version=version+1,updated_at=$2
				WHERE id=$1 AND state='AVAILABLE'
			`, activation.PositionID, now); err != nil {
				return err
			}
			activation.State = domain.MOFundingActivationPending
			if err := EmitMOFundingEvent(tx, q, activation.PositionID, now); err != nil {
				return err
			}
		default:
			return domain.ErrFundingNotAvailable
		}
		return nil
	})
	if err != nil {
		return paymentapp.MOFundingActivation{}, false,
			fmt.Errorf("prepare MO funding activation: %w", err)
	}
	return activation, replay, nil
}

func (r *Repository) MarkMOFundingActivationSent(
	ctx context.Context,
	positionID, operationID string,
	firstSentAt, deadline time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		result, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SENT',first_sent_at=COALESCE(first_sent_at,$3),
			    idempotency_deadline=COALESCE(idempotency_deadline,$4),updated_at=$3
			WHERE id=$2 AND owner_kind='MO_FUNDING_POSITION' AND owner_id=$1
			  AND purpose='PAYPAL_MO_CAPTURE'
			  AND state IN ('PREPARED','UNKNOWN')
			  AND (idempotency_deadline IS NULL OR idempotency_deadline>$3)
		`, positionID, operationID, firstSentAt, deadline)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return domain.ErrConflict
		}
		_, err = q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state='ACTIVATION_PENDING',version=version+1,updated_at=$2
			WHERE id=$1 AND state='ACTIVATION_PENDING'
		`, positionID, firstSentAt)
		return err
	})
}

func (r *Repository) RecordMOFundingActivationCompleted(
	ctx context.Context,
	activation paymentapp.MOFundingActivation,
	receipt domain.MOCashReceipt,
	now time.Time,
) (domain.MOFundingPosition, bool, error) {
	applied := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var state string
		if err := q.QueryRowContext(tx, `
			SELECT state FROM payment_mo_funding_positions WHERE id=$1 FOR UPDATE
		`, activation.PositionID).Scan(&state); err != nil {
			return err
		}
		if state == string(domain.MOFundingActive) {
			return nil
		}
		if state != string(domain.MOFundingActivationPending) &&
			state != string(domain.MOFundingActivationUnknown) {
			return domain.ErrFundingNotAvailable
		}
		if receipt.FundingPositionID != activation.PositionID ||
			receipt.AllocationID != activation.AllocationID ||
			receipt.AgencyOrderID != activation.AgencyOrderID ||
			receipt.CustomerPaymentID != activation.CustomerPaymentID ||
			receipt.PayPalAuthorizationID != activation.AuthorizationID ||
			receipt.GrossMinor != activation.AmountMinor ||
			receipt.Currency != activation.Currency {
			return domain.ErrInstructionMismatch
		}
		var fee, net any
		if receipt.EconomicsReconciled {
			fee, net = receipt.ProcessorFeeMinor, receipt.NetReceivableMinor
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_mo_cash_receipts(
				id,funding_position_id,allocation_id,agency_order_id,
				customer_payment_id,paypal_authorization_id,provider_environment,
				kind,provider_capture_id,gross_minor,economics_reconciled,
				processor_fee_minor,net_receivable_minor,currency,
				execution_profile_hash,occurred_at,created_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,'PAYPAL_CAPTURE',$8,$9,$10,$11,$12,$13,$14,$15,$16)
		`, receipt.ID, activation.PositionID, activation.AllocationID,
			activation.AgencyOrderID, activation.CustomerPaymentID,
			activation.AuthorizationID, activation.ProviderEnvironment,
			receipt.ProviderCaptureID, receipt.GrossMinor,
			receipt.EconomicsReconciled, fee, net, receipt.Currency,
			activation.ExecutionProfileHash, receipt.OccurredAt, receipt.CreatedAt); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state='ACTIVE',activated_at=$2,version=version+1,updated_at=$2
			WHERE id=$1
		`, activation.PositionID, now); err != nil {
			return err
		}
		if err := EmitMOFundingEvent(tx, q, activation.PositionID, now); err != nil {
			return err
		}
		authState, paymentState := "PARTIALLY_CAPTURED", "PARTIALLY_CAPTURED"
		var terminalAt any
		if activation.FinalCapture {
			authState, paymentState, terminalAt = "CAPTURED", "CAPTURED", now
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_paypal_authorizations
			SET state=$2,terminal_at=$3,version=version+1,updated_at=$4
			WHERE id=$1 AND state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
		`, activation.AuthorizationID, authState, terminalAt, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_customer_payments
			SET state=$2,version=version+1,updated_at=$3 WHERE id=$1
		`, activation.CustomerPaymentID, paymentState, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state='SUCCEEDED',provider_resource_id=$2,resolved_at=$3,updated_at=$3
			WHERE id=$1 AND state IN ('SENT','UNKNOWN')
		`, activation.OperationID, receipt.ProviderCaptureID, now); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return domain.MOFundingPosition{}, false, err
	}
	current, _, err := r.GetMOFundingActivation(ctx, activation.MerchantOrderID)
	return fundingPosition(current), applied, err
}

func (r *Repository) RecordMOFundingActivationOutcome(
	ctx context.Context,
	activation paymentapp.MOFundingActivation,
	operationState domain.OperationState,
	fundingState domain.MOFundingState,
	reason string,
	now time.Time,
) (domain.MOFundingPosition, error) {
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var resolved any
		if operationState == domain.OperationFailed {
			resolved = now
		}
		result, err := q.ExecContext(tx, `
			UPDATE payment_external_operations
			SET state=$2,last_reason_code=NULLIF($3,''),resolved_at=$4,updated_at=$5,
			    provider_resource_id=CASE
			        WHEN NULLIF($6,'') IS NULL THEN provider_resource_id
			        WHEN provider_resource_id IS NULL OR provider_resource_id=$6 THEN $6
			        ELSE provider_resource_id
			    END
			WHERE id=$1 AND state IN ('PREPARED','SENT','UNKNOWN')
			  AND (provider_resource_id IS NULL OR NULLIF($6,'') IS NULL
			       OR provider_resource_id=$6)
		`, activation.OperationID, operationState, reason, resolved, now,
			activation.ProviderCaptureID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return domain.ErrConflict
		}
		if _, err = q.ExecContext(tx, `
			UPDATE payment_mo_funding_positions
			SET state=$2,version=version+1,updated_at=$3
			WHERE id=$1 AND state IN ('ACTIVATION_PENDING','ACTIVATION_UNKNOWN')
		`, activation.PositionID, fundingState, now); err != nil {
			return err
		}
		return EmitMOFundingEvent(tx, q, activation.PositionID, now)
	})
	if err != nil {
		return domain.MOFundingPosition{}, err
	}
	current, _, err := r.GetMOFundingActivation(ctx, activation.MerchantOrderID)
	return fundingPosition(current), err
}

func (r *Repository) GetMOFundingActivation(
	ctx context.Context,
	merchantOrderID string,
) (paymentapp.MOFundingActivation, *domain.MOCashReceipt, error) {
	activation, err := scanMOFundingActivation(r.database.Queryer(ctx).QueryRowContext(ctx,
		moFundingActivationSelect+` WHERE fp.merchant_order_id=$1`, merchantOrderID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.MOFundingActivation{}, nil, domain.ErrFundingNotAvailable
	}
	if err != nil {
		return paymentapp.MOFundingActivation{}, nil, err
	}
	var receipt domain.MOCashReceipt
	var fee, net sql.NullInt64
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id::text,funding_position_id::text,allocation_id::text,
		       agency_order_id::text,customer_payment_id::text,
		       paypal_authorization_id::text,provider_environment,
		       provider_capture_id,gross_minor,economics_reconciled,
		       processor_fee_minor,net_receivable_minor,currency,occurred_at,created_at
		FROM payment_mo_cash_receipts WHERE funding_position_id=$1
	`, activation.PositionID).Scan(
		&receipt.ID, &receipt.FundingPositionID, &receipt.AllocationID,
		&receipt.AgencyOrderID, &receipt.CustomerPaymentID,
		&receipt.PayPalAuthorizationID, &receipt.ProviderEnvironment,
		&receipt.ProviderCaptureID, &receipt.GrossMinor,
		&receipt.EconomicsReconciled, &fee, &net, &receipt.Currency,
		&receipt.OccurredAt, &receipt.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activation, nil, nil
	}
	if err != nil {
		return paymentapp.MOFundingActivation{}, nil, err
	}
	if fee.Valid {
		receipt.ProcessorFeeMinor = fee.Int64
	}
	if net.Valid {
		receipt.NetReceivableMinor = net.Int64
	}
	return activation, &receipt, nil
}

func fundingPosition(value paymentapp.MOFundingActivation) domain.MOFundingPosition {
	return domain.MOFundingPosition{
		ID: value.PositionID, AllocationID: value.AllocationID,
		AgencyOrderID: value.AgencyOrderID, CustomerPaymentID: value.CustomerPaymentID,
		PayPalAuthorizationID: value.AuthorizationID,
		Rail:                  value.Rail, ProviderEnvironment: value.ProviderEnvironment,
		AmountMinor: value.AmountMinor, Currency: value.Currency, State: value.State,
	}
}

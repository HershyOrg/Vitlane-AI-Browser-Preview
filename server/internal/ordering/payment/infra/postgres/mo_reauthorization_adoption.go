package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const payPalReauthorizationAdoptionColumns = `
	adoption.id::text,adoption.merchant_order_id::text,
	adoption.target_funding_position_id::text,
	adoption.paypal_authorization_id::text,adoption.operation_id::text,
	adoption.provider_environment,
	adoption.previous_provider_authorization_id,
	adoption.provider_authorization_id,adoption.provider_status,
	adoption.operation_state,adoption.amount_minor,adoption.currency,
	adoption.paypal_order_id,adoption.payee_merchant_id,
	adoption.provider_created_at,adoption.actor_user_id::text,
	adoption.evidence_source,adoption.evidence_hash,
	COALESCE(adoption.internal_note,''),adoption.observed_at,
	adoption.operation_first_sent_at,
	adoption.operation_idempotency_deadline,
	adoption.original_authorized_at,adoption.idempotency_key_hash,
	adoption.request_hash,adoption.created_at`

func scanPayPalReauthorizationAdoption(
	scanner interface{ Scan(...any) error },
) (paymentapp.PayPalReauthorizationAdoption, error) {
	var adoption paymentapp.PayPalReauthorizationAdoption
	err := scanner.Scan(
		&adoption.ID, &adoption.MerchantOrderID,
		&adoption.TargetFundingPositionID, &adoption.PayPalAuthorizationID,
		&adoption.OperationID, &adoption.ProviderEnvironment,
		&adoption.PreviousProviderAuthorizationID,
		&adoption.ProviderAuthorizationID, &adoption.ProviderStatus,
		&adoption.OperationState, &adoption.AmountMinor, &adoption.Currency,
		&adoption.PayPalOrderID, &adoption.PayeeMerchantID,
		&adoption.ProviderCreatedAt, &adoption.ActorUserID,
		&adoption.EvidenceSource, &adoption.EvidenceHash,
		&adoption.InternalNote, &adoption.ObservedAt,
		&adoption.OperationFirstSentAt,
		&adoption.OperationIdempotencyDeadline,
		&adoption.OriginalAuthorizedAt, &adoption.IdempotencyKeyHash,
		&adoption.RequestHash, &adoption.CreatedAt,
	)
	return adoption, err
}

func loadPayPalReauthorizationAdoption(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	idempotencyKeyHash string,
) (paymentapp.PayPalReauthorizationAdoption, bool, error) {
	adoption, err := scanPayPalReauthorizationAdoption(q.QueryRowContext(ctx, `
		SELECT `+payPalReauthorizationAdoptionColumns+`
		FROM payment_paypal_reauthorization_adoptions adoption
		WHERE adoption.idempotency_key_hash=$1
	`, idempotencyKeyHash))
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.PayPalReauthorizationAdoption{}, false, nil
	}
	return adoption, err == nil, err
}

func ensurePayPalReauthorizationCandidateUnique(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	environment, providerAuthorizationID string,
) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM payment_paypal_authorizations paypal_authorization
			WHERE paypal_authorization.provider_environment=$1
			  AND paypal_authorization.paypal_authorization_id=$2
			UNION ALL
			SELECT 1
			FROM payment_paypal_reauthorizations reauthorization
			WHERE reauthorization.provider_environment=$1
			  AND reauthorization.provider_authorization_id=$2
			UNION ALL
			SELECT 1
			FROM payment_paypal_reauthorization_adoptions adoption
			WHERE adoption.provider_environment=$1
			  AND adoption.provider_authorization_id=$2
			UNION ALL
			SELECT 1
			FROM payment_external_operations external_operation
			JOIN payment_paypal_authorizations paypal_authorization
			  ON external_operation.owner_kind='PAYPAL_AUTHORIZATION'
			 AND external_operation.owner_id=paypal_authorization.id
			WHERE external_operation.purpose='PAYPAL_REAUTHORIZE'
			  AND external_operation.provider_resource_id=$2
			  AND paypal_authorization.provider_environment=$1
		)
	`, environment, providerAuthorizationID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return domain.ErrConflict
	}
	return nil
}

func lockPayPalReauthorizationAdoptionPlan(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	merchantOrderID, targetPositionID, authorizationID string,
	remainingCapturableMinor int64,
	eligibleAt time.Time,
) (paymentapp.MOReauthorizationAdoptionPlan, error) {
	plan := paymentapp.MOReauthorizationAdoptionPlan{
		MerchantOrderID: merchantOrderID,
		MOReauthorizationPlan: paymentapp.MOReauthorizationPlan{
			Required: true, TargetPositionID: targetPositionID,
			AuthorizationID:          authorizationID,
			RemainingCapturableMinor: remainingCapturableMinor,
		},
	}
	var storedRequestHash string
	err := q.QueryRowContext(ctx, `
		SELECT paypal_authorization.provider_environment,
		       paypal_authorization.paypal_order_id,
		       paypal_authorization.payee_merchant_id,
		       paypal_authorization.paypal_authorization_id,
		       paypal_authorization.amount_minor,paypal_authorization.currency,
		       paypal_authorization.authorized_at,
		       paypal_authorization.honor_refreshed_at,
		       paypal_authorization.reauthorization_count,
		       external_operation.id::text,external_operation.state,
		       external_operation.idempotency_key,
		       external_operation.request_hash,external_operation.first_sent_at,
		       external_operation.idempotency_deadline
		FROM payment_paypal_authorizations paypal_authorization
		JOIN payment_mo_funding_positions funding_position
		  ON funding_position.paypal_authorization_id=paypal_authorization.id
		JOIN merchant_orders merchant_order
		  ON merchant_order.allocation_id=funding_position.allocation_id
		JOIN payment_external_operations external_operation
		  ON external_operation.owner_kind='PAYPAL_AUTHORIZATION'
		 AND external_operation.owner_id=paypal_authorization.id
		 AND external_operation.purpose='PAYPAL_REAUTHORIZE'
		WHERE paypal_authorization.id=$1 AND funding_position.id=$2
		  AND merchant_order.id=$3 AND merchant_order.state='PLANNED'
		  AND funding_position.state='AVAILABLE'
		  AND paypal_authorization.state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
		  AND external_operation.state IN ('SENT','UNKNOWN')
		  AND external_operation.provider_resource_id IS NULL
		  AND external_operation.first_sent_at IS NOT NULL
		  AND external_operation.idempotency_deadline IS NOT NULL
		  AND external_operation.idempotency_deadline <= $4
		FOR UPDATE OF paypal_authorization,external_operation
	`, authorizationID, targetPositionID, merchantOrderID, eligibleAt).Scan(
		&plan.ProviderEnvironment, &plan.PayPalOrderID,
		&plan.PayeeMerchantID, &plan.PreviousProviderAuthorizationID,
		&plan.AuthorizedAmountMinor, &plan.Currency,
		&plan.OriginalAuthorizedAt, &plan.HonorRefreshedAt,
		&plan.ReauthorizationCount, &plan.OperationID,
		&plan.OperationState, &plan.OperationIdempotencyKey,
		&storedRequestHash, &plan.FirstSentAt, &plan.IdempotencyDeadline,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.MOReauthorizationAdoptionPlan{},
			domain.ErrFundingOutcomeUnknown
	}
	if err != nil {
		return paymentapp.MOReauthorizationAdoptionPlan{}, err
	}
	if storedRequestHash != moReauthorizationRequestHash(plan.MOReauthorizationPlan) {
		return paymentapp.MOReauthorizationAdoptionPlan{}, domain.ErrConflict
	}
	if plan.FirstSentAt.Before(plan.OriginalAuthorizedAt) ||
		plan.IdempotencyDeadline.Before(plan.FirstSentAt) {
		return paymentapp.MOReauthorizationAdoptionPlan{},
			domain.ErrInstructionMismatch
	}
	return plan, nil
}

func (r *Repository) PrepareMOReauthorizationAdoption(
	ctx context.Context,
	merchantOrderID string,
	request paymentapp.PayPalReauthorizationAdoption,
) (paymentapp.MOReauthorizationAdoptionPlan,
	*paymentapp.PayPalReauthorizationAdoption, error,
) {
	var plan paymentapp.MOReauthorizationAdoptionPlan
	var existing *paymentapp.PayPalReauthorizationAdoption
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		stored, found, err := loadPayPalReauthorizationAdoption(
			tx, q, request.IdempotencyKeyHash,
		)
		if err != nil {
			return err
		}
		if found {
			if stored.RequestHash != request.RequestHash ||
				stored.MerchantOrderID != merchantOrderID {
				return domain.ErrConflict
			}
			existing = &stored
			return nil
		}

		var targetPositionID, authorizationID, currency string
		var authorizedAmountMinor int64
		if err := q.QueryRowContext(tx, `
			SELECT funding_position.id::text,paypal_authorization.id::text,
			       paypal_authorization.amount_minor,paypal_authorization.currency
			FROM merchant_orders merchant_order
			JOIN payment_mo_funding_positions funding_position
			  ON funding_position.allocation_id=merchant_order.allocation_id
			JOIN payment_paypal_authorizations paypal_authorization
			  ON paypal_authorization.id=funding_position.paypal_authorization_id
			WHERE merchant_order.id=$1 AND merchant_order.state='PLANNED'
			  AND funding_position.state='AVAILABLE'
			  AND paypal_authorization.state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
			  AND EXISTS(
			      SELECT 1 FROM payment_external_operations external_operation
			      WHERE external_operation.owner_kind='PAYPAL_AUTHORIZATION'
			        AND external_operation.owner_id=paypal_authorization.id
			        AND external_operation.purpose='PAYPAL_REAUTHORIZE'
			        AND external_operation.state IN ('SENT','UNKNOWN')
			        AND external_operation.provider_resource_id IS NULL
			        AND external_operation.first_sent_at IS NOT NULL
			        AND external_operation.idempotency_deadline IS NOT NULL
			        AND external_operation.idempotency_deadline <= $2
			  )
		`, merchantOrderID, request.CreatedAt).Scan(
			&targetPositionID, &authorizationID,
			&authorizedAmountMinor, &currency,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrFundingOutcomeUnknown
			}
			return err
		}
		remainingMinor, err := lockRemainingCapturableMinor(
			tx, q, authorizationID, targetPositionID, currency,
			authorizedAmountMinor,
		)
		if err != nil {
			return err
		}
		plan, err = lockPayPalReauthorizationAdoptionPlan(
			tx, q, merchantOrderID, targetPositionID, authorizationID,
			remainingMinor, request.CreatedAt,
		)
		if err != nil {
			return err
		}
		if plan.AuthorizedAmountMinor != authorizedAmountMinor ||
			plan.Currency != currency || remainingMinor <= 0 {
			return domain.ErrConflict
		}
		if request.ProviderAuthorizationID ==
			plan.PreviousProviderAuthorizationID {
			return domain.ErrInstructionMismatch
		}
		return ensurePayPalReauthorizationCandidateUnique(
			tx, q, plan.ProviderEnvironment,
			request.ProviderAuthorizationID,
		)
	})
	if err != nil {
		return paymentapp.MOReauthorizationAdoptionPlan{}, nil,
			fmt.Errorf("prepare PayPal reauthorization adoption: %w", err)
	}
	return plan, existing, nil
}

func payPalReauthorizationAdoptionOutcome(
	providerStatus string,
) (domain.OperationState, string) {
	switch providerStatus {
	case "CREATED":
		return domain.OperationSucceeded, ""
	case "DENIED", "VOIDED", "EXPIRED":
		return domain.OperationFailed, "REAUTHORIZATION_" + providerStatus
	default:
		return domain.OperationUnknown,
			"MANUAL_REAUTHORIZATION_STATUS_REQUIRES_RECONCILIATION"
	}
}

func sameMOReauthorizationAdoptionPlan(
	want, got paymentapp.MOReauthorizationAdoptionPlan,
) bool {
	return want.MerchantOrderID == got.MerchantOrderID &&
		want.AuthorizationID == got.AuthorizationID &&
		want.TargetPositionID == got.TargetPositionID &&
		want.ProviderEnvironment == got.ProviderEnvironment &&
		want.PayPalOrderID == got.PayPalOrderID &&
		want.PayeeMerchantID == got.PayeeMerchantID &&
		want.PreviousProviderAuthorizationID ==
			got.PreviousProviderAuthorizationID &&
		want.AuthorizedAmountMinor == got.AuthorizedAmountMinor &&
		want.RemainingCapturableMinor == got.RemainingCapturableMinor &&
		want.Currency == got.Currency &&
		want.OriginalAuthorizedAt.Equal(got.OriginalAuthorizedAt) &&
		want.HonorRefreshedAt.Equal(got.HonorRefreshedAt) &&
		want.ReauthorizationCount == got.ReauthorizationCount &&
		want.OperationID == got.OperationID &&
		want.OperationIdempotencyKey == got.OperationIdempotencyKey &&
		want.FirstSentAt.Equal(got.FirstSentAt) &&
		want.IdempotencyDeadline.Equal(got.IdempotencyDeadline)
}

func (r *Repository) RecordMOReauthorizationAdoption(
	ctx context.Context,
	plan paymentapp.MOReauthorizationAdoptionPlan,
	adoption paymentapp.PayPalReauthorizationAdoption,
	now time.Time,
) (paymentapp.PayPalReauthorizationAdoption, bool, error) {
	var recorded paymentapp.PayPalReauthorizationAdoption
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		stored, found, err := loadPayPalReauthorizationAdoption(
			tx, q, adoption.IdempotencyKeyHash,
		)
		if err != nil {
			return err
		}
		if found {
			if stored.RequestHash != adoption.RequestHash ||
				stored.MerchantOrderID != plan.MerchantOrderID {
				return domain.ErrConflict
			}
			recorded, replay = stored, true
			return nil
		}

		remainingMinor, err := lockRemainingCapturableMinor(
			tx, q, plan.AuthorizationID, plan.TargetPositionID,
			plan.Currency, plan.AuthorizedAmountMinor,
		)
		if err != nil {
			return err
		}
		locked, err := lockPayPalReauthorizationAdoptionPlan(
			tx, q, plan.MerchantOrderID, plan.TargetPositionID,
			plan.AuthorizationID, remainingMinor, now,
		)
		if err != nil {
			return err
		}
		if !sameMOReauthorizationAdoptionPlan(plan, locked) {
			return domain.ErrConflict
		}

		expectedState, reason := payPalReauthorizationAdoptionOutcome(
			adoption.ProviderStatus,
		)
		if adoption.MerchantOrderID != plan.MerchantOrderID ||
			adoption.TargetFundingPositionID != plan.TargetPositionID ||
			adoption.PayPalAuthorizationID != plan.AuthorizationID ||
			adoption.OperationID != plan.OperationID ||
			adoption.ProviderEnvironment != plan.ProviderEnvironment ||
			adoption.PreviousProviderAuthorizationID !=
				plan.PreviousProviderAuthorizationID ||
			adoption.ProviderAuthorizationID == "" ||
			adoption.ProviderAuthorizationID ==
				plan.PreviousProviderAuthorizationID ||
			adoption.OperationState != expectedState ||
			adoption.AmountMinor != remainingMinor ||
			adoption.Currency != plan.Currency || adoption.Currency != "USD" ||
			adoption.PayPalOrderID != plan.PayPalOrderID ||
			adoption.PayeeMerchantID != plan.PayeeMerchantID ||
			adoption.ProviderCreatedAt.Before(plan.FirstSentAt) ||
			!adoption.ProviderCreatedAt.Before(
				plan.OriginalAuthorizedAt.Add(payPalAuthorizationWindow),
			) || adoption.ObservedAt.Before(adoption.ProviderCreatedAt) ||
			adoption.ObservedAt.After(adoption.CreatedAt.Add(5*time.Minute)) ||
			adoption.CreatedAt.Before(plan.IdempotencyDeadline) ||
			!adoption.OperationFirstSentAt.Equal(plan.FirstSentAt) ||
			!adoption.OperationIdempotencyDeadline.Equal(plan.IdempotencyDeadline) ||
			!adoption.OriginalAuthorizedAt.Equal(plan.OriginalAuthorizedAt) {
			return domain.ErrInstructionMismatch
		}
		if err := ensurePayPalReauthorizationCandidateUnique(
			tx, q, plan.ProviderEnvironment,
			adoption.ProviderAuthorizationID,
		); err != nil {
			return err
		}
		if err := r.RecordMOReauthorizationOutcome(
			tx, plan.MOReauthorizationPlan, expectedState,
			adoption.ProviderAuthorizationID, adoption.AmountMinor,
			adoption.Currency, adoption.ProviderCreatedAt, reason, now,
		); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			INSERT INTO payment_paypal_reauthorization_adoptions(
				id,merchant_order_id,target_funding_position_id,
				paypal_authorization_id,operation_id,operation_owner_kind,
				provider_environment,previous_provider_authorization_id,
				provider_authorization_id,provider_status,operation_state,
				amount_minor,currency,paypal_order_id,payee_merchant_id,
				provider_created_at,actor_user_id,evidence_source,evidence_hash,
				internal_note,observed_at,operation_first_sent_at,
				operation_idempotency_deadline,original_authorized_at,
				idempotency_key_hash,request_hash,created_at
			) VALUES(
				$1,$2,$3,$4,$5,'PAYPAL_AUTHORIZATION',$6,$7,$8,$9,$10,
				$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),$20,$21,
				$22,$23,$24,$25,$26
			)
		`, adoption.ID, adoption.MerchantOrderID,
			adoption.TargetFundingPositionID, adoption.PayPalAuthorizationID,
			adoption.OperationID, adoption.ProviderEnvironment,
			adoption.PreviousProviderAuthorizationID,
			adoption.ProviderAuthorizationID, adoption.ProviderStatus,
			adoption.OperationState, adoption.AmountMinor, adoption.Currency,
			adoption.PayPalOrderID, adoption.PayeeMerchantID,
			adoption.ProviderCreatedAt, adoption.ActorUserID,
			adoption.EvidenceSource, adoption.EvidenceHash,
			adoption.InternalNote, adoption.ObservedAt,
			adoption.OperationFirstSentAt,
			adoption.OperationIdempotencyDeadline,
			adoption.OriginalAuthorizedAt, adoption.IdempotencyKeyHash,
			adoption.RequestHash, adoption.CreatedAt,
		); err != nil {
			return err
		}
		recorded = adoption
		return nil
	})
	if uniqueViolation(err) {
		return paymentapp.PayPalReauthorizationAdoption{}, false,
			domain.ErrConflict
	}
	if err != nil {
		return paymentapp.PayPalReauthorizationAdoption{}, false,
			fmt.Errorf("record PayPal reauthorization adoption: %w", err)
	}
	return recorded, replay, nil
}

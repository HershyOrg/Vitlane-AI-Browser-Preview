package postgres

import (
	"context"
	"fmt"
	"time"

	operatorapp "github.com/vitlane/vitlane/server/internal/ordering/operator/app"
)

// The CTE mirrors the Payment-owned locked eligibility predicates closely
// enough to avoid presenting actions that cannot run. Payment remains the
// authority and repeats these checks under its funding/operation locks.
const paypalResourceAdoptionCandidates = `
	WITH paypal_resource_adoption_candidates AS (
		SELECT
			'PAYPAL_REAUTHORIZATION'::text AS reconciliation_kind,
			operation.id::text AS operation_id,
			paypal_authorization.agency_order_id::text AS agency_order_id,
			target.merchant_order_id,
			''::text AS compensation_id,
			paypal_authorization.provider_environment,
			remaining.amount_minor,
			paypal_authorization.currency,
			operation.state AS operation_state,
			paypal_authorization.state AS owner_state,
			COALESCE(operation.last_reason_code,'') AS reason_code,
			operation.first_sent_at,
			operation.idempotency_deadline,
			operation.updated_at
		FROM payment_external_operations operation
		JOIN payment_paypal_authorizations paypal_authorization
		  ON operation.owner_kind='PAYPAL_AUTHORIZATION'
		 AND operation.owner_id=paypal_authorization.id
		JOIN LATERAL (
			SELECT merchant_order.id::text AS merchant_order_id
			FROM payment_mo_funding_positions position
			JOIN merchant_orders merchant_order
			  ON merchant_order.allocation_id=position.allocation_id
			WHERE position.paypal_authorization_id=paypal_authorization.id
			  AND position.state='AVAILABLE'
			  AND merchant_order.state='PLANNED'
			ORDER BY position.id
			LIMIT 1
		) target ON true
		JOIN LATERAL (
			SELECT SUM(position.amount_minor)::bigint AS amount_minor
			FROM payment_mo_funding_positions position
			WHERE position.paypal_authorization_id=paypal_authorization.id
			  AND position.state='AVAILABLE'
		) remaining ON remaining.amount_minor > 0
		WHERE operation.purpose='PAYPAL_REAUTHORIZE'
		  AND operation.state IN ('SENT','UNKNOWN')
		  AND operation.provider_resource_id IS NULL
		  AND operation.first_sent_at IS NOT NULL
		  AND operation.idempotency_deadline IS NOT NULL
		  AND operation.idempotency_deadline <= $1
		  AND paypal_authorization.state IN ('AUTHORIZED','PARTIALLY_CAPTURED')
		  AND NOT EXISTS (
			SELECT 1
			FROM payment_mo_funding_positions sibling
			WHERE sibling.paypal_authorization_id=paypal_authorization.id
			  AND sibling.state IN (
				'ACTIVATION_PENDING','ACTIVATION_UNKNOWN',
				'RELEASE_PENDING','RELEASE_UNKNOWN'
			  )
		  )

		UNION ALL

		SELECT
			'PAYPAL_MO_REFUND'::text AS reconciliation_kind,
			operation.id::text AS operation_id,
			compensation.agency_order_id::text AS agency_order_id,
			merchant_order.id::text AS merchant_order_id,
			compensation.id::text AS compensation_id,
			compensation.provider_environment,
			compensation.amount_minor,
			compensation.currency,
			operation.state AS operation_state,
			compensation.state AS owner_state,
			COALESCE(operation.last_reason_code,'') AS reason_code,
			operation.first_sent_at,
			operation.idempotency_deadline,
			GREATEST(operation.updated_at,compensation.updated_at) AS updated_at
		FROM payment_mo_compensations compensation
		JOIN merchant_orders merchant_order
		  ON merchant_order.allocation_id=compensation.allocation_id
		JOIN LATERAL (
			SELECT candidate.*
			FROM payment_external_operations candidate
			WHERE candidate.owner_kind='MO_COMPENSATION'
			  AND candidate.owner_id=compensation.id
			  AND candidate.purpose='PAYPAL_MO_REFUND'
			ORDER BY candidate.created_at DESC,candidate.id DESC
			LIMIT 1
		) operation ON true
		WHERE compensation.rail='PAYPAL'
		  AND compensation.action='REFUND'
		  AND compensation.provider_environment IN ('SANDBOX','LIVE')
		  AND compensation.state IN ('EXECUTION_PENDING','OUTCOME_UNKNOWN')
		  AND compensation.provider_resource_id IS NULL
		  AND operation.state IN ('SENT','UNKNOWN')
		  AND operation.provider_resource_id IS NULL
		  AND operation.first_sent_at IS NOT NULL
		  AND operation.idempotency_deadline IS NOT NULL
		  AND operation.idempotency_deadline <= $1
	)
`

func (r *Repository) ListPayPalResourceAdoptions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]operatorapp.PayPalResourceAdoptionItem, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx,
		paypalResourceAdoptionCandidates+`
		SELECT reconciliation_kind,operation_id,agency_order_id,
		       merchant_order_id,compensation_id,provider_environment,
		       amount_minor,currency,operation_state,owner_state,reason_code,
		       first_sent_at,idempotency_deadline,updated_at
		FROM paypal_resource_adoption_candidates
		ORDER BY updated_at DESC,operation_id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list PayPal resource adoption candidates: %w", err)
	}
	defer rows.Close()

	items := make([]operatorapp.PayPalResourceAdoptionItem, 0)
	for rows.Next() {
		var item operatorapp.PayPalResourceAdoptionItem
		if err := rows.Scan(
			&item.ReconciliationKind, &item.OperationID,
			&item.AgencyOrderID, &item.MerchantOrderID,
			&item.CompensationID, &item.ProviderEnvironment,
			&item.AmountMinor, &item.Currency, &item.OperationState,
			&item.OwnerState, &item.ReasonCode, &item.FirstSentAt,
			&item.IdempotencyDeadline, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan PayPal resource adoption candidate: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PayPal resource adoption candidates: %w", err)
	}
	return items, nil
}

func (r *Repository) CountPayPalResourceAdoptions(
	ctx context.Context,
	now time.Time,
) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx,
		paypalResourceAdoptionCandidates+`
		SELECT count(*) FROM paypal_resource_adoption_candidates
	`, now).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count PayPal resource adoption candidates: %w", err)
	}
	return count, nil
}

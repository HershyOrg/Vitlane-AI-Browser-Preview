package postgres

import (
	"context"
	"database/sql"
	"errors"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

func (r *Repository) BindFundingForEffect(ctx context.Context, orderID, moID, allocationID string) (facts paymentapp.FundingFactsForEffect, err error) {
	// This only correlates immutable identities. It cannot activate, release or
	// create an external operation. Existing bindings cannot be transferred.
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `UPDATE payment_mo_funding_positions SET merchant_order_id=$2 WHERE agency_order_id=$1 AND allocation_id=$3 AND (merchant_order_id IS NULL OR merchant_order_id=$2) RETURNING id::text,state,EXISTS(SELECT 1 FROM payment_mo_compensations c WHERE c.allocation_id=$3)`, orderID, moID, allocationID).Scan(&facts.PositionID, &facts.State, &facts.HasCompensation)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrFundingNotAvailable
	}
	return
}

func (r *Repository) LoadCompensationForEffect(ctx context.Context, prepared paymentapp.MOCompensationExecution) (paymentapp.MOCompensationExecution, error) {
	originalOperationID, originalKey := prepared.OperationID, prepared.OperationIdempotencyKey
	current, err := scanMOCompensation(r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT `+moCompensationColumns+` FROM payment_mo_compensations compensation WHERE id=$1`, prepared.Compensation.ID))
	if err != nil {
		return prepared, err
	}
	if current.AllocationID != prepared.Compensation.AllocationID || current.AmountMinor != prepared.Compensation.AmountMinor {
		return prepared, domain.ErrInvalid
	}
	prepared.Compensation = current
	if err := loadMOCompensationOperation(ctx, r.database.Queryer(ctx), &prepared); err != nil {
		return prepared, err
	}
	if prepared.OperationID != "" && (prepared.OperationID != originalOperationID || prepared.OperationIdempotencyKey != originalKey) {
		return prepared, domain.ErrConflict
	}
	return prepared, nil
}

// Recovery refreshes only the stored sender lease, retaining the exact prepared
// authorization request. It never reads Procurement state or grants permission.
func (r *Repository) LoadMOReauthorization(ctx context.Context, p paymentapp.MOReauthorizationPlan, now time.Time) (paymentapp.MOReauthorizationPlan, error) {
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		_, err := q.ExecContext(tx, `UPDATE payment_external_operations SET state='UNKNOWN',last_reason_code='STALE_SEND_RECOVERY',updated_at=$2 WHERE id=$1 AND owner_id=$3 AND purpose='PAYPAL_REAUTHORIZE' AND state='SENT' AND updated_at<=$4 AND (idempotency_deadline IS NULL OR idempotency_deadline>$2)`, p.OperationID, now, p.AuthorizationID, now.Add(-payPalProviderSendLease))
		if err != nil {
			return err
		}
		return q.QueryRowContext(tx, `SELECT state,COALESCE(provider_resource_id,'') FROM payment_external_operations WHERE id=$1 AND owner_id=$2 AND purpose='PAYPAL_REAUTHORIZE' AND idempotency_key=$3 AND request_hash=$4`, p.OperationID, p.AuthorizationID, p.OperationIdempotencyKey, moReauthorizationRequestHash(p)).Scan(&p.OperationState, &p.OperationResourceID)
	})
	return p, err
}

func (r *Repository) BindProcessEffect(ctx context.Context, kind, id string) error {
	scope, ok := procmsg.ExecutionFrom(ctx)
	if !ok {
		return procmsg.ErrExecutionRequired
	}
	table := "payment_mo_funding_positions"
	if kind == "COMPENSATION" {
		table = "payment_mo_compensations"
	} else if kind != "FUNDING" {
		return domain.ErrInvalid
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE `+table+` SET process_flow_id=$2,process_effect_id=$3 WHERE id=$1 AND agency_order_id=$4`, id, scope.FlowID, scope.EffectID, scope.AgencyOrderID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return domain.ErrInvalid
	}
	return nil
}

package postgres

import (
	"context"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// ReleasePurchase closes a reserved pre-merchant permission. It never guesses
// the outcome of STARTED/UNKNOWN merchant work and never releases customer money.
func (r *Repository) ReleasePurchase(ctx context.Context, orderID, moID, flowID string, now time.Time) error {
	if err := procmsg.RequireExecution(ctx, moID, procmsg.EffectReleasePurchase); err != nil {
		return err
	}
	q := r.database.Queryer(ctx)
	var taskID, state, flow string
	if err := q.QueryRowContext(ctx, `SELECT task_id::text,state,process_flow_id::text FROM procurement_effect_locks WHERE merchant_order_id=$1 FOR UPDATE`, moID).Scan(&taskID, &state, &flow); err != nil {
		return err
	}
	if flow != flowID {
		return procmsg.ErrEffectInvalid
	}
	if state == "FAILED" {
		return nil
	}
	if state != "FUNDING_PENDING" && state != "FUNDING_UNKNOWN" {
		return domain.ErrEffectAlreadyStarted
	}
	if _, err := q.ExecContext(ctx, `UPDATE procurement_effect_locks SET state='FAILED',resolved_at=$2,version=version+1 WHERE merchant_order_id=$1`, moID, now); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE merchant_orders SET state='FAILED',failure_code='PURCHASE_STOPPED',result_hash=COALESCE(result_hash,'process-release:'||$3),version=version+1,updated_at=$4 WHERE id=$1 AND agency_order_id=$2 AND state IN ('PLANNED','READY_TO_PLACE')`, moID, orderID, flowID, now); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE merchant_order_execution_tasks SET state='FAILED',handled_at=$2,version=version+1,updated_at=$2 WHERE id=$1 AND state IN ('QUEUED','CLAIMED','IN_PROGRESS')`, taskID, now); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE merchant_payments SET state='FAILED',version=version+1,updated_at=$2 WHERE merchant_order_id=$1 AND state IN ('PLANNED','READY_TO_PLACE')`, moID, now); err != nil {
		return err
	}
	if err := emitEffectLockEvent(ctx, q, moID, now); err != nil {
		return err
	}
	return emitMerchantOrderEvent(ctx, q, moID, now)
}

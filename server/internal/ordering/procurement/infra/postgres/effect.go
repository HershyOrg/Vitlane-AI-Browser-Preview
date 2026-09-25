package postgres

import (
	"context"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	"time"
)

func (r *Repository) ValidatePurchaseActor(ctx context.Context, taskID, actorID string, now time.Time) error {
	task, _, err := r.lockTask(ctx, taskID)
	if err != nil {
		return err
	}
	return requireActiveAssignment(task, actorID, now)
}
func (r *Repository) PurchaseSubjectByMO(ctx context.Context, moID string) (procurementapp.QueueItem, error) {
	var taskID string
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id::text FROM merchant_order_execution_tasks WHERE merchant_order_id=$1`, moID).Scan(&taskID); err != nil {
		return procurementapp.QueueItem{}, err
	}
	return r.GetPurchaseSubject(ctx, taskID)
}

func (r *Repository) LockPurchaseSubject(ctx context.Context, taskID string) (procurementapp.QueueItem, error) {
	if _, _, err := r.lockTask(ctx, taskID); err != nil {
		return procurementapp.QueueItem{}, err
	}
	return r.GetPurchaseSubject(ctx, taskID)
}

// GetPurchaseSubject reads only Procurement-owned identities and state. Queue
// projections join other Owners for presentation and cannot authorize execution.
func (r *Repository) GetPurchaseSubject(ctx context.Context, taskID string) (item procurementapp.QueueItem, err error) {
	q := r.database.Queryer(ctx)
	err = q.QueryRowContext(ctx, `SELECT t.id::text,t.merchant_order_id::text,t.agency_order_id::text,t.state,COALESCE(t.assigned_operator_user_id::text,''),mo.id::text,mo.agency_order_id::text,mo.allocation_id::text,mo.execution_mode,mo.state FROM merchant_order_execution_tasks t JOIN merchant_orders mo ON mo.id=t.merchant_order_id WHERE t.id=$1`, taskID).Scan(&item.Task.ID, &item.Task.MerchantOrderID, &item.Task.AgencyOrderID, &item.Task.State, &item.Task.AssignedOperatorUserID, &item.MerchantOrder.ID, &item.MerchantOrder.AgencyOrderID, &item.MerchantOrder.AllocationID, &item.MerchantOrder.ExecutionMode, &item.MerchantOrder.State)
	if err != nil {
		return item, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id::text FROM merchant_order_units WHERE merchant_order_id=$1 ORDER BY id`, item.MerchantOrder.ID)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	for rows.Next() {
		var u domain.MerchantOrderUnit
		if err := rows.Scan(&u.ID); err != nil {
			return item, err
		}
		item.Units = append(item.Units, u)
	}
	return item, rows.Err()
}

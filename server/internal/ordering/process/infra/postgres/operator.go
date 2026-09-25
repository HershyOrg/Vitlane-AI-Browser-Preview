package postgres

import (
	"context"
	"database/sql"
	"errors"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

const pendingExpectations = `WITH expected AS (
 SELECT p.agency_order_id,x.value AS fact FROM agency_order_processes p
 CROSS JOIN LATERAL jsonb_each(COALESCE(p.process_state->'effects','{}')) x
 UNION ALL
 SELECT p.agency_order_id,e.value FROM agency_order_processes p
 CROSS JOIN LATERAL jsonb_each(COALESCE(p.process_state->'merchantOrders','{}')) mo
 CROSS JOIN LATERAL jsonb_each(COALESCE(mo.value->'effects','{}')) e
), waiting AS (
 SELECT q.*,x.fact FROM expected x JOIN order_process_effects q ON q.id::text=x.fact->>'effectId'
 WHERE COALESCE(x.fact->>'outcome','') NOT IN ('SUCCEEDED','REJECTED')
 AND (x.fact->>'outcome' IN ('EFFECT_UNKNOWN','ATTENTION_REQUIRED','WAITING') OR q.attempt_count>=5)
)`

func (r *Store) ListInterventions(ctx context.Context, n int) ([]processapp.InterventionItem, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, pendingExpectations+` SELECT id::text,agency_order_id::text,COALESCE(merchant_order_id::text,''),target,type,COALESCE(fact->>'reason','OWNER_RESULT_PENDING'),attempt_count,updated_at FROM waiting ORDER BY updated_at,id LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []processapp.InterventionItem{}
	for rows.Next() {
		var i processapp.InterventionItem
		if err := rows.Scan(&i.EffectID, &i.AgencyOrderID, &i.MerchantOrderID, &i.Target, &i.Type, &i.LastErrorCode, &i.AttemptCount, &i.UpdatedAt); err != nil {
			return nil, err
		}
		i.Guidance = procmsg.Guidance{ReasonCode: i.LastErrorCode, WaitingFor: i.Type, CustomerAction: "WAIT", OperatorAction: "RETRY"}
		if i.Target == string(procmsg.TargetPayment) {
			i.Guidance.OperatorAction = "RECONCILE_PAYMENT"
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
func (r *Store) CountInterventions(ctx context.Context) (int, error) {
	var n int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, pendingExpectations+` SELECT count(*) FROM waiting`).Scan(&n)
	return n, err
}
func (r *Store) ReadEffectScope(ctx context.Context, id string) (e procmsg.ProcessEffect, err error) {
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id::text,agency_order_id::text,COALESCE(merchant_order_id::text,''),target,type FROM order_process_effects WHERE id=$1`, id).Scan(&e.ID, &e.AgencyOrderID, &e.MerchantOrderID, &e.Target, &e.Type)
	if errors.Is(err, sql.ErrNoRows) {
		err = processapp.ErrInterventionNotFound
	}
	return
}

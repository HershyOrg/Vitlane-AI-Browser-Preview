package postgres

import (
	"context"
	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"
)

func (r *Repository) RequirePurchaseRegistration(ctx context.Context, orderID, moID string, unitIDs []string) error {
	if len(unitIDs) == 0 {
		return logisticsapp.ErrPurchaseRegistrationMissing
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT merchant_order_unit_id::text FROM logistics_expected_units WHERE agency_order_id=$1 AND merchant_order_id=$2 AND merchant_order_unit_id=ANY(string_to_array($3,',')::uuid[]) FOR SHARE`, orderID, moID, strings.Join(unitIDs, ","))
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range unitIDs {
		if !seen[id] {
			return logisticsapp.ErrPurchaseRegistrationMissing
		}
	}
	return nil
}

func (r *Repository) LockCancellationUnits(ctx context.Context, orderID, moID string) (int, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT fulfillment FROM logistics_expected_units WHERE agency_order_id=$1 AND merchant_order_id=$2 ORDER BY id FOR UPDATE`, orderID, moID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return 0, err
		}
		switch state {
		case "DELIVERED_EXPECTED", "RESOLVED", "NONCONFORMING_RESOLVED", "SUPERSEDED_BY_CANCELLATION":
		default:
			n++
		}
	}
	return n, rows.Err()
}
func (r *Repository) ApplyCancellation(ctx context.Context, orderID, moID string, now time.Time) error {
	if err := procmsg.RequireExecution(ctx, moID, procmsg.EffectApplyLogisticsCancellation); err != nil {
		return err
	}
	q := r.database.Queryer(ctx)
	rows, err := q.QueryContext(ctx, `UPDATE logistics_expected_units SET fulfillment='SUPERSEDED_BY_CANCELLATION',version=version+1,updated_at=$3 WHERE agency_order_id=$1 AND merchant_order_id=$2 AND fulfillment NOT IN ('DELIVERED_EXPECTED','RESOLVED','NONCONFORMING_RESOLVED','SUPERSEDED_BY_CANCELLATION') RETURNING id::text`, orderID, moID, now)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := emitUnitEvent(ctx, q, id, now); err != nil {
			return err
		}
	}
	return nil
}

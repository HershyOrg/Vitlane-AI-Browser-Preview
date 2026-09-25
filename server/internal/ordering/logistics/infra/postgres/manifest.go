package postgres

import (
	"context"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

// RegisterExpectedUnitManifest reads no mutable Procurement state. Unit identity
// and the observed lifecycle arrive in an immutable effect from the reducer.
func (r *Repository) RegisterExpectedUnitManifest(ctx context.Context, orderID string, p procmsg.RegisterExpectedUnitsPayload, now time.Time) error {
	if err := procmsg.RequireExecution(ctx, p.MerchantOrderID, procmsg.EffectRegisterExpectedUnits); err != nil {
		return err
	}
	if len(p.Units) == 0 {
		return procmsg.ErrEffectInvalid
	}
	fulfillment := "AWAITING_EFFECT"
	switch p.State {
	case "PLANNED", "READY_TO_PLACE", "PLACEMENT_PENDING", "PLACEMENT_UNKNOWN", "PLACED":
	case "FAILED":
		fulfillment = "NO_PLACEMENT"
	case "CANCELLED":
		fulfillment = "SUPERSEDED_BY_CANCELLATION"
	default:
		return procmsg.ErrEffectInvalid
	}
	q := r.database.Queryer(ctx)
	seen := map[string]bool{}
	for _, u := range p.Units {
		if u.ID == "" || u.LineID == "" || u.UnitIndex < 0 || seen[u.ID] {
			return procmsg.ErrEffectInvalid
		}
		seen[u.ID] = true
		var id string
		err := q.QueryRowContext(ctx, `INSERT INTO logistics_expected_units(id,merchant_order_unit_id,merchant_order_id,agency_order_id,line_id,unit_index,fulfillment,registered_at,version,updated_at)
 VALUES(md5($1||':expected')::uuid,$1::uuid,$2,$3,$4,$5,$6,$7,1,$7)
 ON CONFLICT(merchant_order_unit_id) DO UPDATE SET updated_at=logistics_expected_units.updated_at
 WHERE logistics_expected_units.merchant_order_id=EXCLUDED.merchant_order_id AND logistics_expected_units.agency_order_id=EXCLUDED.agency_order_id
 AND logistics_expected_units.line_id=EXCLUDED.line_id AND logistics_expected_units.unit_index=EXCLUDED.unit_index RETURNING id::text`, u.ID, p.MerchantOrderID, orderID, u.LineID, u.UnitIndex, fulfillment, now).Scan(&id)
		if err != nil {
			return err
		}
		if fulfillment != "AWAITING_EFFECT" {
			if _, err := q.ExecContext(ctx, `UPDATE logistics_expected_units SET fulfillment=$2,version=version+1,updated_at=$3 WHERE id=$1 AND fulfillment IN ('AWAITING_EFFECT','IN_TRANSIT_EXPECTED') AND fulfillment<>$2`, id, fulfillment, now); err != nil {
				return err
			}
		}
		if err := emitUnitEvent(ctx, q, id, now); err != nil {
			return err
		}
	}
	return nil
}

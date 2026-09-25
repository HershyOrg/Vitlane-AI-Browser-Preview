package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

func (r *Repository) ReserveCancellation(ctx context.Context, e procmsg.ProcessEffect, p procmsg.CancellationContext, now time.Time) (procmsg.CancellationReservation, error) {
	q := r.database.Queryer(ctx)
	result := procmsg.CancellationReservation{ReservationID: e.ID}
	var flow, state string
	err := q.QueryRowContext(ctx, `SELECT flow_id::text,state,undelivered_units FROM logistics_cancellation_reservations WHERE id=$1`, e.ID).Scan(&flow, &state, &result.UndeliveredUnits)
	if err == nil {
		if flow != e.FlowID || state == "RELEASED" {
			return result, procmsg.ErrEffectInvalid
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	// All unit rows are locked in one stable order. A delivery that commits first
	// contributes to eligibility; a later delivery remains a physical fact and
	// cannot retract a cancellation already reserved by this customer request.
	rows, err := q.QueryContext(ctx, `SELECT merchant_order_unit_id::text,fulfillment FROM logistics_expected_units WHERE agency_order_id=$1 AND merchant_order_id=$2 ORDER BY id FOR UPDATE`, e.AgencyOrderID, e.MerchantOrderID)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id, fulfillment string
		if err := rows.Scan(&id, &fulfillment); err != nil {
			rows.Close()
			return result, err
		}
		seen[id] = true
		switch fulfillment {
		case "DELIVERED_EXPECTED", "RESOLVED", "NONCONFORMING_RESOLVED", "SUPERSEDED_BY_CANCELLATION", "NO_PLACEMENT":
		default:
			result.UndeliveredUnits++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(p.UnitIDs) == 0 || len(seen) != len(p.UnitIDs) {
		return result, logisticsapp.ErrPurchaseRegistrationMissing
	}
	for _, id := range p.UnitIDs {
		if !seen[id] {
			return result, logisticsapp.ErrPurchaseRegistrationMissing
		}
	}
	if p.CancelKind == "DELAY_RULE" && p.OwnerState == "PLACED" && result.UndeliveredUnits == 0 {
		return result, logisticsapp.ErrCancellationNotEligible
	}
	_, err = q.ExecContext(ctx, `INSERT INTO logistics_cancellation_reservations(id,agency_order_id,merchant_order_id,flow_id,undelivered_units,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'RESERVED',$6,$6)`, e.ID, e.AgencyOrderID, e.MerchantOrderID, e.FlowID, result.UndeliveredUnits, now)
	return result, err
}

func (r *Repository) ResolveCancellation(ctx context.Context, e procmsg.ProcessEffect, p procmsg.CancellationContext, confirm bool, now time.Time) error {
	q := r.database.Queryer(ctx)
	var state, flow string
	if err := q.QueryRowContext(ctx, `SELECT state,flow_id::text FROM logistics_cancellation_reservations WHERE id=$1 AND agency_order_id=$2 AND merchant_order_id=$3 FOR UPDATE`, p.ReservationID, e.AgencyOrderID, e.MerchantOrderID).Scan(&state, &flow); err != nil {
		return err
	}
	if flow != e.FlowID {
		return procmsg.ErrEffectInvalid
	}
	next := "RELEASED"
	if confirm {
		next = "CONFIRMED"
	}
	if state == next {
		return nil
	}
	if state != "RESERVED" {
		return procmsg.ErrEffectInvalid
	}
	if confirm {
		if err := r.ApplyCancellation(ctx, e.AgencyOrderID, e.MerchantOrderID, now); err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `UPDATE logistics_cancellation_reservations SET state=$2,updated_at=$3 WHERE id=$1`, p.ReservationID, next, now)
	return err
}

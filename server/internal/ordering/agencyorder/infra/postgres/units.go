package postgres

import (
	"context"
	"database/sql"
	"fmt"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

// listUnitFacts composes physical logistics identities with their parent MO's
// compensation state. It never joins a monetary unit/slice table.
func (r *Repository) listUnitFacts(
	ctx context.Context,
	agencyOrderID string,
) ([]agencydomain.UnitFacts, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT unit.id::text, merchant_order.id::text,
		       merchant_order.allocation_id::text, unit.line_id, unit.unit_index,
		       merchant_order.shop_domain,
		       CASE
		         WHEN compensation.action IN ('REFUND','TVIT_REFUND')
		              AND compensation.state='SUCCEEDED' THEN 'REFUNDED'
		         WHEN compensation.action IN ('REFUND','TVIT_REFUND') THEN 'REFUND_PENDING'
		         WHEN refund.state IN ('REQUESTED','REVIEWING') THEN 'REQUESTED'
		         ELSE 'AVAILABLE'
		       END,
		       merchant_order.state, COALESCE(expected.fulfillment,''),
		       COALESCE(return_record.state,''), COALESCE(resolution.cause,''),
		       COALESCE(resolution.decision,''), shipment.id, shipment.carrier,
		       shipment.tracking_ref, shipment.state
		FROM merchant_order_units unit
		JOIN merchant_orders merchant_order ON merchant_order.id=unit.merchant_order_id
		LEFT JOIN logistics_expected_units expected
		  ON expected.merchant_order_unit_id=unit.id
		LEFT JOIN logistics_returns return_record
		  ON return_record.expected_unit_id=expected.id
		LEFT JOIN logistics_delivery_resolutions resolution
		  ON resolution.expected_unit_id=expected.id
		LEFT JOIN logistics_shipment_allocations allocation
		  ON allocation.expected_unit_id=expected.id AND allocation.active
		LEFT JOIN logistics_shipments shipment ON shipment.id=allocation.shipment_id
		LEFT JOIN LATERAL (
			SELECT request.state FROM agency_order_refund_requests request
			WHERE request.allocation_id=merchant_order.allocation_id
			ORDER BY request.created_at DESC LIMIT 1
		) refund ON TRUE
		LEFT JOIN payment_mo_compensations compensation
		  ON compensation.allocation_id=merchant_order.allocation_id
		WHERE merchant_order.agency_order_id=$1
		ORDER BY merchant_order.checkout_ordinal,unit.line_id,unit.unit_index
	`, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list unit facts: %w", err)
	}
	defer rows.Close()
	facts := make([]agencydomain.UnitFacts, 0)
	for rows.Next() {
		var fact agencydomain.UnitFacts
		var shipmentID, carrier, trackingRef, shipmentState sql.NullString
		if err := rows.Scan(
			&fact.MerchantOrderUnitID, &fact.MerchantOrderID, &fact.AllocationID,
			&fact.LineID, &fact.UnitIndex, &fact.ShopDomain, &fact.RefundStatus,
			&fact.MerchantOrderState, &fact.Fulfillment, &fact.ReturnState,
			&fact.ResolutionCause, &fact.ResolutionDecision,
			&shipmentID, &carrier, &trackingRef, &shipmentState,
		); err != nil {
			return nil, err
		}
		if shipmentID.Valid {
			fact.Shipment = &agencydomain.UnitShipmentRef{
				ID: shipmentID.String, Carrier: carrier.String,
				TrackingRef: trackingRef.String, State: shipmentState.String,
			}
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// logistics 소유 fulfillment 전이의 process 이벤트 발행이다(ADR-0056 §2).
// coverage 집계(PLACED unit의 물리 미종결 수·수령 가치 존재 — 계약 v7 §9.1)는
// 전이를 만든 transaction이 COUNT해 싣는다.

type logisticsQueryer interface {
	procmsg.Queryer
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func emitUnitEvent(
	ctx context.Context,
	q logisticsQueryer,
	expectedUnitID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, fulfillment string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text, fulfillment, version
		FROM logistics_expected_units WHERE id=$1
	`, expectedUnitID).Scan(&agencyOrderID, &merchantOrderID, &fulfillment, &version); err != nil {
		return fmt.Errorf("read expected unit for event: %w", err)
	}
	// 주문 단위 coverage(미종결 수·수령 가치)는 리듀서가 unit identity fold에서
	// 파생한다(ADR-0070 §4.1) — 발행 transaction은 자기 행 값만 싣는다.
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceLogistics,
		Type:          procmsg.EventUnitFulfillmentChanged,
		Payload: procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: expectedUnitID, MerchantOrderID: merchantOrderID,
			Fulfillment: fulfillment,
		},
		DedupKey: procmsg.EventDedupKey("logistics_expected_unit", expectedUnitID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

func emitDeliveryFaultJudged(
	ctx context.Context,
	q logisticsQueryer,
	expectedUnitID, judgment string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, allocationID, resolutionID, cause string
	if err := q.QueryRowContext(ctx, `
		SELECT expected.agency_order_id::text, expected.merchant_order_id::text,
		       orders.allocation_id::text, resolution.id::text, resolution.cause
		FROM logistics_expected_units expected
		JOIN merchant_orders orders ON orders.id=expected.merchant_order_id
		JOIN logistics_delivery_resolutions resolution
		  ON resolution.expected_unit_id=expected.id
		WHERE expected.id=$1
	`, expectedUnitID).Scan(
		&agencyOrderID, &merchantOrderID, &allocationID, &resolutionID, &cause,
	); err != nil {
		return fmt.Errorf("read expected unit for fault event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceLogistics,
		Type:          procmsg.EventDeliveryFaultJudged,
		Payload: procmsg.DeliveryFaultJudgedPayload{
			ResolutionID: resolutionID, ExpectedUnitID: expectedUnitID,
			MerchantOrderID: merchantOrderID, AllocationID: allocationID,
			Cause: cause, Judgment: judgment,
		},
		DedupKey: procmsg.EventDedupKey("logistics_expected_unit", expectedUnitID,
			"FAULT_"+judgment),
	}, now)
	return err
}

// emitShipmentEvent는 실물 패키지 전이의 보고다(ADR-0070 §4.2). 배정된 기대
// unit id를 함께 실어 리듀서가 FULFILLING 세분·타임라인을 만든다.
func emitShipmentEvent(
	ctx context.Context,
	q logisticsQueryer,
	shipmentID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, state, unitList string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT shipment.agency_order_id::text, shipment.merchant_order_id::text,
		       shipment.state, shipment.version,
		       COALESCE((SELECT string_agg(allocation.expected_unit_id::text, ','
		                                   ORDER BY allocation.expected_unit_id)
		                 FROM logistics_shipment_allocations allocation
		                 WHERE allocation.shipment_id=shipment.id AND allocation.active), '')
		FROM logistics_shipments shipment WHERE shipment.id=$1
	`, shipmentID).Scan(&agencyOrderID, &merchantOrderID, &state, &version, &unitList); err != nil {
		return fmt.Errorf("read shipment for event: %w", err)
	}
	var unitIDs []string
	if unitList != "" {
		unitIDs = strings.Split(unitList, ",")
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceLogistics,
		Type:          procmsg.EventShipmentStateChanged,
		Payload: procmsg.ShipmentStateChangedPayload{
			ShipmentID: shipmentID, MerchantOrderID: merchantOrderID, State: state,
			ExpectedUnitIDs: unitIDs, Version: version,
		},
		DedupKey: procmsg.EventDedupKey("logistics_shipment", shipmentID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

// emitReturnEvent는 수동 회수 lane 전이의 보고다.
func emitReturnEvent(
	ctx context.Context,
	q logisticsQueryer,
	returnID string,
	now time.Time,
) error {
	var agencyOrderID, expectedUnitID, merchantOrderID, state, disposition string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT ret.agency_order_id::text, ret.expected_unit_id::text,
		       expected.merchant_order_id::text, ret.state,
		       COALESCE(ret.merchant_disposition,''), ret.version
		FROM logistics_returns ret
		JOIN logistics_expected_units expected ON expected.id=ret.expected_unit_id
		WHERE ret.id=$1
	`, returnID).Scan(&agencyOrderID, &expectedUnitID, &merchantOrderID, &state,
		&disposition, &version); err != nil {
		return fmt.Errorf("read return for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceLogistics,
		Type:          procmsg.EventReturnStateChanged,
		Payload: procmsg.ReturnStateChangedPayload{
			ReturnID: returnID, ExpectedUnitID: expectedUnitID,
			MerchantOrderID: merchantOrderID, State: state,
			MerchantDisposition: disposition, Version: version,
		},
		DedupKey: procmsg.EventDedupKey("logistics_return", returnID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

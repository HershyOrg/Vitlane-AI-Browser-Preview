package domain

import "testing"

// 전수 표: DB CHECK가 허용하는 모든 owner 어휘가 정확히 한 stage로 떨어진다.
func TestDeriveUnitStageFulfillmentExhaustive(t *testing.T) {
	cases := map[string]UnitStage{
		"AWAITING_EFFECT":                  UnitAwaitingShipment,
		"IN_TRANSIT_EXPECTED":              UnitInTransit,
		"DELIVERED_EXPECTED":               UnitDelivered,
		"MISSING":                          UnitException,
		"WRONG_ACTUAL":                     UnitException,
		"LOST":                             UnitException,
		"RETURNED":                         UnitReturnInProgress,
		"DELIVERY_RESOLUTION_PENDING":      UnitException,
		"NONCONFORMING_RESOLUTION_PENDING": UnitException,
		"RESOLVED":                         UnitDelivered,
		"NONCONFORMING_RESOLVED":           UnitDelivered,
		"SUPERSEDED_BY_CANCELLATION":       UnitCancelled,
		"NO_PLACEMENT":                     UnitProcurementFailed,
		"SIMULATED_NO_EFFECT":              UnitDelivered,
	}
	for fulfillment, want := range cases {
		got := DeriveUnitStage("AVAILABLE", UnitFacts{Fulfillment: fulfillment})
		if got != want {
			t.Errorf("fulfillment %s: got %s want %s", fulfillment, got, want)
		}
	}
	// 미지 어휘는 조용한 정상이 아니라 확인 필요로 떨어진다.
	if got := DeriveUnitStage("AVAILABLE", UnitFacts{Fulfillment: "FUTURE_STATE"}); got != UnitException {
		t.Errorf("unknown fulfillment: got %s want %s", got, UnitException)
	}
}

func TestDeriveUnitStageMerchantOrderExhaustive(t *testing.T) {
	cases := map[string]UnitStage{
		"":                  UnitOrdered,
		"PLANNED":           UnitOrdered,
		"READY_TO_PLACE":    UnitProcuring,
		"PLACEMENT_PENDING": UnitProcuring,
		"PLACEMENT_UNKNOWN": UnitProcuring,
		"PLACED":            UnitProcuring,
		"FAILED":            UnitProcurementFailed,
		"CANCELLED":         UnitCancelled,
	}
	for state, want := range cases {
		got := DeriveUnitStage("AVAILABLE", UnitFacts{MerchantOrderState: state})
		if got != want {
			t.Errorf("merchant order %q: got %s want %s", state, got, want)
		}
	}
}

// 우선순위: 환불 청구 > 진행 중 회수 > fulfillment > 조달.
func TestDeriveUnitStagePriority(t *testing.T) {
	richFacts := UnitFacts{
		MerchantOrderState: "PLACED",
		Fulfillment:        "WRONG_ACTUAL",
		ReturnState:        "RETURN_IN_TRANSIT",
	}
	for status, want := range map[string]UnitStage{
		"REQUESTED":      UnitRefundRequested,
		"REFUND_PENDING": UnitRefundPending,
		"REFUNDED":       UnitRefunded,
	} {
		if got := DeriveUnitStage(status, richFacts); got != want {
			t.Errorf("refund %s over facts: got %s want %s", status, got, want)
		}
	}
	if got := DeriveUnitStage("AVAILABLE", richFacts); got != UnitReturnInProgress {
		t.Errorf("open return over fulfillment: got %s", got)
	}
	// 회수 종결(CLOSED·CANCELLED)은 stage를 점유하지 않는다.
	for _, closed := range []string{"CLOSED", "CANCELLED"} {
		facts := UnitFacts{Fulfillment: "RESOLVED", ReturnState: closed}
		if got := DeriveUnitStage("AVAILABLE", facts); got != UnitDelivered {
			t.Errorf("closed return %s: got %s want %s", closed, got, UnitDelivered)
		}
	}
	// fulfillment가 있으면 조달 상태는 보지 않는다.
	facts := UnitFacts{MerchantOrderState: "FAILED", Fulfillment: "IN_TRANSIT_EXPECTED"}
	if got := DeriveUnitStage("AVAILABLE", facts); got != UnitInTransit {
		t.Errorf("fulfillment over merchant order: got %s", got)
	}
}

func TestComposeUnitViews(t *testing.T) {
	shipment := &UnitShipmentRef{ID: "ship-1", Carrier: "SANDBOX",
		TrackingRef: "SBX-1", State: "IN_TRANSIT"}
	facts := []UnitFacts{
		{MerchantOrderUnitID: "unit-1", MerchantOrderID: "mo-1", AllocationID: "allocation-1",
			LineID: "line-a", UnitIndex: 1, ShopDomain: "alpha.example",
			RefundStatus: "AVAILABLE", MerchantOrderState: "PLACED",
			Fulfillment: "IN_TRANSIT_EXPECTED", Shipment: shipment},
		{MerchantOrderUnitID: "unit-2", MerchantOrderID: "mo-1", AllocationID: "allocation-1",
			LineID: "line-a", UnitIndex: 2, ShopDomain: "alpha.example",
			RefundStatus: "REFUNDED", MerchantOrderState: "PLACED",
			Fulfillment: "RESOLVED", ReturnState: "CLOSED"},
		{MerchantOrderUnitID: "unit-3", MerchantOrderID: "mo-2", AllocationID: "allocation-2",
			LineID: "line-b", UnitIndex: 1, ShopDomain: "beta.example",
			RefundStatus: "AVAILABLE"},
	}
	units := ComposeUnitViews(facts)
	if len(units) != 3 {
		t.Fatalf("units: got %d want 3", len(units))
	}
	if units[0].Stage != UnitInTransit || units[0].Shipment == nil ||
		units[0].Shipment.ID != "ship-1" {
		t.Errorf("unit-1: %+v", units[0])
	}
	if units[1].Stage != UnitRefunded || units[1].ReturnState != "CLOSED" {
		t.Errorf("unit-2: %+v", units[1])
	}
	if units[2].Stage != UnitOrdered || units[2].RefundStatus != "AVAILABLE" {
		t.Errorf("unit-3: %+v", units[2])
	}
}

package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// §9.2 package projection: carrier 이력이 단조롭지 않아도 projection은 허용
// 전이만 따른다 — delivered 후 재이동 금지, exception 재개 허용이 핵심이다.
func TestAllowedTransitionMatrix(t *testing.T) {
	cases := []struct {
		from, to ShipmentState
		allowed  bool
	}{
		{ShipmentCreated, ShipmentInTransit, true},
		{ShipmentCreated, ShipmentDelivered, false},
		{ShipmentInTransit, ShipmentOutForDelivery, true},
		{ShipmentInTransit, ShipmentDelivered, true},
		{ShipmentOutForDelivery, ShipmentDelivered, true},
		{ShipmentException, ShipmentInTransit, true},
		{ShipmentDelivered, ShipmentInTransit, false},
		{ShipmentDelivered, ShipmentExceptionRecon, true},
		{ShipmentReturnToSender, ShipmentReturned, true},
		{ShipmentReturned, ShipmentInTransit, false},
		{ShipmentDelivered, ShipmentDelivered, true},
	}
	for _, item := range cases {
		if got := AllowedTransition(item.from, item.to); got != item.allowed {
			t.Errorf("AllowedTransition(%s, %s) = %v, want %v", item.from, item.to, got, item.allowed)
		}
	}
}

// 판정 대상은 배송 예외 3종뿐이고 결정 어휘도 2종뿐이다(§9.1).
func TestResolutionVocabulary(t *testing.T) {
	for _, fulfillment := range []Fulfillment{FulfillmentMissing, FulfillmentWrongActual, FulfillmentLost} {
		if !ResolvableFulfillment(fulfillment) {
			t.Errorf("%s must be resolvable", fulfillment)
		}
	}
	for _, fulfillment := range []Fulfillment{FulfillmentAwaitingEffect, FulfillmentDeliveredExpected, FulfillmentResolved, FulfillmentNoPlacement} {
		if ResolvableFulfillment(fulfillment) {
			t.Errorf("%s must not be resolvable", fulfillment)
		}
	}
	if _, err := ValidateResolutionDecision("REFUND"); err != nil {
		t.Fatalf("REFUND rejected: %v", err)
	}
	if _, err := ValidateResolutionDecision("PARTIAL"); err != ErrResolutionInvalid {
		t.Fatalf("unknown decision must be rejected, got %v", err)
	}
}

// §9.3 회수 lane: 수령 후 처분 기록 전에는 닫히지 않고, 닫힌 뒤 재이동은 없다.
func TestReturnTransitions(t *testing.T) {
	cases := []struct {
		from, to ReturnState
		allowed  bool
	}{
		{ReturnRequested, ReturnInTransit, true},
		{ReturnRequested, ReturnReceived, false},
		{ReturnInTransit, ReturnReceived, true},
		{ReturnReceived, ReturnMerchantReturned, true},
		{ReturnReceived, ReturnClosed, true},
		{ReturnMerchantReturned, ReturnClosed, true},
		{ReturnClosed, ReturnRequested, false},
		{ReturnCancelled, ReturnInTransit, false},
	}
	for _, item := range cases {
		if got := AllowedReturnTransition(item.from, item.to); got != item.allowed {
			t.Errorf("AllowedReturnTransition(%s, %s) = %v, want %v", item.from, item.to, got, item.allowed)
		}
	}
}

func TestValidateShipmentInput(t *testing.T) {
	if err := ValidateShipmentInput("UPS", "1Z999AA10123456784"); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, item := range [][2]string{{"", "TRACK-1"}, {"UPS", ""}, {"  ", "TRACK-1"}} {
		if err := ValidateShipmentInput(item[0], item[1]); err != ErrShipmentInvalid {
			t.Errorf("ValidateShipmentInput(%q, %q) = %v, want ErrShipmentInvalid", item[0], item[1], err)
		}
	}
}

func TestExpectedUnitIsPhysicalIdentityOnly(t *testing.T) {
	raw, err := json.Marshal(ExpectedUnit{
		ID: "expected-1", MerchantOrderUnitID: "unit-1",
		MerchantOrderID: "mo-1", AgencyOrderID: "order-1",
		LineID: "line-1", UnitIndex: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "slice") {
		t.Fatalf("monetary unit identity leaked into ExpectedUnit: %s", raw)
	}
}

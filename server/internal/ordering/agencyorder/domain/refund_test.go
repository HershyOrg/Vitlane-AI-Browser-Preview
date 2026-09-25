package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
)

func allocationFixture() AgencyOrder {
	return AgencyOrder{
		ID: "order-1", ExecutionProfileHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MerchantCheckouts: []MerchantCheckout{
			{MerchantID: "alpha", ShopDomain: "alpha.example",
				AuthoritativeTotal: Money{AmountMinor: 4403, Currency: "USD"}},
			{MerchantID: "beta", ShopDomain: "beta.example",
				AuthoritativeTotal: Money{AmountMinor: 2799, Currency: "USD"}},
		},
		PassThroughTotal: Money{AmountMinor: 7202, Currency: "USD"},
		AgencyFee: FeeBreakdown{
			VariableAmount: Money{AmountMinor: 390, Currency: "USD"},
			FixedAmount:    Money{AmountMinor: 60, Currency: "USD"},
			Total:          Money{AmountMinor: 450, Currency: "USD"},
			PolicyVersion:  policy.PayPalMOFeePolicyVersion,
			MerchantOrders: []MerchantOrderFeeAllocation{
				{CheckoutOrdinal: 1, ShopDomain: "alpha.example",
					PassThroughAmount:    Money{AmountMinor: 4403, Currency: "USD"},
					VariableAmount:       Money{AmountMinor: 238, Currency: "USD"},
					FixedAmount:          Money{AmountMinor: 30, Currency: "USD"},
					Total:                Money{AmountMinor: 268, Currency: "USD"},
					CustomerPayableTotal: Money{AmountMinor: 4671, Currency: "USD"}},
				{CheckoutOrdinal: 2, ShopDomain: "beta.example",
					PassThroughAmount:    Money{AmountMinor: 2799, Currency: "USD"},
					VariableAmount:       Money{AmountMinor: 152, Currency: "USD"},
					FixedAmount:          Money{AmountMinor: 30, Currency: "USD"},
					Total:                Money{AmountMinor: 182, Currency: "USD"},
					CustomerPayableTotal: Money{AmountMinor: 2981, Currency: "USD"}},
			},
		},
		CustomerPayableTotal: Money{AmountMinor: 7652, Currency: "USD"},
	}
}

func TestMerchantOrderAllocationsAreWholeMOBoundaries(t *testing.T) {
	order := allocationFixture()
	allocations, err := MerchantOrderAllocations(order)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 || allocations[0].CustomerGrossAmount.AmountMinor != 4671 ||
		allocations[1].CustomerGrossAmount.AmountMinor != 2981 {
		t.Fatalf("unexpected allocations: %+v", allocations)
	}
	for index, allocation := range allocations {
		if allocation.CheckoutOrdinal != index+1 || allocation.AllocationHash == "" ||
			allocation.CustomerGrossAmount.AmountMinor !=
				allocation.PassThroughAmount.AmountMinor+allocation.FeeTotalAmount.AmountMinor {
			t.Fatalf("invalid immutable allocation: %+v", allocation)
		}
	}
	second, err := MerchantOrderAllocations(order)
	if err != nil || second[0].AllocationHash != allocations[0].AllocationHash {
		t.Fatalf("allocation hashes must be deterministic: first=%+v second=%+v err=%v", allocations, second, err)
	}
}

func TestMerchantOrderAllocationsRejectCrossMODrift(t *testing.T) {
	order := allocationFixture()
	order.AgencyFee.MerchantOrders[0].CustomerPayableTotal.AmountMinor++
	if _, err := MerchantOrderAllocations(order); err != ErrRefundRequestInvalid {
		t.Fatalf("MO gross drift must fail closed, got %v", err)
	}
	order = allocationFixture()
	order.AgencyFee.MerchantOrders[0].ShopDomain = "beta.example"
	if _, err := MerchantOrderAllocations(order); err != ErrRefundRequestInvalid {
		t.Fatalf("MO identity drift must fail closed, got %v", err)
	}
}

func TestRefundRequestCustomerJSONNeverExposesInternalNote(t *testing.T) {
	payload, err := json.Marshal(RefundRequest{
		MerchantOrderID: "mo-1", AllocationID: "allocation-1",
		RequestedGrossAmount:    Money{AmountMinor: 4671, Currency: "USD"},
		PublicRationale:         "The item arrived damaged.",
		Decision:                RefundDecisionApproved,
		DecisionPublicRationale: "Photos confirm the defect.",
		InternalNote:            "private accounting reference OPS-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private accounting reference") ||
		strings.Contains(string(payload), "internalNote") {
		t.Fatalf("customer refund JSON leaked an internal note: %s", payload)
	}
	for _, expected := range []string{`"merchantOrderId":"mo-1"`,
		`"allocationId":"allocation-1"`, `"requestedGrossAmount":{"amountMinor":4671`,
		`"decisionPublicRationale":"Photos confirm the defect."`} {
		if !strings.Contains(string(payload), expected) {
			t.Fatalf("customer contract missing %s: %s", expected, payload)
		}
	}
}

func TestRefundReviewContextCarriesWholeMOFactsWithoutUnitMoney(t *testing.T) {
	payload, err := json.Marshal(RefundReviewContext{
		OrderNumber: "order-1", MerchantOrderID: "mo-1", AllocationID: "allocation-1",
		ShopDomain: "shop.example", MerchantID: "merchant-1", ExternalOrderRef: "SHOP-42",
		MerchantOrderState:   "PLACED",
		RequestedGrossAmount: Money{AmountMinor: 2599, Currency: "USD"},
		Lines: []RefundReviewLine{{LineID: "line-1", ProductTitle: "Travel case",
			VariantTitle: "Blue / Large", Quantity: 2}},
		Units: []RefundReviewUnitFact{{MerchantOrderUnitID: "unit-1", LineID: "line-1",
			UnitIndex: 1, Disposition: "PENDING",
			DeliveryFacts: RefundDeliveryFacts{Recorded: true, ExpectedFulfillment: "WRONG_ACTUAL"}}},
		InternalNote: "accounting case OPS-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"merchantOrderId":"mo-1"`,
		`"requestedGrossAmount":{"amountMinor":2599`, `"quantity":2`,
		`"merchantOrderUnitId":"unit-1"`, `"expectedFulfillment":"WRONG_ACTUAL"`,
		`"internalNote":"accounting case OPS-17"`} {
		if !strings.Contains(string(payload), required) {
			t.Errorf("operator review payload missing %s: %s", required, payload)
		}
	}
	if strings.Contains(string(payload), "slice") || strings.Contains(string(payload), "refundAmount") {
		t.Fatalf("review contract must not restore unit monetary allocation: %s", payload)
	}
}

func TestRefundReasonCodeGate(t *testing.T) {
	for _, code := range policy.RefundReasonCodes {
		if err := ValidateRefundReasonCode(code); err != nil {
			t.Errorf("allowed code %s rejected: %v", code, err)
		}
	}
	for _, code := range []string{"CHANGE_OF_MIND", "NO_LONGER_NEEDED", "", "item_not_received"} {
		if err := ValidateRefundReasonCode(code); err != ErrRefundRequestInvalid {
			t.Errorf("code %q must be rejected, got %v", code, err)
		}
	}
	if err := ValidateRefundRequestReason(""); err != ErrRefundRequestInvalid {
		t.Fatalf("customer-visible rationale must be required, got %v", err)
	}
	if err := ValidateRefundDecisionPublicRationale(""); err != ErrRefundRequestInvalid {
		t.Fatalf("decision public rationale must be required, got %v", err)
	}
	if err := ValidateRefundDecisionPublicRationale(strings.Repeat("가", 2001)); err != ErrRefundRequestInvalid {
		t.Fatalf("oversized decision rationale must be rejected, got %v", err)
	}
	if err := ValidateRefundInternalNote(strings.Repeat("n", 4001)); err != ErrRefundRequestInvalid {
		t.Fatalf("oversized internal note must be rejected, got %v", err)
	}
}

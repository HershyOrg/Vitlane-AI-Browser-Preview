package policy

import (
	"errors"
	"math"
	"slices"
	"testing"
)

// ADR-0052 §2 표의 cause→basis 전표를 전수 고정한다. 이 테스트가 곧 정책
// 문서다 — 값이 바뀌면 ADR 개정이 선행되어야 한다.
func TestBasisForCauseTable(t *testing.T) {
	cases := []struct {
		cause      string
		disclosure string
		want       RefundBasis
	}{
		// Every accepted whole-MO compensation is gross, independent of cause
		// and historical disclosure text.
		{CauseCustomerCancelPreEffect, DisclosureVersionMOGross, BasisGross},
		{CauseCustomerCancelPreEffect, "AGENCY_ORDER_TEST_DISCLOSURE_LEGACY", BasisGross},
		{CauseCustomerCancelPreEffect, "", BasisGross},
		{CauseCustomerCancelAfterPlacementConfirmed, DisclosureVersionMOGross, BasisGross},
		{CauseCustomerCancelAfterPlacementConfirmed, "AGENCY_ORDER_TEST_DISCLOSURE_LEGACY", BasisGross},
		{CauseCustomerCancelAfterPlacementConfirmed, "", BasisGross},
		// mandatory 계열은 disclosure와 무관하게 GROSS다.
		{CauseOrderFailure, DisclosureVersionMOGross, BasisGross},
		{CauseCustomerRequest, DisclosureVersionMOGross, BasisGross},
		{CauseMerchantFault, DisclosureVersionMOGross, BasisGross},
		{CauseDelayRuleCancel, DisclosureVersionMOGross, BasisGross},
		// 미지의 cause도 fail-safe로 GROSS(고객에게 불리하지 않은 기본값)다.
		{"UNKNOWN_FUTURE_CAUSE", DisclosureVersionMOGross, BasisGross},
	}
	for _, c := range cases {
		if got := BasisForCause(c.cause, c.disclosure); got != c.want {
			t.Errorf("BasisForCause(%s, %s)=%s want %s", c.cause, c.disclosure, got, c.want)
		}
	}
}

func TestCalculateAgencyFee(t *testing.T) {
	tests := []struct {
		name        string
		rail        string
		bases       []MerchantFeeBase
		variable    int64
		fixed       int64
		payable     int64
		version     string
		allocations []MerchantFeeAllocation
	}{
		{
			name: "paypal rounds and charges fixed per merchant order", rail: "PAYPAL_SANDBOX",
			bases:    []MerchantFeeBase{{CheckoutOrdinal: 2, PassThroughMinor: 2799}, {CheckoutOrdinal: 1, PassThroughMinor: 4403}},
			variable: 390, fixed: 60, payable: 7652, version: PayPalMOFeePolicyVersion,
			allocations: []MerchantFeeAllocation{
				{CheckoutOrdinal: 1, PassThroughMinor: 4403, VariableMinor: 238, FixedMinor: 30, TotalMinor: 268, CustomerPayableMinor: 4671},
				{CheckoutOrdinal: 2, PassThroughMinor: 2799, VariableMinor: 152, FixedMinor: 30, TotalMinor: 182, CustomerPayableMinor: 2981},
			},
		},
		{
			name: "paypal live uses the same economic policy", rail: "PAYPAL_LIVE",
			bases:    []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: 1}},
			variable: 1, fixed: 30, payable: 32, version: PayPalMOFeePolicyVersion,
			allocations: []MerchantFeeAllocation{{CheckoutOrdinal: 1, PassThroughMinor: 1, VariableMinor: 1, FixedMinor: 30, TotalMinor: 31, CustomerPayableMinor: 32}},
		},
		{
			name: "tvit rounds once then allocates by largest remainder", rail: "TVITUSD",
			bases:    []MerchantFeeBase{{CheckoutOrdinal: 3, PassThroughMinor: 1}, {CheckoutOrdinal: 1, PassThroughMinor: 1}, {CheckoutOrdinal: 2, PassThroughMinor: 1}},
			variable: 1, fixed: 0, payable: 4, version: TVITUSDCFeePolicyVersion,
			allocations: []MerchantFeeAllocation{
				{CheckoutOrdinal: 1, PassThroughMinor: 1, VariableMinor: 1, TotalMinor: 1, CustomerPayableMinor: 2},
				{CheckoutOrdinal: 2, PassThroughMinor: 1, CustomerPayableMinor: 1},
				{CheckoutOrdinal: 3, PassThroughMinor: 1, CustomerPayableMinor: 1},
			},
		},
		{
			name: "tvit proportional remainder", rail: "TVITUSD",
			bases:    []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: 10000}, {CheckoutOrdinal: 2, PassThroughMinor: 5000}},
			variable: 150, fixed: 0, payable: 15150, version: TVITUSDCFeePolicyVersion,
			allocations: []MerchantFeeAllocation{
				{CheckoutOrdinal: 1, PassThroughMinor: 10000, VariableMinor: 100, TotalMinor: 100, CustomerPayableMinor: 10100},
				{CheckoutOrdinal: 2, PassThroughMinor: 5000, VariableMinor: 50, TotalMinor: 50, CustomerPayableMinor: 5050},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote, err := CalculateAgencyFee(test.rail, test.bases)
			if err != nil {
				t.Fatal(err)
			}
			if quote.VariableMinor != test.variable || quote.FixedMinor != test.fixed ||
				quote.TotalMinor != test.variable+test.fixed || quote.CustomerPayableMinor != test.payable ||
				quote.PolicyVersion != test.version || !slices.Equal(quote.MerchantOrders, test.allocations) {
				t.Fatalf("quote=%+v want variable=%d fixed=%d payable=%d version=%s allocations=%+v",
					quote, test.variable, test.fixed, test.payable, test.version, test.allocations)
			}
			var passThrough, variable, fixed, payable int64
			for _, allocation := range quote.MerchantOrders {
				passThrough += allocation.PassThroughMinor
				variable += allocation.VariableMinor
				fixed += allocation.FixedMinor
				payable += allocation.CustomerPayableMinor
			}
			if passThrough != quote.PassThroughMinor || variable != quote.VariableMinor ||
				fixed != quote.FixedMinor || payable != quote.CustomerPayableMinor {
				t.Fatalf("MO sums do not conserve order quote: %+v", quote)
			}
		})
	}
}

func TestCalculateAgencyFeeRejectsInvalidAndOverflow(t *testing.T) {
	tests := []struct {
		name  string
		rail  string
		bases []MerchantFeeBase
		want  error
	}{
		{name: "empty", rail: "TVITUSD", want: ErrInvalidFeeInput},
		{name: "zero amount", rail: "TVITUSD", bases: []MerchantFeeBase{{CheckoutOrdinal: 1}}, want: ErrInvalidFeeInput},
		{name: "negative amount", rail: "PAYPAL_LIVE", bases: []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: -1}}, want: ErrInvalidFeeInput},
		{name: "zero ordinal", rail: "PAYPAL_LIVE", bases: []MerchantFeeBase{{PassThroughMinor: 1}}, want: ErrInvalidFeeInput},
		{name: "duplicate ordinal", rail: "PAYPAL_LIVE", bases: []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: 1}, {CheckoutOrdinal: 1, PassThroughMinor: 2}}, want: ErrInvalidFeeInput},
		{name: "unknown rail", rail: "CASH", bases: []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: 1}}, want: ErrUnknownRail},
		{name: "pass through sum overflow", rail: "TVITUSD", bases: []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: math.MaxInt64}, {CheckoutOrdinal: 2, PassThroughMinor: 1}}, want: ErrFeeOverflow},
		{name: "customer payable overflow", rail: "PAYPAL_LIVE", bases: []MerchantFeeBase{{CheckoutOrdinal: 1, PassThroughMinor: math.MaxInt64}}, want: ErrFeeOverflow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CalculateAgencyFee(test.rail, test.bases)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

// 단순변심 OFF 게이트: CHANGE_OF_MIND는 어휘에 존재하지 않는다(ADR-0052 §1).
func TestRefundReasonVocabulary(t *testing.T) {
	for _, code := range RefundReasonCodes {
		if !ValidRefundReasonCode(code) {
			t.Errorf("declared code %s must validate", code)
		}
	}
	for _, invalid := range []string{"CHANGE_OF_MIND", "", "item_not_received"} {
		if ValidRefundReasonCode(invalid) {
			t.Errorf("code %q must be rejected", invalid)
		}
	}
}

func TestProcurementFailureVocabulary(t *testing.T) {
	if !ValidProcurementFailureCode("product_unavailable") {
		t.Fatal("case-insensitive declared code must validate")
	}
	if ValidProcurementFailureCode("OPERATOR_TIRED") {
		t.Fatal("undeclared discretionary code must be rejected")
	}
}

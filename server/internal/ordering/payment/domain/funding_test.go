package domain

import (
	"testing"
	"time"
)

func accountingAt(minute int) time.Time {
	return time.Date(2026, 8, 27, 1, minute, 0, 0, time.UTC)
}

func paypalMO(
	allocationID string,
	ordinal int,
	passThroughMinor int64,
	state MOFundingState,
) MOAccountingInput {
	variable := ceilRatio(passThroughMinor, 54, 1000)
	return MOAccountingInput{
		AllocationID: allocationID, ShopDomain: allocationID + ".example",
		CheckoutOrdinal: ordinal, PassThroughMinor: passThroughMinor,
		FeeVariableMinor: variable, FeeFixedMinor: 30,
		FeeTotalMinor:      variable + 30,
		CustomerGrossMinor: passThroughMinor + variable + 30,
		FeePolicyVersion:   "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
		FundingState:       state,
	}
}

func giwaMO(
	allocationID string,
	ordinal int,
	passThroughMinor int64,
	state MOFundingState,
) MOAccountingInput {
	variable := ceilRatio(passThroughMinor, 1, 100)
	return MOAccountingInput{
		AllocationID: allocationID, ShopDomain: allocationID + ".example",
		CheckoutOrdinal: ordinal, PassThroughMinor: passThroughMinor,
		FeeVariableMinor: variable, FeeTotalMinor: variable,
		CustomerGrossMinor: passThroughMinor + variable,
		FeePolicyVersion:   "TVITUSD_ORDER_PASS_THROUGH_100BPS_MO_ALLOC_V1",
		FundingState:       state,
	}
}

func TestProjectOrderAccountingUsesActualEventsThenForecastsUnresolvedPayPalMO(t *testing.T) {
	first := paypalMO("allocation-1", 1, 1_000, MOFundingActive)
	first.MerchantOrderID = "mo-1"
	first.MerchantOrderState = "PLACED"
	first.Cash = &AccountingCashInput{
		ID: "cash-1", GrossMinor: first.CustomerGrossMinor,
		EconomicsReconciled: true, ProcessorFeeMinor: 78,
		NetReceivableMinor: first.CustomerGrossMinor - 78,
		OccurredAt:         accountingAt(1),
	}
	first.MerchantSpend = &AccountingMerchantSpendInput{
		ID: "spend-1", State: "SUCCEEDED", AmountMinor: 1_000,
		OccurredAt: accountingAt(2),
	}
	second := paypalMO("allocation-2", 2, 2_000, MOFundingAvailable)
	second.MerchantOrderID = "mo-2"
	second.MerchantOrderState = "PLANNED"

	projection, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-1", CustomerPaymentID: "payment-1",
		Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
		PaymentState: "PARTIALLY_CAPTURED", Currency: "USD",
		MerchantOrders: []MOAccountingInput{second, first}, CreatedAt: accountingAt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	// MO 1: 1084 gross - 78 processor fee - 1000 purchase = +6 actual.
	// MO 2: expected 2138 gross - 125 processor fee - 2000 purchase = +13.
	if projection.RealizedBalanceMinor != 6 || projection.ForecastAdjustmentMinor != 13 ||
		projection.ForecastBalanceMinor != 19 {
		t.Fatalf("unexpected PayPal balances: %+v", projection)
	}
	if projection.ForecastNetCashInMinor != 2_013 ||
		projection.ForecastProcessorFeeMinor != 125 ||
		projection.ForecastMerchantSpendMinor != 2_000 {
		t.Fatalf("unexpected PayPal forecast components: %+v", projection)
	}
	if len(projection.Events) != 2 || projection.Events[0].Kind != AccountingCustomerCashIn ||
		projection.Events[1].Kind != AccountingMerchantPurchase {
		t.Fatalf("actual event ledger=%+v", projection.Events)
	}
	if projection.MerchantOrders[0].AllocationID != "allocation-1" ||
		projection.MerchantOrders[1].AllocationID != "allocation-2" {
		t.Fatalf("merchant orders not sorted by checkout ordinal: %+v", projection.MerchantOrders)
	}
}

func TestProjectOrderAccountingForecastsGIWAExceptionAsWholeMORefund(t *testing.T) {
	first := giwaMO("allocation-1", 1, 1_000, MOFundingActive)
	first.MerchantOrderID = "mo-1"
	first.MerchantOrderState = "PLACED"
	first.MerchantSpend = &AccountingMerchantSpendInput{
		ID: "spend-1", State: "SUCCEEDED", AmountMinor: 1_000,
		OccurredAt: accountingAt(2),
	}
	second := giwaMO("allocation-2", 2, 2_000, MOFundingReleasePending)
	second.MerchantOrderID = "mo-2"
	second.MerchantOrderState = "FAILED"
	second.ExceptionCause = "PROCUREMENT_FAILURE"
	second.Compensation = &AccountingCompensationInput{
		ID: "compensation-2", Action: "TVIT_REFUND", Cause: "PROCUREMENT_FAILURE",
		State: "EXECUTION_PENDING", AmountMinor: second.CustomerGrossMinor,
		OccurredAt: accountingAt(3),
	}
	total := first.CustomerGrossMinor + second.CustomerGrossMinor

	projection, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-2", CustomerPaymentID: "payment-2",
		Rail: "GIWA", ProviderEnvironment: "TESTNET", PaymentState: "SUCCEEDED",
		Currency: "USD", MerchantOrders: []MOAccountingInput{first, second},
		OrderCash: &AccountingCashInput{
			ID: "giwa-receipt", GrossMinor: total, EconomicsReconciled: true,
			NetReceivableMinor: total, OccurredAt: accountingAt(1),
		}, CreatedAt: accountingAt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.ActualNetCashInMinor != 3_030 || projection.RealizedBalanceMinor != 2_030 ||
		projection.ExpectedCompensationMinor != 2_020 ||
		projection.ForecastBalanceMinor != 10 {
		t.Fatalf("unexpected GIWA exception accounting: %+v", projection)
	}
	if !projection.RequiresAttention || len(projection.Events) != 2 {
		t.Fatalf("pending compensation visibility/events: %+v", projection)
	}
}

func TestProjectOrderAccountingUsesOrderLevelTVitFeeAllocation(t *testing.T) {
	first := giwaMO("allocation-1", 1, 1, MOFundingAvailable)
	second := giwaMO("allocation-2", 2, 1, MOFundingAvailable)
	// One percent of the two-cent order rounds once to one cent. The stable
	// largest-remainder tie break assigns it to checkout ordinal 1.
	first.FeeVariableMinor, first.FeeTotalMinor, first.CustomerGrossMinor = 1, 1, 2
	second.FeeVariableMinor, second.FeeTotalMinor, second.CustomerGrossMinor = 0, 0, 1
	input := OrderAccountingInput{
		AgencyOrderID: "order-tvit-small", CustomerPaymentID: "payment-tvit-small",
		Rail: "GIWA", ProviderEnvironment: "TESTNET", PaymentState: "CAPTURED",
		Currency: "USD", MerchantOrders: []MOAccountingInput{first, second},
		OrderCash: &AccountingCashInput{
			ID: "cash-tvit-small", GrossMinor: 3, EconomicsReconciled: true,
			NetReceivableMinor: 3, OccurredAt: accountingAt(1),
		},
		CreatedAt: accountingAt(0),
	}
	projection, err := ProjectOrderAccounting(input)
	if err != nil {
		t.Fatal(err)
	}
	if projection.MerchantOrders[0].FeeTotalMinor != 1 ||
		projection.MerchantOrders[1].FeeTotalMinor != 0 {
		t.Fatalf("unexpected tVit fee allocation: %+v", projection.MerchantOrders)
	}
	input.MerchantOrders[1].FeeVariableMinor = 1
	input.MerchantOrders[1].FeeTotalMinor = 1
	input.MerchantOrders[1].CustomerGrossMinor = 2
	input.OrderCash.GrossMinor, input.OrderCash.NetReceivableMinor = 4, 4
	if _, err := ProjectOrderAccounting(input); err != ErrInvalid {
		t.Fatalf("per-MO rounded tVit fee drift err=%v want ErrInvalid", err)
	}
}

func TestProjectOrderAccountingPayPalPreEffectVoidHasNoCashDownside(t *testing.T) {
	merchantOrder := paypalMO("allocation-1", 1, 1_000, MOFundingAvailable)
	merchantOrder.MerchantOrderID = "mo-1"
	merchantOrder.MerchantOrderState = "CANCELLED"
	merchantOrder.ExceptionCause = "CUSTOMER_CANCEL_PRE_EFFECT"
	merchantOrder.Compensation = &AccountingCompensationInput{
		ID: "void-1", Action: "VOID", Cause: "CUSTOMER_CANCEL_PRE_EFFECT",
		State: "APPROVED", AmountMinor: merchantOrder.CustomerGrossMinor,
		OccurredAt: accountingAt(1),
	}
	projection, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-3", CustomerPaymentID: "payment-3", Rail: "PAYPAL",
		ProviderEnvironment: "SANDBOX", PaymentState: "AUTHORIZED", Currency: "USD",
		MerchantOrders: []MOAccountingInput{merchantOrder}, CreatedAt: accountingAt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.RealizedBalanceMinor != 0 || projection.ForecastNetCashInMinor != 0 ||
		projection.ForecastMerchantSpendMinor != 0 || projection.ExpectedCompensationMinor != 0 ||
		projection.ForecastBalanceMinor != 0 {
		t.Fatalf("pre-effect void created a false cash effect: %+v", projection)
	}
}

func TestProjectOrderAccountingFailedUncapturedFundingHasNoFalseRefundDownside(t *testing.T) {
	merchantOrder := paypalMO("allocation-1", 1, 1_000, MOFundingFailed)
	merchantOrder.MerchantOrderID = "mo-1"
	merchantOrder.MerchantOrderState = "FAILED"
	merchantOrder.ExceptionCause = "PROCUREMENT_FAILURE"
	projection, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-failed-funding", CustomerPaymentID: "payment-failed-funding",
		Rail: "PAYPAL", ProviderEnvironment: "SANDBOX", PaymentState: "AUTHORIZED",
		Currency: "USD", MerchantOrders: []MOAccountingInput{merchantOrder},
		CreatedAt: accountingAt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.RealizedBalanceMinor != 0 || projection.ForecastNetCashInMinor != 0 ||
		projection.ForecastMerchantSpendMinor != 0 || projection.ExpectedCompensationMinor != 0 ||
		projection.ForecastBalanceMinor != 0 {
		t.Fatalf("uncaptured funding failure created a false cash refund: %+v", projection)
	}
}

func TestProjectOrderAccountingCapturedRefundShowsProcessorFeeLoss(t *testing.T) {
	merchantOrder := paypalMO("allocation-1", 1, 1_000, MOFundingReleasePending)
	merchantOrder.MerchantOrderID = "mo-1"
	merchantOrder.MerchantOrderState = "PLACED"
	merchantOrder.ExceptionCause = "DELIVERY_EXCEPTION"
	merchantOrder.Cash = &AccountingCashInput{
		ID: "cash-1", GrossMinor: merchantOrder.CustomerGrossMinor,
		EconomicsReconciled: true, ProcessorFeeMinor: 78,
		NetReceivableMinor: merchantOrder.CustomerGrossMinor - 78,
		OccurredAt:         accountingAt(1),
	}
	merchantOrder.MerchantSpend = &AccountingMerchantSpendInput{
		ID: "spend-1", State: "SUCCEEDED", AmountMinor: 1_000,
		OccurredAt: accountingAt(2),
	}
	merchantOrder.Compensation = &AccountingCompensationInput{
		ID: "refund-1", Action: "REFUND", Cause: "DELIVERY_EXCEPTION",
		State: "EXECUTION_PENDING", AmountMinor: merchantOrder.CustomerGrossMinor,
		OccurredAt: accountingAt(3),
	}
	projection, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-4", CustomerPaymentID: "payment-4", Rail: "PAYPAL",
		ProviderEnvironment: "SANDBOX", PaymentState: "CAPTURED", Currency: "USD",
		MerchantOrders: []MOAccountingInput{merchantOrder}, CreatedAt: accountingAt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.RealizedBalanceMinor != 6 ||
		projection.ExpectedCompensationMinor != merchantOrder.CustomerGrossMinor ||
		projection.ForecastBalanceMinor != -1_078 {
		t.Fatalf("refund downside does not expose PayPal fee loss: %+v", projection)
	}
}

func TestProjectOrderAccountingRejectsFeePolicyDrift(t *testing.T) {
	merchantOrder := paypalMO("allocation-1", 1, 1_000, MOFundingAvailable)
	merchantOrder.FeeVariableMinor--
	if _, err := ProjectOrderAccounting(OrderAccountingInput{
		AgencyOrderID: "order-5", CustomerPaymentID: "payment-5", Rail: "PAYPAL",
		ProviderEnvironment: "SANDBOX", Currency: "USD",
		MerchantOrders: []MOAccountingInput{merchantOrder},
	}); err != ErrInvalid {
		t.Fatalf("fee policy drift err=%v want ErrInvalid", err)
	}
}

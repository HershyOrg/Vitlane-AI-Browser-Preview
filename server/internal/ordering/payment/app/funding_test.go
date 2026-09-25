package app

import (
	"context"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

type accountingRepository struct {
	inputs []domain.OrderAccountingInput
}

func (r *accountingRepository) GetOrderAccountingInput(
	_ context.Context,
	agencyOrderID string,
) (domain.OrderAccountingInput, bool, error) {
	for _, input := range r.inputs {
		if input.AgencyOrderID == agencyOrderID {
			return input, true, nil
		}
	}
	return domain.OrderAccountingInput{}, false, nil
}

func (r *accountingRepository) ListOrderAccountingInputs(
	_ context.Context,
	environment string,
	_ int,
) ([]domain.OrderAccountingInput, error) {
	result := make([]domain.OrderAccountingInput, 0)
	for _, input := range r.inputs {
		if input.ProviderEnvironment == environment {
			result = append(result, input)
		}
	}
	return result, nil
}

func accountingServiceMO(allocationID string, passThroughMinor int64) domain.MOAccountingInput {
	variable := (passThroughMinor*54 + 999) / 1000
	return domain.MOAccountingInput{
		AllocationID: allocationID, ShopDomain: allocationID + ".example",
		CheckoutOrdinal: 1, PassThroughMinor: passThroughMinor,
		FeeVariableMinor: variable, FeeFixedMinor: 30,
		FeeTotalMinor:      variable + 30,
		CustomerGrossMinor: passThroughMinor + variable + 30,
		FeePolicyVersion:   "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
		FundingState:       domain.MOFundingAvailable,
	}
}

func TestAccountingSummarySeparatesRealizedEventsFromForecast(t *testing.T) {
	now := time.Date(2026, 8, 27, 2, 3, 4, 0, time.UTC)
	actual := accountingServiceMO("allocation-actual", 1_000)
	actual.FundingState = domain.MOFundingActive
	actual.MerchantOrderID = "mo-actual"
	actual.MerchantOrderState = "PLACED"
	actual.Cash = &domain.AccountingCashInput{
		ID: "cash-actual", GrossMinor: actual.CustomerGrossMinor,
		EconomicsReconciled: true, ProcessorFeeMinor: 78,
		NetReceivableMinor: actual.CustomerGrossMinor - 78, OccurredAt: now,
	}
	actual.MerchantSpend = &domain.AccountingMerchantSpendInput{
		ID: "spend-actual", State: "SUCCEEDED", AmountMinor: 1_000,
		OccurredAt: now,
	}
	forecast := accountingServiceMO("allocation-forecast", 2_000)
	forecast.CheckoutOrdinal = 2
	service := NewAccountingService(&accountingRepository{inputs: []domain.OrderAccountingInput{{
		AgencyOrderID: "order-1", CustomerPaymentID: "payment-1", Rail: "PAYPAL",
		ProviderEnvironment: "SANDBOX", PaymentState: "PARTIALLY_CAPTURED",
		Currency: "USD", MerchantOrders: []domain.MOAccountingInput{actual, forecast},
		CreatedAt: now,
	}}}, &fakeClock{now: now})

	summary, err := service.GetAccounting(context.Background(), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if summary.OrderCount != 1 || summary.ActualNetCashInMinor != 1_006 ||
		summary.ActualMerchantSpendMinor != 1_000 || summary.RealizedBalanceMinor != 6 ||
		summary.ForecastAdjustmentMinor != 13 || summary.ForecastBalanceMinor != 19 {
		t.Fatalf("summary=%+v", summary)
	}
	order, found, err := service.GetOrderAccounting(context.Background(), "order-1")
	if err != nil || !found || order.ForecastBalanceMinor != 19 {
		t.Fatalf("order projection found=%v err=%v order=%+v", found, err, order)
	}
}

func TestAccountingSummaryRequiresExactStoredEnvironment(t *testing.T) {
	now := time.Date(2026, 8, 27, 2, 3, 4, 0, time.UTC)
	service := NewAccountingService(&accountingRepository{}, &fakeClock{now: now})
	for _, environment := range []string{"", "PRODUCTION", "TEST"} {
		if _, err := service.GetAccounting(context.Background(), environment); err != domain.ErrInvalid {
			t.Fatalf("environment %q error=%v, want ErrInvalid", environment, err)
		}
	}
	for _, environment := range []string{"SANDBOX", "LIVE", "TESTNET"} {
		if _, err := service.GetAccounting(context.Background(), environment); err != nil {
			t.Fatalf("environment %q error=%v", environment, err)
		}
	}
}

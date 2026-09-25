package app

import (
	"context"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

const orderAccountingLimit = 500

// AccountingRepository reads immutable allocation and actual owner facts. It
// has no mutation port because accounting is never a payment or merchant gate.
type AccountingRepository interface {
	GetOrderAccountingInput(context.Context, string) (domain.OrderAccountingInput, bool, error)
	ListOrderAccountingInputs(context.Context, string, int) ([]domain.OrderAccountingInput, error)
}

type AccountingService struct {
	repository AccountingRepository
	clock      sharedapp.Clock
}

func NewAccountingService(
	repository AccountingRepository,
	clock sharedapp.Clock,
) *AccountingService {
	return &AccountingService{repository: repository, clock: clock}
}

func (s *AccountingService) GetOrderAccounting(
	ctx context.Context,
	agencyOrderID string,
) (domain.OrderAccountingProjection, bool, error) {
	input, found, err := s.repository.GetOrderAccountingInput(
		ctx, strings.TrimSpace(agencyOrderID),
	)
	if err != nil || !found {
		return domain.OrderAccountingProjection{}, found, err
	}
	projection, err := domain.ProjectOrderAccounting(input)
	return projection, true, err
}

type AccountingSummary struct {
	ProviderEnvironment            string                             `json:"providerEnvironment"`
	Currency                       string                             `json:"currency"`
	ActualCustomerGrossInMinor     int64                              `json:"actualCustomerGrossInMinor"`
	ActualProcessorFeeMinor        int64                              `json:"actualProcessorFeeMinor"`
	ActualNetCashInMinor           int64                              `json:"actualNetCashInMinor"`
	ActualMerchantSpendMinor       int64                              `json:"actualMerchantSpendMinor"`
	ActualCustomerCompensatedMinor int64                              `json:"actualCustomerCompensatedMinor"`
	ActualMerchantRecoveredMinor   int64                              `json:"actualMerchantRecoveredMinor"`
	UnreconciledCashGrossMinor     int64                              `json:"unreconciledCashGrossMinor"`
	RealizedBalanceMinor           int64                              `json:"realizedBalanceMinor"`
	ForecastNetCashInMinor         int64                              `json:"forecastNetCashInMinor"`
	ForecastProcessorFeeMinor      int64                              `json:"forecastProcessorFeeMinor"`
	ForecastMerchantSpendMinor     int64                              `json:"forecastMerchantSpendMinor"`
	ExpectedCompensationMinor      int64                              `json:"expectedCompensationMinor"`
	ForecastAdjustmentMinor        int64                              `json:"forecastAdjustmentMinor"`
	ForecastBalanceMinor           int64                              `json:"forecastBalanceMinor"`
	OrderCount                     int                                `json:"orderCount"`
	AttentionOrderCount            int                                `json:"attentionOrderCount"`
	Orders                         []domain.OrderAccountingProjection `json:"orders"`
	AsOf                           time.Time                          `json:"asOf"`
}

func (s *AccountingService) GetAccounting(
	ctx context.Context,
	providerEnvironment string,
) (AccountingSummary, error) {
	environment := strings.ToUpper(strings.TrimSpace(providerEnvironment))
	if environment != "SANDBOX" && environment != "LIVE" && environment != "TESTNET" {
		return AccountingSummary{}, domain.ErrInvalid
	}
	inputs, err := s.repository.ListOrderAccountingInputs(
		ctx, environment, orderAccountingLimit,
	)
	if err != nil {
		return AccountingSummary{}, err
	}
	summary := AccountingSummary{
		ProviderEnvironment: environment,
		Currency:            "USD",
		Orders:              make([]domain.OrderAccountingProjection, 0, len(inputs)),
		AsOf:                s.clock.Now(),
	}
	for _, input := range inputs {
		order, projectErr := domain.ProjectOrderAccounting(input)
		if projectErr != nil {
			return AccountingSummary{}, projectErr
		}
		summary.ActualCustomerGrossInMinor += order.ActualCustomerGrossInMinor
		summary.ActualProcessorFeeMinor += order.ActualProcessorFeeMinor
		summary.ActualNetCashInMinor += order.ActualNetCashInMinor
		summary.ActualMerchantSpendMinor += order.ActualMerchantSpendMinor
		summary.ActualCustomerCompensatedMinor += order.ActualCustomerCompensatedMinor
		summary.ActualMerchantRecoveredMinor += order.ActualMerchantRecoveredMinor
		summary.UnreconciledCashGrossMinor += order.UnreconciledCashGrossMinor
		summary.ForecastNetCashInMinor += order.ForecastNetCashInMinor
		summary.ForecastProcessorFeeMinor += order.ForecastProcessorFeeMinor
		summary.ForecastMerchantSpendMinor += order.ForecastMerchantSpendMinor
		summary.ExpectedCompensationMinor += order.ExpectedCompensationMinor
		summary.ForecastAdjustmentMinor += order.ForecastAdjustmentMinor
		if order.RequiresAttention {
			summary.AttentionOrderCount++
		}
		summary.Orders = append(summary.Orders, order)
	}
	summary.OrderCount = len(summary.Orders)
	summary.RealizedBalanceMinor = summary.ActualNetCashInMinor -
		summary.ActualMerchantSpendMinor - summary.ActualCustomerCompensatedMinor +
		summary.ActualMerchantRecoveredMinor
	summary.ForecastBalanceMinor = summary.RealizedBalanceMinor +
		summary.ForecastAdjustmentMinor
	return summary, nil
}

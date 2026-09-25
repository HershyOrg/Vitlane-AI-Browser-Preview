package domain

import (
	"sort"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
)

// AccountingEventKind is a cash fact, not an inferred funding obligation.
// Amounts are always positive; Direction determines the balance sign.
type AccountingEventKind string

const (
	AccountingCustomerCashIn       AccountingEventKind = "CUSTOMER_CASH_IN"
	AccountingMerchantPurchase     AccountingEventKind = "MERCHANT_PURCHASE"
	AccountingCustomerCompensation AccountingEventKind = "CUSTOMER_COMPENSATION"
	AccountingAuthorizationRelease AccountingEventKind = "AUTHORIZATION_RELEASE"
	AccountingMerchantRecovery     AccountingEventKind = "MERCHANT_RECOVERY"
)

type AccountingDirection string

const (
	AccountingCredit  AccountingDirection = "CREDIT"
	AccountingDebit   AccountingDirection = "DEBIT"
	AccountingNeutral AccountingDirection = "NEUTRAL"
)

type AccountingCashInput struct {
	ID                  string
	GrossMinor          int64
	EconomicsReconciled bool
	ProcessorFeeMinor   int64
	NetReceivableMinor  int64
	OccurredAt          time.Time
}

type AccountingMerchantSpendInput struct {
	ID          string
	State       string
	AmountMinor int64
	OccurredAt  time.Time
}

type AccountingCompensationInput struct {
	ID          string
	Action      string
	Cause       string
	State       string
	AmountMinor int64
	OccurredAt  time.Time
}

type AccountingRecoveryInput struct {
	ID          string
	Cause       string
	AmountMinor int64
	OccurredAt  time.Time
}

// MOAccountingInput contains only immutable allocation facts and actual owner
// facts. Forecast values are derived in ProjectOrderAccounting.
type MOAccountingInput struct {
	AllocationID       string
	MerchantOrderID    string
	ShopDomain         string
	CheckoutOrdinal    int
	PassThroughMinor   int64
	FeeVariableMinor   int64
	FeeFixedMinor      int64
	FeeTotalMinor      int64
	CustomerGrossMinor int64
	FeePolicyVersion   string
	FundingState       MOFundingState
	MerchantOrderState string
	ExceptionCause     string
	Cash               *AccountingCashInput
	MerchantSpend      *AccountingMerchantSpendInput
	Compensation       *AccountingCompensationInput
	Recoveries         []AccountingRecoveryInput
}

type OrderAccountingInput struct {
	AgencyOrderID       string
	CustomerPaymentID   string
	Rail                string
	ProviderEnvironment string
	PaymentState        string
	Currency            string
	OrderCash           *AccountingCashInput
	MerchantOrders      []MOAccountingInput
	CreatedAt           time.Time
}

type OrderAccountingEvent struct {
	ID                  string              `json:"id"`
	Kind                AccountingEventKind `json:"kind"`
	Direction           AccountingDirection `json:"direction"`
	MerchantOrderID     string              `json:"merchantOrderId,omitempty"`
	AllocationID        string              `json:"allocationId,omitempty"`
	AmountMinor         int64               `json:"amountMinor"`
	CustomerGrossMinor  int64               `json:"customerGrossMinor,omitempty"`
	ProcessorFeeMinor   int64               `json:"processorFeeMinor,omitempty"`
	EconomicsReconciled bool                `json:"economicsReconciled"`
	Source              string              `json:"source"`
	Cause               string              `json:"cause,omitempty"`
	OccurredAt          time.Time           `json:"occurredAt"`
}

type MOAccountingProjection struct {
	AllocationID                   string         `json:"allocationId"`
	MerchantOrderID                string         `json:"merchantOrderId,omitempty"`
	ShopDomain                     string         `json:"shopDomain"`
	CheckoutOrdinal                int            `json:"checkoutOrdinal"`
	PassThroughMinor               int64          `json:"passThroughMinor"`
	FeeVariableMinor               int64          `json:"feeVariableMinor"`
	FeeFixedMinor                  int64          `json:"feeFixedMinor"`
	FeeTotalMinor                  int64          `json:"feeTotalMinor"`
	CustomerGrossMinor             int64          `json:"customerGrossMinor"`
	FeePolicyVersion               string         `json:"feePolicyVersion"`
	FundingState                   MOFundingState `json:"fundingState"`
	MerchantOrderState             string         `json:"merchantOrderState,omitempty"`
	MerchantPaymentState           string         `json:"merchantPaymentState,omitempty"`
	CompensationAction             string         `json:"compensationAction,omitempty"`
	CompensationState              string         `json:"compensationState,omitempty"`
	CompensationCause              string         `json:"compensationCause,omitempty"`
	ExceptionCause                 string         `json:"exceptionCause,omitempty"`
	ActualCustomerGrossInMinor     int64          `json:"actualCustomerGrossInMinor"`
	ActualProcessorFeeMinor        int64          `json:"actualProcessorFeeMinor"`
	ActualNetCashInMinor           int64          `json:"actualNetCashInMinor"`
	ActualMerchantSpendMinor       int64          `json:"actualMerchantSpendMinor"`
	ActualCustomerCompensatedMinor int64          `json:"actualCustomerCompensatedMinor"`
	ActualMerchantRecoveredMinor   int64          `json:"actualMerchantRecoveredMinor"`
	UnreconciledCashGrossMinor     int64          `json:"unreconciledCashGrossMinor"`
	RealizedBalanceMinor           int64          `json:"realizedBalanceMinor"`
	ForecastNetCashInMinor         int64          `json:"forecastNetCashInMinor"`
	ForecastProcessorFeeMinor      int64          `json:"forecastProcessorFeeMinor"`
	ForecastMerchantSpendMinor     int64          `json:"forecastMerchantSpendMinor"`
	ExpectedCompensationMinor      int64          `json:"expectedCompensationMinor"`
	ForecastAdjustmentMinor        int64          `json:"forecastAdjustmentMinor"`
	ForecastBalanceMinor           int64          `json:"forecastBalanceMinor"`
	AttentionReasons               []string       `json:"attentionReasons"`
}

type OrderAccountingProjection struct {
	AgencyOrderID                  string                   `json:"agencyOrderId"`
	CustomerPaymentID              string                   `json:"customerPaymentId"`
	Rail                           string                   `json:"rail"`
	ProviderEnvironment            string                   `json:"providerEnvironment"`
	PaymentState                   string                   `json:"paymentState"`
	Currency                       string                   `json:"currency"`
	ActualCustomerGrossInMinor     int64                    `json:"actualCustomerGrossInMinor"`
	ActualProcessorFeeMinor        int64                    `json:"actualProcessorFeeMinor"`
	ActualNetCashInMinor           int64                    `json:"actualNetCashInMinor"`
	ActualMerchantSpendMinor       int64                    `json:"actualMerchantSpendMinor"`
	ActualCustomerCompensatedMinor int64                    `json:"actualCustomerCompensatedMinor"`
	ActualMerchantRecoveredMinor   int64                    `json:"actualMerchantRecoveredMinor"`
	UnreconciledCashGrossMinor     int64                    `json:"unreconciledCashGrossMinor"`
	RealizedBalanceMinor           int64                    `json:"realizedBalanceMinor"`
	ForecastNetCashInMinor         int64                    `json:"forecastNetCashInMinor"`
	ForecastProcessorFeeMinor      int64                    `json:"forecastProcessorFeeMinor"`
	ForecastMerchantSpendMinor     int64                    `json:"forecastMerchantSpendMinor"`
	ExpectedCompensationMinor      int64                    `json:"expectedCompensationMinor"`
	ForecastAdjustmentMinor        int64                    `json:"forecastAdjustmentMinor"`
	ForecastBalanceMinor           int64                    `json:"forecastBalanceMinor"`
	RequiresAttention              bool                     `json:"requiresAttention"`
	AttentionReasons               []string                 `json:"attentionReasons"`
	MerchantOrders                 []MOAccountingProjection `json:"merchantOrders"`
	Events                         []OrderAccountingEvent   `json:"events"`
	CreatedAt                      time.Time                `json:"createdAt"`
}

func ceilRatio(value, numerator, denominator int64) int64 {
	whole := value / denominator
	remainder := value % denominator
	return whole*numerator + (remainder*numerator+denominator-1)/denominator
}

func expectedPayPalProcessorFee(grossMinor int64) int64 {
	// Forecast only: the currently approved Korean commercial rate assumption
	// is 4.4% of each captured MO gross plus USD 0.30. Actual accounting always
	// replaces this value with the provider receipt's seller breakdown.
	return ceilRatio(grossMinor, 44, 1000) + 30
}

func validateOrderFeeAllocations(input OrderAccountingInput) error {
	rail := "PAYPAL_SANDBOX"
	if input.Rail == "GIWA" {
		rail = "TVITUSD"
	}
	bases := make([]policy.MerchantFeeBase, len(input.MerchantOrders))
	for index, merchantOrder := range input.MerchantOrders {
		bases[index] = policy.MerchantFeeBase{
			CheckoutOrdinal:  merchantOrder.CheckoutOrdinal,
			PassThroughMinor: merchantOrder.PassThroughMinor,
		}
	}
	quote, err := policy.CalculateAgencyFee(rail, bases)
	if err != nil || len(quote.MerchantOrders) != len(input.MerchantOrders) {
		return ErrInvalid
	}
	expected := make(map[int]policy.MerchantFeeAllocation, len(quote.MerchantOrders))
	for _, allocation := range quote.MerchantOrders {
		expected[allocation.CheckoutOrdinal] = allocation
	}
	for _, merchantOrder := range input.MerchantOrders {
		allocation, ok := expected[merchantOrder.CheckoutOrdinal]
		if !ok || merchantOrder.FeePolicyVersion != quote.PolicyVersion ||
			merchantOrder.FeeVariableMinor != allocation.VariableMinor ||
			merchantOrder.FeeFixedMinor != allocation.FixedMinor ||
			merchantOrder.FeeTotalMinor != allocation.TotalMinor ||
			merchantOrder.CustomerGrossMinor != allocation.CustomerPayableMinor {
			return ErrInvalid
		}
	}
	return nil
}

func actualMerchantSpend(value *AccountingMerchantSpendInput) int64 {
	if value == nil {
		return 0
	}
	switch value.State {
	case "SUCCEEDED", "NONCONFORMING_CHARGE":
		return value.AmountMinor
	default:
		return 0
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func projectMOAccounting(
	rail string,
	wholeOrderCashPresent bool,
	input MOAccountingInput,
) (MOAccountingProjection, []OrderAccountingEvent, error) {
	if input.AllocationID == "" || input.ShopDomain == "" || input.CheckoutOrdinal <= 0 ||
		input.PassThroughMinor <= 0 || input.FeeVariableMinor < 0 ||
		input.FeeFixedMinor < 0 || input.FeeTotalMinor < 0 ||
		input.CustomerGrossMinor <= 0 || input.FeePolicyVersion == "" {
		return MOAccountingProjection{}, nil, ErrInvalid
	}
	projection := MOAccountingProjection{
		AllocationID: input.AllocationID, MerchantOrderID: input.MerchantOrderID,
		ShopDomain: input.ShopDomain, CheckoutOrdinal: input.CheckoutOrdinal,
		PassThroughMinor: input.PassThroughMinor,
		FeeVariableMinor: input.FeeVariableMinor, FeeFixedMinor: input.FeeFixedMinor,
		FeeTotalMinor: input.FeeTotalMinor, CustomerGrossMinor: input.CustomerGrossMinor,
		FeePolicyVersion: input.FeePolicyVersion, FundingState: input.FundingState,
		MerchantOrderState: input.MerchantOrderState, ExceptionCause: input.ExceptionCause,
		AttentionReasons: make([]string, 0),
	}
	events := make([]OrderAccountingEvent, 0, 4+len(input.Recoveries))

	if rail == "GIWA" && wholeOrderCashPresent {
		projection.ActualCustomerGrossInMinor = input.CustomerGrossMinor
		projection.ActualNetCashInMinor = input.CustomerGrossMinor
	}
	if rail == "PAYPAL" && input.FundingState == MOFundingActive && input.Cash == nil {
		return MOAccountingProjection{}, nil, ErrInvalid
	}
	if rail == "PAYPAL" && input.FundingState == MOFundingAvailable && input.Cash != nil {
		return MOAccountingProjection{}, nil, ErrInvalid
	}
	if input.Cash != nil {
		cash := input.Cash
		if rail != "PAYPAL" || cash.ID == "" || cash.GrossMinor != input.CustomerGrossMinor ||
			cash.GrossMinor <= 0 {
			return MOAccountingProjection{}, nil, ErrInvalid
		}
		projection.ActualCustomerGrossInMinor = cash.GrossMinor
		event := OrderAccountingEvent{
			ID: cash.ID, Kind: AccountingCustomerCashIn, Direction: AccountingCredit,
			MerchantOrderID: input.MerchantOrderID, AllocationID: input.AllocationID,
			CustomerGrossMinor: cash.GrossMinor, EconomicsReconciled: cash.EconomicsReconciled,
			Source: "PAYPAL_MO_CAPTURE", OccurredAt: cash.OccurredAt,
		}
		if cash.EconomicsReconciled {
			if cash.ProcessorFeeMinor < 0 || cash.NetReceivableMinor < 0 ||
				cash.GrossMinor-cash.ProcessorFeeMinor != cash.NetReceivableMinor {
				return MOAccountingProjection{}, nil, ErrInvalid
			}
			projection.ActualProcessorFeeMinor = cash.ProcessorFeeMinor
			projection.ActualNetCashInMinor = cash.NetReceivableMinor
			event.AmountMinor = cash.NetReceivableMinor
			event.ProcessorFeeMinor = cash.ProcessorFeeMinor
		} else {
			if cash.ProcessorFeeMinor != 0 || cash.NetReceivableMinor != 0 {
				return MOAccountingProjection{}, nil, ErrInvalid
			}
			projection.UnreconciledCashGrossMinor = cash.GrossMinor
			projection.AttentionReasons = append(projection.AttentionReasons,
				"PAYPAL_CASH_UNRECONCILED")
		}
		events = append(events, event)
	}

	if input.MerchantSpend != nil {
		projection.MerchantPaymentState = input.MerchantSpend.State
		if input.MerchantSpend.ID == "" || input.MerchantSpend.AmountMinor <= 0 {
			return MOAccountingProjection{}, nil, ErrInvalid
		}
		projection.ActualMerchantSpendMinor = actualMerchantSpend(input.MerchantSpend)
		if projection.ActualMerchantSpendMinor > 0 {
			events = append(events, OrderAccountingEvent{
				ID: input.MerchantSpend.ID, Kind: AccountingMerchantPurchase,
				Direction: AccountingDebit, MerchantOrderID: input.MerchantOrderID,
				AllocationID: input.AllocationID,
				AmountMinor:  input.MerchantSpend.AmountMinor, EconomicsReconciled: true,
				Source: "MERCHANT_PAYMENT", OccurredAt: input.MerchantSpend.OccurredAt,
			})
		} else if input.MerchantSpend.State == "OUTCOME_UNKNOWN" {
			projection.AttentionReasons = append(projection.AttentionReasons,
				"MERCHANT_PURCHASE_UNKNOWN")
		}
	}

	if input.Compensation != nil {
		compensation := input.Compensation
		if compensation.ID == "" || compensation.AmountMinor != input.CustomerGrossMinor {
			return MOAccountingProjection{}, nil, ErrInvalid
		}
		projection.CompensationAction = compensation.Action
		projection.CompensationState = compensation.State
		projection.CompensationCause = compensation.Cause
		if projection.ExceptionCause == "" {
			projection.ExceptionCause = compensation.Cause
		}
		if compensation.State == "SUCCEEDED" {
			kind, direction, amount := AccountingCustomerCompensation, AccountingDebit,
				compensation.AmountMinor
			if compensation.Action == "VOID" {
				kind, direction, amount = AccountingAuthorizationRelease, AccountingNeutral, 0
			}
			events = append(events, OrderAccountingEvent{
				ID: compensation.ID, Kind: kind, Direction: direction,
				MerchantOrderID: input.MerchantOrderID, AllocationID: input.AllocationID,
				AmountMinor: amount, CustomerGrossMinor: compensation.AmountMinor,
				EconomicsReconciled: true, Source: "MO_COMPENSATION",
				Cause: compensation.Cause, OccurredAt: compensation.OccurredAt,
			})
			if compensation.Action != "VOID" {
				projection.ActualCustomerCompensatedMinor = compensation.AmountMinor
			}
		} else {
			if compensation.Action == "VOID" {
				projection.AttentionReasons = append(projection.AttentionReasons,
					"AUTHORIZATION_RELEASE_EXPECTED")
			} else {
				projection.AttentionReasons = append(projection.AttentionReasons,
					"CUSTOMER_COMPENSATION_EXPECTED")
			}
			if compensation.State == "OUTCOME_UNKNOWN" {
				projection.AttentionReasons = append(projection.AttentionReasons,
					"COMPENSATION_OUTCOME_UNKNOWN")
			}
		}
	}

	for _, recovery := range input.Recoveries {
		if recovery.ID == "" || recovery.AmountMinor < 0 {
			return MOAccountingProjection{}, nil, ErrInvalid
		}
		if recovery.AmountMinor == 0 {
			continue
		}
		projection.ActualMerchantRecoveredMinor += recovery.AmountMinor
		events = append(events, OrderAccountingEvent{
			ID: recovery.ID, Kind: AccountingMerchantRecovery, Direction: AccountingCredit,
			MerchantOrderID: input.MerchantOrderID, AllocationID: input.AllocationID,
			AmountMinor: recovery.AmountMinor, EconomicsReconciled: true,
			Source: "PROCUREMENT_RECOVERY", Cause: recovery.Cause,
			OccurredAt: recovery.OccurredAt,
		})
	}

	projection.RealizedBalanceMinor = projection.ActualNetCashInMinor -
		projection.ActualMerchantSpendMinor - projection.ActualCustomerCompensatedMinor +
		projection.ActualMerchantRecoveredMinor

	exceptionOpen := projection.ExceptionCause != "" &&
		(input.Compensation == nil || input.Compensation.State != "SUCCEEDED")
	preEffectPayPalRelease := rail == "PAYPAL" && input.Cash == nil &&
		projection.ActualMerchantSpendMinor == 0 &&
		(input.FundingState == MOFundingAvailable ||
			input.FundingState == MOFundingReleasePending ||
			input.FundingState == MOFundingReleased ||
			input.FundingState == MOFundingFailed) &&
		(exceptionOpen || (input.Compensation != nil && input.Compensation.Action == "VOID"))

	if rail == "PAYPAL" && projection.ActualNetCashInMinor == 0 &&
		input.FundingState != MOFundingReleased && input.FundingState != MOFundingFailed &&
		!preEffectPayPalRelease {
		projection.ForecastProcessorFeeMinor = expectedPayPalProcessorFee(input.CustomerGrossMinor)
		projection.ForecastNetCashInMinor = max(
			input.CustomerGrossMinor-projection.ForecastProcessorFeeMinor, 0,
		)
	}

	if input.Compensation != nil && input.Compensation.State != "SUCCEEDED" &&
		input.Compensation.Action != "VOID" {
		projection.ExpectedCompensationMinor = input.CustomerGrossMinor
	} else if input.Compensation == nil && exceptionOpen && !preEffectPayPalRelease {
		projection.ExpectedCompensationMinor = input.CustomerGrossMinor
	}

	terminalWithoutSpend := input.MerchantOrderState == "FAILED" ||
		input.MerchantOrderState == "CANCELLED" || input.FundingState == MOFundingReleased ||
		input.FundingState == MOFundingFailed
	if projection.ActualMerchantSpendMinor == 0 && !exceptionOpen &&
		input.Compensation == nil && !terminalWithoutSpend {
		projection.ForecastMerchantSpendMinor = input.PassThroughMinor
	}

	if input.FundingState == MOFundingActivationUnknown ||
		input.FundingState == MOFundingReleaseUnknown {
		projection.AttentionReasons = append(projection.AttentionReasons,
			"FUNDING_EFFECT_UNKNOWN")
	}
	if exceptionOpen && projection.ExpectedCompensationMinor > 0 {
		projection.AttentionReasons = append(projection.AttentionReasons,
			"CUSTOMER_COMPENSATION_EXPECTED")
	}
	projection.AttentionReasons = uniqueStrings(projection.AttentionReasons)
	projection.ForecastAdjustmentMinor = projection.ForecastNetCashInMinor -
		projection.ForecastMerchantSpendMinor - projection.ExpectedCompensationMinor
	projection.ForecastBalanceMinor = projection.RealizedBalanceMinor +
		projection.ForecastAdjustmentMinor
	return projection, events, nil
}

// ProjectOrderAccounting folds actual order events first, then adds forecast
// adjustments only for unresolved MO economics. It is a read model and grants
// no authority to capture, purchase, compensate, or recover money.
func ProjectOrderAccounting(input OrderAccountingInput) (OrderAccountingProjection, error) {
	if input.AgencyOrderID == "" || input.CustomerPaymentID == "" || input.Currency != "USD" ||
		len(input.MerchantOrders) == 0 ||
		(input.Rail != "PAYPAL" && input.Rail != "GIWA") ||
		(input.Rail == "PAYPAL" && input.ProviderEnvironment != "SANDBOX" &&
			input.ProviderEnvironment != "LIVE") ||
		(input.Rail == "GIWA" && input.ProviderEnvironment != "TESTNET") {
		return OrderAccountingProjection{}, ErrInvalid
	}
	if err := validateOrderFeeAllocations(input); err != nil {
		return OrderAccountingProjection{}, err
	}
	projection := OrderAccountingProjection{
		AgencyOrderID: input.AgencyOrderID, CustomerPaymentID: input.CustomerPaymentID,
		Rail: input.Rail, ProviderEnvironment: input.ProviderEnvironment,
		PaymentState: input.PaymentState, Currency: input.Currency,
		MerchantOrders: make([]MOAccountingProjection, 0, len(input.MerchantOrders)),
		Events:         make([]OrderAccountingEvent, 0), AttentionReasons: make([]string, 0),
		CreatedAt: input.CreatedAt,
	}
	allocationIDs := make(map[string]struct{}, len(input.MerchantOrders))
	var allocatedGross int64
	for _, merchantOrder := range input.MerchantOrders {
		if _, exists := allocationIDs[merchantOrder.AllocationID]; exists {
			return OrderAccountingProjection{}, ErrInvalid
		}
		allocationIDs[merchantOrder.AllocationID] = struct{}{}
		allocatedGross += merchantOrder.CustomerGrossMinor
	}
	wholeOrderCashPresent := input.OrderCash != nil
	if input.Rail == "PAYPAL" && wholeOrderCashPresent {
		return OrderAccountingProjection{}, ErrInvalid
	}
	if input.Rail == "GIWA" && !wholeOrderCashPresent {
		return OrderAccountingProjection{}, ErrInvalid
	}
	if input.Rail == "GIWA" && wholeOrderCashPresent {
		cash := input.OrderCash
		if cash.ID == "" || !cash.EconomicsReconciled || cash.GrossMinor != allocatedGross ||
			cash.ProcessorFeeMinor != 0 || cash.NetReceivableMinor != cash.GrossMinor {
			return OrderAccountingProjection{}, ErrInvalid
		}
		projection.Events = append(projection.Events, OrderAccountingEvent{
			ID: cash.ID, Kind: AccountingCustomerCashIn, Direction: AccountingCredit,
			AmountMinor: cash.NetReceivableMinor, CustomerGrossMinor: cash.GrossMinor,
			EconomicsReconciled: true, Source: "GIWA_FINALIZED_PAY",
			OccurredAt: cash.OccurredAt,
		})
	}

	for _, inputMO := range input.MerchantOrders {
		merchantOrder, events, err := projectMOAccounting(
			input.Rail, wholeOrderCashPresent, inputMO,
		)
		if err != nil {
			return OrderAccountingProjection{}, err
		}
		projection.MerchantOrders = append(projection.MerchantOrders, merchantOrder)
		projection.Events = append(projection.Events, events...)
		projection.ActualCustomerGrossInMinor += merchantOrder.ActualCustomerGrossInMinor
		projection.ActualProcessorFeeMinor += merchantOrder.ActualProcessorFeeMinor
		projection.ActualNetCashInMinor += merchantOrder.ActualNetCashInMinor
		projection.ActualMerchantSpendMinor += merchantOrder.ActualMerchantSpendMinor
		projection.ActualCustomerCompensatedMinor += merchantOrder.ActualCustomerCompensatedMinor
		projection.ActualMerchantRecoveredMinor += merchantOrder.ActualMerchantRecoveredMinor
		projection.UnreconciledCashGrossMinor += merchantOrder.UnreconciledCashGrossMinor
		projection.ForecastNetCashInMinor += merchantOrder.ForecastNetCashInMinor
		projection.ForecastProcessorFeeMinor += merchantOrder.ForecastProcessorFeeMinor
		projection.ForecastMerchantSpendMinor += merchantOrder.ForecastMerchantSpendMinor
		projection.ExpectedCompensationMinor += merchantOrder.ExpectedCompensationMinor
		projection.ForecastAdjustmentMinor += merchantOrder.ForecastAdjustmentMinor
		projection.AttentionReasons = append(projection.AttentionReasons,
			merchantOrder.AttentionReasons...)
	}
	projection.RealizedBalanceMinor = projection.ActualNetCashInMinor -
		projection.ActualMerchantSpendMinor - projection.ActualCustomerCompensatedMinor +
		projection.ActualMerchantRecoveredMinor
	projection.ForecastBalanceMinor = projection.RealizedBalanceMinor +
		projection.ForecastAdjustmentMinor
	projection.AttentionReasons = uniqueStrings(projection.AttentionReasons)
	projection.RequiresAttention = len(projection.AttentionReasons) > 0
	sort.SliceStable(projection.MerchantOrders, func(left, right int) bool {
		return projection.MerchantOrders[left].CheckoutOrdinal <
			projection.MerchantOrders[right].CheckoutOrdinal
	})
	sort.SliceStable(projection.Events, func(left, right int) bool {
		if projection.Events[left].OccurredAt.Equal(projection.Events[right].OccurredAt) {
			return projection.Events[left].ID < projection.Events[right].ID
		}
		return projection.Events[left].OccurredAt.Before(projection.Events[right].OccurredAt)
	})
	return projection, nil
}

type PayPalAuthorizationState string

const (
	AuthorizationOrderCreated      PayPalAuthorizationState = "ORDER_CREATED"
	AuthorizationPayerAction       PayPalAuthorizationState = "PAYER_ACTION_REQUIRED"
	AuthorizationPayerApproved     PayPalAuthorizationState = "PAYER_APPROVED"
	AuthorizationPending           PayPalAuthorizationState = "AUTHORIZE_PENDING"
	AuthorizationUnknown           PayPalAuthorizationState = "AUTHORIZE_UNKNOWN"
	AuthorizationAuthorized        PayPalAuthorizationState = "AUTHORIZED"
	AuthorizationPartiallyCaptured PayPalAuthorizationState = "PARTIALLY_CAPTURED"
	AuthorizationCaptured          PayPalAuthorizationState = "CAPTURED"
	AuthorizationVoided            PayPalAuthorizationState = "VOIDED"
	AuthorizationDenied            PayPalAuthorizationState = "DENIED"
	AuthorizationExpired           PayPalAuthorizationState = "EXPIRED"
	AuthorizationFailed            PayPalAuthorizationState = "FAILED"
)

type PayPalAuthorization struct {
	ID                    string                   `json:"id"`
	CustomerPaymentID     string                   `json:"customerPaymentId"`
	AgencyOrderID         string                   `json:"agencyOrderId"`
	PayPalAttemptID       string                   `json:"paypalAttemptId"`
	ProviderEnvironment   string                   `json:"providerEnvironment"`
	PayPalOrderID         string                   `json:"paypalOrderId"`
	PayeeMerchantID       string                   `json:"payeeMerchantId"`
	PayPalAuthorizationID string                   `json:"paypalAuthorizationId"`
	AmountMinor           int64                    `json:"amountMinor"`
	Currency              string                   `json:"currency"`
	State                 PayPalAuthorizationState `json:"state"`
	Version               int64                    `json:"version"`
	AuthorizedAt          *time.Time               `json:"authorizedAt,omitempty"`
	HonorRefreshedAt      *time.Time               `json:"honorRefreshedAt,omitempty"`
	ReauthorizationCount  int                      `json:"reauthorizationCount"`
	TerminalAt            *time.Time               `json:"terminalAt,omitempty"`
	CreatedAt             time.Time                `json:"createdAt"`
	UpdatedAt             time.Time                `json:"updatedAt"`
}

type MOFundingState string

const (
	MOFundingAvailable         MOFundingState = "AVAILABLE"
	MOFundingActivationPending MOFundingState = "ACTIVATION_PENDING"
	MOFundingActivationUnknown MOFundingState = "ACTIVATION_UNKNOWN"
	MOFundingActive            MOFundingState = "ACTIVE"
	MOFundingReleasePending    MOFundingState = "RELEASE_PENDING"
	MOFundingReleaseUnknown    MOFundingState = "RELEASE_UNKNOWN"
	MOFundingReleased          MOFundingState = "RELEASED"
	MOFundingFailed            MOFundingState = "FAILED"
)

type MOFundingPosition struct {
	ID                    string         `json:"id"`
	AllocationID          string         `json:"allocationId"`
	AgencyOrderID         string         `json:"agencyOrderId"`
	CustomerPaymentID     string         `json:"customerPaymentId"`
	PayPalAuthorizationID string         `json:"paypalAuthorizationId,omitempty"`
	Rail                  string         `json:"rail"`
	Source                string         `json:"source"`
	ProviderEnvironment   string         `json:"providerEnvironment"`
	AmountMinor           int64          `json:"amountMinor"`
	Currency              string         `json:"currency"`
	State                 MOFundingState `json:"state"`
	Version               int64          `json:"version"`
	AvailableAt           time.Time      `json:"availableAt"`
	ActivatedAt           *time.Time     `json:"activatedAt,omitempty"`
	ReleasedAt            *time.Time     `json:"releasedAt,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	UpdatedAt             time.Time      `json:"updatedAt"`
}

type MOCashReceipt struct {
	ID                    string    `json:"id"`
	FundingPositionID     string    `json:"fundingPositionId"`
	AllocationID          string    `json:"allocationId"`
	AgencyOrderID         string    `json:"agencyOrderId"`
	CustomerPaymentID     string    `json:"customerPaymentId"`
	PayPalAuthorizationID string    `json:"paypalAuthorizationId"`
	ProviderEnvironment   string    `json:"providerEnvironment"`
	ProviderCaptureID     string    `json:"providerCaptureId"`
	GrossMinor            int64     `json:"grossMinor"`
	EconomicsReconciled   bool      `json:"economicsReconciled"`
	ProcessorFeeMinor     int64     `json:"processorFeeMinor"`
	NetReceivableMinor    int64     `json:"netReceivableMinor"`
	Currency              string    `json:"currency"`
	OccurredAt            time.Time `json:"occurredAt"`
	CreatedAt             time.Time `json:"createdAt"`
}

// MOCompensationCause is the business fact that permits one immutable
// whole-MO gross to be returned. It deliberately has no free-form fallback:
// the owning review/cancellation process state must classify the obligation first.
type MOCompensationCause string

const (
	MOCompensationCustomerCancelPreEffect  MOCompensationCause = "CUSTOMER_CANCEL_PRE_EFFECT"
	MOCompensationCustomerRefundPostEffect MOCompensationCause = "CUSTOMER_REFUND_POST_EFFECT"
	MOCompensationProcurementFailure       MOCompensationCause = "PROCUREMENT_FAILURE"
	MOCompensationDeliveryException        MOCompensationCause = "DELIVERY_EXCEPTION"
	MOCompensationDelayRule                MOCompensationCause = "DELAY_RULE"
)

func ValidMOCompensationCause(cause MOCompensationCause) bool {
	switch cause {
	case MOCompensationCustomerCancelPreEffect,
		MOCompensationCustomerRefundPostEffect,
		MOCompensationProcurementFailure,
		MOCompensationDeliveryException,
		MOCompensationDelayRule:
		return true
	default:
		return false
	}
}

type MOCompensationAction string

const (
	MOCompensationVoid       MOCompensationAction = "VOID"
	MOCompensationRefund     MOCompensationAction = "REFUND"
	MOCompensationTVitRefund MOCompensationAction = "TVIT_REFUND"
)

type MOCompensationState string

const (
	MOCompensationApproved         MOCompensationState = "APPROVED"
	MOCompensationExecutionPending MOCompensationState = "EXECUTION_PENDING"
	MOCompensationOutcomeUnknown   MOCompensationState = "OUTCOME_UNKNOWN"
	MOCompensationSucceeded        MOCompensationState = "SUCCEEDED"
	MOCompensationFailed           MOCompensationState = "FAILED"
)

type MOCompensation struct {
	ID                   string               `json:"id"`
	AllocationID         string               `json:"allocationId"`
	FundingPositionID    string               `json:"fundingPositionId"`
	AgencyOrderID        string               `json:"agencyOrderId"`
	CustomerPaymentID    string               `json:"customerPaymentId"`
	Rail                 string               `json:"rail"`
	ProviderEnvironment  string               `json:"providerEnvironment"`
	Action               MOCompensationAction `json:"action"`
	Cause                MOCompensationCause  `json:"cause"`
	State                MOCompensationState  `json:"state"`
	AmountMinor          int64                `json:"amountMinor"`
	Currency             string               `json:"currency"`
	ExecutionProfileHash string               `json:"executionProfileHash"`
	ProviderResourceID   string               `json:"providerResourceId,omitempty"`
	IdempotencyKey       string               `json:"idempotencyKey"`
	Version              int64                `json:"version"`
	ApprovedAt           time.Time            `json:"approvedAt"`
	CompletedAt          *time.Time           `json:"completedAt,omitempty"`
	CreatedAt            time.Time            `json:"createdAt"`
	UpdatedAt            time.Time            `json:"updatedAt"`
}

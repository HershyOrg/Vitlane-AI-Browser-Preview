package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

var ErrRefundRequestInvalid = errors.New("AGENCY_ORDER_REFUND_REQUEST_INVALID")

// MerchantOrderAllocation is the immutable customer-money boundary for one
// checkout/MerchantOrder. Refund and cancellation amounts are always Gross;
// lines and physical units inside the MerchantOrder never carry money.
type MerchantOrderAllocation struct {
	ID                   string `json:"id,omitempty"`
	AgencyOrderID        string `json:"agencyOrderId"`
	CheckoutOrdinal      int    `json:"checkoutOrdinal"`
	MerchantID           string `json:"merchantId"`
	ShopDomain           string `json:"shopDomain"`
	PassThroughAmount    Money  `json:"passThroughAmount"`
	FeeVariableAmount    Money  `json:"feeVariableAmount"`
	FeeFixedAmount       Money  `json:"feeFixedAmount"`
	FeeTotalAmount       Money  `json:"feeTotalAmount"`
	CustomerGrossAmount  Money  `json:"customerGrossAmount"`
	FeePolicyVersion     string `json:"feePolicyVersion"`
	ExecutionProfileHash string `json:"executionProfileHash"`
	AllocationHash       string `json:"allocationHash"`
}

// MerchantOrderAllocations derives the exact issuance rows from the order
// snapshot. It validates conservation once; later refunds read these immutable
// rows and never recalculate policy or allocate down to lines/units.
func MerchantOrderAllocations(order AgencyOrder) ([]MerchantOrderAllocation, error) {
	if order.ID == "" || order.ExecutionProfileHash == "" ||
		len(order.MerchantCheckouts) == 0 ||
		len(order.AgencyFee.MerchantOrders) != len(order.MerchantCheckouts) {
		return nil, ErrRefundRequestInvalid
	}
	result := make([]MerchantOrderAllocation, 0, len(order.MerchantCheckouts))
	var passThroughTotal, variableTotal, fixedTotal, grossTotal int64
	for index, checkout := range order.MerchantCheckouts {
		fee := order.AgencyFee.MerchantOrders[index]
		if fee.CheckoutOrdinal != index+1 || fee.ShopDomain != checkout.ShopDomain ||
			checkout.MerchantID == "" || checkout.ShopDomain == "" ||
			fee.PassThroughAmount != checkout.AuthoritativeTotal ||
			fee.VariableAmount.Currency != "USD" || fee.FixedAmount.Currency != "USD" ||
			fee.Total.Currency != "USD" || fee.CustomerPayableTotal.Currency != "USD" ||
			fee.Total.AmountMinor != fee.VariableAmount.AmountMinor+fee.FixedAmount.AmountMinor ||
			fee.CustomerPayableTotal.AmountMinor != fee.PassThroughAmount.AmountMinor+fee.Total.AmountMinor ||
			fee.PassThroughAmount.AmountMinor <= 0 || fee.VariableAmount.AmountMinor < 0 ||
			fee.FixedAmount.AmountMinor < 0 {
			return nil, ErrRefundRequestInvalid
		}
		allocation := MerchantOrderAllocation{
			AgencyOrderID: order.ID, CheckoutOrdinal: index + 1,
			MerchantID: checkout.MerchantID, ShopDomain: checkout.ShopDomain,
			PassThroughAmount: fee.PassThroughAmount,
			FeeVariableAmount: fee.VariableAmount, FeeFixedAmount: fee.FixedAmount,
			FeeTotalAmount: fee.Total, CustomerGrossAmount: fee.CustomerPayableTotal,
			FeePolicyVersion:     order.AgencyFee.PolicyVersion,
			ExecutionProfileHash: order.ExecutionProfileHash,
		}
		hash, err := shareddomain.CanonicalJSONHash(struct {
			AgencyOrderID, ExecutionProfileHash, MerchantID, ShopDomain   string
			CheckoutOrdinal                                               int
			PassThroughMinor, FeeVariableMinor, FeeFixedMinor, GrossMinor int64
			Currency, FeePolicyVersion                                    string
		}{
			AgencyOrderID: order.ID, ExecutionProfileHash: order.ExecutionProfileHash,
			MerchantID: checkout.MerchantID, ShopDomain: checkout.ShopDomain,
			CheckoutOrdinal:  index + 1,
			PassThroughMinor: fee.PassThroughAmount.AmountMinor,
			FeeVariableMinor: fee.VariableAmount.AmountMinor,
			FeeFixedMinor:    fee.FixedAmount.AmountMinor,
			GrossMinor:       fee.CustomerPayableTotal.AmountMinor,
			Currency:         "USD", FeePolicyVersion: order.AgencyFee.PolicyVersion,
		})
		if err != nil {
			return nil, ErrRefundRequestInvalid
		}
		allocation.AllocationHash = hash
		result = append(result, allocation)
		passThroughTotal += fee.PassThroughAmount.AmountMinor
		variableTotal += fee.VariableAmount.AmountMinor
		fixedTotal += fee.FixedAmount.AmountMinor
		grossTotal += fee.CustomerPayableTotal.AmountMinor
	}
	if passThroughTotal != order.PassThroughTotal.AmountMinor ||
		variableTotal != order.AgencyFee.VariableAmount.AmountMinor ||
		fixedTotal != order.AgencyFee.FixedAmount.AmountMinor ||
		variableTotal+fixedTotal != order.AgencyFee.Total.AmountMinor ||
		grossTotal != order.CustomerPayableTotal.AmountMinor {
		return nil, ErrRefundRequestInvalid
	}
	return result, nil
}

type RefundRequestState string

const (
	RefundRequestRequested RefundRequestState = "REQUESTED"
	RefundRequestReviewing RefundRequestState = "REVIEWING"
	RefundRequestResolved  RefundRequestState = "RESOLVED"
)

type RefundDecisionOutcome string

const (
	RefundDecisionApproved RefundDecisionOutcome = "APPROVED"
	RefundDecisionRejected RefundDecisionOutcome = "REJECTED"
)

// RefundRequest targets exactly one MerchantOrder and its immutable allocation.
// RequestedGrossAmount is the full allocation including Vitlane fee. The
// operator makes one approve/reject decision for the whole request.
type RefundRequest struct {
	ID                      string                `json:"id"`
	AgencyOrderID           string                `json:"agencyOrderId"`
	MerchantOrderID         string                `json:"merchantOrderId"`
	AllocationID            string                `json:"allocationId"`
	RequestedGrossAmount    Money                 `json:"requestedGrossAmount"`
	UserID                  string                `json:"-"`
	State                   RefundRequestState    `json:"state"`
	ReasonCode              string                `json:"reasonCode"`
	PublicRationale         string                `json:"publicRationale"`
	Decision                RefundDecisionOutcome `json:"decision,omitempty"`
	DecisionPublicRationale string                `json:"decisionPublicRationale,omitempty"`
	InternalNote            string                `json:"-"`
	DecidedBy               string                `json:"-"`
	DecidedAt               *time.Time            `json:"decidedAt,omitempty"`
	ReviewContext           *RefundReviewContext  `json:"reviewContext,omitempty"`
	CreatedAt               time.Time             `json:"createdAt"`
	UpdatedAt               time.Time             `json:"updatedAt"`
}

// RefundReviewContext is only populated for the operator queue. It contains
// the whole MO's order lines and current fulfillment facts without inventing a
// per-unit monetary allocation.
type RefundReviewContext struct {
	OrderNumber          string                 `json:"orderNumber"`
	MerchantOrderID      string                 `json:"merchantOrderId"`
	AllocationID         string                 `json:"allocationId"`
	ShopDomain           string                 `json:"shopDomain"`
	MerchantID           string                 `json:"merchantId"`
	ExternalOrderRef     string                 `json:"externalOrderRef,omitempty"`
	MerchantOrderState   string                 `json:"merchantOrderState"`
	RequestedGrossAmount Money                  `json:"requestedGrossAmount"`
	Lines                []RefundReviewLine     `json:"lines"`
	Units                []RefundReviewUnitFact `json:"units"`
	InternalNote         string                 `json:"internalNote,omitempty"`
}

type RefundReviewLine struct {
	LineID          string   `json:"lineId"`
	ProductURL      string   `json:"productUrl,omitempty"`
	ProductTitle    string   `json:"productTitle"`
	VariantID       string   `json:"variantId,omitempty"`
	VariantTitle    string   `json:"variantTitle,omitempty"`
	SelectedOptions []string `json:"selectedOptions"`
	Quantity        int      `json:"quantity"`
}

type RefundReviewUnitFact struct {
	MerchantOrderUnitID string              `json:"merchantOrderUnitId"`
	LineID              string              `json:"lineId"`
	UnitIndex           int                 `json:"unitIndex"`
	Disposition         string              `json:"disposition"`
	DeliveryFacts       RefundDeliveryFacts `json:"deliveryFacts"`
	ReturnFacts         RefundReturnFacts   `json:"returnFacts"`
}

type RefundDeliveryFacts struct {
	Recorded              bool       `json:"recorded"`
	ExpectedFulfillment   string     `json:"expectedFulfillment,omitempty"`
	ShipmentState         string     `json:"shipmentState,omitempty"`
	Carrier               string     `json:"carrier,omitempty"`
	TrackingRef           string     `json:"trackingRef,omitempty"`
	LatestEventStatus     string     `json:"latestEventStatus,omitempty"`
	LatestEventNote       string     `json:"latestEventNote,omitempty"`
	LatestEventOccurredAt *time.Time `json:"latestEventOccurredAt,omitempty"`
	ResolutionCause       string     `json:"resolutionCause,omitempty"`
	ResolutionDecision    string     `json:"resolutionDecision,omitempty"`
	ResolutionNote        string     `json:"resolutionNote,omitempty"`
	ResolutionRecordedAt  *time.Time `json:"resolutionRecordedAt,omitempty"`
}

type RefundReturnFacts struct {
	Recorded            bool       `json:"recorded"`
	State               string     `json:"state,omitempty"`
	MerchantDisposition string     `json:"merchantDisposition,omitempty"`
	Note                string     `json:"note,omitempty"`
	UpdatedAt           *time.Time `json:"updatedAt,omitempty"`
}

// Post-effect requests accept only the typed legitimate-fault vocabulary.
// Pre-effect cancellation is a separate command and intentionally has no
// reason field.
func ValidateRefundReasonCode(code string) error {
	if !policy.ValidRefundReasonCode(code) {
		return ErrRefundRequestInvalid
	}
	return nil
}

func ValidateRefundRequestReason(reason string) error {
	length := len([]rune(strings.TrimSpace(reason)))
	if length < 1 || length > 500 {
		return ErrRefundRequestInvalid
	}
	return nil
}

func ValidateRefundDecisionPublicRationale(rationale string) error {
	length := len([]rune(strings.TrimSpace(rationale)))
	if length < 1 || length > 2000 {
		return ErrRefundRequestInvalid
	}
	return nil
}

func ValidateRefundInternalNote(note string) error {
	if len([]rune(strings.TrimSpace(note))) > 4000 {
		return ErrRefundRequestInvalid
	}
	return nil
}

package app

import "time"

type PayPalResourceAdoptionKind string

const (
	PayPalReauthorizationAdoption PayPalResourceAdoptionKind = "PAYPAL_REAUTHORIZATION"
	PayPalMORefundAdoption        PayPalResourceAdoptionKind = "PAYPAL_MO_REFUND"
)

const (
	ActionAdoptPayPalReauthorization = "ADOPT_PAYPAL_REAUTHORIZATION"
	ActionAdoptPayPalMORefund        = "ADOPT_PAYPAL_MO_REFUND"
)

// PayPalResourceAdoptionItem is a PII-safe operator projection of an expired
// SENT/UNKNOWN operation with no provider resource identity. It deliberately
// excludes the original request payload and provider credentials.
type PayPalResourceAdoptionItem struct {
	ReconciliationKind  PayPalResourceAdoptionKind `json:"reconciliationKind"`
	OperationID         string                     `json:"operationId"`
	AgencyOrderID       string                     `json:"agencyOrderId"`
	MerchantOrderID     string                     `json:"merchantOrderId"`
	CompensationID      string                     `json:"compensationId,omitempty"`
	ProviderEnvironment string                     `json:"providerEnvironment"`
	AmountMinor         int64                      `json:"amountMinor"`
	Currency            string                     `json:"currency"`
	OperationState      string                     `json:"operationState"`
	OwnerState          string                     `json:"ownerState"`
	ReasonCode          string                     `json:"reasonCode,omitempty"`
	FirstSentAt         time.Time                  `json:"firstSentAt"`
	IdempotencyDeadline time.Time                  `json:"idempotencyDeadline"`
	UpdatedAt           time.Time                  `json:"updatedAt"`
}

func (item PayPalResourceAdoptionItem) Actions() []string {
	switch item.ReconciliationKind {
	case PayPalReauthorizationAdoption:
		return []string{ActionAdoptPayPalReauthorization}
	case PayPalMORefundAdoption:
		return []string{ActionAdoptPayPalMORefund}
	default:
		return []string{}
	}
}

package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrPayPalRefundAdoptionInvalid  = errors.New("PAYPAL_REFUND_ADOPTION_INVALID")
	ErrPayPalRefundAdoptionMissing  = errors.New("PAYPAL_REFUND_ADOPTION_NOT_AVAILABLE")
	ErrPayPalRefundAdoptionMismatch = errors.New("PAYPAL_REFUND_ADOPTION_MISMATCH")
)

type PayPalRefundAdoptionEvidenceSource string

const (
	PayPalRefundAdoptionEvidenceDashboard PayPalRefundAdoptionEvidenceSource = "PAYPAL_DASHBOARD"
	PayPalRefundAdoptionEvidenceAPI       PayPalRefundAdoptionEvidenceSource = "PAYPAL_API"
	PayPalRefundAdoptionEvidenceWebhook   PayPalRefundAdoptionEvidenceSource = "PAYPAL_WEBHOOK"
	PayPalRefundAdoptionEvidenceOther     PayPalRefundAdoptionEvidenceSource = "OTHER"
)

// PayPalRefundAdoption is the immutable operator evidence that binds a refund
// identity discovered after the original same-key sender window expired. The
// provider resource remains authoritative only after a fresh exact GET.
type PayPalRefundAdoption struct {
	ID                           string                             `json:"id"`
	CompensationID               string                             `json:"compensationId"`
	OperationID                  string                             `json:"operationId"`
	ProviderEnvironment          string                             `json:"providerEnvironment"`
	ProviderRefundID             string                             `json:"providerRefundId"`
	ProviderStatus               string                             `json:"providerStatus"`
	OutcomeState                 MOCompensationState                `json:"outcomeState"`
	AmountMinor                  int64                              `json:"amountMinor"`
	Currency                     string                             `json:"currency"`
	ParentCaptureID              string                             `json:"parentCaptureId"`
	InvoiceID                    string                             `json:"invoiceId"`
	OperationFirstSentAt         time.Time                          `json:"operationFirstSentAt"`
	OperationIdempotencyDeadline time.Time                          `json:"operationIdempotencyDeadline"`
	OperatorUserID               string                             `json:"operatorUserId"`
	PublicRationale              string                             `json:"publicRationale"`
	EvidenceSource               PayPalRefundAdoptionEvidenceSource `json:"evidenceSource"`
	EvidenceHash                 string                             `json:"evidenceHash"`
	ObservedAt                   time.Time                          `json:"observedAt"`
	RequestHash                  string                             `json:"-"`
	CreatedAt                    time.Time                          `json:"createdAt"`
}

var paypalRefundAdoptionReferencePattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$`,
)

func (a PayPalRefundAdoption) Validate(now time.Time) error {
	status := strings.ToUpper(strings.TrimSpace(a.ProviderStatus))
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.CompensationID) == "" ||
		strings.TrimSpace(a.OperationID) == "" || strings.TrimSpace(a.OperatorUserID) == "" ||
		(a.ProviderEnvironment != "SANDBOX" && a.ProviderEnvironment != "LIVE") ||
		!paypalRefundAdoptionReferencePattern.MatchString(strings.TrimSpace(a.ProviderRefundID)) ||
		!paypalRefundAdoptionReferencePattern.MatchString(strings.TrimSpace(a.ParentCaptureID)) ||
		!paypalRefundAdoptionReferencePattern.MatchString(strings.TrimSpace(a.InvoiceID)) ||
		a.AmountMinor <= 0 || a.Currency != "USD" ||
		!validPayPalRefundAdoptionStatus(status, a.OutcomeState) ||
		!evidenceHashPattern.MatchString(strings.ToLower(strings.TrimSpace(a.RequestHash))) ||
		a.OperationFirstSentAt.IsZero() || a.OperationIdempotencyDeadline.IsZero() ||
		a.OperationFirstSentAt.After(a.OperationIdempotencyDeadline) ||
		a.CreatedAt.Before(a.OperationIdempotencyDeadline) ||
		a.CreatedAt.IsZero() {
		return ErrPayPalRefundAdoptionInvalid
	}
	return ValidatePayPalRefundAdoptionInput(
		a.ProviderRefundID,
		a.OperatorUserID,
		a.PublicRationale,
		a.EvidenceSource,
		a.EvidenceHash,
		a.ObservedAt,
		now,
	)
}

// ValidatePayPalRefundAdoptionInput rejects malformed operator evidence before
// Payment performs the provider GET. The repository still owns eligibility
// checks because those depend on locked persisted state.
func ValidatePayPalRefundAdoptionInput(
	providerRefundID string,
	operatorUserID string,
	publicRationale string,
	evidenceSource PayPalRefundAdoptionEvidenceSource,
	evidenceHash string,
	observedAt time.Time,
	now time.Time,
) error {
	if !paypalRefundAdoptionReferencePattern.MatchString(strings.TrimSpace(providerRefundID)) ||
		strings.TrimSpace(operatorUserID) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(publicRationale)) < 1 ||
		utf8.RuneCountInString(strings.TrimSpace(publicRationale)) > 2000 ||
		!validPayPalRefundAdoptionEvidenceSource(evidenceSource) ||
		!evidenceHashPattern.MatchString(strings.ToLower(strings.TrimSpace(evidenceHash))) ||
		observedAt.IsZero() || observedAt.After(now.Add(5*time.Minute)) {
		return ErrPayPalRefundAdoptionInvalid
	}
	return nil
}

func validPayPalRefundAdoptionStatus(
	status string,
	outcome MOCompensationState,
) bool {
	switch status {
	case "COMPLETED":
		return outcome == MOCompensationSucceeded
	case "FAILED", "CANCELLED":
		return outcome == MOCompensationFailed
	case "PENDING":
		return outcome == MOCompensationOutcomeUnknown
	default:
		return false
	}
}

func validPayPalRefundAdoptionEvidenceSource(
	value PayPalRefundAdoptionEvidenceSource,
) bool {
	switch value {
	case PayPalRefundAdoptionEvidenceDashboard, PayPalRefundAdoptionEvidenceAPI,
		PayPalRefundAdoptionEvidenceWebhook, PayPalRefundAdoptionEvidenceOther:
		return true
	default:
		return false
	}
}

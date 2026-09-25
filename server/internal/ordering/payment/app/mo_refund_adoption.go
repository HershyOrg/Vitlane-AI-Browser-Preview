package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

// AdoptPayPalMORefundInput is an operator-authored discovery record. It does
// not authorize a provider write; Payment only performs a fresh GET against the
// provider environment already stored on the compensation.
type AdoptPayPalMORefundInput struct {
	CompensationID   string
	ProviderRefundID string
	OperatorUserID   string
	PublicRationale  string
	EvidenceSource   domain.PayPalRefundAdoptionEvidenceSource
	EvidenceHash     string
	ObservedAt       time.Time
}

type PayPalMORefundAdoptionPlan struct {
	Execution            MOCompensationExecution
	OperationFirstSentAt time.Time
	IdempotencyDeadline  time.Time
	ExistingAdoption     *domain.PayPalRefundAdoption
}

type AdoptPayPalMORefundResult struct {
	Compensation domain.MOCompensation       `json:"compensation"`
	Adoption     domain.PayPalRefundAdoption `json:"adoption"`
	Replay       bool                        `json:"replay"`
}

type paypalMORefundAdoptionRepository interface {
	PreparePayPalMORefundAdoption(
		context.Context, string, string, time.Time,
	) (PayPalMORefundAdoptionPlan, error)
	RecordPayPalMORefundAdoption(
		context.Context, PayPalMORefundAdoptionPlan, domain.PayPalRefundAdoption,
		domain.MOCompensationState, domain.OperationState, string, time.Time,
	) (domain.MOCompensation, domain.PayPalRefundAdoption, bool, error)
}

// AdoptPayPalMORefund closes the no-resource-id deadline hole without issuing
// another PayPal POST or allocating a new request key. The supplied refund ID
// is fetched from the compensation's stored environment and must match the
// exact Capture, immutable gross and original operation invoice.
func (s *Service) AdoptPayPalMORefund(
	ctx context.Context,
	input AdoptPayPalMORefundInput,
) (AdoptPayPalMORefundResult, error) {
	now := s.clock.Now()
	input.CompensationID = strings.TrimSpace(input.CompensationID)
	input.ProviderRefundID = strings.TrimSpace(input.ProviderRefundID)
	input.OperatorUserID = strings.TrimSpace(input.OperatorUserID)
	input.PublicRationale = strings.TrimSpace(input.PublicRationale)
	input.EvidenceHash = strings.ToLower(strings.TrimSpace(input.EvidenceHash))
	input.ObservedAt = input.ObservedAt.UTC()
	if input.CompensationID == "" || utf8.RuneCountInString(input.CompensationID) > 255 {
		return AdoptPayPalMORefundResult{}, domain.ErrPayPalRefundAdoptionInvalid
	}
	if err := domain.ValidatePayPalRefundAdoptionInput(
		input.ProviderRefundID,
		input.OperatorUserID,
		input.PublicRationale,
		input.EvidenceSource,
		input.EvidenceHash,
		input.ObservedAt,
		now,
	); err != nil {
		return AdoptPayPalMORefundResult{}, err
	}
	requestHash, err := payPalRefundAdoptionRequestHash(input)
	if err != nil {
		return AdoptPayPalMORefundResult{}, domain.ErrPayPalRefundAdoptionInvalid
	}
	repository, ok := s.repository.(paypalMORefundAdoptionRepository)
	if !ok {
		return AdoptPayPalMORefundResult{}, domain.ErrPayPalRefundAdoptionMissing
	}
	plan, err := repository.PreparePayPalMORefundAdoption(
		ctx, input.CompensationID, input.ProviderRefundID, now,
	)
	if err != nil {
		return AdoptPayPalMORefundResult{}, err
	}
	registration, err := s.providerFor(plan.Execution.Compensation.ProviderEnvironment)
	if err != nil {
		return AdoptPayPalMORefundResult{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	refund, err := registration.Client.GetRefund(queryCtx, input.ProviderRefundID)
	cancel()
	if err != nil {
		return AdoptPayPalMORefundResult{}, err
	}
	status := strings.ToUpper(strings.TrimSpace(refund.Status))
	compensationState, operationState, reason, validStatus :=
		payPalRefundAdoptionOutcome(status)
	if !validStatus || refund.ID != input.ProviderRefundID ||
		refund.AmountMinor != plan.Execution.Compensation.AmountMinor ||
		refund.Currency != "USD" ||
		refund.ParentCaptureID != plan.Execution.ProviderCaptureID ||
		refund.InvoiceID != plan.Execution.OperationIdempotencyKey {
		return AdoptPayPalMORefundResult{}, domain.ErrPayPalRefundAdoptionMismatch
	}
	adoption := domain.PayPalRefundAdoption{
		ID: s.ids.NewID(), CompensationID: plan.Execution.Compensation.ID,
		OperationID:         plan.Execution.OperationID,
		ProviderEnvironment: plan.Execution.Compensation.ProviderEnvironment,
		ProviderRefundID:    refund.ID, ProviderStatus: status,
		OutcomeState: compensationState, AmountMinor: refund.AmountMinor,
		Currency: refund.Currency, ParentCaptureID: refund.ParentCaptureID,
		InvoiceID:                    refund.InvoiceID,
		OperationFirstSentAt:         plan.OperationFirstSentAt,
		OperationIdempotencyDeadline: plan.IdempotencyDeadline,
		OperatorUserID:               input.OperatorUserID,
		PublicRationale:              input.PublicRationale, EvidenceSource: input.EvidenceSource,
		EvidenceHash: input.EvidenceHash, ObservedAt: input.ObservedAt,
		RequestHash: requestHash, CreatedAt: now,
	}
	if err := adoption.Validate(now); err != nil {
		return AdoptPayPalMORefundResult{}, err
	}
	compensation, recorded, replay, err := repository.RecordPayPalMORefundAdoption(
		ctx, plan, adoption, compensationState, operationState, reason, now,
	)
	result := AdoptPayPalMORefundResult{
		Compensation: compensation, Adoption: recorded, Replay: replay,
	}
	if err != nil {
		return result, err
	}
	switch compensationState {
	case domain.MOCompensationOutcomeUnknown:
		return result, domain.ErrCompensationOutcomeUnknown
	case domain.MOCompensationFailed:
		return result, domain.ErrCompensationAttemptFailed
	default:
		return result, nil
	}
}

func payPalRefundAdoptionOutcome(
	status string,
) (domain.MOCompensationState, domain.OperationState, string, bool) {
	switch status {
	case "COMPLETED":
		return domain.MOCompensationSucceeded, domain.OperationSucceeded, "", true
	case "FAILED", "CANCELLED":
		return domain.MOCompensationFailed, domain.OperationFailed, "REFUND_" + status, true
	case "PENDING":
		return domain.MOCompensationOutcomeUnknown, domain.OperationUnknown,
			"REFUND_NOT_COMPLETED", true
	default:
		return "", "", "", false
	}
}

func payPalRefundAdoptionRequestHash(input AdoptPayPalMORefundInput) (string, error) {
	canonical := struct {
		CompensationID   string `json:"compensationId"`
		ProviderRefundID string `json:"providerRefundId"`
		OperatorUserID   string `json:"operatorUserId"`
		PublicRationale  string `json:"publicRationale"`
		EvidenceSource   string `json:"evidenceSource"`
		EvidenceHash     string `json:"evidenceHash"`
		ObservedAt       string `json:"observedAt"`
	}{
		CompensationID: input.CompensationID, ProviderRefundID: input.ProviderRefundID,
		OperatorUserID: input.OperatorUserID, PublicRationale: input.PublicRationale,
		EvidenceSource: string(input.EvidenceSource), EvidenceHash: input.EvidenceHash,
		ObservedAt: input.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("paypal-mo-refund-adoption-v1\x00"), encoded...))
	return "0x" + hex.EncodeToString(digest[:]), nil
}

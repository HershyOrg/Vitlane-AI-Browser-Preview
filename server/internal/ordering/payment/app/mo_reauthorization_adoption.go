package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

type PayPalReauthorizationAdoptionEvidenceSource string

const (
	PayPalReauthorizationEvidenceDashboard PayPalReauthorizationAdoptionEvidenceSource = "PAYPAL_DASHBOARD"
	PayPalReauthorizationEvidenceSupport   PayPalReauthorizationAdoptionEvidenceSource = "PAYPAL_SUPPORT"
	PayPalReauthorizationEvidenceOther     PayPalReauthorizationAdoptionEvidenceSource = "OTHER"
)

var reauthorizationAdoptionEvidenceHashPattern = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

// AdoptMOReauthorizationInput contains only an operator-supplied provider
// identity and immutable evidence. Environment, order/payee, amount and the
// original request operation are always loaded from Payment-owned state.
type AdoptMOReauthorizationInput struct {
	ProviderAuthorizationID string                                      `json:"providerAuthorizationId"`
	EvidenceSource          PayPalReauthorizationAdoptionEvidenceSource `json:"evidenceSource"`
	EvidenceHash            string                                      `json:"evidenceHash"`
	InternalNote            string                                      `json:"internalNote,omitempty"`
	ObservedAt              time.Time                                   `json:"observedAt"`
}

type MOReauthorizationAdoptionPlan struct {
	MOReauthorizationPlan
	MerchantOrderID     string
	FirstSentAt         time.Time
	IdempotencyDeadline time.Time
}

// PayPalReauthorizationAdoption is append-only operator evidence for binding
// an otherwise undiscoverable reauthorization resource to its original SENT
// operation. It does not authorize another provider POST or request key.
type PayPalReauthorizationAdoption struct {
	ID                              string                                      `json:"id"`
	MerchantOrderID                 string                                      `json:"merchantOrderId"`
	TargetFundingPositionID         string                                      `json:"targetFundingPositionId"`
	PayPalAuthorizationID           string                                      `json:"paypalAuthorizationId"`
	OperationID                     string                                      `json:"operationId"`
	ProviderEnvironment             string                                      `json:"providerEnvironment"`
	PreviousProviderAuthorizationID string                                      `json:"previousProviderAuthorizationId"`
	ProviderAuthorizationID         string                                      `json:"providerAuthorizationId"`
	ProviderStatus                  string                                      `json:"providerStatus"`
	OperationState                  domain.OperationState                       `json:"operationState"`
	AmountMinor                     int64                                       `json:"amountMinor"`
	Currency                        string                                      `json:"currency"`
	PayPalOrderID                   string                                      `json:"paypalOrderId"`
	PayeeMerchantID                 string                                      `json:"payeeMerchantId"`
	ProviderCreatedAt               time.Time                                   `json:"providerCreatedAt"`
	ActorUserID                     string                                      `json:"actorUserId"`
	EvidenceSource                  PayPalReauthorizationAdoptionEvidenceSource `json:"evidenceSource"`
	EvidenceHash                    string                                      `json:"evidenceHash"`
	InternalNote                    string                                      `json:"internalNote,omitempty"`
	ObservedAt                      time.Time                                   `json:"observedAt"`
	OperationFirstSentAt            time.Time                                   `json:"operationFirstSentAt"`
	OperationIdempotencyDeadline    time.Time                                   `json:"operationIdempotencyDeadline"`
	OriginalAuthorizedAt            time.Time                                   `json:"originalAuthorizedAt"`
	IdempotencyKeyHash              string                                      `json:"-"`
	RequestHash                     string                                      `json:"-"`
	CreatedAt                       time.Time                                   `json:"createdAt"`
}

type AdoptMOReauthorizationResult struct {
	Adoption PayPalReauthorizationAdoption `json:"adoption"`
	Replay   bool                          `json:"replay"`
}

type moReauthorizationAdoptionRepository interface {
	PrepareMOReauthorizationAdoption(
		context.Context, string, PayPalReauthorizationAdoption,
	) (MOReauthorizationAdoptionPlan, *PayPalReauthorizationAdoption, error)
	RecordMOReauthorizationAdoption(
		context.Context, MOReauthorizationAdoptionPlan,
		PayPalReauthorizationAdoption, time.Time,
	) (PayPalReauthorizationAdoption, bool, error)
}

// AdoptMOReauthorization is a GET-only manual reconciliation path for a
// reauthorization whose provider resource ID never reached Vitlane. It can
// adopt only the exact resource created by the already-SENT operation.
func (s *Service) AdoptMOReauthorization(
	ctx context.Context,
	merchantOrderID, actorUserID, idempotencyKey string,
	input AdoptMOReauthorizationInput,
) (AdoptMOReauthorizationResult, error) {
	repository, ok := s.repository.(moReauthorizationAdoptionRepository)
	merchantOrderID = strings.TrimSpace(merchantOrderID)
	actorUserID = strings.TrimSpace(actorUserID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	input.ProviderAuthorizationID = strings.TrimSpace(input.ProviderAuthorizationID)
	input.EvidenceSource = PayPalReauthorizationAdoptionEvidenceSource(
		strings.ToUpper(strings.TrimSpace(string(input.EvidenceSource))),
	)
	input.EvidenceHash = strings.ToLower(strings.TrimSpace(input.EvidenceHash))
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	now := s.clock.Now()
	if !ok || merchantOrderID == "" || actorUserID == "" ||
		len(idempotencyKey) < 8 || len(idempotencyKey) > 200 ||
		input.ProviderAuthorizationID == "" || len(input.ProviderAuthorizationID) > 255 ||
		!validPayPalReauthorizationEvidenceSource(input.EvidenceSource) ||
		!reauthorizationAdoptionEvidenceHashPattern.MatchString(input.EvidenceHash) ||
		utf8.RuneCountInString(input.InternalNote) > 4000 ||
		input.ObservedAt.IsZero() || input.ObservedAt.After(now.Add(5*time.Minute)) {
		return AdoptMOReauthorizationResult{}, domain.ErrInvalid
	}
	input.ObservedAt = input.ObservedAt.UTC()
	requestHash, err := reauthorizationAdoptionRequestHash(
		merchantOrderID, actorUserID, input,
	)
	if err != nil {
		return AdoptMOReauthorizationResult{}, domain.ErrInvalid
	}
	adoption := PayPalReauthorizationAdoption{
		ID: s.ids.NewID(), MerchantOrderID: merchantOrderID,
		ProviderAuthorizationID: input.ProviderAuthorizationID,
		ActorUserID:             actorUserID, EvidenceSource: input.EvidenceSource,
		EvidenceHash: input.EvidenceHash, InternalNote: input.InternalNote,
		ObservedAt: input.ObservedAt,
		IdempotencyKeyHash: hashReauthorizationAdoption(
			"paypal-reauthorization-adoption-key", []byte(idempotencyKey),
		),
		RequestHash: requestHash, CreatedAt: now,
	}
	plan, existing, err := repository.PrepareMOReauthorizationAdoption(
		ctx, merchantOrderID, adoption,
	)
	if err != nil {
		return AdoptMOReauthorizationResult{}, err
	}
	if existing != nil {
		return classifyMOReauthorizationAdoptionResult(*existing, true)
	}
	if adoption.ProviderAuthorizationID == plan.PreviousProviderAuthorizationID {
		return AdoptMOReauthorizationResult{}, domain.ErrInstructionMismatch
	}
	registration, err := s.providerFor(plan.ProviderEnvironment)
	if err != nil {
		return AdoptMOReauthorizationResult{}, err
	}
	provider, ok := registration.Client.(authorizationProviderClient)
	if !ok {
		return AdoptMOReauthorizationResult{}, domain.ErrAuthorizationBlocked
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	providerAuthorization, err := provider.GetAuthorization(
		queryCtx, adoption.ProviderAuthorizationID,
	)
	cancel()
	if err != nil {
		return AdoptMOReauthorizationResult{}, err
	}
	providerCreatedAt, err := time.Parse(
		time.RFC3339Nano, strings.TrimSpace(providerAuthorization.CreateTime),
	)
	if err != nil {
		return AdoptMOReauthorizationResult{}, domain.ErrInstructionMismatch
	}
	providerCreatedAt = providerCreatedAt.UTC()
	providerStatus := strings.ToUpper(strings.TrimSpace(providerAuthorization.Status))
	if providerAuthorization.ID != adoption.ProviderAuthorizationID ||
		providerAuthorization.ID == plan.PreviousProviderAuthorizationID ||
		providerStatus == "" ||
		providerAuthorization.AmountMinor != plan.RemainingCapturableMinor ||
		providerAuthorization.Currency != plan.Currency ||
		providerAuthorization.ParentOrderID != plan.PayPalOrderID ||
		providerAuthorization.PayeeMerchant != plan.PayeeMerchantID ||
		providerCreatedAt.Before(plan.FirstSentAt) ||
		!providerCreatedAt.Before(
			plan.OriginalAuthorizedAt.Add(29*24*time.Hour),
		) || adoption.ObservedAt.Before(providerCreatedAt) {
		return AdoptMOReauthorizationResult{}, domain.ErrInstructionMismatch
	}

	operationState := domain.OperationUnknown
	switch providerStatus {
	case "CREATED":
		operationState = domain.OperationSucceeded
	case "DENIED", "VOIDED", "EXPIRED":
		operationState = domain.OperationFailed
	}
	adoption.TargetFundingPositionID = plan.TargetPositionID
	adoption.PayPalAuthorizationID = plan.AuthorizationID
	adoption.OperationID = plan.OperationID
	adoption.ProviderEnvironment = plan.ProviderEnvironment
	adoption.PreviousProviderAuthorizationID = plan.PreviousProviderAuthorizationID
	adoption.ProviderStatus = providerStatus
	adoption.OperationState = operationState
	adoption.AmountMinor = providerAuthorization.AmountMinor
	adoption.Currency = providerAuthorization.Currency
	adoption.PayPalOrderID = providerAuthorization.ParentOrderID
	adoption.PayeeMerchantID = providerAuthorization.PayeeMerchant
	adoption.ProviderCreatedAt = providerCreatedAt
	adoption.OperationFirstSentAt = plan.FirstSentAt
	adoption.OperationIdempotencyDeadline = plan.IdempotencyDeadline
	adoption.OriginalAuthorizedAt = plan.OriginalAuthorizedAt
	recorded, replay, err := repository.RecordMOReauthorizationAdoption(
		ctx, plan, adoption, s.clock.Now(),
	)
	if err != nil {
		return AdoptMOReauthorizationResult{}, err
	}
	return classifyMOReauthorizationAdoptionResult(recorded, replay)
}

func classifyMOReauthorizationAdoptionResult(
	adoption PayPalReauthorizationAdoption,
	replay bool,
) (AdoptMOReauthorizationResult, error) {
	result := AdoptMOReauthorizationResult{Adoption: adoption, Replay: replay}
	switch adoption.OperationState {
	case domain.OperationFailed:
		return result, domain.ErrFundingNotAvailable
	case domain.OperationUnknown:
		return result, domain.ErrFundingOutcomeUnknown
	default:
		return result, nil
	}
}

func validPayPalReauthorizationEvidenceSource(
	value PayPalReauthorizationAdoptionEvidenceSource,
) bool {
	switch value {
	case PayPalReauthorizationEvidenceDashboard,
		PayPalReauthorizationEvidenceSupport,
		PayPalReauthorizationEvidenceOther:
		return true
	default:
		return false
	}
}

func reauthorizationAdoptionRequestHash(
	merchantOrderID, actorUserID string,
	input AdoptMOReauthorizationInput,
) (string, error) {
	canonical := struct {
		MerchantOrderID         string                                      `json:"merchantOrderId"`
		ProviderAuthorizationID string                                      `json:"providerAuthorizationId"`
		ActorUserID             string                                      `json:"actorUserId"`
		EvidenceSource          PayPalReauthorizationAdoptionEvidenceSource `json:"evidenceSource"`
		EvidenceHash            string                                      `json:"evidenceHash"`
		InternalNote            string                                      `json:"internalNote,omitempty"`
		ObservedAt              string                                      `json:"observedAt"`
	}{
		MerchantOrderID:         merchantOrderID,
		ProviderAuthorizationID: input.ProviderAuthorizationID,
		ActorUserID:             actorUserID, EvidenceSource: input.EvidenceSource,
		EvidenceHash: input.EvidenceHash, InternalNote: input.InternalNote,
		ObservedAt: input.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return hashReauthorizationAdoption(
		"paypal-reauthorization-adoption-request", encoded,
	), nil
}

func hashReauthorizationAdoption(namespace string, value []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(namespace))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(value)
	return "0x" + hex.EncodeToString(hash.Sum(nil))
}

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

type DisputeQueueFilter struct {
	Environment string
	State       string
	Limit       int
}

func (f DisputeQueueFilter) Normalize() (DisputeQueueFilter, error) {
	var err error
	f.Environment, err = normalizePayPalDisputeEnvironment(f.Environment)
	if err != nil {
		return DisputeQueueFilter{}, err
	}
	f.State = strings.ToUpper(strings.TrimSpace(f.State))
	if f.State == "" {
		f.State = string(domain.DisputeOpen)
	}
	if f.State != string(domain.DisputeOpen) &&
		f.State != string(domain.DisputeResolved) && f.State != "ALL" {
		return DisputeQueueFilter{}, domain.ErrDisputeInvalid
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 200 {
		return DisputeQueueFilter{}, domain.ErrDisputeInvalid
	}
	return f, nil
}

type PayPalDisputeView struct {
	Case    domain.PayPalDisputeCase           `json:"case"`
	Actions []domain.PayPalDisputeManualAction `json:"actions"`
}

func (s *Service) ListPayPalDisputeQueue(
	ctx context.Context,
	filter DisputeQueueFilter,
) ([]domain.PayPalDisputeCase, error) {
	filter, err := filter.Normalize()
	if err != nil {
		return nil, err
	}
	return s.repository.ListPayPalDisputes(ctx, filter)
}

func (s *Service) GetPayPalDisputeView(
	ctx context.Context,
	environment, caseID string,
) (PayPalDisputeView, error) {
	environment, err := normalizePayPalDisputeEnvironment(environment)
	if err != nil {
		return PayPalDisputeView{}, err
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return PayPalDisputeView{}, domain.ErrDisputeInvalid
	}
	item, err := s.repository.GetPayPalDispute(ctx, environment, caseID)
	if err != nil {
		return PayPalDisputeView{}, err
	}
	actions, err := s.repository.ListPayPalDisputeActions(ctx, environment, caseID)
	if err != nil {
		return PayPalDisputeView{}, err
	}
	return PayPalDisputeView{Case: item, Actions: actions}, nil
}

type RecordPayPalDisputeActionInput struct {
	ExpectedVersion        int64                        `json:"expectedVersion"`
	ActionKind             domain.DisputeActionKind     `json:"actionKind"`
	ExternalReference      string                       `json:"externalReference"`
	PublicRationale        string                       `json:"publicRationale"`
	InternalNote           string                       `json:"internalNote"`
	ObservedProviderStatus domain.DisputeProviderStatus `json:"observedProviderStatus"`
	ObservedOutcome        domain.DisputeOutcome        `json:"observedOutcome"`
	EvidenceSource         domain.DisputeEvidenceSource `json:"evidenceSource"`
	EvidenceHash           string                       `json:"evidenceHash"`
	ObservedAt             time.Time                    `json:"observedAt"`
}

type RecordPayPalDisputeActionResult struct {
	Case   domain.PayPalDisputeCase         `json:"case"`
	Action domain.PayPalDisputeManualAction `json:"action"`
	Replay bool                             `json:"replay"`
}

func (s *Service) RecordPayPalDisputeAction(
	ctx context.Context,
	environment, caseID, actorUserID, idempotencyKey string,
	input RecordPayPalDisputeActionInput,
) (RecordPayPalDisputeActionResult, error) {
	environment, err := normalizePayPalDisputeEnvironment(environment)
	if err != nil {
		return RecordPayPalDisputeActionResult{}, err
	}
	caseID = strings.TrimSpace(caseID)
	actorUserID = strings.TrimSpace(actorUserID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	publicRationale := strings.TrimSpace(input.PublicRationale)
	if caseID == "" || actorUserID == "" || idempotencyKey == "" ||
		len(idempotencyKey) > 200 || input.ExpectedVersion <= 0 ||
		utf8.RuneCountInString(publicRationale) > 2000 ||
		utf8.RuneCountInString(input.InternalNote) > 4000 {
		return RecordPayPalDisputeActionResult{}, domain.ErrDisputeInvalid
	}
	evidenceHash := strings.ToLower(strings.TrimSpace(input.EvidenceHash))
	canonical := struct {
		Environment            string                       `json:"environment"`
		CaseID                 string                       `json:"caseId"`
		ExpectedVersion        int64                        `json:"expectedVersion"`
		ActionKind             domain.DisputeActionKind     `json:"actionKind"`
		ExternalReference      string                       `json:"externalReference"`
		PublicRationale        string                       `json:"publicRationale"`
		InternalNote           string                       `json:"internalNote"`
		ObservedProviderStatus domain.DisputeProviderStatus `json:"observedProviderStatus"`
		ObservedOutcome        domain.DisputeOutcome        `json:"observedOutcome"`
		EvidenceSource         domain.DisputeEvidenceSource `json:"evidenceSource"`
		EvidenceHash           string                       `json:"evidenceHash"`
		ObservedAt             string                       `json:"observedAt"`
	}{
		Environment: environment, CaseID: caseID, ExpectedVersion: input.ExpectedVersion,
		ActionKind:        input.ActionKind,
		ExternalReference: strings.TrimSpace(input.ExternalReference),
		PublicRationale:   publicRationale, InternalNote: input.InternalNote,
		ObservedProviderStatus: input.ObservedProviderStatus,
		ObservedOutcome:        input.ObservedOutcome, EvidenceSource: input.EvidenceSource,
		EvidenceHash: evidenceHash, ObservedAt: input.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return RecordPayPalDisputeActionResult{}, domain.ErrDisputeInvalid
	}
	action := domain.PayPalDisputeManualAction{
		ID: s.ids.NewID(), DisputeCaseID: caseID,
		ActionKind:        input.ActionKind,
		ExternalReference: strings.TrimSpace(input.ExternalReference),
		PublicRationale:   publicRationale, InternalNote: input.InternalNote,
		ActorUserID:            actorUserID,
		ObservedProviderStatus: input.ObservedProviderStatus,
		ObservedOutcome:        input.ObservedOutcome,
		EvidenceSource:         input.EvidenceSource, EvidenceHash: evidenceHash,
		ObservedAt:         input.ObservedAt.UTC(),
		IdempotencyKeyHash: hashDisputeEvidence("paypal-dispute-action-key", []byte(idempotencyKey)),
		RequestHash:        hashDisputeEvidence("paypal-dispute-action-request", encoded),
		CreatedAt:          s.clock.Now(),
	}
	if err := action.Validate(s.clock.Now()); err != nil {
		return RecordPayPalDisputeActionResult{}, err
	}
	item, recorded, replay, err := s.repository.RecordPayPalDisputeAction(
		ctx, environment, action, input.ExpectedVersion,
	)
	if err != nil {
		return RecordPayPalDisputeActionResult{}, err
	}
	return RecordPayPalDisputeActionResult{Case: item, Action: recorded, Replay: replay}, nil
}

func normalizePayPalDisputeEnvironment(value string) (string, error) {
	environment := strings.ToUpper(strings.TrimSpace(value))
	if environment != "SANDBOX" && environment != "LIVE" {
		return "", domain.ErrDisputeInvalid
	}
	return environment, nil
}

func hashDisputeEvidence(namespace string, value []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(namespace))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(value)
	return "0x" + hex.EncodeToString(hash.Sum(nil))
}

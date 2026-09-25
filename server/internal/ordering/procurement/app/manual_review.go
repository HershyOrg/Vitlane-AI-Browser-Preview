package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

const customerRequestTTL = 7 * 24 * time.Hour

// ManualReviewRepository is deliberately separate from the original queue
// port.  Development fakes remain small, while the production repository
// fails closed when the live-switch-ready manual-review surface is absent.
type ManualReviewRepository interface {
	RecordManualDecision(
		ctx context.Context,
		taskID, operatorUserID, idempotencyKey string,
		decision domain.ManualDecision,
		publicRationale, internalNote, observedCondition string,
		evidenceSource domain.EvidenceSource,
		evidenceHash string,
		observedAt, now time.Time,
	) (domain.DecisionRecord, bool, error)
	ListManualDecisionsForTask(
		ctx context.Context, taskID, operatorUserID string,
	) ([]domain.DecisionRecord, error)
	CreateCustomerRequest(
		ctx context.Context,
		taskID, operatorUserID, idempotencyKey string,
		observedCondition, internalNote string,
		evidenceSource domain.EvidenceSource,
		evidenceHash string,
		observedAt time.Time,
		kind domain.CustomerRequestKind,
		prompt string,
		responseType domain.CustomerResponseType,
		responseOptions []string,
		publicContext string,
		dueAt, now time.Time,
	) (domain.CustomerRequest, bool, error)
	ListCustomerRequestsForTask(
		ctx context.Context, taskID, operatorUserID string,
	) ([]domain.CustomerRequest, error)
	ListCustomerRequestsForOrder(
		ctx context.Context, agencyOrderID, userID string,
	) ([]domain.CustomerRequest, error)
	ResolveCustomerRequest(
		ctx context.Context,
		requestID, actorUserID, idempotencyKey string,
		expectedVersion int64,
		state domain.CustomerRequestState,
		response json.RawMessage,
		resolutionReason string,
		now time.Time,
	) (domain.CustomerRequest, bool, error)
}

type merchantEffectFundingRepository interface {
	PrepareMerchantEffectFunding(
		ctx context.Context,
		taskID, operatorUserID, idempotencyKey string,
		liveEnabled bool,
		authority PurchasePreparation,
		now time.Time,
	) (QueueItem, bool, error)
	ResolveMerchantEffectFunding(
		ctx context.Context,
		taskID, operatorUserID, idempotencyKey, positionID, fundingState string,
		authority PurchasePreparation,
		now time.Time,
	) (QueueItem, bool, error)
}

type RecordManualDecisionInput struct {
	Decision          domain.ManualDecision `json:"decision"`
	PublicRationale   string                `json:"publicRationale"`
	InternalNote      string                `json:"internalNote,omitempty"`
	ObservedCondition string                `json:"observedCondition"`
	EvidenceSource    domain.EvidenceSource `json:"evidenceSource"`
	EvidenceHash      string                `json:"evidenceHash"`
	ObservedAt        time.Time             `json:"observedAt"`
}

func (s *Service) RecordManualDecision(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	input RecordManualDecisionInput,
) (domain.DecisionRecord, bool, error) {
	if s.manual == nil {
		return domain.DecisionRecord{}, false, domain.ErrManualReviewUnavailable
	}
	input.PublicRationale = strings.TrimSpace(input.PublicRationale)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	input.ObservedCondition = strings.TrimSpace(input.ObservedCondition)
	input.EvidenceHash = strings.TrimSpace(input.EvidenceHash)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = s.clock.Now()
	}
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(taskID) == "" ||
		strings.TrimSpace(operatorUserID) == "" ||
		input.Decision == domain.DecisionMaterialCondition ||
		domain.ValidateDecision(input.Decision, input.PublicRationale, input.InternalNote,
			input.ObservedCondition, input.EvidenceSource, input.EvidenceHash,
			input.ObservedAt) != nil {
		return domain.DecisionRecord{}, false, domain.ErrDecisionInvalid
	}
	record, replay, err := s.manual.RecordManualDecision(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), input.Decision, input.PublicRationale,
		input.InternalNote, input.ObservedCondition, input.EvidenceSource,
		input.EvidenceHash, input.ObservedAt, s.clock.Now(),
	)
	// 고객 카드는 리듀서가 procurement.decision.recorded 이벤트에서 발행한다
	// (ADR-0070 §4.5 — 구매 불가만 standalone 카드, ADR-0066 정책 유지).
	return record, replay, err
}

type CreateCustomerRequestInput struct {
	ObservedCondition string                      `json:"observedCondition"`
	InternalNote      string                      `json:"internalNote,omitempty"`
	EvidenceSource    domain.EvidenceSource       `json:"evidenceSource"`
	EvidenceHash      string                      `json:"evidenceHash"`
	ObservedAt        time.Time                   `json:"observedAt"`
	Kind              domain.CustomerRequestKind  `json:"kind"`
	Prompt            string                      `json:"prompt"`
	ResponseType      domain.CustomerResponseType `json:"responseType"`
	ResponseOptions   []string                    `json:"responseOptions,omitempty"`
	PublicContext     string                      `json:"publicContext"`
}

func (s *Service) CreateCustomerRequest(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	input CreateCustomerRequestInput,
) (domain.CustomerRequest, bool, error) {
	if s.manual == nil {
		return domain.CustomerRequest{}, false, domain.ErrManualReviewUnavailable
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.PublicContext = strings.TrimSpace(input.PublicContext)
	input.ObservedCondition = strings.TrimSpace(input.ObservedCondition)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	input.EvidenceHash = strings.TrimSpace(input.EvidenceHash)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = s.clock.Now()
	}
	for index := range input.ResponseOptions {
		input.ResponseOptions[index] = strings.TrimSpace(input.ResponseOptions[index])
	}
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(taskID) == "" ||
		strings.TrimSpace(operatorUserID) == "" ||
		domain.ValidateCustomerRequest(input.Kind, input.Prompt, input.ResponseType,
			input.ResponseOptions, input.PublicContext) != nil ||
		domain.ValidateDecision(domain.DecisionMaterialCondition, input.PublicContext,
			input.InternalNote, input.ObservedCondition, input.EvidenceSource,
			input.EvidenceHash, input.ObservedAt) != nil {
		return domain.CustomerRequest{}, false, domain.ErrRequestInvalid
	}
	now := s.clock.Now()
	request, replay, err := s.manual.CreateCustomerRequest(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), input.ObservedCondition,
		input.InternalNote, input.EvidenceSource, input.EvidenceHash,
		input.ObservedAt, input.Kind, input.Prompt,
		input.ResponseType, input.ResponseOptions, input.PublicContext,
		now.Add(customerRequestTTL), now,
	)
	return request, replay, err
}

func (s *Service) ListTaskManualDecisions(
	ctx context.Context, taskID, operatorUserID string,
) ([]domain.DecisionRecord, error) {
	if s.manual == nil {
		return nil, domain.ErrManualReviewUnavailable
	}
	return s.manual.ListManualDecisionsForTask(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
	)
}

func (s *Service) ListTaskCustomerRequests(
	ctx context.Context, taskID, operatorUserID string,
) ([]domain.CustomerRequest, error) {
	if s.manual == nil {
		return nil, domain.ErrManualReviewUnavailable
	}
	return s.manual.ListCustomerRequestsForTask(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
	)
}

func (s *Service) ListOrderCustomerRequests(
	ctx context.Context, agencyOrderID, userID string,
) ([]domain.CustomerRequest, error) {
	if s.manual == nil {
		return nil, domain.ErrManualReviewUnavailable
	}
	return s.manual.ListCustomerRequestsForOrder(
		ctx, strings.TrimSpace(agencyOrderID), strings.TrimSpace(userID),
	)
}

func (s *Service) RespondToCustomerRequest(
	ctx context.Context,
	requestID, userID, idempotencyKey string,
	expectedVersion int64,
	response json.RawMessage,
	decline bool,
) (domain.CustomerRequest, bool, error) {
	if s.manual == nil {
		return domain.CustomerRequest{}, false, domain.ErrManualReviewUnavailable
	}
	if !validIdempotencyKey(idempotencyKey) || expectedVersion < 1 {
		return domain.CustomerRequest{}, false, domain.ErrRequestResponseInvalid
	}
	state := domain.RequestAnswered
	reason := ""
	if decline {
		state, response, reason = domain.RequestDeclined, nil, "CUSTOMER_DECLINED"
	}
	request, replay, err := s.manual.ResolveCustomerRequest(
		ctx, strings.TrimSpace(requestID), strings.TrimSpace(userID),
		strings.TrimSpace(idempotencyKey), expectedVersion, state, response,
		reason, s.clock.Now(),
	)
	return request, replay, err
}

func (s *Service) ResolveCustomerRequestWithoutResponse(
	ctx context.Context,
	requestID, operatorUserID, idempotencyKey string,
	expectedVersion int64,
	cancel bool,
	reason string,
) (domain.CustomerRequest, bool, error) {
	if s.manual == nil {
		return domain.CustomerRequest{}, false, domain.ErrManualReviewUnavailable
	}
	if !validIdempotencyKey(idempotencyKey) || expectedVersion < 1 ||
		strings.TrimSpace(reason) == "" {
		return domain.CustomerRequest{}, false, domain.ErrRequestInvalid
	}
	state := domain.RequestFailedNoResponse
	if cancel {
		state = domain.RequestCancelled
	}
	request, replay, err := s.manual.ResolveCustomerRequest(
		ctx, strings.TrimSpace(requestID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), expectedVersion, state, nil,
		strings.TrimSpace(reason), s.clock.Now(),
	)
	return request, replay, err
}

// manualReviewLookupRepository는 SUPPORT executor가 카드 payload를 만들 때 읽는
// owner 사실 조회다(ADR-0070 §4.5).
type manualReviewLookupRepository interface {
	GetDecisionRecord(ctx context.Context, decisionID string) (domain.DecisionRecord, error)
	GetCustomerRequest(ctx context.Context, requestID string) (domain.CustomerRequest, error)
}

func (s *Service) GetDecisionRecord(ctx context.Context, decisionID string) (domain.DecisionRecord, error) {
	lookup, ok := s.manual.(manualReviewLookupRepository)
	if !ok {
		return domain.DecisionRecord{}, domain.ErrManualReviewUnavailable
	}
	return lookup.GetDecisionRecord(ctx, strings.TrimSpace(decisionID))
}

func (s *Service) GetCustomerRequest(ctx context.Context, requestID string) (domain.CustomerRequest, error) {
	lookup, ok := s.manual.(manualReviewLookupRepository)
	if !ok {
		return domain.CustomerRequest{}, domain.ErrManualReviewUnavailable
	}
	return lookup.GetCustomerRequest(ctx, strings.TrimSpace(requestID))
}

// PurchasePreparation contains authority read by the Process from each owning
// app port in the same short transaction. It is never decoded from HTTP.
type PurchasePreparation struct {
	AuthorizationKind    string
	AuthorizationHash    string
	ExecutionProfileHash string
	ExecutionMode        string
	HasRefundObligation  bool
	FundingPositionID    string
	FundingState         string
}

func (s *Service) PurchaseSubject(ctx context.Context, taskID string) (QueueItem, error) {
	repo, ok := s.repository.(interface {
		GetPurchaseSubject(context.Context, string) (QueueItem, error)
	})
	if !ok {
		return QueueItem{}, domain.ErrTaskNotFound
	}
	return repo.GetPurchaseSubject(ctx, taskID)
}
func (s *Service) ValidatePurchaseActor(ctx context.Context, taskID, operatorID string) error {
	repo, ok := s.repository.(purchaseEffectRepository)
	if !ok {
		return domain.ErrAssignmentRequired
	}
	return repo.ValidatePurchaseActor(ctx, taskID, operatorID, s.clock.Now())
}

func (s *Service) PreparePurchase(ctx context.Context, taskID, operatorID, key string, authority PurchasePreparation) (QueueItem, bool, error) {
	repository, ok := s.repository.(merchantEffectFundingRepository)
	if !ok || s.manual == nil {
		return QueueItem{}, false, domain.ErrManualReviewUnavailable
	}
	item, err := s.PurchaseSubject(ctx, taskID)
	if err != nil {
		return QueueItem{}, false, err
	}
	if err = procmsg.RequireExecution(ctx, item.MerchantOrder.ID, procmsg.EffectReservePurchase); err != nil {
		return QueueItem{}, false, err
	}
	if item.MerchantOrder.ExecutionMode == domain.ModeLiveMerchantEffect {
		if s.liveMerchantGate == nil {
			return QueueItem{}, false, domain.ErrLiveModeClosed
		}
		allowed, err := s.liveMerchantGate.AllowLiveMerchantEffect(ctx)
		if err != nil || !allowed {
			return QueueItem{}, false, domain.ErrLiveModeClosed
		}
	}
	return repository.PrepareMerchantEffectFunding(ctx, taskID, operatorID, key, s.liveEffectEnabled, authority, s.clock.Now())
}
func (s *Service) AuthorizeMerchant(ctx context.Context, taskID, operatorID, key string, authority PurchasePreparation) (QueueItem, bool, error) {
	repository, ok := s.repository.(merchantEffectFundingRepository)
	if !ok {
		return QueueItem{}, false, domain.ErrManualReviewUnavailable
	}
	item, err := s.PurchaseSubject(ctx, taskID)
	if err != nil {
		return QueueItem{}, false, err
	}
	if err = procmsg.RequireExecution(ctx, item.MerchantOrder.ID, procmsg.EffectGrantMerchantPurchase); err != nil {
		return QueueItem{}, false, err
	}
	if item.MerchantOrder.ExecutionMode == domain.ModeLiveMerchantEffect && authority.FundingState == "ACTIVE" {
		if s.liveMerchantGate == nil {
			return QueueItem{}, false, domain.ErrLiveModeClosed
		}
		allowed, err := s.liveMerchantGate.AllowLiveMerchantEffect(ctx)
		if err != nil || !allowed {
			return QueueItem{}, false, domain.ErrLiveModeClosed
		}
	}
	return repository.ResolveMerchantEffectFunding(ctx, taskID, operatorID, key, authority.FundingPositionID, authority.FundingState, authority, s.clock.Now())
}

type purchaseEffectRepository interface {
	ValidatePurchaseActor(context.Context, string, string, time.Time) error
	PurchaseSubjectByMO(context.Context, string) (QueueItem, error)
}

func (s *Service) PurchaseSubjectByMO(ctx context.Context, moID string) (QueueItem, error) {
	repo, ok := s.repository.(purchaseEffectRepository)
	if !ok {
		return QueueItem{}, domain.ErrTaskNotFound
	}
	return repo.PurchaseSubjectByMO(ctx, moID)
}

func (s *Service) LockPurchaseSubject(ctx context.Context, taskID string) (QueueItem, error) {
	repo, ok := s.repository.(interface {
		LockPurchaseSubject(context.Context, string) (QueueItem, error)
	})
	if !ok {
		return QueueItem{}, domain.ErrTaskNotFound
	}
	return repo.LockPurchaseSubject(ctx, taskID)
}

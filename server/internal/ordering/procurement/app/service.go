// Package app은 Procurement의 조율자다: accepted CustomerFundsReceipt를 inbox로
// 소비해 manifest/MerchantOrder/Unit/Task root를 전수 생성하고(계약 v7 §12.2),
// 운영자 Task claim/결과/감사 reveal을 소유한다. 실행 창구는 단일
// ManualOperatorExecutor + ExecutionMode다(ADR-0052 §4) — LIVE_MERCHANT_EFFECT는
// Step 6 activation까지 구조적으로 fail-close다.
package app

import (
	"context"
	"encoding/json"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// QueueItem은 운영자 큐 사영이다. AgencyOrder snapshot은 immutable 계약 입력의
// raw JSON이며(정보 계약 §4.3의 표면화 근거), 다른 Context 타입을 import하지
// 않는다.
// LogisticsSummary는 exact MO의 배송 진실 요약이다. canonical operational
// projection의 owner fact 입력이며 AgencyOrder process는 이 합성에 관여하지
// 않는다. Delivered는 정상 수령(DELIVERED_EXPECTED)만 세고 예외 lane과 회수는
// 독립 count로 나른다.
type LogisticsSummary struct {
	ExpectedUnits         int `json:"expectedUnits"`
	AwaitingUnits         int `json:"awaitingUnits"`
	InTransitUnits        int `json:"inTransitUnits"`
	DeliveredUnits        int `json:"deliveredUnits"`
	ExceptionUnits        int `json:"exceptionUnits"`
	ReturnInProgressUnits int `json:"returnInProgressUnits"`
}

// DeliverySelection은 조달 지시의 선택 배송 옵션 사영이다(운영정합 5차 C2).
// FE가 checkout 스냅샷을 직접 파싱하며 실패를 "정보 없음"으로 무언 강등하던
// 것을 서버 파생 + 명시 플래그로 바꾼다(funding 구성의 D-g 전례).
type DeliverySelection struct {
	Derived     bool   `json:"derived"`
	Title       string `json:"title,omitempty"`
	AmountMinor int64  `json:"amountMinor"`
}

type QueueItem struct {
	Task               domain.ExecutionTask       `json:"task"`
	MerchantOrder      domain.MerchantOrder       `json:"merchantOrder"`
	Units              []domain.MerchantOrderUnit `json:"units"`
	OrderSnapshot      json.RawMessage            `json:"agencyOrder"`
	ProcessState       string                     `json:"processState"`
	TerminalReason     string                     `json:"terminalReason,omitempty"`
	LogisticsSummary   LogisticsSummary           `json:"logisticsSummary"`
	Delivery           DeliverySelection          `json:"delivery"`
	Funding            MerchantOrderFunding       `json:"funding"`
	RefundRequestState string                     `json:"refundRequestState,omitempty"`
	CancellationState  string                     `json:"cancellationState,omitempty"`
	ResolutionCause    string                     `json:"resolutionCause,omitempty"`
	ResolutionDecision string                     `json:"resolutionDecision,omitempty"`
	ReturnState        string                     `json:"returnState,omitempty"`
	CompensationAction string                     `json:"compensationAction,omitempty"`
	CompensationState  string                     `json:"compensationState,omitempty"`
	DisputeState       string                     `json:"disputeState,omitempty"`
	DisputeOutcome     string                     `json:"disputeOutcome,omitempty"`
}

type MerchantOrderFunding struct {
	PositionID  string `json:"positionId,omitempty"`
	State       string `json:"state,omitempty"`
	AmountMinor int64  `json:"amountMinor"`
	Rail        string `json:"rail,omitempty"`
}

type Repository interface {
	ListQueue(ctx context.Context, limit int) ([]QueueItem, error)
	CountOpenTasks(ctx context.Context) (int, error)
	GetQueueItem(ctx context.Context, taskID string) (QueueItem, error)
	ClaimTask(ctx context.Context, taskID, operatorUserID, idempotencyKey string, leaseUntil, now time.Time) (QueueItem, bool, error)
	// RecordPlaced는 done 결과의 단일 창구다(ADR-0053 — 모드 무관 PLANNED→
	// PLACED). SANDBOX는 evidence 없이 "처리했다 치고"(synthetic 지출 기록),
	// LIVE는 외부 주문 참조·정확한 금액과 이미 시작된 effect lock을 요구한다.
	// liveEnabled는 새 Begin에만 적용된다; 결과 기록 시점의 kill switch는
	// 이미 발생한 외부 effect의 증거 보존을 막지 않는다.
	RecordPlaced(ctx context.Context, taskID, operatorUserID, idempotencyKey string, evidence domain.PlacementEvidence, liveEnabled bool, now time.Time) (QueueItem, bool, error)
	RecordFailure(ctx context.Context, taskID, operatorUserID, idempotencyKey, failureCode string, now time.Time) (QueueItem, bool, error)
	// AuthorizeReveal은 claim/lease/사유를 검증하고 append-only 감사를 남긴 뒤
	// 대상 참조(배송 snapshot ID 또는 continue_url safe-ref와 소유자)를 돌려준다.
	AuthorizeShippingReveal(ctx context.Context, taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string, now time.Time) (string, bool, error)
	AuthorizeContinueURLReveal(ctx context.Context, taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string, now time.Time) (ContinueURLRef, bool, error)
	// 간이 회수 원장 기입(운영정합 5차 PR-D) — 자동 entry 포함 목록·수동
	// 생성·수취 기입·포기·수동 행 삭제. 상태는 금액 파생, 감사는
	// execution_audits RECOVERY_* 행.
	ListRecoveryEntries(ctx context.Context, agencyOrderID string) (RecoverySurface, error)
	CreateRecoveryEntry(ctx context.Context, merchantOrderID, operatorUserID, idempotencyKey, cause string, expectedMinor, receivedMinor int64, note string, now time.Time) (RecoveryEntry, bool, error)
	RecordRecoveryEntry(ctx context.Context, entryID, operatorUserID, idempotencyKey string, receivedMinor int64, note string, expectedVersion int64, now time.Time) (RecoveryEntry, bool, error)
	WaiveRecoveryEntry(ctx context.Context, entryID, operatorUserID, idempotencyKey, note string, expectedVersion int64, now time.Time) (RecoveryEntry, bool, error)
	DeleteRecoveryEntry(ctx context.Context, entryID, operatorUserID string, expectedVersion int64, now time.Time) error
}

type fundingPlanningRepository interface {
	// PlanFromFunding is the PayPal AUTHORIZE planning handoff. It creates every
	// MO/task from the immutable order snapshot before any MO capture occurs.
	PlanFromFunding(ctx context.Context, orderID string, input procmsg.PlanFromFundingPayload, now time.Time) error
}

type ContinueURLRef struct {
	OwnerUserID string
	SafeRef     string
	Hash        string
	ShopDomain  string
	ExpiresAt   time.Time
}

type OperatorShippingAddress struct {
	RecipientName string `json:"recipientName"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2,omitempty"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Phone         string `json:"phone,omitempty"`
}

type OperatorShippingReader interface {
	RevealShippingSnapshot(context.Context, string) (OperatorShippingAddress, error)
}

// ContinueURLVault는 발행 시 암호화 보관된 checkout continue_url을 소유자
// 키로 복호한다(agencyorder capability vault 구현을 wiring이 주입한다).
type ContinueURLVault interface {
	Load(ctx context.Context, userID, safeRef, purpose string) (string, error)
}

type Service struct {
	inputs     procmsg.ActionInputs
	repository Repository
	manual     ManualReviewRepository
	shipping   OperatorShippingReader
	vault      ContinueURLVault
	clock      sharedapp.Clock
	leaseTTL   time.Duration
	// liveEffectEnabled는 Step 6 activation gate다. false면 LIVE 결과 기록
	// 자체가 거절된다(MANUAL_MERCHANT_EFFECT_ENABLED).
	liveEffectEnabled bool
	liveMerchantGate  LiveMerchantGate
}

type LiveMerchantGate interface {
	AllowLiveMerchantEffect(context.Context) (bool, error)
}

func (s *Service) EnableLiveMerchantGate(gate LiveMerchantGate) {
	s.liveMerchantGate = gate
}

// CancellationSupportProjection is the public-safe view of the immutable
// PRE_EFFECT/DELAY_RULE cancellation fact.
type CancellationSupportProjection struct {
	AgencyOrderID   string
	MerchantOrderID string
	UserID          string
	Kind            string
	RefundBasis     policy.RefundBasis
	CreatedAt       time.Time
}

// cancellationLookupRepository는 SUPPORT executor가 취소 카드를 만들 때 읽는
// 취소 사실 조회다(ADR-0070 §4.5).
type cancellationLookupRepository interface {
	GetCancellationSupport(context.Context, string) (CancellationSupportProjection, bool, error)
}

// GetCancellationProjection은 MO의 commit된 취소 사실이다(없으면 false).
func (s *Service) GetCancellationProjection(
	ctx context.Context,
	merchantOrderID string,
) (CancellationSupportProjection, bool, error) {
	lookup, ok := s.repository.(cancellationLookupRepository)
	if !ok {
		return CancellationSupportProjection{}, false, domain.ErrOrderNotFound
	}
	return lookup.GetCancellationSupport(ctx, strings.TrimSpace(merchantOrderID))
}

func NewService(
	repository Repository,
	shipping OperatorShippingReader,
	vault ContinueURLVault,
	clock sharedapp.Clock,
	liveEffectEnabled bool,
) *Service {
	manual, _ := repository.(ManualReviewRepository)
	return &Service{
		repository: repository, manual: manual, shipping: shipping, vault: vault,
		clock: clock, leaseTTL: 30 * time.Minute,
		liveEffectEnabled: liveEffectEnabled,
	}
}

type CancellationAuthority struct {
	UserID           string
	IssuedAt         time.Time
	FundingState     string
	UndeliveredUnits int
}
type cancellationEffectRepository interface {
	CancelMerchantOrder(context.Context, string, string, string, string, CancellationAuthority, time.Time) (bool, error)
}

func (s *Service) ExecuteCancellation(ctx context.Context, orderID, moID, userID, kind string, authority CancellationAuthority) (bool, error) {
	repo, ok := s.repository.(cancellationEffectRepository)
	if !ok {
		return false, domain.ErrCancelNotEligible
	}
	return repo.CancelMerchantOrder(ctx, orderID, moID, userID, kind, authority, s.clock.Now())
}

const DelayRuleWindowDays = policy.DelayRuleWindowDays

// PlanFromFunding executes the clean-cut funding-ready command. The repository
// accepts only a verified full-order funding source and complete MO allocations.
func (s *Service) PlanFromFunding(ctx context.Context, orderID string, input procmsg.PlanFromFundingPayload) error {
	if err := procmsg.RequireExecution(ctx, "", procmsg.EffectPlanMerchantOrders); err != nil {
		return err
	}
	scope, _ := procmsg.ExecutionFrom(ctx)
	if scope.AgencyOrderID != orderID {
		return procmsg.ErrEffectInvalid
	}
	customerPaymentID := input.CustomerPaymentID
	if strings.TrimSpace(customerPaymentID) == "" {
		return domain.ErrPlanNotEligible
	}
	planner, ok := s.repository.(fundingPlanningRepository)
	if !ok {
		return domain.ErrPlanNotEligible
	}
	return planner.PlanFromFunding(
		ctx, orderID, input, s.clock.Now(),
	)
}

func (s *Service) ListQueue(ctx context.Context, limit int) ([]QueueItem, error) {
	items, err := s.repository.ListQueue(ctx, limit)
	if err != nil {
		return nil, err
	}
	// 조달 지시의 선택 배송은 서버가 파생해 계약으로 내린다(5차 C2).
	for index := range items {
		items[index] = withDeliverySelection(items[index])
	}
	return items, nil
}

// CountOpenTasks는 진행 국면 Task의 전역 카운트다(ADR-0057 2차 P2 — 목록
// limit 캡과 무관한 nav 뱃지 근거).
func (s *Service) CountOpenTasks(ctx context.Context) (int, error) {
	return s.repository.CountOpenTasks(ctx)
}

func (s *Service) Claim(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
) (QueueItem, bool, error) {
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(operatorUserID) == "" {
		return QueueItem{}, false, domain.ErrTaskStateInvalid
	}
	now := s.clock.Now()
	item, replayed, err := s.repository.ClaimTask(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), now.Add(s.leaseTTL), now,
	)
	return withDeliverySelection(item), replayed, err
}

// RecordResult는 운영자 결과 제출이다(ADR-0053 — done은 모드 무관 PLACED로
// 수렴한다). Sandbox와 Live 모두 같은 완전한 evidence shape를 요구하지만,
// Sandbox kind는 TEST 기록이며 실제 merchant effect를 주장하지 않는다. Live kind만
// 실제 effect 증거다. Activation gate는 Begin에만 적용된다.
func (s *Service) RecordResult(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	done bool,
	failureCode string,
	evidence domain.PlacementEvidence,
) (QueueItem, bool, error) {
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(operatorUserID) == "" {
		return QueueItem{}, false, domain.ErrResultInvalid
	}
	now := s.clock.Now()
	if done {
		// evidence kind와 immutable 실행 mode 일치는 repository가 재검사한다.
		// Kill switch는 새 Begin만 닫으며, 이미 시작된 실제 외부 effect의 결과
		// 기록은 항상 열어 둔다.
		// 운영정합 3차 D-f: funding coverage는 view다 — 지출 기록을 차단하지
		// 않는다. 부족은 화면 경고·간이 회계 추적이 담당한다.
		item, replayed, err := s.repository.RecordPlaced(
			ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
			strings.TrimSpace(idempotencyKey), evidence, s.liveEffectEnabled, now,
		)
		return withDeliverySelection(item), replayed, err
	}
	failureCode = strings.ToUpper(strings.TrimSpace(failureCode))
	if err := domain.ValidateFailureCode(failureCode); err != nil {
		return QueueItem{}, false, err
	}
	item, replayed, err := s.repository.RecordFailure(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), failureCode, now,
	)
	return withDeliverySelection(item), replayed, err
}

func (s *Service) RevealShipping(
	ctx context.Context,
	taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string,
) (OperatorShippingAddress, bool, error) {
	if s.shipping == nil || !validIdempotencyKey(idempotencyKey) ||
		strings.TrimSpace(correlationID) == "" ||
		domain.ValidatePIIReveal(reasonCode, reasonDetail) != nil {
		return OperatorShippingAddress{}, false, domain.ErrPIIAccessDenied
	}
	snapshotID, replayed, err := s.repository.AuthorizeShippingReveal(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(reasonCode), strings.TrimSpace(reasonDetail),
		strings.TrimSpace(correlationID), strings.TrimSpace(idempotencyKey), s.clock.Now(),
	)
	if err != nil {
		return OperatorShippingAddress{}, replayed, err
	}
	address, err := s.shipping.RevealShippingSnapshot(ctx, snapshotID)
	return address, replayed, err
}

// RevealContinueURL은 운용 참조 정보 인계다(ADR-0052 §4.4): claim/lease/사유
// 감사 뒤 발행 시 보관된 continue_url을 복호한다. 만료·회수됐으면
// ErrContinueURLGone으로 답한다. 이 참조의 유무와 무관하게 운영자는 판매처에
// 직접 들어가 주문 sheet의 Shop·상품·variant/options/quantity를 수동 입력한다.
// 만료 checkout 재사용·refresh나 상품 URL 자동구매 fallback은 없다.
func (s *Service) RevealContinueURL(
	ctx context.Context,
	taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string,
) (string, string, bool, error) {
	if s.vault == nil || !validIdempotencyKey(idempotencyKey) ||
		strings.TrimSpace(correlationID) == "" ||
		domain.ValidatePIIReveal(reasonCode, reasonDetail) != nil {
		return "", "", false, domain.ErrPIIAccessDenied
	}
	ref, replayed, err := s.repository.AuthorizeContinueURLReveal(
		ctx, strings.TrimSpace(taskID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(reasonCode), strings.TrimSpace(reasonDetail),
		strings.TrimSpace(correlationID), strings.TrimSpace(idempotencyKey), s.clock.Now(),
	)
	if err != nil {
		return "", "", replayed, err
	}
	if ref.SafeRef == "" {
		return "", "", replayed, domain.ErrContinueURLGone
	}
	value, err := s.vault.Load(ctx, ref.OwnerUserID, ref.SafeRef, "UCP_CONTINUE_URL")
	if err != nil {
		return "", "", replayed, domain.ErrContinueURLGone
	}
	return value, ref.Hash, replayed, nil
}

func validIdempotencyKey(value string) bool {
	length := len(strings.TrimSpace(value))
	return length >= 8 && length <= 200
}

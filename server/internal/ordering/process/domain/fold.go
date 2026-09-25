package domain

import (
	"sort"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

// 이 파일은 Order-MO-Unit fold다(ADR-0070 §4.1). owner가 보고한 전이를 identity
// (MerchantOrder id · expected unit id)로 접고, 주문 단위 수량은 그 집합에서
// 파생한다. 같은 identity의 재발행은 같은 key를 덮어써 멱등이라 +1/−1 산술과
// 달리 누락 1건이 영구 드리프트가 되지 않는다(ADR-0056 §4 개정).

// ProcessModelVersion identifies the reducer state schema after atomic cutover.
const ProcessModelVersion = 3

// MOPhase는 리듀서가 소유하는 MerchantOrder 결정 어휘다(§3.2). 고지 어휘
// (MerchantOrderOperationalStage)는 이 phase를 spine으로 owner 세부를 보강한다.
type MOPhase string

const (
	MOPhasePlanned      MOPhase = "PLANNED"
	MOPhaseFunding      MOPhase = "FUNDING"
	MOPhasePurchasing   MOPhase = "PURCHASING"
	MOPhasePlaced       MOPhase = "PLACED"
	MOPhaseFulfilling   MOPhase = "FULFILLING"
	MOPhaseDelivered    MOPhase = "DELIVERED"
	MOPhaseCompensating MOPhase = "COMPENSATING"
	MOPhaseCompensated  MOPhase = "COMPENSATED"
	MOPhaseFailed       MOPhase = "FAILED"
	MOPhaseCancelled    MOPhase = "CANCELLED"
)

// IntentOutcome은 고객 intent(취소)의 리듀서 판정이다.
const (
	IntentOutcomeEffectIssued = "EFFECT_ISSUED" // Effect 발행됨
	IntentOutcomeDeferred     = "DEFERRED"      // 진행 중 effect 뒤로 미룸(PLACED/실패 시 재평가)
	IntentOutcomeRejected     = "REJECTED"      // 자격 없음 — 고객 거절 카드 대상
	IntentOutcomeSucceeded    = "SUCCEEDED"     // 실행 성공
	IntentOutcomeSuperseded   = "SUPERSEDED"    // 다른 종결(실패·보상)이 앞섬
)

// MOCompensationFold는 whole-MO 보상의 마지막 관찰이다.
type MOCompensationFold struct {
	ID     string `json:"id,omitempty"`
	State  string `json:"state,omitempty"`
	Action string `json:"action,omitempty"`
	Cause  string `json:"cause,omitempty"`
}

// MOIntent preserves the last customer cancellation decision per MO.
type MOIntent struct {
	RequestID string `json:"requestId,omitempty"`
	Kind      string `json:"kind"`
	Seq       int64  `json:"seq,omitempty"`
	Outcome   string `json:"outcome"`
	Code      string `json:"code,omitempty"`
	UserID    string `json:"userId,omitempty"`
}

// MOAttention remains tied to its unresolved effect until that result is observed.
type MOAttention struct {
	Code       string `json:"code"`
	EffectID   string `json:"effectId,omitempty"`
	EffectType string `json:"effectType,omitempty"`
}

type MODispute struct {
	CaseID  string `json:"caseId"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
}

// MOState는 한 MerchantOrder의 결정 단위 관찰이다.
type MOState struct {
	Cancellation      *CancellationIntent          `json:"cancellation,omitempty"`
	UnitManifest      []procmsg.UnitManifestEntry  `json:"unitManifest,omitempty"`
	TaskID            string                       `json:"taskId,omitempty"`
	UnitIDs           []string                     `json:"unitIds,omitempty"`
	FundingPositionID string                       `json:"fundingPositionId,omitempty"`
	Effects           map[string]EffectExpectation `json:"effects,omitempty"`
	Purchase          *PurchaseIntent              `json:"purchase,omitempty"`
	PendingRequests   []procmsg.ActionRequest      `json:"pendingRequests,omitempty"`
	AllocationID      string                       `json:"allocationId,omitempty"`
	ShopDomain        string                       `json:"shopDomain,omitempty"`
	OwnerState        string                       `json:"ownerState"` // merchant_orders.state 원값
	FailureCode       string                       `json:"failureCode,omitempty"`
	UnitCount         int                          `json:"unitCount,omitempty"`
	FundingState      string                       `json:"fundingState,omitempty"` // payment_mo_funding_positions.state
	LockState         string                       `json:"lockState,omitempty"`    // procurement_effect_locks.state
	// CompensationCause는 MO 이벤트가 실은 보상 사유(실패·취소)다 — 보상 행이
	// 생기기 전에도 COMPENSATING 판정의 근거다.
	CompensationCause string             `json:"compensationCause,omitempty"`
	Compensation      MOCompensationFold `json:"compensation,omitempty"`
	// Requests는 고객 정보·동의 요청의 identity 집합(id → state)이다 — 열린
	// 요청 수는 파생한다.
	Requests           map[string]string `json:"requests,omitempty"`
	LastDecision       string            `json:"lastDecision,omitempty"`
	RefundRequestState string            `json:"refundRequestState,omitempty"`
	RefundDecision     string            `json:"refundDecision,omitempty"`
	CancellationKind   string            `json:"cancellationKind,omitempty"`
	Intent             *MOIntent         `json:"intent,omitempty"`
	Attention          *MOAttention      `json:"attention,omitempty"`
	Dispute            *MODispute        `json:"dispute,omitempty"`
	Phase              MOPhase           `json:"phase,omitempty"`
	LastReason         string            `json:"lastReason,omitempty"`
}

// UnitFold는 물리 unit(logistics expected unit)의 관찰이다 — 돈 없음.
type UnitFold struct {
	MerchantOrderID string `json:"merchantOrderId"`
	Fulfillment     string `json:"fulfillment"`
	ShipmentID      string `json:"shipmentId,omitempty"`
	ShipmentState   string `json:"shipmentState,omitempty"`
	ReturnState     string `json:"returnState,omitempty"`
}

// OpenRequests는 PENDING 고객 요청 수다(identity 집합에서 파생).
func (f MOState) OpenRequests() int {
	open := 0
	for _, state := range f.Requests {
		if state == "PENDING" {
			open++
		}
	}
	return open
}

var closedFulfillments = map[string]bool{
	"DELIVERED_EXPECTED": true, "RESOLVED": true, "NONCONFORMING_RESOLVED": true,
	"SUPERSEDED_BY_CANCELLATION": true, "NO_PLACEMENT": true,
}

var deliveredFulfillments = map[string]bool{
	"DELIVERED_EXPECTED": true, "RESOLVED": true, "NONCONFORMING_RESOLVED": true,
}

func merchantOrderTerminal(ownerState string) bool {
	return ownerState == "PLACED" || ownerState == "FAILED" || ownerState == "CANCELLED"
}

func compensationActive(c MOCompensationFold) bool {
	return c.State != "" && c.State != "SUCCEEDED"
}

// mo는 fold를 찾거나 만든다(이벤트 순서가 MO 생성 이벤트를 앞세우지 않는
// 스트림 — 예: 컷오버 전 스냅샷 — 에서도 identity 키가 안정적이다).
func (w *ProcessState) mo(merchantOrderID string) *MOState {
	if w.MerchantOrders == nil {
		w.MerchantOrders = map[string]*MOState{}
	}
	fold, ok := w.MerchantOrders[merchantOrderID]
	if !ok {
		fold = &MOState{}
		w.MerchantOrders[merchantOrderID] = fold
	}
	return fold
}

func (w *ProcessState) unit(expectedUnitID string) *UnitFold {
	if w.Units == nil {
		w.Units = map[string]*UnitFold{}
	}
	fold, ok := w.Units[expectedUnitID]
	if !ok {
		fold = &UnitFold{}
		w.Units[expectedUnitID] = fold
	}
	return fold
}

// sortedMerchantOrderIDs는 결정적 순회 순서다(Effect 발행·diff 순서 안정).
func (w ProcessState) sortedMerchantOrderIDs() []string {
	ids := make([]string, 0, len(w.MerchantOrders))
	for id := range w.MerchantOrders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// 파생 수량 — 종전 owner COUNT payload의 후신(identity 집합의 술어).

func (w ProcessState) merchantOrdersTotal() int { return len(w.MerchantOrders) }

func (w ProcessState) merchantOrdersOpen() int {
	open := 0
	for _, fold := range w.MerchantOrders {
		if !merchantOrderTerminal(fold.OwnerState) {
			open++
		}
	}
	return open
}

func (w ProcessState) activeCompensations() int {
	active := 0
	for _, fold := range w.MerchantOrders {
		if compensationActive(fold.Compensation) {
			active++
		}
	}
	return active
}

func (w ProcessState) succeededCompensations() int {
	succeeded := 0
	for _, fold := range w.MerchantOrders {
		if fold.Compensation.State == "SUCCEEDED" {
			succeeded++
		}
	}
	return succeeded
}

func (w ProcessState) allMerchantOrdersCompensated() bool {
	total := w.merchantOrdersTotal()
	return total > 0 && w.succeededCompensations() == total
}

// unitsOf는 MO의 등록된 unit fold를 센다(닫힘·수령 가치).
func (w ProcessState) unitsOf(merchantOrderID string) (registered, closed int, delivered bool) {
	for _, unit := range w.Units {
		if unit.MerchantOrderID != merchantOrderID {
			continue
		}
		registered++
		if closedFulfillments[unit.Fulfillment] {
			closed++
		}
		if deliveredFulfillments[unit.Fulfillment] {
			delivered = true
		}
	}
	return registered, closed, delivered
}

// placedUnitsPendingPhysical는 PLACED MO의 물리 미종결 unit 수다. 등록 전
// unit(expected 행 없음)도 미종결이라 owner가 실은 UnitCount를 상한으로 쓴다.
func (w ProcessState) placedUnitsPendingPhysical() int {
	pending := 0
	for id, fold := range w.MerchantOrders {
		if fold.OwnerState != "PLACED" {
			continue
		}
		registered, closed, _ := w.unitsOf(id)
		total := fold.UnitCount
		if registered > total {
			total = registered
		}
		if remaining := total - closed; remaining > 0 {
			pending += remaining
		}
	}
	return pending
}

func (w ProcessState) deliveredValueExists() bool {
	for id, fold := range w.MerchantOrders {
		if fold.OwnerState != "PLACED" {
			continue
		}
		if _, _, delivered := w.unitsOf(id); delivered {
			return true
		}
	}
	return false
}

func (w ProcessState) anyMerchantOrderAttention() *MOAttention {
	for _, id := range w.sortedMerchantOrderIDs() {
		if attention := w.MerchantOrders[id].Attention; attention != nil {
			return attention
		}
	}
	return nil
}

// derivePhase는 MO fold에서 결정 phase를 파생한다(§3.2 표). 돈 사실이 물리
// 사실보다 우선한다 — 보상된 MO는 배송 이력이 있어도 다시 물류로 돌아가지
// 않는다(고지 어휘 규칙과 같은 우선순위).
func (w ProcessState) derivePhase(merchantOrderID string, fold *MOState, compensationPending bool) MOPhase {
	switch {
	case compensationActive(fold.Compensation) || compensationPending ||
		(fold.CompensationCause != "" && fold.Compensation.State == ""):
		// 보상 행이 아직 없는 trigger(같은 결정의 Effect 발행·REJECTED 거절)도
		// 열린 자금 obligation이다 — compensationWorkOpen과 같은 술어.
		return MOPhaseCompensating
	case fold.Compensation.State == "SUCCEEDED":
		return MOPhaseCompensated
	case fold.OwnerState == "CANCELLED":
		return MOPhaseCancelled
	case fold.OwnerState == "FAILED":
		return MOPhaseFailed
	case fold.OwnerState == "PLACED":
		registered, closed, _ := w.unitsOf(merchantOrderID)
		total := fold.UnitCount
		if registered > total {
			total = registered
		}
		if total > 0 && closed >= total {
			return MOPhaseDelivered
		}
		return MOPhaseFulfilling
	case fold.OwnerState == "PLACEMENT_PENDING" || fold.OwnerState == "PLACEMENT_UNKNOWN" ||
		fold.LockState == "STARTED":
		return MOPhasePurchasing
	case fold.Purchase != nil || fold.LockState == "FUNDING_PENDING" || fold.LockState == "FUNDING_UNKNOWN" ||
		fold.FundingState == "ACTIVATION_PENDING" || fold.FundingState == "ACTIVATION_UNKNOWN":
		return MOPhaseFunding
	default:
		return MOPhasePlanned
	}
}

// preEffectCancellable은 사전 취소가 executor 검증을 통과할 조건의 리듀서
// 판정이다(cancel_live.go의 규칙과 같은 술어: PLANNED/READY, funding
// AVAILABLE, effect lock 없음, 보상 없음).
func preEffectCancellable(fold *MOState) bool {
	if fold.OwnerState != "PLANNED" && fold.OwnerState != "READY_TO_PLACE" {
		return false
	}
	if fold.LockState != "" && fold.LockState != "FAILED" {
		return false
	}
	if fold.FundingState != "" && fold.FundingState != "AVAILABLE" {
		return false
	}
	return fold.Compensation.State == "" && fold.CompensationCause == ""
}

// ApplyMerchantOrdersSnapshot은 watchdog의 읽기 전용 Owner 관찰을 구성한다.
// 저장된 Process나 실행 권한을 갱신하지 않고 진단용 사영의 사실만 채운다.
func (w *ProcessState) ApplyMerchantOrdersSnapshot(payload procmsg.MerchantOrdersSnapshotPayload) {
	w.MerchantOrders = map[string]*MOState{}
	w.Units = map[string]*UnitFold{}
	for _, snapshot := range payload.MerchantOrders {
		fold := w.mo(snapshot.MerchantOrderID)
		fold.AllocationID = snapshot.AllocationID
		fold.ShopDomain = snapshot.ShopDomain
		fold.OwnerState = snapshot.State
		fold.FailureCode = snapshot.FailureCode
		fold.UnitCount = snapshot.UnitCount
		fold.FundingState = snapshot.FundingState
		fold.LockState = snapshot.EffectLockState
		fold.Compensation = MOCompensationFold{
			State: snapshot.CompensationState, Action: snapshot.CompensationAction,
			Cause: snapshot.CompensationCause,
		}
		for _, requestID := range snapshot.OpenRequestIDs {
			if fold.Requests == nil {
				fold.Requests = map[string]string{}
			}
			fold.Requests[requestID] = "PENDING"
		}
		fold.CancellationKind = snapshot.CancellationKind
		fold.RefundRequestState = snapshot.RefundRequestState
		if snapshot.Dispute != nil {
			fold.Dispute = &MODispute{
				CaseID: snapshot.Dispute.CaseID, State: snapshot.Dispute.State,
				Outcome: snapshot.Dispute.Outcome,
			}
		}
		for _, unit := range snapshot.Units {
			u := w.unit(unit.ExpectedUnitID)
			u.MerchantOrderID = snapshot.MerchantOrderID
			u.Fulfillment = unit.Fulfillment
		}
	}
	w.ModelVersion = ProcessModelVersion
}

// MerchantOrderDecision은 결정이 기록하는 MO 사영 행이다(agency_order_process_
// merchant_orders). Changed는 이 결정에서 값이 바뀐 MO만 upsert하기 위한 표식.
type MerchantOrderDecision struct {
	MerchantOrderID string  `json:"merchantOrderId"`
	AllocationID    string  `json:"allocationId,omitempty"`
	Phase           MOPhase `json:"phase"`
	PhaseBefore     MOPhase `json:"phaseBefore,omitempty"`
	OwnerState      string  `json:"ownerState"`
	FundingState    string  `json:"fundingState,omitempty"`
	LockState       string  `json:"lockState,omitempty"`
	Compensation    MOCompensationFold
	FailureCode     string       `json:"failureCode,omitempty"`
	Attention       *MOAttention `json:"attention,omitempty"`
	Intent          *MOIntent    `json:"intent,omitempty"`
	Changed         bool         `json:"changed"`
}

func (w ProcessState) merchantOrderDecisions(before ProcessState) []MerchantOrderDecision {
	decisions := make([]MerchantOrderDecision, 0, len(w.MerchantOrders))
	for _, id := range w.sortedMerchantOrderIDs() {
		fold := w.MerchantOrders[id]
		decision := MerchantOrderDecision{
			MerchantOrderID: id, AllocationID: fold.AllocationID, Phase: fold.Phase,
			OwnerState: fold.OwnerState, FundingState: fold.FundingState,
			LockState: fold.LockState, Compensation: fold.Compensation,
			FailureCode: fold.FailureCode, Attention: fold.Attention, Intent: fold.Intent,
			Changed: true,
		}
		if previous, ok := before.MerchantOrders[id]; ok && previous != nil {
			decision.PhaseBefore = previous.Phase
			decision.Changed = !sameMOState(previous, fold)
		}
		decisions = append(decisions, decision)
	}
	return decisions
}

func sameMOState(a, b *MOState) bool {
	if a.Phase != b.Phase || a.OwnerState != b.OwnerState || a.FundingState != b.FundingState ||
		a.LockState != b.LockState || a.Compensation != b.Compensation ||
		a.FailureCode != b.FailureCode || a.LastReason != b.LastReason ||
		a.AllocationID != b.AllocationID {
		return false
	}
	if (a.Attention == nil) != (b.Attention == nil) ||
		(a.Attention != nil && *a.Attention != *b.Attention) {
		return false
	}
	if (a.Intent == nil) != (b.Intent == nil) ||
		(a.Intent != nil && *a.Intent != *b.Intent) {
		return false
	}
	return true
}

// cloneProcessState는 결정 전 fold 사본이다(diff 계산용 — fold는 포인터 맵이라
// 얕은 복사로는 전후가 같아진다).
func cloneProcessState(w ProcessState) ProcessState {
	w.Effects = cloneEffects(w.Effects)
	if w.EntityVersions != nil {
		v := map[string]int64{}
		for k, n := range w.EntityVersions {
			v[k] = n
		}
		w.EntityVersions = v
	}
	if w.CommunicationAttention != nil {
		v := map[string]string{}
		for k, n := range w.CommunicationAttention {
			v[k] = n
		}
		w.CommunicationAttention = v
	}
	clone := w
	clone.MerchantOrders = make(map[string]*MOState, len(w.MerchantOrders))
	for id, fold := range w.MerchantOrders {
		copied := *fold
		copied.Effects = cloneEffects(fold.Effects)
		copied.UnitIDs = append([]string(nil), fold.UnitIDs...)
		copied.UnitManifest = append([]procmsg.UnitManifestEntry(nil), fold.UnitManifest...)
		copied.PendingRequests = append([]procmsg.ActionRequest(nil), fold.PendingRequests...)
		if fold.Purchase != nil {
			purchase := *fold.Purchase
			copied.Purchase = &purchase
		}
		if fold.Cancellation != nil {
			cancellation := *fold.Cancellation
			copied.Cancellation = &cancellation
		}
		if fold.Intent != nil {
			intent := *fold.Intent
			copied.Intent = &intent
		}
		if fold.Attention != nil {
			attention := *fold.Attention
			copied.Attention = &attention
		}
		if fold.Dispute != nil {
			dispute := *fold.Dispute
			copied.Dispute = &dispute
		}
		if fold.Requests != nil {
			copied.Requests = make(map[string]string, len(fold.Requests))
			for requestID, state := range fold.Requests {
				copied.Requests[requestID] = state
			}
		}
		clone.MerchantOrders[id] = &copied
	}
	clone.Units = make(map[string]*UnitFold, len(w.Units))
	for id, unit := range w.Units {
		copied := *unit
		clone.Units[id] = &copied
	}
	if w.FundingByAllocation != nil {
		clone.FundingByAllocation = make(map[string]procmsg.FundingResult, len(w.FundingByAllocation))
		for id, state := range w.FundingByAllocation {
			clone.FundingByAllocation[id] = state
		}
	}
	return clone
}

func cloneEffects(source map[string]EffectExpectation) map[string]EffectExpectation {
	if source == nil {
		return nil
	}
	clone := make(map[string]EffectExpectation, len(source))
	for id, effect := range source {
		clone[id] = effect
	}
	return clone
}

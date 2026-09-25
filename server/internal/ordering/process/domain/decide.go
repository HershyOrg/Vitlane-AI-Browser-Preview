package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

// 이 파일은 stage 전이 규칙의 유일한 표현이다(ADR-0056 §4 → ADR-0070 §4.1).
// arm 순서는 종전과 같다: 수납 합류 → 결제 전·예외(U1·U2) → 환불 재개(U3) →
// 재개 해소(U4) → 물류 단계(U4b) → terminal 판정(U5·U6). 관찰 매체는 Order·MO·
// Unit identity fold이며 주문 단위 수량은 그 집합에서 파생한다. MO 단위 결정
// (phase·취소 라우팅·attention)은 fold 위에서 내리고, Order stage는 종전 arm
// 그대로 파생해 고객 계약 어휘를 보존한다.

// DelayRuleWindowDays는 FTC 30일 rule의 고지·취소 창이다(policy 소유).
const DelayRuleWindowDays = policy.DelayRuleWindowDays

func settlementTerminal(state string) bool {
	return state == "COMPLETED" || state == "REFUNDED"
}

func openPaymentNeedsReconciliation(state string) bool {
	return state == "OUTCOME_UNKNOWN"
}

// eventEffects는 이벤트 하나에서 관찰한 직접 효과와 사유다.
type eventEffects struct {
	currentEventID   int64
	attention        bool
	attentionCode    string
	failureCode      string
	cancelKindReason string
	// Whole-MO compensation effects use the immutable target as their
	// idempotency scope, so replay cannot compensate the same MO twice.
	compensationTriggers []compensationTrigger
	// planPaymentIDs·registerScopes는 각각 조달 plan·물류 등록 Effect의
	// 트리거다(구 inbox·Tick 폴링의 대체 — 컷오버 B).
	planPaymentIDs []string
	planCauses     map[string]int64
	planInputs     map[string]procmsg.PlanFromFundingPayload
	registerScopes []string
	// cards는 고객 통신(Support 카드) Effect의 트리거다(ADR-0070 §4.5) — owner
	// 어댑터·marker 워커의 대체. SupportKey는 종전 발신 멱등 키 그대로다.
	cards []supportCard
}

// supportCard는 카드 Effect draft다. supportKey가 Support 발신 멱등 키이자
// Effect idempotency scope라 컷오버 전후·재결정에서 카드가 중복되지 않는다.
type supportCard struct {
	causedByEventID int64
	card            string
	referenceID     string
	merchantOrderID string
	actionID        string
	supportKey      string
	detail          map[string]string
}

func (e *eventEffects) card(card supportCard) {
	if card.causedByEventID == 0 {
		card.causedByEventID = e.currentEventID
	}
	e.cards = append(e.cards, card)
}

type compensationTrigger struct {
	causedByEventID int64
	merchantOrderID string
	allocationID    string
	cause           string
	scope           string
	requestID       string
	expectedUnitID  string
}

func validateMOTarget(merchantOrderID, allocationID string) error {
	if strings.TrimSpace(merchantOrderID) == "" || strings.TrimSpace(allocationID) == "" {
		return fmt.Errorf("whole-MO effect target is incomplete")
	}
	return nil
}

func (e *eventEffects) triggerCompensation(w *ProcessState, trigger compensationTrigger) {
	trigger.causedByEventID = e.currentEventID
	e.compensationTriggers = append(e.compensationTriggers, trigger)
	fold := w.mo(trigger.merchantOrderID)
	if fold.AllocationID == "" {
		fold.AllocationID = trigger.allocationID
	}
	if fold.CompensationCause == "" {
		fold.CompensationCause = trigger.cause
	}
}

// applyEvent는 이벤트 하나를 process state에 fold하고 직접 효과를 수집한다.
// 파싱 실패는 관찰 누락이 아니라 규격 위반이므로 오류로 올린다(멈춘 주문은
// watchdog·운영 개입 경로로 드러난다 — 조용한 skip 금지).
func applyEvent(w *ProcessState, effects *eventEffects, event Event) error {
	if event.SourceEntityVersion > 0 && event.EntityKey != "" {
		if w.EntityVersions == nil {
			w.EntityVersions = map[string]int64{}
		}
		key := event.Type + ":" + event.EntityKey
		if w.EntityVersions[key] >= event.SourceEntityVersion {
			return nil
		}
		w.EntityVersions[key] = event.SourceEntityVersion
	}
	switch event.Type {
	case procmsg.EventOrderIssued:
		payload, err := procmsg.ParsePayload[procmsg.OrderIssuedPayload](event.Payload)
		if err != nil {
			return err
		}
		w.Rail = payload.Rail
		w.UserID = payload.UserID
		w.Authorization = payload.Authorization
		w.ModelVersion = ProcessModelVersion
		issuedAt := payload.IssuedAt
		w.IssuedAt = &issuedAt

	case procmsg.EventCustomerPaymentStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.CustomerPaymentStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		w.OpenPaymentState, w.OpenPaymentReason = payload.OpenState, payload.OpenReason

	case procmsg.EventCustomerFundingReady:
		payload, err := procmsg.ParsePayload[procmsg.CustomerFundingReadyPayload](event.Payload)
		if err != nil {
			return err
		}
		w.PaymentSucceeded = true
		if payload.Rail == "PAYPAL" {
			w.PayPalSucceeded = true
		}
		w.OpenPaymentState, w.OpenPaymentReason = "", ""
		effects.planPaymentIDs = append(effects.planPaymentIDs, payload.CustomerPaymentID)
		if effects.planCauses == nil {
			effects.planCauses = map[string]int64{}
		}
		effects.planCauses[payload.CustomerPaymentID] = event.ID
		if effects.planInputs == nil {
			effects.planInputs = map[string]procmsg.PlanFromFundingPayload{}
		}
		effects.planInputs[payload.CustomerPaymentID] = procmsg.PlanFromFundingPayload{CustomerPaymentID: payload.CustomerPaymentID, Rail: payload.Rail, AmountMinor: payload.AmountMinor, FundsReceiptID: payload.FundsReceiptID, Positions: append([]procmsg.FundingPositionSnapshot(nil), payload.Positions...)}
		if w.FundingByAllocation == nil {
			w.FundingByAllocation = map[string]procmsg.FundingResult{}
		}
		for _, position := range payload.Positions {
			w.FundingByAllocation[position.AllocationID] = procmsg.FundingResult{PositionID: position.PositionID, State: position.State}
		}

	case procmsg.EventMOCompensationStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.MOCompensationStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.MerchantOrderID == "" {
			return fmt.Errorf("MO compensation event %s has no merchant order", payload.CompensationID)
		}
		fold := w.mo(payload.MerchantOrderID)
		if fold.AllocationID == "" {
			fold.AllocationID = payload.AllocationID
		}
		fold.Compensation = MOCompensationFold{
			ID: payload.CompensationID, State: payload.State,
			Action: payload.Action, Cause: payload.Cause,
		}
		if fold.CompensationCause == "" {
			fold.CompensationCause = payload.Cause
		}
		if payload.State == "SUCCEEDED" {
			effects.card(supportCard{
				card: procmsg.SupportCardRefundStatus, referenceID: payload.CompensationID,
				merchantOrderID: payload.MerchantOrderID,
				supportKey:      "support:mo-compensation:status:" + payload.CompensationID,
			})
		}

	case procmsg.EventSettlementStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.SettlementStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		w.SettlementState, w.SettlementReason = payload.State, payload.Reason

	case procmsg.EventSettlementRefundConflict:
		payload, err := procmsg.ParsePayload[procmsg.SettlementRefundConflictPayload](event.Payload)
		if err != nil {
			return err
		}
		// 종전 giwa infra의 finalize 충돌 직접 UPDATE(ATTENTION_REQUIRED)의
		// 이벤트 표현 — facts에서 파생 불가능했던 전이다.
		effects.attention = true
		effects.attentionCode = payload.Reason

	case procmsg.EventMerchantOrderStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.MerchantOrderStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		if strings.TrimSpace(payload.MerchantOrderID) == "" {
			return fmt.Errorf("merchant order event has no merchant order id")
		}
		fold := w.mo(payload.MerchantOrderID)
		if payload.AllocationID != "" {
			fold.AllocationID = payload.AllocationID
		}
		if payload.TaskID != "" {
			fold.TaskID = payload.TaskID
		}
		if len(payload.Units) > 0 {
			fold.UnitManifest = append([]procmsg.UnitManifestEntry(nil), payload.Units...)
		}
		if len(payload.UnitIDs) > 0 {
			fold.UnitIDs = append([]string(nil), payload.UnitIDs...)
		}
		if payload.ShopDomain != "" {
			fold.ShopDomain = payload.ShopDomain
		}
		fold.OwnerState = payload.State
		fold.FailureCode = payload.FailureCode
		if payload.UnitCount > 0 {
			fold.UnitCount = payload.UnitCount
		}
		if fold.FundingState == "" && fold.AllocationID != "" {
			if state, ok := w.FundingByAllocation[fold.AllocationID]; ok {
				fold.FundingState, fold.FundingPositionID = state.State, state.PositionID
			}
		}
		if payload.State == "FAILED" && payload.FailureCode != "" {
			// 종전 procurement RecordResult 실패의 last_reason_code 직접 쓰기.
			effects.failureCode = payload.FailureCode
			fold.LastReason = payload.FailureCode
		}
		if payload.State == "FAILED" || payload.State == "CANCELLED" {
			if err := validateMOTarget(payload.MerchantOrderID, payload.AllocationID); err != nil {
				return err
			}
			cause := payload.CompensationCause
			if payload.State == "FAILED" {
				cause = procmsg.CompensationCauseProcurementFailure
			}
			if cause == "" {
				return fmt.Errorf("merchant order %s terminal event missing compensation cause", payload.MerchantOrderID)
			}
			if payload.State == "CANCELLED" &&
				cause != procmsg.CompensationCauseCustomerCancelPreEffect &&
				cause != procmsg.CompensationCauseDelayRule {
				return fmt.Errorf("merchant order %s has invalid cancellation compensation cause", payload.MerchantOrderID)
			}
			if payload.State == "CANCELLED" {
				fold.CancellationKind = "PRE_EFFECT"
				if cause == procmsg.CompensationCauseDelayRule {
					fold.CancellationKind = "DELAY_RULE"
				}
			}
			effects.triggerCompensation(w, compensationTrigger{
				merchantOrderID: payload.MerchantOrderID,
				allocationID:    payload.AllocationID,
				cause:           cause,
				scope:           "merchant-order:" + payload.MerchantOrderID,
			})
		}
		// 어떤 전이든 물류 기대의 파생·대사가 필요하다(생성 시 등록, 취소·실패
		// 시 물리 기대 폐기) — 종전 logistics Tick의 merchant_orders 폴링 대체.
		effects.registerScopes = append(effects.registerScopes,
			payload.MerchantOrderID+":"+payload.State)

	case procmsg.EventUnitFulfillmentChanged:
		payload, err := procmsg.ParsePayload[procmsg.UnitFulfillmentChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.MerchantOrderID == "" {
			// procurement의 종전 대리 발행(주문 단위 coverage 관찰)은 unit
			// identity가 없다 — v2 fold는 실제 unit 전이만 접는다.
			return nil
		}
		unit := w.unit(payload.ExpectedUnitID)
		unit.MerchantOrderID = payload.MerchantOrderID
		unit.Fulfillment = payload.Fulfillment
		w.mo(payload.MerchantOrderID)

	case procmsg.EventShipmentStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.ShipmentStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		for _, unitID := range payload.ExpectedUnitIDs {
			unit := w.unit(unitID)
			if unit.MerchantOrderID == "" {
				unit.MerchantOrderID = payload.MerchantOrderID
			}
			unit.ShipmentID = payload.ShipmentID
			unit.ShipmentState = payload.State
		}

	case procmsg.EventReturnStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.ReturnStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		unit := w.unit(payload.ExpectedUnitID)
		if unit.MerchantOrderID == "" {
			unit.MerchantOrderID = payload.MerchantOrderID
		}
		unit.ReturnState = payload.State

	case procmsg.EventMOFundingStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.MOFundingStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		if w.FundingByAllocation == nil {
			w.FundingByAllocation = map[string]procmsg.FundingResult{}
		}
		if payload.AllocationID != "" {
			w.FundingByAllocation[payload.AllocationID] = procmsg.FundingResult{PositionID: payload.PositionID, State: payload.State}
		}
		if payload.MerchantOrderID != "" {
			fold := w.mo(payload.MerchantOrderID)
			fold.FundingState, fold.FundingPositionID = payload.State, payload.PositionID
			if fold.AllocationID == "" {
				fold.AllocationID = payload.AllocationID
			}
		}

	case procmsg.EventEffectLockStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.EffectLockStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		fold := w.mo(payload.MerchantOrderID)
		fold.LockState = payload.State
		if fold.AllocationID == "" {
			fold.AllocationID = payload.AllocationID
		}

	case procmsg.EventCustomerRequestStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.CustomerRequestStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		fold := w.mo(payload.MerchantOrderID)
		if fold.Requests == nil {
			fold.Requests = map[string]string{}
		}
		fold.Requests[payload.RequestID] = payload.State
		if payload.State == "PENDING" {
			effects.card(supportCard{
				card: procmsg.SupportCardProcurementRequest, referenceID: payload.RequestID,
				merchantOrderID: payload.MerchantOrderID,
				supportKey:      "support:procurement:request:" + payload.RequestID,
			})
		} else {
			effects.card(supportCard{
				card: procmsg.SupportCardProcurementResponse, referenceID: payload.RequestID,
				merchantOrderID: payload.MerchantOrderID,
				supportKey:      "support:procurement:resolution:" + payload.RequestID,
			})
		}

	case procmsg.EventProcurementDecisionRecorded:
		payload, err := procmsg.ParsePayload[procmsg.ProcurementDecisionRecordedPayload](event.Payload)
		if err != nil {
			return err
		}
		fold := w.mo(payload.MerchantOrderID)
		fold.LastDecision = payload.Decision
		if payload.Decision == "UNABLE_TO_PURCHASE" {
			// 정상·경미 판단은 내부 감사만, 중대한 조건은 요청 카드가 설명한다
			// (ADR-0066) — 구매 불가만 standalone 카드다.
			effects.card(supportCard{
				card: procmsg.SupportCardProcurementDecision, referenceID: payload.DecisionRecordID,
				merchantOrderID: payload.MerchantOrderID,
				supportKey:      "support:procurement:decision:" + payload.DecisionRecordID,
			})
		}

	case procmsg.EventDisputeStateChanged:
		payload, err := procmsg.ParsePayload[procmsg.DisputeStateChangedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.MerchantOrderID != "" {
			fold := w.mo(payload.MerchantOrderID)
			fold.Dispute = &MODispute{
				CaseID: payload.CaseID, State: payload.State, Outcome: payload.Outcome,
			}
		}
		supportKey := fmt.Sprintf("support:paypal-dispute:webhook:%s:%d", payload.CaseID, payload.Version)
		if payload.ActionID != "" {
			supportKey = "support:paypal-dispute:action:" + payload.ActionID
		}
		effects.card(supportCard{
			card: procmsg.SupportCardPayPalDispute, referenceID: payload.CaseID,
			merchantOrderID: payload.MerchantOrderID, actionID: payload.ActionID,
			supportKey: supportKey,
		})

	case procmsg.EventNoticeRecorded:
		payload, err := procmsg.ParsePayload[procmsg.NoticeRecordedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.Kind == procmsg.NoticeKindDelayRule {
			w.DelayNoticeRecorded = true
			effects.card(supportCard{
				card: procmsg.SupportCardDeliveryDelay, referenceID: payload.IdempotencyKey,
				supportKey: "support:delivery-delay:" + w.agencyOrderIDHint,
			})
		}

	case procmsg.EventReceiptIssued:
		payload, err := procmsg.ParsePayload[procmsg.ReceiptIssuedPayload](event.Payload)
		if err != nil {
			return err
		}
		w.ReceiptIssued = true
		w.ReceiptState = payload.TerminalState
		w.ReceiptTerminalTxFinalized = payload.TerminalTxFinalized

	case procmsg.EventTimerFired:
		payload, err := procmsg.ParsePayload[procmsg.TimerFiredPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.Kind == procmsg.TimerKindDelayRuleNotice {
			w.DelayNoticeDue = true
		}

	case procmsg.EventWatchdogDivergence:
		payload, err := procmsg.ParsePayload[procmsg.WatchdogDivergencePayload](event.Payload)
		if err != nil {
			return err
		}
		effects.attention = true
		effects.attentionCode = "PROCESS_DIVERGENCE"
		_ = payload

	case procmsg.EventRefundReviewDecided:
		payload, err := procmsg.ParsePayload[procmsg.RefundReviewDecidedPayload](event.Payload)
		if err != nil {
			return err
		}
		fold := w.mo(payload.MerchantOrderID)
		fold.RefundRequestState = "RESOLVED"
		fold.RefundDecision = payload.Decision
		effects.card(supportCard{
			card: procmsg.SupportCardRefundDecision, referenceID: payload.RequestID,
			merchantOrderID: payload.MerchantOrderID,
			supportKey:      "support:refund:decision:" + payload.RequestID,
		})
		if payload.Decision == "APPROVED" {
			if err := validateMOTarget(payload.MerchantOrderID, payload.AllocationID); err != nil {
				return err
			}
			effects.triggerCompensation(w, compensationTrigger{
				merchantOrderID: payload.MerchantOrderID,
				allocationID:    payload.AllocationID,
				cause:           procmsg.CompensationCauseCustomerRefundPostEffect,
				requestID:       payload.RequestID,
				scope:           "merchant-order:" + payload.MerchantOrderID,
			})
		}

	case procmsg.EventRefundRequested:
		payload, err := procmsg.ParsePayload[procmsg.RefundRequestedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.MerchantOrderID != "" {
			fold := w.mo(payload.MerchantOrderID)
			fold.RefundRequestState = "REQUESTED"
			fold.RefundDecision = ""
		}
		effects.card(supportCard{
			card: procmsg.SupportCardRefundRequest, referenceID: payload.RequestID,
			merchantOrderID: payload.MerchantOrderID,
			supportKey:      "support:refund:request:" + payload.RequestID,
		})

	case procmsg.EventDeliveryFaultJudged:
		payload, err := procmsg.ParsePayload[procmsg.DeliveryFaultJudgedPayload](event.Payload)
		if err != nil {
			return err
		}
		if payload.Judgment == "REFUND" {
			if err := validateMOTarget(payload.MerchantOrderID, payload.AllocationID); err != nil {
				return err
			}
			effects.triggerCompensation(w, compensationTrigger{
				merchantOrderID: payload.MerchantOrderID,
				allocationID:    payload.AllocationID,
				cause:           procmsg.CompensationCauseDeliveryException,
				expectedUnitID:  payload.ExpectedUnitID,
				scope:           "merchant-order:" + payload.MerchantOrderID,
			})
		}
		effects.card(supportCard{
			card: procmsg.SupportCardDeliveryResolution, referenceID: payload.ResolutionID,
			merchantOrderID: payload.MerchantOrderID,
			supportKey:      "support:delivery-resolution:" + payload.ResolutionID,
		})

	case procmsg.EventFundsReceiptRecorded:
		_, err := procmsg.ParsePayload[procmsg.FundsReceiptRecordedPayload](event.Payload)
		if err != nil {
			return err
		}
		// Receipt remains a cash-evidence event. Planning authority moved to the
		// rail-neutral customer_funding.ready.v2 event so PayPal can plan before
		// its first MO capture and GIWA follows the same application flow.

	default:
		// 카탈로그 밖 타입은 fold에 영향이 없다 — 감사 기록이다.
	}
	return nil
}

// compensationWorkOpen includes observed obligations and unresolved effects.
func compensationWorkOpen(w ProcessState, open []PendingEffect) bool {
	if w.activeCompensations() > 0 {
		return true
	}
	for _, effect := range open {
		if effect.Type == procmsg.EffectCompensateMO {
			return true
		}
	}
	for _, fold := range w.MerchantOrders {
		if fold.CompensationCause != "" && fold.Compensation.State == "" {
			return true
		}
	}
	return false
}

// Unrelated success never resolves attention on a pending business effect.
func unresolvedAttentionRemains(open []PendingEffect) bool {
	for _, effect := range open {
		if effect.NeedsAttention && effect.Type != procmsg.EffectPublishSupportCard {
			return true
		}
	}
	return false
}

// openCompensationByMO는 미종결 보상 Effect의 MO 집합이다(phase 파생 입력).
func openCompensationByMO(open []PendingEffect) map[string]bool {
	result := map[string]bool{}
	for _, effect := range open {
		if effect.Type == procmsg.EffectCompensateMO && effect.MerchantOrderID != "" {
			result[effect.MerchantOrderID] = true
		}
	}
	return result
}

// reduceObservedFact folds exactly one Owner fact before deriving its consequences.
func reduceObservedFact(p Process, event Event, open []PendingEffect, now time.Time) (Decision, error) {
	w := cloneProcessState(p.ProcessState)
	w.agencyOrderIDHint = p.AgencyOrderID
	effects := eventEffects{currentEventID: event.ID}
	if err := applyEvent(&w, &effects, event); err != nil {
		return Decision{}, err
	}
	return deriveDecision(p, w, effects, open, now), nil
}

// StateProjection is diagnostic output; it cannot authorize Owner execution.
type StateProjection struct {
	State          State
	TerminalReason TerminalReason
	LastReasonCode string
}

// ProjectState lets the watchdog compare facts without inventing an input event.
func ProjectState(p Process, now time.Time) StateProjection {
	d := deriveDecision(p, cloneProcessState(p.ProcessState), eventEffects{}, pendingEffects(p.ProcessState), now)
	return StateProjection{State: d.State, TerminalReason: d.TerminalReason, LastReasonCode: d.LastReasonCode}
}

func deriveDecision(p Process, w ProcessState, effects eventEffects, open []PendingEffect, now time.Time) Decision {
	before := p.ProcessState
	w.agencyOrderIDHint = p.AgencyOrderID
	if p.Version == 0 {
		w.ModelVersion = ProcessModelVersion
	}

	state := p.State
	terminalReason := p.TerminalReason
	lastReason := p.LastReasonCode
	if p.Version == 0 {
		state = StateWaitingCustomerPayment
	}
	// 소진 Effect가 하나라도 남아 있으면 운영자 개입 국면은 고정된다 — 어떤
	// arm도 stage를 움직이지 못하고, 무관한 Effect의 성공도 해소가 아니다
	// (ADR-0070 P7). 소진이 아닌 attention(발산·정산 환불 충돌)은 종전처럼
	// 다음 결정의 재파생으로 풀린다.
	attentionHeld := state == StateAttentionRequired &&
		(unresolvedAttentionRemains(open) || w.anyMerchantOrderAttention() != nil)
	if state == StateAttentionRequired && !attentionHeld {
		// 소진 Effect가 전부 해소됐다(해당 Owner 결과 확인) — 운영자 개입
		// 국면을 닫고 관찰된 process state에서 stage를 재파생한다. 이번 이벤트에 새
		// attention 효과가 있으면 마지막 arm이 다시 세운다.
		state = StateWaitingCustomerPayment
		terminalReason = ""
		w.OrderAttention = ""
	}

	adoptReason := func(reason string) {
		if reason != "" {
			lastReason = reason
		}
	}

	openCompensation := openCompensationByMO(open)

	// [MO 결정] 취소 라우팅 → 미룬 intent 재평가 → phase 파생.
	drafts := make([]EffectDraft, 0, 4)

	// Compensation drafts immediately participate in the open-work guard, so
	// the order cannot terminate in the effect/event handoff window.
	for _, trigger := range effects.compensationTriggers {
		if fold := w.MerchantOrders[trigger.merchantOrderID]; fold != nil && fold.Compensation.State != "" {
			continue
		}
		drafts = append(drafts, EffectDraft{
			CausedByEventID: trigger.causedByEventID,
			Target:          string(procmsg.TargetPayment),
			Type:            procmsg.EffectCompensateMO,
			Payload: procmsg.ExecuteMOCompensationPayload{
				MerchantOrderID: trigger.merchantOrderID,
				AllocationID:    trigger.allocationID,
				Cause:           trigger.cause,
				RequestID:       trigger.requestID,
				ExpectedUnitID:  trigger.expectedUnitID,
			},
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectCompensateMO, p.AgencyOrderID, trigger.scope),
		})
	}
	for _, paymentID := range effects.planPaymentIDs {
		drafts = append(drafts, EffectDraft{
			CausedByEventID: effects.planCauses[paymentID],
			Target:          string(procmsg.TargetProcurement),
			Type:            procmsg.EffectPlanMerchantOrders,
			Payload:         effects.planInputs[paymentID],
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectPlanMerchantOrders, p.AgencyOrderID, paymentID),
		})
	}
	for _, scope := range effects.registerScopes {
		moID := strings.SplitN(scope, ":", 2)[0]
		mo := w.MerchantOrders[moID]
		drafts = append(drafts, EffectDraft{
			Target:  string(procmsg.TargetLogistics),
			Type:    procmsg.EffectRegisterExpectedUnits,
			Payload: procmsg.RegisterExpectedUnitsPayload{MerchantOrderID: moID, State: mo.OwnerState, Units: append([]procmsg.UnitManifestEntry(nil), mo.UnitManifest...)},
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectRegisterExpectedUnits, p.AgencyOrderID, scope),
		})
	}
	// 고객 통신 카드 — owner 사실이 commit된 뒤에만 도착하는 이벤트에서 파생되며
	// SUPPORT executor가 참조 id로 사실을 읽어 카드를 만든다(ADR-0070 §4.5).
	for _, card := range effects.cards {
		drafts = append(drafts, EffectDraft{
			CausedByEventID: card.causedByEventID,
			Target:          string(procmsg.TargetSupport),
			Type:            procmsg.EffectPublishSupportCard,
			Payload: procmsg.PublishSupportCardPayload{
				Card: card.card, ReferenceID: card.referenceID,
				MerchantOrderID: card.merchantOrderID, ActionID: card.actionID,
				SupportKey: card.supportKey, Detail: card.detail,
			},
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectPublishSupportCard, p.AgencyOrderID, card.supportKey),
		})
	}
	for _, id := range w.sortedMerchantOrderIDs() {
		fold := w.MerchantOrders[id]
		fold.Phase = w.derivePhase(id, fold, openCompensation[id])
	}
	compensationOpen := compensationWorkOpen(w, open)
	merchantOrdersTotal := w.merchantOrdersTotal()
	merchantOrdersOpen := w.merchantOrdersOpen()
	placedUnitsPendingPhysical := w.placedUnitsPendingPhysical()

	// [수납 합류]
	if !attentionHeld && w.PaymentSucceeded &&
		(state == StateWaitingCustomerPayment || state == StatePaymentReconciliation) {
		state = StateProcurementInProgress
		lastReason = ""
	}

	// [U1 — GIWA 결제 전·예외]
	if !attentionHeld && w.SettlementState != "" && !settlementTerminal(w.SettlementState) &&
		state != StateTerminal {
		next := state
		switch {
		case w.SettlementState == "REFUND_PENDING":
			next = StateResolutionInProgress
		case (w.SettlementState == "SUBMISSION_UNKNOWN" || w.SettlementState == "FAILED") &&
			state == StateWaitingCustomerPayment:
			next = StatePaymentReconciliation
		case (w.SettlementState == "AUTHORIZED" || w.SettlementState == "AWAITING_ALLOWANCE" ||
			w.SettlementState == "PAYMENT_SUBMITTED" || w.SettlementState == "SAFE") &&
			state == StatePaymentReconciliation:
			next = StateWaitingCustomerPayment
		}
		if next != state {
			state = next
			adoptReason(w.SettlementReason)
		}
	}

	// [U2 — 중립 결제 전·예외]
	if !attentionHeld && w.OpenPaymentState != "" &&
		(state == StateWaitingCustomerPayment || state == StatePaymentReconciliation) {
		next := StateWaitingCustomerPayment
		if openPaymentNeedsReconciliation(w.OpenPaymentState) {
			next = StatePaymentReconciliation
		}
		if next != state {
			state = next
			adoptReason(w.OpenPaymentReason)
		}
	}

	// [U3 — 보상 재개] 미종결 whole-MO compensation 또는 실행 Effect.
	if !attentionHeld && compensationOpen &&
		(state == StateProcurementInProgress || state == StateLogisticsInProgress ||
			state == StateAttentionRequired || state == StateTerminal) {
		state = StateResolutionInProgress
		terminalReason = ""
	}

	// [U4 — 재개 해소]
	if !attentionHeld && state == StateResolutionInProgress && !compensationOpen &&
		w.PaymentSucceeded && merchantOrdersOpen > 0 &&
		w.SettlementState != "REFUND_PENDING" {
		state = StateProcurementInProgress
	}

	// [U4b — 물류 단계]
	if !attentionHeld &&
		(state == StateProcurementInProgress || state == StateResolutionInProgress) &&
		!compensationOpen && w.PaymentSucceeded &&
		w.SettlementState != "REFUND_PENDING" &&
		merchantOrdersOpen == 0 && placedUnitsPendingPhysical > 0 {
		state = StateLogisticsInProgress
	}

	// [U5 — rail-neutral terminal] GIWA prepayment finality and PayPal
	// authorization/captures are funding milestones, not order completion.
	// Physical completion and whole-MO compensation projection decide the end.
	if !attentionHeld && w.PaymentSucceeded && state != StateAttentionRequired &&
		merchantOrdersTotal > 0 && merchantOrdersOpen == 0 &&
		!compensationOpen && w.SettlementState != "REFUND_PENDING" &&
		!openPaymentNeedsReconciliation(w.OpenPaymentState) &&
		placedUnitsPendingPhysical == 0 {
		state = StateTerminal
		switch {
		case w.allMerchantOrdersCompensated():
			terminalReason = TerminalReasonRefundedAll
		case w.succeededCompensations() > 0:
			terminalReason = TerminalReasonCompletedPartial
		default:
			terminalReason = TerminalReasonCompletedAll
		}
	}

	// 이벤트 직접 사유(조달 실패 코드·취소 실행 라벨)는 이번 이벤트 판단의 마지막
	// 말이다 — 종전 infra 직접 쓰기가 리듀서 관찰 사유를 덮던 순서와 같다.
	if !attentionHeld {
		adoptReason(effects.failureCode)
		adoptReason(effects.cancelKindReason)
	}

	// [ATTENTION — 이벤트 직접 전이] 정산 환불 finalize 충돌·watchdog 발산·
	// Effect 재시도 소진. 종전 직접 UPDATE처럼 이번 이벤트 판단의 마지막 말이며
	// TERMINAL도 다시 연다(운영자 개입 국면). MO 플래그가 남아 있어도 같다.
	if effects.attention {
		state = StateAttentionRequired
		terminalReason = ""
		adoptReason(effects.attentionCode)
		if effects.attentionCode != "" {
			w.OrderAttention = effects.attentionCode
		}
	} else if attention := w.anyMerchantOrderAttention(); attention != nil &&
		state != StateAttentionRequired {
		state = StateAttentionRequired
		terminalReason = ""
		adoptReason(attention.Code)
	}

	// 상태 결론이 유발하는 Effect — 결정적 idempotency key라 재실행이 중복을
	// 만들지 않는다.
	// 사유가 재종결에서 갱신되면(예: 종결 후 전액 환불) 영수증도 갱신 발급한다
	// (D-e) — key에 사유를 넣어 같은 사유 재발급은 Effect 원장에서 dedup된다.
	receiptFinalityReady := w.Rail != "GIWA" || w.SettlementState == "COMPLETED" || terminalReason == TerminalReasonRefundedAll
	if receiptFinalityReady && state == StateTerminal &&
		(terminalReason == TerminalReasonCompletedAll ||
			terminalReason == TerminalReasonCompletedPartial ||
			terminalReason == TerminalReasonRefundedAll) &&
		(!w.ReceiptIssued || w.ReceiptState != string(terminalReason) || (w.Rail == "GIWA" && !w.ReceiptTerminalTxFinalized)) {
		receiptKey := string(terminalReason)
		if w.Rail == "GIWA" {
			receiptKey += ":finalized"
		}
		drafts = append(drafts, EffectDraft{
			Target:  string(procmsg.TargetAgencyOrder),
			Type:    procmsg.EffectIssueReceipt,
			Payload: struct{}{},
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectIssueReceipt, p.AgencyOrderID, receiptKey),
		})
	}
	if w.DelayNoticeDue && state != StateTerminal &&
		w.PaymentSucceeded && !w.DelayNoticeRecorded &&
		(merchantOrdersOpen > 0 || placedUnitsPendingPhysical > 0) {
		// 종전 EnsureDelayRuleNotices 스캔의 자격 검사와 동일한 조건·동일한
		// notice idempotency key('delay-rule-30d:{order}')다 — 컷오버 전후
		// 고지가 이중 발송되지 않는다. Due는 상태라, 타이머 발화 시점에 조달
		// 관찰이 아직 없어도 이후 관찰이 미배송 작업을 보이면 고지한다(종전
		// 스캔의 "매 tick 재검사" 의미 보존).
		noticeKey := "delay-rule-30d:" + p.AgencyOrderID
		drafts = append(drafts, EffectDraft{
			Target: string(procmsg.TargetAgencyOrder),
			Type:   procmsg.EffectSendNotice,
			Payload: procmsg.SendNoticePayload{
				Kind:           procmsg.NoticeKindDelayRule,
				IdempotencyKey: noticeKey,
			},
			IdempotencyKey: procmsg.EffectIdempotencyKey(
				procmsg.EffectSendNotice, p.AgencyOrderID, "delay-rule-30d"),
		})
	}

	// 시간 기반 결정의 단일 타이머(ADR-0056 §4) — 수납 완료·미고지 주문만
	// 발행+30일에 깨운다. 발화 뒤에는 Due 상태가 이어받으므로 다시 서지 않는다
	// (고지 결과는 notice.recorded 이벤트가 DelayNoticeRecorded로 봉인한다).
	var wake *time.Time
	if state != StateTerminal && w.PaymentSucceeded && !w.DelayNoticeRecorded &&
		w.IssuedAt != nil && !w.DelayNoticeDue {
		at := w.IssuedAt.AddDate(0, 0, DelayRuleWindowDays)
		wake = &at
	}
	_ = now

	return Decision{
		State:          state,
		TerminalReason: terminalReason,
		LastReasonCode: lastReason,
		ProcessState:   w,
		Effects:        drafts,
		WakeAt:         wake,
		StageChanged: state != p.State || terminalReason != p.TerminalReason ||
			lastReason != p.LastReasonCode,
		MerchantOrders: w.merchantOrderDecisions(before),
	}
}

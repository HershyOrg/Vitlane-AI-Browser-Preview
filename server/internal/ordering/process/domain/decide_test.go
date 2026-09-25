package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

func processEvent(t *testing.T, seq int64, eventType string, payload any) Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Event{Seq: seq, Type: eventType, Payload: raw, OccurredAt: time.Unix(seq, 0)}
}

// fundedProcessState는 수납 완료·현재 ProcessState가 선 주문의 기준 process state다.
func fundedProcessState() ProcessState {
	return ProcessState{PaymentSucceeded: true, ModelVersion: ProcessModelVersion}
}

// withMO는 fold에 MO 관찰을 더한다(테스트 빌더).
func withMO(w ProcessState, id string, fold MOState) ProcessState {
	if w.MerchantOrders == nil {
		w.MerchantOrders = map[string]*MOState{}
	}
	if fold.AllocationID == "" {
		fold.AllocationID = "allocation-" + id
	}
	copied := fold
	w.MerchantOrders[id] = &copied
	return w
}

func withUnit(w ProcessState, id, merchantOrderID, fulfillment string) ProcessState {
	if w.Units == nil {
		w.Units = map[string]*UnitFold{}
	}
	w.Units[id] = &UnitFold{MerchantOrderID: merchantOrderID, Fulfillment: fulfillment}
	return w
}

func runningProcess(process_state ProcessState) Process {
	if process_state.ModelVersion == 0 {
		process_state.ModelVersion = ProcessModelVersion
	}
	return Process{
		AgencyOrderID: "order-1", State: StateProcurementInProgress,
		Version: 3, ProcessState: process_state,
	}
}

func findEffect(t *testing.T, decision Decision, effectType string) EffectDraft {
	t.Helper()
	for _, effect := range decision.Effects {
		if effect.Type == effectType {
			return effect
		}
	}
	t.Fatalf("effect %s not found in %+v", effectType, decision.Effects)
	return EffectDraft{}
}

func hasEffect(decision Decision, effectType string) bool {
	for _, effect := range decision.Effects {
		if effect.Type == effectType {
			return true
		}
	}
	return false
}

func TestFundingReadyPlansBeforeFirstMOCapture(t *testing.T) {
	now := time.Unix(100, 0)
	p, _ := reduceAtForTest(t, Process{AgencyOrderID: "order-1"},
		processEvent(t, 1, procmsg.EventOrderIssued, procmsg.OrderIssuedPayload{
			UserID: "user-1", Rail: "PAYPAL", IssuedAt: now,
		}), now)
	_, decision := reduceAtForTest(t, p, processEvent(t, 2, procmsg.EventCustomerFundingReady,
		procmsg.CustomerFundingReadyPayload{
			CustomerPaymentID: "payment-1", Rail: "PAYPAL",
			Source: "PAYPAL_AUTHORIZATION", ProviderEnvironment: "SANDBOX",
		}), now)
	if decision.State != StateProcurementInProgress || !decision.ProcessState.PaymentSucceeded ||
		decision.ProcessState.ModelVersion != ProcessModelVersion {
		t.Fatalf("decision=%+v", decision)
	}
	effect := findEffect(t, decision, procmsg.EffectPlanMerchantOrders)
	if effect.IdempotencyKey != "procurement.plan_from_funding.v2:order-1:payment-1" {
		t.Fatalf("key=%q", effect.IdempotencyKey)
	}
}

func TestMerchantOrderFailureIssuesWholeMOCompensation(t *testing.T) {
	decision, err := reduceObservedFact(runningProcess(withMO(fundedProcessState(), "mo-2",
		MOState{OwnerState: "PLANNED"})), processEvent(t, 5, procmsg.EventMerchantOrderStateChanged,
		procmsg.MerchantOrderStateChangedPayload{
			MerchantOrderID: "mo-1", AllocationID: "allocation-1",
			ShopDomain: "shop-a", State: "FAILED", FailureCode: "OUT_OF_STOCK",
			UnitCount: 2,
		}), nil, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	effect := findEffect(t, decision, procmsg.EffectCompensateMO)
	payload := effect.Payload.(procmsg.ExecuteMOCompensationPayload)
	if payload.MerchantOrderID != "mo-1" || payload.AllocationID != "allocation-1" ||
		payload.Cause != procmsg.CompensationCauseProcurementFailure ||
		effect.IdempotencyKey != "payment.execute_mo_compensation.v1:order-1:merchant-order:mo-1" {
		t.Fatalf("effect=%+v", effect)
	}
	if decision.State != StateResolutionInProgress || decision.LastReasonCode != "OUT_OF_STOCK" {
		t.Fatalf("decision=%+v", decision)
	}
	failed := decision.ProcessState.MerchantOrders["mo-1"]
	if failed.Phase != MOPhaseCompensating || failed.CompensationCause != procmsg.CompensationCauseProcurementFailure ||
		failed.UnitCount != 2 {
		t.Fatalf("mo-1 fold=%+v", failed)
	}
	if decision.ProcessState.MerchantOrders["mo-2"].Phase != MOPhasePlanned {
		t.Fatalf("sibling phase=%+v", decision.ProcessState.MerchantOrders["mo-2"])
	}
}

func TestCancelledMerchantOrderRequiresExplicitCause(t *testing.T) {
	_, err := reduceObservedFact(runningProcess(fundedProcessState()), processEvent(t, 5, procmsg.EventMerchantOrderStateChanged,
		procmsg.MerchantOrderStateChangedPayload{
			MerchantOrderID: "mo-1", AllocationID: "allocation-1", State: "CANCELLED",
		}), nil, time.Unix(100, 0))
	if err == nil {
		t.Fatal("cancelled merchant order without a typed cause must fail closed")
	}
}

func TestApprovedRefundReviewTargetsOneMO(t *testing.T) {
	decision, err := reduceObservedFact(runningProcess(withMO(fundedProcessState(), "mo-2",
		MOState{OwnerState: "PLACED", UnitCount: 1})), processEvent(t, 6, procmsg.EventRefundReviewDecided,
		procmsg.RefundReviewDecidedPayload{
			RequestID: "request-1", MerchantOrderID: "mo-2",
			AllocationID: "allocation-2", Decision: "APPROVED",
		}), nil, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	effect := findEffect(t, decision, procmsg.EffectCompensateMO)
	payload := effect.Payload.(procmsg.ExecuteMOCompensationPayload)
	if payload.MerchantOrderID != "mo-2" || payload.RequestID != "request-1" ||
		payload.Cause != procmsg.CompensationCauseCustomerRefundPostEffect {
		t.Fatalf("payload=%+v", payload)
	}
	fold := decision.ProcessState.MerchantOrders["mo-2"]
	if fold.Phase != MOPhaseCompensating || fold.RefundRequestState != "RESOLVED" ||
		fold.RefundDecision != "APPROVED" {
		t.Fatalf("fold=%+v", fold)
	}
}

func TestDeliveryFaultCarriesMOAndEvidenceIdentity(t *testing.T) {
	decision, err := reduceObservedFact(runningProcess(withMO(fundedProcessState(), "mo-3",
		MOState{OwnerState: "PLACED"})), processEvent(t, 7, procmsg.EventDeliveryFaultJudged,
		procmsg.DeliveryFaultJudgedPayload{
			ResolutionID: "resolution-1", ExpectedUnitID: "unit-9",
			MerchantOrderID: "mo-3", AllocationID: "allocation-3",
			Cause: "MISSING", Judgment: "REFUND",
		}), nil, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	effect := findEffect(t, decision, procmsg.EffectCompensateMO)
	payload := effect.Payload.(procmsg.ExecuteMOCompensationPayload)
	if payload.MerchantOrderID != "mo-3" || payload.ExpectedUnitID != "unit-9" ||
		payload.Cause != procmsg.CompensationCauseDeliveryException {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestCompensationProjectionDrivesTerminalReason(t *testing.T) {
	tests := []struct {
		name       string
		build      func() ProcessState
		wantState  State
		wantReason TerminalReason
	}{
		{name: "normal completion", build: func() ProcessState {
			w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1})
			return withUnit(w, "unit-1", "mo-1", "DELIVERED_EXPECTED")
		}, wantState: StateTerminal, wantReason: TerminalReasonCompletedAll},
		{name: "one of two MOs compensated", build: func() ProcessState {
			w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1})
			w = withUnit(w, "unit-1", "mo-1", "DELIVERED_EXPECTED")
			return withMO(w, "mo-2", MOState{OwnerState: "FAILED",
				Compensation: MOCompensationFold{ID: "c-2", State: "SUCCEEDED", Action: "VOID"}})
		}, wantState: StateTerminal, wantReason: TerminalReasonCompletedPartial},
		{name: "every MO compensated", build: func() ProcessState {
			return withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "CANCELLED",
				Compensation: MOCompensationFold{ID: "c-1", State: "SUCCEEDED", Action: "VOID"}})
		}, wantState: StateTerminal, wantReason: TerminalReasonRefundedAll},
		{name: "execution pending", build: func() ProcessState {
			return withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "FAILED",
				Compensation: MOCompensationFold{ID: "c-1", State: "EXECUTION_PENDING", Action: "REFUND"}})
		}, wantState: StateResolutionInProgress},
		{name: "placed unit still in transit", build: func() ProcessState {
			w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 2})
			w = withUnit(w, "unit-1", "mo-1", "DELIVERED_EXPECTED")
			return withUnit(w, "unit-2", "mo-1", "IN_TRANSIT_EXPECTED")
		}, wantState: StateLogisticsInProgress},
		{name: "placed unit not yet registered", build: func() ProcessState {
			return withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1})
		}, wantState: StateLogisticsInProgress},
		{name: "sibling still planned keeps procurement", build: func() ProcessState {
			w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1})
			w = withUnit(w, "unit-1", "mo-1", "IN_TRANSIT_EXPECTED")
			return withMO(w, "mo-2", MOState{OwnerState: "PLANNED"})
		}, wantState: StateProcurementInProgress},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := projectDecisionForTest(runningProcess(test.build()), nil, time.Unix(100, 0))
			if decision.State != test.wantState || decision.TerminalReason != test.wantReason {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestGIWAFinalityDoesNotCompleteOrderBeforeMOs(t *testing.T) {
	w := fundedProcessState()
	w.SettlementState = "COMPLETED"
	decision := projectDecisionForTest(runningProcess(w), nil, time.Unix(100, 0))
	if decision.State == StateTerminal {
		t.Fatal("prepaid settlement finality is not order completion")
	}
}

func TestOpenCompensationEffectGuardsTerminal(t *testing.T) {
	w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "FAILED"})
	decision := projectDecisionForTest(runningProcess(w), []PendingEffect{{
		Type: procmsg.EffectCompensateMO, MerchantOrderID: "mo-1",
	}}, time.Unix(100, 0))
	if decision.State != StateResolutionInProgress ||
		decision.ProcessState.MerchantOrders["mo-1"].Phase != MOPhaseCompensating {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestTriggeredCauseWithoutCompensationRowStaysOpen(t *testing.T) {
	// 보상 Effect가 REJECTED로 닫혀 보상 행이 생기지 않아도 자금 obligation은
	// 열려 있다 — 조용히 TERMINAL로 넘어가지 않는다(ADR-0070 §4.1).
	w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "FAILED",
		CompensationCause: procmsg.CompensationCauseProcurementFailure})
	decision := projectDecisionForTest(runningProcess(w), nil, time.Unix(100, 0))
	fold := decision.ProcessState.MerchantOrders["mo-1"]
	if decision.State != StateResolutionInProgress || fold.Phase != MOPhaseCompensating {
		t.Fatalf("decision=%+v fold=%+v", decision, fold)
	}
}

func TestExhaustedCompensationEffectGuardsTerminal(t *testing.T) {
	w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "FAILED"})
	decision := projectDecisionForTest(runningProcess(w), []PendingEffect{{
		ID: "effect-a", Type: procmsg.EffectCompensateMO,
		NeedsAttention: true, MerchantOrderID: "mo-1",
	}}, time.Unix(100, 0))
	if decision.State != StateResolutionInProgress {
		t.Fatalf("state=%s", decision.State)
	}
}

func TestFundingObservedBeforePlanningIsAdoptedByMerchantOrder(t *testing.T) {
	now := time.Unix(100, 0)
	p, _ := reduceAtForTest(t, runningProcess(fundedProcessState()),
		processEvent(t, 3, procmsg.EventMOFundingStateChanged, procmsg.MOFundingStateChangedPayload{
			PositionID: "position-1", AllocationID: "allocation-1", Rail: "PAYPAL", State: "AVAILABLE",
		}), now)
	_, decision := reduceAtForTest(t, p, processEvent(t, 4, procmsg.EventMerchantOrderStateChanged,
		procmsg.MerchantOrderStateChangedPayload{
			MerchantOrderID: "mo-1", AllocationID: "allocation-1", ShopDomain: "shop-a",
			State: "PLANNED", UnitCount: 3,
		}), now)
	fold := decision.ProcessState.MerchantOrders["mo-1"]
	if fold.FundingState != "AVAILABLE" || fold.Phase != MOPhasePlanned || fold.UnitCount != 3 {
		t.Fatalf("fold=%+v", fold)
	}
	if !hasEffect(decision, procmsg.EffectRegisterExpectedUnits) {
		t.Fatalf("registration effect not issued: %+v", decision.Effects)
	}
}

func TestMerchantOrderDecisionsReportOnlyChangedFolds(t *testing.T) {
	w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLANNED", Phase: MOPhasePlanned})
	w = withMO(w, "mo-2", MOState{OwnerState: "PLANNED", Phase: MOPhasePlanned})
	decision, err := reduceObservedFact(runningProcess(w), processEvent(t, 5, procmsg.EventEffectLockStateChanged, procmsg.EffectLockStateChangedPayload{
		MerchantOrderID: "mo-2", AllocationID: "allocation-mo-2", State: "STARTED",
	}), nil, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]MerchantOrderDecision{}
	for _, merchantOrder := range decision.MerchantOrders {
		if merchantOrder.Changed {
			changed[merchantOrder.MerchantOrderID] = merchantOrder
		}
	}
	if len(changed) != 1 || changed["mo-2"].Phase != MOPhasePurchasing ||
		changed["mo-2"].PhaseBefore != MOPhasePlanned {
		t.Fatalf("decisions=%+v", decision.MerchantOrders)
	}
}

// 고객 통신 카드는 리듀서가 owner 이벤트에서 SUPPORT Effect로 발행한다(ADR-0070
// §4.5). 카드 key는 종전 어댑터의 발신 멱등 키와 같다.
func supportCards(decision Decision) map[string]procmsg.PublishSupportCardPayload {
	cards := map[string]procmsg.PublishSupportCardPayload{}
	for _, effect := range decision.Effects {
		if effect.Type != procmsg.EffectPublishSupportCard || effect.Target != string(procmsg.TargetSupport) {
			continue
		}
		payload := effect.Payload.(procmsg.PublishSupportCardPayload)
		cards[payload.SupportKey] = payload
	}
	return cards
}

func TestRefundRequestDraftsSupportCard(t *testing.T) {
	process_state := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1})
	decision, err := reduceObservedFact(runningProcess(process_state), processEvent(t, 4, procmsg.EventRefundRequested, procmsg.RefundRequestedPayload{
		RequestID: "request-1", MerchantOrderID: "mo-1",
	}), nil, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	cards := supportCards(decision)
	card, ok := cards["support:refund:request:request-1"]
	if !ok || card.Card != procmsg.SupportCardRefundRequest || card.ReferenceID != "request-1" ||
		card.MerchantOrderID != "mo-1" {
		t.Fatalf("refund request card=%+v cards=%+v", card, cards)
	}
	effect := findEffect(t, decision, procmsg.EffectPublishSupportCard)
	if effect.IdempotencyKey != "support.publish_card.v1:order-1:support:refund:request:request-1" {
		t.Fatalf("card idempotency key=%s", effect.IdempotencyKey)
	}
}

func TestProcurementDecisionCardOnlyForUnableToPurchase(t *testing.T) {
	for decisionKind, want := range map[string]bool{
		"WITHIN_AUTHORIZATION": false, "IMMATERIAL_VARIANCE": false,
		"MATERIAL_NEW_CONDITION": false, "UNABLE_TO_PURCHASE": true,
	} {
		process_state := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLANNED", UnitCount: 1})
		decision, err := reduceObservedFact(runningProcess(process_state), processEvent(t, 4, procmsg.EventProcurementDecisionRecorded,
			procmsg.ProcurementDecisionRecordedPayload{
				DecisionRecordID: "decision-1", MerchantOrderID: "mo-1", Decision: decisionKind,
			}), nil, time.Unix(200, 0))
		if err != nil {
			t.Fatal(err)
		}
		_, got := supportCards(decision)["support:procurement:decision:decision-1"]
		if got != want {
			t.Fatalf("decision=%s card issued=%v want=%v", decisionKind, got, want)
		}
	}
}

// These tests inspect the state projection with explicit unresolved expectations.
func projectDecisionForTest(p Process, open []PendingEffect, now time.Time) Decision {
	return deriveDecision(p, cloneProcessState(p.ProcessState), eventEffects{}, open, now)
}

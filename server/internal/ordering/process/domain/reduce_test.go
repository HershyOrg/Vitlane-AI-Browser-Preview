package domain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

var reducerNow = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

func reducerFixture() Process {
	issued := reducerNow.Add(-31 * 24 * time.Hour)
	s := ProcessState{UserID: "customer", IssuedAt: &issued, Rail: "PAYPAL", PaymentSucceeded: true, PayPalSucceeded: true, ModelVersion: ProcessModelVersion,
		Authorization:  procmsg.OrderAuthorization{Kind: "MANUAL_OPERATOR_PURCHASE", Hash: "approval", ExecutionProfileHash: "profile", ExecutionMode: "SIMULATED_NO_EFFECT"},
		MerchantOrders: map[string]*MOState{}, Units: map[string]*UnitFold{}}
	for _, id := range []string{"mo-a", "mo-b"} {
		s.MerchantOrders[id] = &MOState{AllocationID: "allocation-" + id, TaskID: "task-" + id, OwnerState: "PLANNED", FundingState: "AVAILABLE", FundingPositionID: "funding-" + id, UnitCount: 1, UnitIDs: []string{"unit-" + id}}
		s.Units["expected-"+id] = &UnitFold{MerchantOrderID: id, Fulfillment: "AWAITING_EFFECT"}
	}
	return Process{AgencyOrderID: "order", Version: 1, State: StateProcurementInProgress, ProcessState: s}
}
func reducerRequest(id, mo string, kind procmsg.RequestKind) procmsg.ActionRequest {
	r := procmsg.ActionRequest{ID: id, AgencyOrderID: "order", MerchantOrderID: mo, Kind: kind, ActorID: "operator", ActorRole: "OPERATOR", TaskID: "task-" + mo}
	if kind == procmsg.RequestCancel || kind == procmsg.RequestRefund || kind == procmsg.RequestCustomerResponse {
		r.ActorID = "customer"
		r.ActorRole = "CUSTOMER"
		r.CancelKind = "PRE_EFFECT"
	}
	return r
}
func reducerEvent(id int64, kind string, payload any, source string) Event {
	b, _ := json.Marshal(payload)
	return Event{ID: id, Seq: id, Type: kind, Payload: b, Source: source, OccurredAt: reducerNow}
}
func requested(id int64, r procmsg.ActionRequest) Event {
	return reducerEvent(id, procmsg.EventActionRequested, r, r.ActorRole)
}
func reduceForTest(t *testing.T, p Process, event Event) (Process, Decision) {
	t.Helper()
	return reduceAtForTest(t, p, event, reducerNow)
}
func reduceAtForTest(t *testing.T, p Process, event Event, now time.Time) (Process, Decision) {
	t.Helper()
	d, err := Reduce(p, event, now)
	if err != nil {
		t.Fatal(err)
	}
	p.ProcessState = d.ProcessState
	p.State = d.State
	p.TerminalReason = d.TerminalReason
	p.LastReasonCode = d.LastReasonCode
	p.Version++
	p.LastAppliedSeq = event.Seq
	p.WakeAt = d.WakeAt
	return p, d
}
func onlyEffect(t *testing.T, d Decision, kind string) EffectDraft {
	t.Helper()
	var found []EffectDraft
	for _, effect := range d.Effects {
		if effect.Type == kind {
			found = append(found, effect)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s, got %+v", kind, d.Effects)
	}
	return found[0]
}
func effectReportEvent(effect EffectDraft, outcome, code string, result any) Event {
	b, _ := json.Marshal(effect.Payload)
	var scope struct {
		MerchantOrderID string `json:"merchantOrderId"`
		RequestID       string `json:"requestId"`
	}
	_ = json.Unmarshal(b, &scope)
	raw, _ := json.Marshal(result)
	return reducerEvent(100, procmsg.EventEffectReported, procmsg.EffectReport{EffectID: procmsg.EffectIdentity("order", effect.IdempotencyKey), EffectType: effect.Type, MerchantOrderID: scope.MerchantOrderID, RequestID: scope.RequestID, Outcome: outcome, Code: code, Result: raw}, effect.Target)
}

func TestReducerPurchaseAndCancelRaceBothOrders(t *testing.T) {
	for _, purchaseFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "purchase-first", false: "cancel-first"}[purchaseFirst], func(t *testing.T) {
			p := requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase))
			c := requested(2, reducerRequest("cancel", "mo-a", procmsg.RequestCancel))
			events := []Event{p, c}
			if !purchaseFirst {
				events = []Event{c, p}
			}
			state, first := reduceForTest(t, reducerFixture(), events[0])
			_, second := reduceForTest(t, state, events[1])
			if len(first.Receipts) != 1 || first.Receipts[0].Outcome != "ACCEPTED" || len(second.Receipts) != 1 || second.Receipts[0].Outcome != "REJECTED" {
				t.Fatalf("incorrect successive decisions: first=%+v second=%+v", first.Receipts, second.Receipts)
			}
			if second.Receipts[0].Guidance.ReasonCode != "EFFECT_IN_PROGRESS" {
				t.Fatalf("missing conflict guidance: %+v", second.Receipts[0])
			}
			if purchaseFirst {
				onlyEffect(t, first, procmsg.EffectReservePurchase)
			} else {
				onlyEffect(t, first, procmsg.EffectReserveCancellation)
			}
			if len(first.Effects) != 1 || len(second.Effects) != 0 {
				t.Fatalf("conflicting effects: first=%+v second=%+v", first.Effects, second.Effects)
			}
		})
	}
}

func TestReducerPendingEffectsBlockConditionChangesBeforeOwnerRuns(t *testing.T) {
	p, _ := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	for _, kind := range []procmsg.RequestKind{procmsg.RequestManualDecision, procmsg.RequestCustomerQuestion, procmsg.RequestCustomerResponse, procmsg.RequestCloseCustomerQuestion, procmsg.RequestRefund, procmsg.RequestRefundDecision} {
		t.Run(string(kind), func(t *testing.T) {
			_, d := reduceForTest(t, p, requested(2, reducerRequest("change", "mo-a", kind)))
			if len(d.Effects) != 0 || len(d.Receipts) != 1 || d.Receipts[0].Outcome != "REJECTED" || d.Receipts[0].Guidance.ReasonCode != "EFFECT_IN_PROGRESS" {
				t.Fatalf("pending purchase bypass: %+v %+v", d.Effects, d.Receipts)
			}
		})
	}
}

func TestReducerSiblingMOStillProceeds(t *testing.T) {
	p, _ := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase-a", "mo-a", procmsg.RequestPurchase)))
	_, d := reduceForTest(t, p, requested(2, reducerRequest("purchase-b", "mo-b", procmsg.RequestPurchase)))
	if d.Receipts[0].Outcome != "ACCEPTED" {
		t.Fatalf("sibling blocked: %+v", d.Receipts)
	}
	onlyEffect(t, d, procmsg.EffectReservePurchase)
}

func TestReducerPurchaseWaitsForVerifiedFundingBeforeMerchantGrant(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	prepare := onlyEffect(t, d, procmsg.EffectReservePurchase)
	p, d = reduceForTest(t, p, effectReportEvent(prepare, "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	funding := onlyEffect(t, d, procmsg.EffectEnsureMOFunding)
	if len(d.Effects) != 1 {
		t.Fatalf("premature merchant grant: %+v", d.Effects)
	}
	p, d = reduceForTest(t, p, effectReportEvent(funding, "EFFECT_UNKNOWN", "CAPTURE_UNKNOWN", nil))
	if len(d.Effects) != 0 || d.Receipts[0].Guidance.OperatorAction != "RECONCILE_PAYMENT" {
		t.Fatalf("unknown is not recoverable waiting: %+v", d)
	}
	_, d = reduceForTest(t, p, requested(3, reducerRequest("cancel", "mo-a", procmsg.RequestCancel)))
	if d.Receipts[0].Outcome != "REJECTED" {
		t.Fatalf("unknown released purchase: %+v", d.Receipts)
	}
	p, d = reduceForTest(t, p, effectReportEvent(funding, "SUCCEEDED", "", procmsg.FundingResult{PositionID: "funding-mo-a", State: "ACTIVE"}))
	grant := onlyEffect(t, d, procmsg.EffectGrantMerchantPurchase)
	p, d = reduceForTest(t, p, effectReportEvent(grant, "SUCCEEDED", "", nil))
	if p.ProcessState.MerchantOrders["mo-a"].Purchase == nil || d.Receipts[0].Guidance.OperatorAction != "RECORD_MERCHANT_RESULT" {
		t.Fatal("grant was treated as completed merchant purchase")
	}
	_, d = reduceForTest(t, p, effectReportEvent(grant, "SUCCEEDED", "", nil))
	if len(d.Effects) != 0 {
		t.Fatalf("duplicate result issued effects: %+v", d.Effects)
	}
}

func TestReducerFailedFundingNeverGrantsMerchantPermission(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectEnsureMOFunding), "REJECTED", "AUTHORIZATION_EXPIRED", nil))
	onlyEffect(t, d, procmsg.EffectReleasePurchase)
	if len(d.Effects) != 1 || p.ProcessState.MerchantOrders["mo-a"].Purchase == nil {
		t.Fatal("failed funding bypassed cleanup or opened merchant execution")
	}
}

func TestReducerSameEffectWrongOwnerScopeOrOutcomeIsRejected(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	e := effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil)
	for _, mutate := range []func(*Event){func(e *Event) { e.Source = "PAYMENT" }, func(e *Event) {
		var r procmsg.EffectReport
		_ = json.Unmarshal(e.Payload, &r)
		r.MerchantOrderID = "mo-b"
		e.Payload, _ = json.Marshal(r)
	}, func(e *Event) {
		var r procmsg.EffectReport
		_ = json.Unmarshal(e.Payload, &r)
		r.Outcome = "ACKNOWLEDGED"
		e.Payload, _ = json.Marshal(r)
	}} {
		bad := e
		bad.Payload = append([]byte(nil), e.Payload...)
		mutate(&bad)
		if _, err := Reduce(p, bad, reducerNow); err == nil {
			t.Fatal("forged fact accepted")
		}
	}
}

func TestReducerDelayCancellationIsBoundedAndNotConfirmedEarly(t *testing.T) {
	p, _ := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	for i := int64(2); i < 40; i++ {
		r := reducerRequest("delay", "mo-a", procmsg.RequestCancel)
		r.CancelKind = "DELAY_RULE"
		var d Decision
		p, d = reduceForTest(t, p, requested(i, r))
		if len(d.Effects) != 0 || d.Receipts[0].Outcome != "DEFERRED" {
			t.Fatalf("delay was confirmed or executed before purchase result: %+v", d)
		}
	}
	if len(p.ProcessState.MerchantOrders["mo-a"].PendingRequests) != 1 {
		t.Fatal("unbounded deferred requests")
	}
}

func TestReducerDoesNotMutateInputAndIsDeterministic(t *testing.T) {
	p := reducerFixture()
	before, _ := json.Marshal(p)
	e := requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase))
	a, err := Reduce(p, e, reducerNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Reduce(p, e, reducerNow)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("reducer mutated previous state")
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same input yielded different decision")
	}
}

func TestReducerOneEventCanIssueSeveralEffectsWithOneCause(t *testing.T) {
	p := reducerFixture()
	e := reducerEvent(42, procmsg.EventRefundReviewDecided, procmsg.RefundReviewDecidedPayload{
		RequestID: "refund", MerchantOrderID: "mo-a", AllocationID: "allocation-mo-a", Decision: "APPROVED",
	}, "AGENCYORDER")
	_, d := reduceForTest(t, p, e)
	onlyEffect(t, d, procmsg.EffectCompensateMO)
	onlyEffect(t, d, procmsg.EffectPublishSupportCard)
	for _, effect := range d.Effects {
		if effect.CausedByEventID != e.ID {
			t.Fatalf("effect %s lost single-event cause: %d", effect.Type, effect.CausedByEventID)
		}
	}
}

func TestReducerRecordsIntentChangeEvenWhenMOPhaseIsUnchanged(t *testing.T) {
	p := reducerFixture()
	p.ProcessState.MerchantOrders["mo-a"].Phase = MOPhasePlanned
	_, d := reduceForTest(t, p, requested(42, reducerRequest("cancel", "mo-a", procmsg.RequestCancel)))
	for _, mo := range d.MerchantOrders {
		if mo.MerchantOrderID == "mo-a" {
			if !mo.Changed || mo.PhaseBefore != MOPhasePlanned || mo.Phase != MOPhasePlanned {
				t.Fatalf("same-phase intent change disappeared from event audit: %+v", mo)
			}
			return
		}
	}
	t.Fatal("cancelled MO missing from decision")
}

func TestReducerRequiresAnEventAndProjectionDoesNotMutateState(t *testing.T) {
	p := reducerFixture()
	before, _ := json.Marshal(p)
	if _, err := Reduce(p, Event{}, reducerNow); err == nil {
		t.Fatal("empty event used as a reduction trigger")
	}
	projection := ProjectState(p, reducerNow)
	if projection.State != StateProcurementInProgress {
		t.Fatalf("unexpected diagnostic projection: %+v", projection)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("diagnostic projection mutated business state")
	}
}

func TestReducerRegistrationBarrierAndWaitingDoNotReleasePermission(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("buy", "mo-a", procmsg.RequestPurchase)))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	registration := onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration)
	if hasEffect(d, procmsg.EffectEnsureMOFunding) || hasEffect(d, procmsg.EffectGrantMerchantPurchase) {
		t.Fatal("funding before registered exact units")
	}
	p, d = reduceForTest(t, p, effectReportEvent(registration, "WAITING", "LOGISTICS_REGISTRATION_REQUIRED", nil))
	if len(d.Effects) != 0 || d.Receipts[0].Guidance.WaitingFor != procmsg.EffectConfirmPurchaseRegistration {
		t.Fatal("registration wait lost")
	}
	_, d = reduceForTest(t, p, requested(2, reducerRequest("cancel", "mo-a", procmsg.RequestCancel)))
	if d.Receipts[0].Outcome != "REJECTED" {
		t.Fatal("waiting registration released purchase")
	}
	_, d = reduceForTest(t, p, effectReportEvent(registration, "SUCCEEDED", "", nil))
	onlyEffect(t, d, procmsg.EffectEnsureMOFunding)
}

func TestReducerObservedStopDuringFundingWaitsForActualResult(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("buy", "mo-a", procmsg.RequestPurchase)))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	funding := onlyEffect(t, d, procmsg.EffectEnsureMOFunding)
	stop := reducerEvent(4, procmsg.EventMerchantOrderStateChanged, procmsg.MerchantOrderStateChangedPayload{MerchantOrderID: "mo-a", AllocationID: "allocation-mo-a", State: "FAILED", FailureCode: "OWNER_STOPPED"}, "PROCUREMENT")
	p, d = reduceForTest(t, p, stop)
	if hasEffect(d, procmsg.EffectCompensateMO) {
		t.Fatal("compensation raced unresolved capture")
	}
	p, d = reduceForTest(t, p, effectReportEvent(funding, "SUCCEEDED", "", procmsg.FundingResult{PositionID: "funding-mo-a", State: "ACTIVE"}))
	release := onlyEffect(t, d, procmsg.EffectReleasePurchase)
	if hasEffect(d, procmsg.EffectGrantMerchantPurchase) || hasEffect(d, procmsg.EffectCompensateMO) {
		t.Fatal("stop crossed seller grant or release barrier")
	}
	_, d = reduceForTest(t, p, effectReportEvent(release, "SUCCEEDED", "", nil))
	onlyEffect(t, d, procmsg.EffectCompensateMO)
}

func TestReducerEarlyPlacementFactDoesNotLosePendingGrantReport(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("buy", "mo-a", procmsg.RequestPurchase)))
	for _, kind := range []string{procmsg.EffectReservePurchase, procmsg.EffectConfirmPurchaseRegistration} {
		p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, kind), "SUCCEEDED", "", nil))
	}
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectEnsureMOFunding), "SUCCEEDED", "", procmsg.FundingResult{PositionID: "funding-mo-a", State: "ACTIVE"}))
	grant := onlyEffect(t, d, procmsg.EffectGrantMerchantPurchase)
	p, _ = reduceForTest(t, p, reducerEvent(5, procmsg.EventMerchantOrderStateChanged, procmsg.MerchantOrderStateChangedPayload{MerchantOrderID: "mo-a", AllocationID: "allocation-mo-a", State: "PLACED"}, "PROCUREMENT"))
	if p.ProcessState.MerchantOrders["mo-a"].Purchase == nil {
		t.Fatal("lost grant expectation before report")
	}
	p, d = reduceForTest(t, p, effectReportEvent(grant, "SUCCEEDED", "", nil))
	if p.ProcessState.MerchantOrders["mo-a"].Purchase != nil {
		t.Fatal("verified placement did not complete purchase")
	}
	if d.Receipts[len(d.Receipts)-1].Guidance.ReasonCode != "MERCHANT_PLACED" {
		t.Fatal("incorrect completion guidance")
	}
}

func TestReducerRequestSourceCannotForgeActorRole(t *testing.T) {
	e := requested(1, reducerRequest("buy", "mo-a", procmsg.RequestPurchase))
	e.Source = "CUSTOMER"
	if _, err := Reduce(reducerFixture(), e, reducerNow); err == nil {
		t.Fatal("customer event granted operator effect")
	}
}

func TestReducerSupportFailureAndRetryDoNotBlockBusiness(t *testing.T) {
	p := reducerFixture()
	d := Decision{ProcessState: p.ProcessState}
	card := EffectDraft{Type: procmsg.EffectPublishSupportCard, Target: string(procmsg.TargetSupport), Payload: map[string]any{"merchantOrderId": "mo-a"}, IdempotencyKey: "support-card"}
	registerEffect(&d, "order", card, reducerNow)
	p.ProcessState = d.ProcessState
	p, _ = reduceForTest(t, p, effectReportEvent(card, "ATTENTION_REQUIRED", "SUPPORT_UNAVAILABLE", nil))
	if len(p.ProcessState.CommunicationAttention) != 1 || p.ProcessState.MerchantOrders["mo-a"].Attention != nil {
		t.Fatal("support failure escaped communication scope")
	}
	r := reducerRequest("retry-support", "mo-a", procmsg.RequestRetryEffect)
	r.ReferenceID = procmsg.EffectIdentity("order", card.IdempotencyKey)
	p, d = reduceForTest(t, p, requested(2, r))
	retry := onlyEffect(t, d, procmsg.EffectRetryDelivery)
	p, d = reduceForTest(t, p, effectReportEvent(retry, "SUCCEEDED", "", nil))
	if len(d.Receipts) != 1 || d.Receipts[0].Outcome != "COMPLETED" {
		t.Fatal("support retry receipt was lost", d.Receipts)
	}
	_, d = reduceForTest(t, p, requested(3, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	if d.Receipts[0].Outcome != "ACCEPTED" {
		t.Fatal("support failure blocked purchase")
	}
}
func TestReducerUnrelatedSuccessCannotClearMoneyAttention(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	funding := onlyEffect(t, d, procmsg.EffectEnsureMOFunding)
	p, _ = reduceForTest(t, p, effectReportEvent(funding, "EFFECT_UNKNOWN", "CAPTURE_UNKNOWN", nil))
	p, d = reduceForTest(t, p, requested(2, reducerRequest("sibling", "mo-b", procmsg.RequestPurchase)))
	p, _ = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	if p.ProcessState.MerchantOrders["mo-a"].Attention == nil || p.ProcessState.MerchantOrders["mo-a"].Purchase == nil {
		t.Fatal("sibling success cleared unknown capture")
	}
	_, d = reduceForTest(t, p, requested(3, reducerRequest("cancel", "mo-a", procmsg.RequestCancel)))
	if d.Receipts[0].Outcome != "REJECTED" {
		t.Fatal("unknown capture lost exclusion")
	}
}
func TestReducerDeferredDelayResumesOnlyAfterActualMerchantResult(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("purchase", "mo-a", procmsg.RequestPurchase)))
	prepare := onlyEffect(t, d, procmsg.EffectReservePurchase)
	r := reducerRequest("delay", "mo-a", procmsg.RequestCancel)
	r.CancelKind = "DELAY_RULE"
	p, _ = reduceForTest(t, p, requested(2, r))
	p, d = reduceForTest(t, p, effectReportEvent(prepare, "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectEnsureMOFunding), "SUCCEEDED", "", procmsg.FundingResult{PositionID: "funding-mo-a", State: "ACTIVE"}))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectGrantMerchantPurchase), "SUCCEEDED", "", nil))
	if hasEffect(d, procmsg.EffectReserveCancellation) {
		t.Fatal("merchant grant was treated as purchase completion")
	}
	p, d = reduceForTest(t, p, reducerEvent(8, procmsg.EventMerchantOrderStateChanged, procmsg.MerchantOrderStateChangedPayload{MerchantOrderID: "mo-a", State: "PLACED"}, "PROCUREMENT"))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectRegisterExpectedUnits), "SUCCEEDED", "", nil))
	onlyEffect(t, d, procmsg.EffectReserveCancellation)
	if len(p.ProcessState.MerchantOrders["mo-a"].PendingRequests) != 0 {
		t.Fatal("deferred request was not resumed")
	}
}

func TestReducerRepeatedPurchasePreservesUnknownGuidanceAndFlow(t *testing.T) {
	p, d := reduceForTest(t, reducerFixture(), requested(1, reducerRequest("original", "mo-a", procmsg.RequestPurchase)))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectReservePurchase), "SUCCEEDED", "", nil))
	p, d = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectConfirmPurchaseRegistration), "SUCCEEDED", "", nil))
	p, _ = reduceForTest(t, p, effectReportEvent(onlyEffect(t, d, procmsg.EffectEnsureMOFunding), "EFFECT_UNKNOWN", "CAPTURE_UNKNOWN", nil))
	_, d = reduceForTest(t, p, requested(2, reducerRequest("alias", "mo-a", procmsg.RequestPurchase)))
	if len(d.Effects) != 0 || len(d.Receipts) != 1 {
		t.Fatal("repeat purchase created another effect")
	}
	r := d.Receipts[0]
	if r.RequestID != "alias" || r.FlowID != procmsg.RequestFlowID("order", "original") || r.Outcome != "WAITING" || r.Guidance.ReasonCode != "PAYMENT_OUTCOME_UNKNOWN" || r.Guidance.OperatorAction != "RECONCILE_PAYMENT" {
		t.Fatalf("repeat lost actual waiting instruction: %+v", r)
	}
}

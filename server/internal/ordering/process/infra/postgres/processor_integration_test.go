package postgres_test

import (
	"context"
	"errors"
	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencypg "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/postgres"
	"strings"
	"sync"
	"testing"
	"time"

	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	logisticspg "github.com/vitlane/vitlane/server/internal/ordering/logistics/infra/postgres"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentpg "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	processpg "github.com/vitlane/vitlane/server/internal/ordering/process/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg/queue"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	procurementpg "github.com/vitlane/vitlane/server/internal/ordering/procurement/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpg "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type reducerHarness struct {
	ctx       context.Context
	db        *sharedpg.Database
	clock     *testfixture.Clock
	processor *processapp.OrderProcessor
	store     *processpg.Store
	inbox     *queue.Queue
	consumers map[string]interface {
		Accept(context.Context, procmsg.Delivery) error
	}
	procService *procurementapp.Service
	logistics   *logisticsapp.Service
	agency      *agencyapp.LifecycleService
	proc        *procurementpg.Repository
	provider    *testfixture.Provider
	mo, task    string
}

func newReducerHarness(t *testing.T) *reducerHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	db := testfixture.Open(t, ctx)
	clock := &testfixture.Clock{Time: time.Now().UTC()}
	testfixture.SeedOrder(t, ctx, db, clock.Time)
	store := processpg.NewStore(db)
	processor := processapp.NewOrderProcessor(store, clock)
	inbox := queue.New(db, nil)
	proc := procurementpg.NewRepository(db)
	provider := &testfixture.Provider{}
	procService := procurementapp.NewService(proc, nil, nil, clock, false)
	logistics := logisticsapp.NewService(logisticspg.NewRepository(db), clock)
	procService.EnableProcessInputs(queue.NewActionInputs(db, procmsg.TargetProcurement))
	logistics.EnableProcessInputs(queue.NewActionInputs(db, procmsg.TargetLogistics))
	agency := agencyapp.NewLifecycleService(agencypg.NewRepository(db), nil, clock, sharedapp.UUIDGenerator{})
	agency.EnableProcessInputs(queue.NewActionInputs(db, procmsg.TargetAgencyOrder))
	payment := paymentapp.NewService(paymentpg.NewRepository(db), provider, paymentpg.NewInstructionGate(db, nil), db, paymentapp.Config{Environment: "SANDBOX", WebhookID: "reducer-test"}, clock, sharedapp.UUIDGenerator{})
	h := &reducerHarness{ctx: ctx, db: db, procService: procService, logistics: logistics, agency: agency, clock: clock, processor: processor, store: store, inbox: inbox, proc: proc, provider: provider, consumers: map[string]interface {
		Accept(context.Context, procmsg.Delivery) error
	}{"AGENCYORDER": agencyapp.NewEffectConsumer(agency, inbox), "PROCUREMENT": procurementapp.NewEffectConsumer(procService, inbox), "LOGISTICS": logisticsapp.NewEffectConsumer(logistics, inbox), "PAYMENT": paymentapp.NewEffectConsumer(payment, inbox)}}
	err := db.WithinTransaction(ctx, func(tx context.Context) error {
		_, err := procmsg.AppendEvent(tx, db.Queryer(tx), procmsg.ProcessEvent{AgencyOrderID: testfixture.OrderID, Source: procmsg.SourceAgencyOrder, Type: procmsg.EventOrderIssued, DedupKey: "test-issued", Payload: procmsg.OrderIssuedPayload{UserID: testfixture.UserID, Rail: "PAYPAL", IssuedAt: clock.Time, Authorization: procmsg.OrderAuthorization{Kind: "MANUAL_OPERATOR_PURCHASE", Hash: "0x" + strings.Repeat("ab", 32), ExecutionProfileHash: testfixture.ProfileHash, ExecutionMode: "SIMULATED_NO_EFFECT"}}}, clock.Time)
		if err != nil {
			return err
		}
		return paymentpg.EmitCustomerFundingReadyEvent(tx, db.Queryer(tx), testfixture.PaymentID, clock.Time)
	})
	if err != nil {
		t.Fatal(err)
	}
	h.reduce(t)
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	if err := db.DB.QueryRowContext(ctx, `SELECT id::text,(SELECT id::text FROM merchant_order_execution_tasks WHERE merchant_order_id=merchant_orders.id) FROM merchant_orders WHERE agency_order_id=$1`, testfixture.OrderID).Scan(&h.mo, &h.task); err != nil {
		t.Fatal(err)
	}
	if _, _, err := proc.ClaimTask(ctx, h.task, testfixture.OperatorID, "initial-claim", clock.Time.Add(time.Hour), clock.Time); err != nil {
		t.Fatal(err)
	}
	if _, _, err := proc.AuthorizeShippingReveal(ctx, h.task, testfixture.OperatorID, "PLACE_MERCHANT_ORDER", "Verify approved address.", "reducer-correlation", "reducer-reveal", clock.Time); err != nil {
		t.Fatal(err)
	}
	clock.Time = clock.Time.Add(time.Millisecond)
	if _, _, err := proc.RecordManualDecision(ctx, h.task, testfixture.OperatorID, "initial-decision", procurementdomain.DecisionWithinAuthorization, "Approved products match.", "Reducer integration test.", "Exact products and amounts.", procurementdomain.EvidenceMerchantPage, strings.Repeat("ef", 32), clock.Time, clock.Time); err != nil {
		t.Fatal(err)
	}
	h.reduce(t)
	return h
}

func (h *reducerHarness) reduce(t *testing.T) {
	t.Helper()
	if err := h.processor.Tick(h.ctx); err != nil {
		t.Fatal(err)
	}
}
func (h *reducerHarness) deliver(t *testing.T, target string) {
	t.Helper()
	ds, err := h.inbox.Claim(h.ctx, []string{target}, h.clock.Time.Add(time.Minute), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Fatalf("no %s effects", target)
	}
	for _, d := range ds {
		if err := h.consumers[target].Accept(h.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
}
func (h *reducerHarness) request(id string, kind procmsg.RequestKind) procmsg.ActionRequest {
	r := procmsg.ActionRequest{ID: id, AgencyOrderID: testfixture.OrderID, MerchantOrderID: h.mo, TaskID: h.task, ActorID: testfixture.OperatorID, ActorRole: "OPERATOR", Kind: kind}
	if kind == procmsg.RequestCancel {
		r.ActorRole = "CUSTOMER"
		r.ActorID = testfixture.UserID
		r.CancelKind = "PRE_EFFECT"
	}
	return r
}
func (h *reducerHarness) submit(t *testing.T, r procmsg.ActionRequest) procmsg.RequestReceipt {
	t.Helper()
	v, err := h.processor.Submit(h.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReducerProcessorPurchaseThroughIndependentOwners(t *testing.T) {
	h := newReducerHarness(t)
	if r := h.submit(t, h.request("purchase", procmsg.RequestPurchase)); r.Outcome != "ACCEPTED" {
		t.Fatalf("purchase: %+v", r)
	}
	if r := h.submit(t, h.request("cancel-too-late", procmsg.RequestCancel)); r.Outcome != "REJECTED" || r.Guidance.ReasonCode != "EFFECT_IN_PROGRESS" {
		t.Fatalf("pre-owner cancellation: %+v", r)
	}
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	if len(h.provider.Captures) != 0 {
		t.Fatal("capture before registration confirmation")
	}
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	if len(h.provider.Captures) != 1 {
		t.Fatalf("captures=%d", len(h.provider.Captures))
	}
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	var state string
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT state FROM procurement_effect_locks WHERE merchant_order_id=$1`, h.mo).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "STARTED" {
		t.Fatalf("merchant permission=%s", state)
	}
	r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "purchase")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "WAITING" || r.Guidance.OperatorAction != "RECORD_MERCHANT_RESULT" {
		t.Fatalf("grant treated as placement: %+v", r)
	}
}

func TestReducerProcessorConcurrentPurchaseAndCancel(t *testing.T) {
	h := newReducerHarness(t)
	other := processapp.NewOrderProcessor(processpg.NewStore(h.db), h.clock)
	start := make(chan struct{})
	var group sync.WaitGroup
	results := make(chan procmsg.RequestReceipt, 2)
	failures := make(chan error, 2)
	for i, r := range []procmsg.ActionRequest{h.request("purchase", procmsg.RequestPurchase), h.request("cancel", procmsg.RequestCancel)} {
		p := h.processor
		if i == 1 {
			p = other
		}
		group.Add(1)
		go func() { defer group.Done(); <-start; v, err := p.Submit(h.ctx, r); results <- v; failures <- err }()
	}
	close(start)
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	h.reduce(t)
	accepted := 0
	for r := range results {
		// Another submitter may consume our predecessor first. Resolve durable
		// receipts after the worker has drained both requests in separate TXs.
		var err error
		r, err = h.processor.Receipt(h.ctx, testfixture.OrderID, r.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome == "ACCEPTED" {
			accepted++
		} else if r.Outcome != "REJECTED" {
			t.Fatalf("unresolved race: %+v", r)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted conflicting requests=%d", accepted)
	}
	var n int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE merchant_order_id=$1 AND type IN ($2,$3)`, h.mo, procmsg.EffectReservePurchase, procmsg.EffectReserveCancellation).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("conflicting issued effects=%d", n)
	}
}

func TestReducerProcessorIdempotencyUsesBodyAndPreservesAliasReceipt(t *testing.T) {
	h := newReducerHarness(t)
	request := h.request("original", procmsg.RequestPurchase)
	original := h.submit(t, request)
	replay := h.submit(t, request)
	if original != replay {
		t.Fatal("request replay changed receipt")
	}
	request.Kind = procmsg.RequestManualDecision
	if _, err := h.processor.Submit(h.ctx, request); !errors.Is(err, processdomain.ErrRequestConflict) {
		t.Fatalf("key reused for another action: %v", err)
	}
	alias := h.submit(t, h.request("alias", procmsg.RequestPurchase))
	if alias.FlowID != original.FlowID || alias.RequestID != "alias" {
		t.Fatalf("duplicate intent created new flow: %+v", alias)
	}
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	a, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "alias")
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestID != "alias" || a.FlowID != procmsg.RequestFlowID(testfixture.OrderID, "original") || a.Guidance.OperatorAction != "RECORD_MERCHANT_RESULT" {
		t.Fatalf("alias lost identity/progress: %+v", a)
	}
	var effectFlow string
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT flow_id::text FROM order_process_effects WHERE request_id='original' LIMIT 1`).Scan(&effectFlow); err != nil {
		t.Fatal(err)
	}
	if a.FlowID != effectFlow {
		t.Fatalf("receipt/effect correlation diverged: receipt=%s effect=%s", a.FlowID, effectFlow)
	}

}

func TestReducerProcessorUnknownSurvivesRestartAndDoesNotRepeatCapture(t *testing.T) {
	h := newReducerHarness(t)
	h.provider.Unknown = true
	h.submit(t, h.request("purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "purchase")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "WAITING" || r.Guidance.OperatorAction != "RECONCILE_PAYMENT" {
		t.Fatalf("UNKNOWN guidance: %+v", r)
	}
	for _, kind := range []procmsg.RequestKind{procmsg.RequestCancel, procmsg.RequestManualDecision, procmsg.RequestCustomerQuestion} {
		if r := h.submit(t, h.request("during-unknown-"+string(kind), kind)); r.Outcome != "REJECTED" {
			t.Fatalf("UNKNOWN allowed %s: %+v", kind, r)
		}
	}
	h.processor = processapp.NewOrderProcessor(processpg.NewStore(h.db), h.clock)
	h.provider.Unknown = false
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	if len(h.provider.Captures) != 1 {
		t.Fatalf("reconciliation sent another capture: %+v", h.provider.Captures)
	}
	r, err = h.processor.Receipt(h.ctx, testfixture.OrderID, "purchase")
	if err != nil {
		t.Fatal(err)
	}
	if r.Guidance.OperatorAction != "RECORD_MERCHANT_RESULT" {
		t.Fatalf("restarted processor did not progress: %+v", r)
	}
}

type failDecisionStore struct{ *processpg.Store }

func (s failDecisionStore) SaveDecision(context.Context, processapp.DecisionInput, processdomain.Decision, []processapp.EffectWrite, time.Time) (processapp.DecisionOutcome, error) {
	return processapp.DecisionOutcome{}, errors.New("injected crash after effect insertion")
}

func TestReducerProcessorRollbackLeavesDurableRequestForRecovery(t *testing.T) {
	h := newReducerHarness(t)
	broken := processapp.NewOrderProcessor(failDecisionStore{h.store}, h.clock)
	r, err := broken.Submit(h.ctx, h.request("recover-request", procmsg.RequestPurchase))
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "RECEIVED" {
		t.Fatalf("rolled-back decision reported acceptance: %+v", r)
	}
	var n int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE type=$1`, procmsg.EffectReservePurchase).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("effect survived decision rollback")
	}
	h.reduce(t)
	r, err = h.processor.Receipt(h.ctx, testfixture.OrderID, "recover-request")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "ACCEPTED" {
		t.Fatalf("durable request failed to recover: %+v", r)
	}
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE type=$1`, procmsg.EffectReservePurchase).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("recovery did not issue exactly one effect")
	}
}

func TestReducerProcessorRemainsResponsiveDuringProviderIO(t *testing.T) {
	h := newReducerHarness(t)
	h.submit(t, h.request("purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.provider.OnCapture = func(context.Context) { close(started); <-release }
	ds, err := h.inbox.Claim(h.ctx, []string{"PAYMENT"}, h.clock.Time.Add(time.Minute), 50)
	if err != nil || len(ds) != 1 {
		t.Fatalf("claim payment: %v %+v", err, ds)
	}
	done := make(chan error, 1)
	go func() { done <- h.consumers["PAYMENT"].Accept(h.ctx, ds[0]) }()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not reached")
	}
	short, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	r, err := h.processor.Submit(short, h.request("cancel-during-io", procmsg.RequestCancel))
	if err != nil {
		t.Fatal(err)
	}
	// The pending funding fact can precede this request. Consume each event
	// while the provider remains blocked, then check the actual admission.
	if err := h.processor.Tick(short); err != nil {
		t.Fatal(err)
	}
	r, err = h.processor.Receipt(short, testfixture.OrderID, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "REJECTED" || r.Guidance.ReasonCode != "EFFECT_IN_PROGRESS" {
		t.Fatalf("Order lock held through I/O or conflict accepted: %+v", r)
	}
}

func TestReducerProcessorCancellationWaitsForReservationAndRefund(t *testing.T) {
	h := newReducerHarness(t)
	r := h.submit(t, h.request("cancel", procmsg.RequestCancel))
	if r.Outcome != "ACCEPTED" || r.Guidance.WaitingFor != procmsg.EffectReserveCancellation {
		t.Fatalf("cancel intake: %+v", r)
	}
	var count int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM agency_order_cancellations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("request pretended cancellation already happened")
	}
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "WAITING" || r.Guidance.ReasonCode != "CANCELLATION_CONFIRMED_REFUND_PENDING" {
		t.Fatalf("cancel confused with refunded: %+v", r)
	}
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	r, err = h.processor.Receipt(h.ctx, testfixture.OrderID, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "COMPLETED" || r.Guidance.ReasonCode != "CANCELLED_AND_REFUNDED" {
		t.Fatalf("cancel not completed: %+v", r)
	}
	if len(h.provider.Captures) != 0 {
		t.Fatal("cancelled order captured")
	}
	var action string
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT action FROM payment_mo_compensations`).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "VOID" {
		t.Fatalf("unused authorization compensation=%s", action)
	}
}

func (h *reducerHarness) place(t *testing.T) {
	t.Helper()
	h.submit(t, h.request("purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	_, _, err := h.proc.RecordPlaced(h.ctx, h.task, testfixture.OperatorID, "placement", procurementdomain.PlacementEvidence{Kind: procurementdomain.PlacementEvidenceSandboxTest, AmountMode: procurementdomain.PlacementAmountUnchanged, ExternalOrderRef: "TEST-MERCHANT-ORDER", ReceiptSafeRef: "TEST-RECEIPT", Currency: "USD", EvidenceSource: procurementdomain.EvidenceReceipt}, false, h.clock.Time)
	if err != nil {
		t.Fatal(err)
	}
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
}

func TestReducerProcessorDeliveryCancellationBothOrders(t *testing.T) {
	for _, ordering := range []string{"delivery-wins", "cancellation-reserves-first", "delivery-after-refund"} {
		t.Run(ordering, func(t *testing.T) {
			deliveredFirst := ordering == "delivery-wins"
			h := newReducerHarness(t)
			h.place(t)
			logistics := logisticspg.NewRepository(h.db)
			request, err := h.logistics.StageAction(h.ctx, procmsg.ActionRequest{ID: "create-shipment", Kind: procmsg.RequestCreateShipment, MerchantOrderID: h.mo, ActorID: testfixture.OperatorID, ActorRole: "OPERATOR"}, logisticsapp.ActionInput{Carrier: "TEST-CARRIER", TrackingRef: "TEST-TRACKING"})
			if err != nil {
				t.Fatal(err)
			}
			h.submit(t, request)
			h.deliver(t, "LOGISTICS")
			h.reduce(t)
			shipments, err := h.logistics.ListOrderShipments(h.ctx, testfixture.OrderID)
			if err != nil || len(shipments) != 1 {
				t.Fatalf("shipments=%+v err=%v", shipments, err)
			}
			shipment := shipments[0].Shipment

			if _, err := logistics.RecordEvent(h.ctx, shipment.ID, "IN_TRANSIT", "Test carrier transit observation.", testfixture.OperatorID, h.clock.Time, h.clock.Time); err != nil {
				t.Fatal(err)
			}
			h.clock.Time = h.clock.Time.Add(31 * 24 * time.Hour)
			deliver := func() {
				t.Helper()
				if _, err := logistics.ConfirmDelivered(h.ctx, shipment.ID, testfixture.OperatorID, nil, h.clock.Time); err != nil {
					t.Fatal(err)
				}
				h.reduce(t)
			}
			if deliveredFirst {
				deliver()
			}
			r := h.request("delay-cancel", procmsg.RequestCancel)
			r.CancelKind = "DELAY_RULE"
			h.submit(t, r)
			h.reduce(t)
			h.deliver(t, "LOGISTICS")
			h.reduce(t)
			if deliveredFirst {
				r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "delay-cancel")
				if err != nil {
					t.Fatal(err)
				}
				if r.Outcome != "REJECTED" || r.Guidance.ReasonCode != "DELIVERED" {
					t.Fatalf("delivered cancellation accepted: %+v", r)
				}
				var n int
				if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM payment_mo_compensations`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatal("delivered-first path refunded")
				}
				return
			}
			if ordering == "cancellation-reserves-first" {
				deliver()
			}
			h.deliver(t, "PROCUREMENT")
			h.reduce(t)
			h.deliver(t, "LOGISTICS")
			h.reduce(t)
			h.deliver(t, "PAYMENT")
			h.reduce(t)
			if ordering == "delivery-after-refund" {
				deliver()
			}
			rc, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "delay-cancel")
			if err != nil {
				t.Fatal(err)
			}
			if rc.Guidance.ReasonCode != "CANCELLED_DELIVERY_REQUIRES_REVIEW" || rc.Outcome != "COMPLETED" || rc.Guidance.OperatorAction != "REVIEW_RETURN" {
				t.Fatalf("reserved cancellation lost: %+v", rc)
			}
			var units, recovery int
			if err := h.db.DB.QueryRowContext(h.ctx, `SELECT (SELECT count(*) FROM logistics_expected_units WHERE fulfillment='DELIVERED_EXPECTED'),(SELECT count(*) FROM procurement_recovery_entries)`).Scan(&units, &recovery); err != nil {
				t.Fatal(err)
			}
			if units != 1 || recovery != 1 {
				t.Fatalf("late physical fact/recovery lost: delivered=%d recovery=%d", units, recovery)
			}
		})
	}
}

func (h *reducerHarness) action(t *testing.T, key string, kind procmsg.RequestKind, input any) procmsg.RequestReceipt {
	t.Helper()
	r, err := h.procService.StageAction(h.ctx, h.request(key, kind), input)
	if err != nil {
		t.Fatal(err)
	}
	return h.submit(t, r)
}

func TestReducerProcessorPendingEffectBlocksConditionChanges(t *testing.T) {
	for _, conditionFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "condition-first", false: "purchase-first"}[conditionFirst], func(t *testing.T) {
			h := newReducerHarness(t)
			input := procurementapp.RecordManualDecisionInput{Decision: procurementdomain.DecisionWithinAuthorization, PublicRationale: "Approved products still match.", ObservedCondition: "Verified products and amount.", EvidenceSource: procurementdomain.EvidenceMerchantPage, EvidenceHash: strings.Repeat("ab", 32), ObservedAt: h.clock.Time}
			var purchase, condition procmsg.RequestReceipt
			if conditionFirst {
				condition = h.action(t, "change", procmsg.RequestManualDecision, input)
				purchase = h.submit(t, h.request("purchase", procmsg.RequestPurchase))
			} else {
				purchase = h.submit(t, h.request("purchase", procmsg.RequestPurchase))
				condition = h.action(t, "change", procmsg.RequestManualDecision, input)
			}
			accepted, rejected := condition, purchase
			if !conditionFirst {
				accepted, rejected = purchase, condition
			}
			if accepted.Outcome != "ACCEPTED" || rejected.Outcome != "REJECTED" || rejected.Guidance.ReasonCode != "EFFECT_IN_PROGRESS" {
				t.Fatalf("purchase=%+v condition=%+v", purchase, condition)
			}
			h.deliver(t, "PROCUREMENT")
			h.reduce(t)
			if conditionFirst {
				r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "change")
				if err != nil || r.Outcome != "COMPLETED" {
					t.Fatalf("condition result=%+v %v", r, err)
				}
			}
		})
	}
}

func TestReducerProcessorApprovedRefundWaitsForPaymentOwner(t *testing.T) {
	h := newReducerHarness(t)
	h.place(t)
	request, err := h.agency.StageAction(h.ctx, procmsg.ActionRequest{ID: "refund-request", Kind: procmsg.RequestRefund, AgencyOrderID: testfixture.OrderID, MerchantOrderID: h.mo, ActorID: testfixture.UserID, ActorRole: "CUSTOMER"}, agencyapp.RefundInput{ReasonCode: "ITEM_DAMAGED_DEFECTIVE", PublicRationale: "The delivered product is cracked."})
	if err != nil {
		t.Fatal(err)
	}
	h.submit(t, request)
	h.deliver(t, "AGENCYORDER")
	h.reduce(t)
	var refundID string
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT id::text FROM agency_order_refund_requests WHERE merchant_order_id=$1`, h.mo).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	decision, err := h.agency.StageAction(h.ctx, procmsg.ActionRequest{ID: "approve-refund", Kind: procmsg.RequestRefundDecision, ReferenceID: refundID, ActorID: testfixture.OperatorID, ActorRole: "OPERATOR"}, agencyapp.RefundDecision{Approve: true, PublicRationale: "The receipt and damage evidence confirm the problem."})
	if err != nil {
		t.Fatal(err)
	}
	h.submit(t, decision)
	h.deliver(t, "AGENCYORDER")
	h.reduce(t)
	var n int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM payment_mo_compensations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("approval executed Payment in AgencyOrder tx: %d %v", n, err)
	}
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM payment_mo_compensations WHERE state='SUCCEEDED'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refund count=%d err=%v", n, err)
	}
	timeline, err := h.processor.Timeline(h.ctx, testfixture.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Requests) < 3 || len(timeline.Effects) == 0 || len(timeline.Decisions) == 0 {
		t.Fatalf("incomplete timeline: %+v", timeline)
	}
	for _, event := range timeline.Events {
		if event.AppliedVersion == nil {
			t.Fatalf("unapplied event in closed run: %+v", event)
		}
	}
}

func TestReducerProcessorRetryCannotReleaseUnknownMoney(t *testing.T) {
	h := newReducerHarness(t)
	h.provider.Unknown = true
	h.submit(t, h.request("purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	items, err := h.processor.ListInterventions(h.ctx, 50)
	if err != nil || len(items) != 1 {
		t.Fatalf("interventions=%+v %v", items, err)
	}
	retry, err := h.processor.RetryEffect(h.ctx, items[0].EffectID, testfixture.OperatorID, "retry-money")
	if err != nil || retry.Outcome != "ACCEPTED" {
		t.Fatalf("retry=%+v %v", retry, err)
	}
	ds, err := h.inbox.Claim(h.ctx, []string{"PAYMENT"}, h.clock.Time, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Effect.Type == procmsg.EffectRetryDelivery {
			if err := h.inbox.RetryAuthorized(h.ctx, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.reduce(t)
	cancel := h.submit(t, h.request("cancel", procmsg.RequestCancel))
	if cancel.Outcome != "REJECTED" {
		t.Fatalf("retry released unknown money: %+v", cancel)
	}
	if len(h.provider.Captures) != 1 {
		t.Fatalf("retry initiated capture again: %d", len(h.provider.Captures))
	}
}

func TestReducerProcessorPausedPaymentReportsActionableWait(t *testing.T) {
	h := newReducerHarness(t)
	registry, err := paymentapp.NewProviderRegistry(paymentapp.ProviderRegistration{Environment: "SANDBOX", Client: h.provider, WebhookID: "test", IssueEnabled: true, CaptureEnabled: false})
	if err != nil {
		t.Fatal(err)
	}
	payment := paymentapp.NewServiceWithProviderRegistry(paymentpg.NewRepository(h.db), registry, paymentpg.NewInstructionGate(h.db, nil), h.db, paymentapp.Config{Environment: "SANDBOX", WebhookID: "test"}, h.clock, sharedapp.UUIDGenerator{})
	h.consumers["PAYMENT"] = paymentapp.NewEffectConsumer(payment, h.inbox)
	h.submit(t, h.request("paused-purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, "paused-purchase")
	if err != nil || r.Outcome != "WAITING" || r.Guidance.ReasonCode != "MONEY_GATE_CLOSED" || r.Guidance.OperatorAction != "CHECK_PAYMENT_GATE" {
		t.Fatalf("pause reason lost: %+v %v", r, err)
	}
	if len(h.provider.Captures) != 0 {
		t.Fatal("paused payment called capture")
	}
	var grants int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE type=$1`, procmsg.EffectGrantMerchantPurchase).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("paused funding granted purchase: %d %v", grants, err)
	}
}

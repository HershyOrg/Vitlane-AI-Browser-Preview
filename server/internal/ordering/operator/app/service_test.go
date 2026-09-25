package app

import (
	"context"
	"errors"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	logisticsdomain "github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
)

type fakePorts struct {
	queue            []procurementapp.QueueItem
	requests         []agencydomain.RefundRequest
	exceptions       []logisticsdomain.ExpectedUnit
	returns          []logisticsdomain.Return
	resolvedRequests []agencydomain.RefundRequest
	resolvedUnits    []logisticsdomain.ResolvedExceptionUnit
	closedReturns    []logisticsdomain.Return
	failQueue        error
}

type fakeAccountingReader struct {
	positions map[string]paymentdomain.OrderAccountingProjection
}

type fakeLivePayPalOrderCounter struct {
	count int
	err   error
}

type fakePaymentReconciliations struct {
	items []paymentapp.PaymentReconciliationItem
}

type fakePayPalResourceAdoptions struct {
	items []PayPalResourceAdoptionItem
}

type fakeProcessInterventions struct {
	items []processapp.InterventionItem
}

func (f *fakeProcessInterventions) ListInterventions(
	_ context.Context, _ int,
) ([]processapp.InterventionItem, error) {
	return f.items, nil
}

func (f *fakeProcessInterventions) CountInterventions(context.Context) (int, error) {
	return len(f.items), nil
}

func (f *fakePaymentReconciliations) ListPaymentReconciliations(
	_ context.Context,
	_ int,
) ([]paymentapp.PaymentReconciliationItem, error) {
	return f.items, nil
}

func (f *fakePaymentReconciliations) CountPaymentReconciliations(_ context.Context) (int, error) {
	return len(f.items), nil
}

func (f *fakePayPalResourceAdoptions) ListPayPalResourceAdoptions(
	_ context.Context, _ time.Time, _ int,
) ([]PayPalResourceAdoptionItem, error) {
	return f.items, nil
}

func (f *fakePayPalResourceAdoptions) CountPayPalResourceAdoptions(
	_ context.Context, _ time.Time,
) (int, error) {
	return len(f.items), nil
}

func (f *fakeAccountingReader) GetOrderAccounting(
	_ context.Context, agencyOrderID string,
) (paymentdomain.OrderAccountingProjection, bool, error) {
	position, found := f.positions[agencyOrderID]
	return position, found, nil
}

func (f *fakeLivePayPalOrderCounter) CountLivePayPalOrders(context.Context) (int, error) {
	return f.count, f.err
}

func (f *fakePorts) ListQueue(_ context.Context, _ int) ([]procurementapp.QueueItem, error) {
	return f.queue, f.failQueue
}

func (f *fakePorts) ListRefundQueue(_ context.Context, _ int) ([]agencydomain.RefundRequest, error) {
	return f.requests, nil
}

func (f *fakePorts) ListExceptionUnits(_ context.Context, _ int) ([]logisticsdomain.ExpectedUnit, error) {
	return f.exceptions, nil
}

func (f *fakePorts) ListReturns(_ context.Context, openOnly bool, _ int) ([]logisticsdomain.Return, error) {
	if !openOnly {
		return nil, errors.New("work surface must only list open returns")
	}
	return f.returns, nil
}

func (f *fakePorts) ListResolvedRefundQueue(_ context.Context, _ int) ([]agencydomain.RefundRequest, error) {
	return f.resolvedRequests, nil
}

func (f *fakePorts) ListResolvedExceptionUnits(_ context.Context, _ int) ([]logisticsdomain.ResolvedExceptionUnit, error) {
	return f.resolvedUnits, nil
}

func (f *fakePorts) ListClosedReturns(_ context.Context, _ int) ([]logisticsdomain.Return, error) {
	return f.closedReturns, nil
}

func (f *fakePorts) CountOpenTasks(_ context.Context) (int, error)       { return 7, nil }
func (f *fakePorts) CountOpenRefundQueue(_ context.Context) (int, error) { return 3, nil }
func (f *fakePorts) CountExceptionUnits(_ context.Context) (int, error)  { return 2, nil }
func (f *fakePorts) CountOpenReturns(_ context.Context) (int, error)     { return 1, nil }

func at(hour int) time.Time { return time.Date(2026, 8, 22, hour, 0, 0, 0, time.UTC) }

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

func leaseUntil(value time.Time) *time.Time { return &value }

// 4개 큐가 하나의 계약으로 합성되고, kind 우선순위·최근 갱신순 정렬과 kind별
// 다음 행동(자문 공간)이 유지된다(ADR-0055 §5).
func TestWorkSurfaceComposesFourQueues(t *testing.T) {
	ports := &fakePorts{
		queue: []procurementapp.QueueItem{
			{Task: procurementdomain.ExecutionTask{ID: "task-old", AgencyOrderID: "order-1",
				State: "QUEUED", UpdatedAt: at(9)}},
			{Task: procurementdomain.ExecutionTask{ID: "task-new", AgencyOrderID: "order-2",
				State: "CLAIMED", AssignedOperatorUserID: "operator-7",
				LeaseUntil: leaseUntil(at(13)), UpdatedAt: at(11)},
				MerchantOrder: procurementdomain.MerchantOrder{State: "PLANNED"}},
		},
		requests: []agencydomain.RefundRequest{{
			ID: "request-1", AgencyOrderID: "order-1", State: "REQUESTED", UpdatedAt: at(10),
		}},
		exceptions: []logisticsdomain.ExpectedUnit{{
			ID: "unit-1", AgencyOrderID: "order-2",
			Fulfillment: logisticsdomain.FulfillmentWrongActual, UpdatedAt: at(8),
		}},
		returns: []logisticsdomain.Return{{
			ID: "return-1", AgencyOrderID: "order-2",
			State: logisticsdomain.ReturnReceived, UpdatedAt: at(7),
		}},
	}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).List(context.Background(), WorkViewOpen, 0)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]string, 0, len(surface.Items))
	for _, item := range surface.Items {
		gotIDs = append(gotIDs, item.ID)
	}
	// kind 우선순위(실행→심사→판정→회수), 같은 kind는 최근 갱신순.
	want := []string{"task-new", "task-old", "request-1", "unit-1", "return-1"}
	for index, id := range want {
		if gotIDs[index] != id {
			t.Fatalf("order=%v want %v", gotIDs, want)
		}
	}
	if surface.Counts != (WorkItemCounts{ProcurementExecution: 2, RefundReview: 1,
		DeliveryResolution: 1, ReturnProgress: 1}) {
		t.Fatalf("counts=%+v", surface.Counts)
	}
	byID := map[string]WorkItem{}
	for _, item := range surface.Items {
		byID[item.ID] = item
	}
	if actions := byID["task-old"].Actions; len(actions) != 1 || actions[0] != "CLAIM" {
		t.Fatalf("queued task actions=%v", actions)
	}
	if byID["task-new"].AssignedOperatorUserID != "operator-7" {
		t.Fatal("claim identity must survive composition")
	}
	// 오배송 판정에는 회수 시작이 열린다.
	wrongActual := byID["unit-1"].Actions
	if len(wrongActual) != 3 || wrongActual[2] != "START_RETURN" {
		t.Fatalf("wrong-actual actions=%v", wrongActual)
	}
	if actions := byID["return-1"].Actions; len(actions) != 2 || actions[0] != "MARK_MERCHANT_RETURNED" {
		t.Fatalf("received return actions=%v", actions)
	}
}

func TestWorkSurfaceOffersOnlyTypedPayPalGETAdoptionActions(t *testing.T) {
	ports := &fakePorts{}
	adoptions := &fakePayPalResourceAdoptions{items: []PayPalResourceAdoptionItem{
		{
			ReconciliationKind: PayPalReauthorizationAdoption,
			OperationID:        "reauthorization-operation", AgencyOrderID: "order-1",
			MerchantOrderID: "merchant-order-1", ProviderEnvironment: "SANDBOX",
			AmountMinor: 12_300, Currency: "USD", OperationState: "UNKNOWN",
			IdempotencyDeadline: at(9), UpdatedAt: at(10),
		},
		{
			ReconciliationKind: PayPalMORefundAdoption,
			OperationID:        "refund-operation", AgencyOrderID: "order-2",
			MerchantOrderID: "merchant-order-2", CompensationID: "compensation-2",
			ProviderEnvironment: "LIVE", AmountMinor: 4_500, Currency: "USD",
			OperationState: "SENT", IdempotencyDeadline: at(9), UpdatedAt: at(11),
		},
	}}
	service := NewService(ports, ports, ports, fakeClock{now: at(12)})
	service.EnablePayPalResourceAdoption(adoptions)

	surface, err := service.List(context.Background(), WorkViewOpen, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Items) != 2 || surface.Counts.PaymentReconciliation != 2 {
		t.Fatalf("surface=%+v", surface)
	}
	byID := make(map[string]WorkItem, len(surface.Items))
	for _, item := range surface.Items {
		byID[item.ID] = item
	}
	if actions := byID["reauthorization-operation"].Actions; len(actions) != 1 || actions[0] != ActionAdoptPayPalReauthorization {
		t.Fatalf("reauthorization actions=%v", actions)
	}
	if actions := byID["refund-operation"].Actions; len(actions) != 1 || actions[0] != ActionAdoptPayPalMORefund {
		t.Fatalf("refund actions=%v", actions)
	}
	for _, item := range surface.Items {
		for _, action := range item.Actions {
			if action == "RETRY" || action == "POST" || action == "CREATE" {
				t.Fatalf("resource adoption must remain GET-only: %+v", item)
			}
		}
	}

	counts, err := service.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.PaymentReconciliation != 2 {
		t.Fatalf("counts=%+v", counts)
	}
}

// RESOLVED view는 종결 항목만 열람하고 행동 공간이 비어 있다(ADR-0057).
func TestWorkSurfaceResolvedView(t *testing.T) {
	ports := &fakePorts{
		// OPEN 큐에 항목이 있어도 RESOLVED에는 섞이지 않는다.
		queue: []procurementapp.QueueItem{{Task: procurementdomain.ExecutionTask{
			ID: "task-open", State: "QUEUED", UpdatedAt: at(9)}}},
		resolvedRequests: []agencydomain.RefundRequest{{
			ID: "request-9", AgencyOrderID: "order-1", State: "RESOLVED", UpdatedAt: at(10),
		}},
		resolvedUnits: []logisticsdomain.ResolvedExceptionUnit{{
			ExpectedUnit: logisticsdomain.ExpectedUnit{ID: "unit-9", AgencyOrderID: "order-2",
				Fulfillment: logisticsdomain.Fulfillment("RESOLVED"), UpdatedAt: at(8)},
			Resolution:         logisticsdomain.DeliveryResolution{Decision: logisticsdomain.ResolutionRefund},
			CompensationAction: "REFUND", CompensationState: "SUCCEEDED",
		}},
		closedReturns: []logisticsdomain.Return{{
			ID: "return-9", AgencyOrderID: "order-2",
			State: logisticsdomain.ReturnClosed, UpdatedAt: at(7),
		}},
	}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).List(context.Background(), WorkViewResolved, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Items) != 3 {
		t.Fatalf("items=%d want 3", len(surface.Items))
	}
	for _, item := range surface.Items {
		if item.ID == "task-open" {
			t.Fatal("open queue must not leak into RESOLVED view")
		}
		if len(item.Actions) != 0 {
			t.Fatalf("resolved item %s must not open actions: %v", item.ID, item.Actions)
		}
	}
	if surface.Counts != (WorkItemCounts{RefundReview: 1, DeliveryResolution: 1,
		ReturnProgress: 1}) {
		t.Fatalf("counts=%+v", surface.Counts)
	}
}

// 주문 전체 process는 요약일 뿐이다. 정확한 MO에 열린 예외가 있을 때만 그
// MO의 행동을 닫고 같은 주문의 형제 MO는 계속 진행한다.
func TestWorkSurfaceScopesExceptionActionsToExactMerchantOrder(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{
		{Task: procurementdomain.ExecutionTask{ID: "task-r", AgencyOrderID: "order-1",
			State: "QUEUED", UpdatedAt: at(9)}, ProcessState: "RESOLUTION_IN_PROGRESS",
			RefundRequestState: "REQUESTED"},
		{Task: procurementdomain.ExecutionTask{ID: "task-sibling", AgencyOrderID: "order-1",
			State: "QUEUED", UpdatedAt: at(10)}, ProcessState: "RESOLUTION_IN_PROGRESS"},
		{Task: procurementdomain.ExecutionTask{ID: "task-a", AgencyOrderID: "order-2",
			State: "CLAIMED", AssignedOperatorUserID: "operator-7", UpdatedAt: at(10)},
			ProcessState: "ATTENTION_REQUIRED"},
		{Task: procurementdomain.ExecutionTask{ID: "task-ok", AgencyOrderID: "order-3",
			State: "QUEUED", UpdatedAt: at(11)}, ProcessState: "PROCUREMENT_IN_PROGRESS"},
	}}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).List(context.Background(), WorkViewOpen, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]string{}
	for _, item := range surface.Items {
		byID[item.ID] = item.Actions
	}
	if len(byID["task-r"]) != 0 {
		t.Fatalf("exact-MO exception actions=%v", byID["task-r"])
	}
	for _, id := range []string{"task-sibling", "task-a", "task-ok"} {
		if len(byID[id]) != 1 || byID[id][0] != "CLAIM" {
			t.Fatalf("%s actions=%v", id, byID[id])
		}
	}
}

// Counts는 목록 limit 캡과 무관한 owner 전역 카운트다(ADR-0057 2차 P2).
func TestWorkSurfaceCounts(t *testing.T) {
	ports := &fakePorts{}
	counts, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 개입 포트 미편입이면 0으로 남는다.
	want := WorkItemCounts{ProcurementExecution: 7, RefundReview: 3,
		DeliveryResolution: 2, ReturnProgress: 1}
	if counts != want {
		t.Fatalf("counts=%+v want %+v", counts, want)
	}
}

func TestLivePayPalOrderCountIsSeparateFromOpenWorkCounts(t *testing.T) {
	ports := &fakePorts{}
	service := NewService(ports, ports, ports, fakeClock{now: at(12)})
	service.EnableLivePayPalOrderCount(&fakeLivePayPalOrderCounter{count: 4})

	count, err := service.CountLivePayPalOrders(context.Background())
	if err != nil || count != 4 {
		t.Fatalf("LIVE PayPal order count=%d err=%v", count, err)
	}
	workCounts, err := service.Counts(context.Background())
	if err != nil || workCounts.ProcurementExecution != 7 {
		t.Fatalf("work counts=%+v err=%v", workCounts, err)
	}
}

func TestWorkSurfaceShowsPaymentReconciliationBeforeProcurement(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{{Task: procurementdomain.ExecutionTask{
		ID: "task-1", AgencyOrderID: "order-paid", State: "QUEUED", UpdatedAt: at(11),
	}}}}
	service := NewService(ports, ports, ports, fakeClock{now: at(12)})
	service.EnablePaymentReconciliation(&fakePaymentReconciliations{items: []paymentapp.PaymentReconciliationItem{{
		PaymentID: "payment-1", AgencyOrderID: "order-review",
		PayPalAttemptID: "attempt-1", PayPalOrderID: "PP-ORDER-1",
		ProviderEnvironment: "SANDBOX", AmountMinor: 5223, Currency: "USD",
		PaymentState: paymentdomain.PaymentOutcomeUnknown,
		AttemptState: paymentdomain.AttemptAuthorizeOutcomeUnknown,
		ReasonCode:   "AUTHORIZATION_OUTCOME_UNKNOWN", UpdatedAt: at(10),
	}}})

	surface, err := service.List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Items) != 2 || surface.Items[0].Kind != KindPaymentReconciliation ||
		surface.Items[0].AgencyOrderID != "order-review" {
		t.Fatalf("payment reconciliation not prioritized: %+v", surface.Items)
	}
	if surface.Counts.PaymentReconciliation != 1 || len(surface.Items[0].Actions) != 0 {
		t.Fatalf("payment reconciliation contract=%+v", surface)
	}
	counts, err := service.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.PaymentReconciliation != 1 {
		t.Fatalf("payment reconciliation count=%d", counts.PaymentReconciliation)
	}
}

func TestWorkSurfaceHidesAbandonForMoneyMovingCompensation(t *testing.T) {
	ports := &fakePorts{}
	service := NewService(ports, ports, ports, fakeClock{now: at(12)})
	service.EnableProcessIntervention(&fakeProcessInterventions{items: []processapp.InterventionItem{
		{EffectID: "compensation-command", AgencyOrderID: "order-1",
			Type: procmsg.EffectCompensateMO, UpdatedAt: at(11)},
		{EffectID: "notice-command", AgencyOrderID: "order-2",
			Type: procmsg.EffectSendNotice, UpdatedAt: at(10)},
	}})

	surface, err := service.List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]string{}
	for _, item := range surface.Items {
		byID[item.ID] = item.Actions
	}
	if actions := byID["compensation-command"]; len(actions) != 1 || actions[0] != "RETRY" {
		t.Fatalf("compensation actions=%v want [RETRY]", actions)
	}
	if actions := byID["notice-command"]; len(actions) != 1 || actions[0] != "RETRY" {
		t.Fatalf("notice actions=%v want [RETRY]", actions)
	}
}

// 절반 진실 금지: 한 owner 조회 실패는 전체 실패다.
func TestWorkSurfaceFailsClosedOnOwnerError(t *testing.T) {
	ports := &fakePorts{failQueue: errors.New("boom")}
	if _, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).List(context.Background(), WorkViewOpen, 10); err == nil {
		t.Fatal("expected error")
	}
}

// Accounting is a read projection. Even a negative forecast never grants or
// removes Procurement actions; the owning workflow remains authoritative.
func TestNegativeAccountingForecastDoesNotGateActions(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{{
		Task: procurementdomain.ExecutionTask{
			ID: "task-1", AgencyOrderID: "order-1", State: "CLAIMED",
			LeaseUntil: leaseUntil(at(13)), UpdatedAt: at(10),
		},
		MerchantOrder: procurementdomain.MerchantOrder{State: "PLACEMENT_PENDING"},
	}}}
	reader := &fakeAccountingReader{positions: map[string]paymentdomain.OrderAccountingProjection{
		"order-1": {AgencyOrderID: "order-1", ForecastBalanceMinor: -500,
			RequiresAttention: true, AttentionReasons: []string{"CUSTOMER_COMPENSATION_EXPECTED"}},
	}}
	service := NewService(ports, ports, ports, fakeClock{now: at(12)})
	service.EnableAccounting(reader)

	surface, err := service.List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := surface.Items[0].Actions; len(got) != 4 || got[0] != "REVEAL_SHIPPING" ||
		got[2] != "RECORD_PLACED" {
		t.Fatalf("shortfall must not gate actions: %v", got)
	}
	if surface.Items[0].Accounting == nil || surface.Items[0].Accounting.ForecastBalanceMinor != -500 {
		t.Fatalf("accounting projection missing: %+v", surface.Items[0].Accounting)
	}
}

// 할당됐어도 lease가 만료(또는 부재)면 진행 행동을 닫고 CLAIM만 연다 — owner
// 재검사가 어차피 거절할 행동을 화면에 남기지 않고, 재담당을 유일한 다음
// 행동으로 만든다(운영정합 5차 B1 — 만료 lease 영구 교착의 화면 측 해소).
func TestClaimedTaskWithExpiredLeaseOffersReclaimOnly(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{
		{Task: procurementdomain.ExecutionTask{ID: "task-expired", AgencyOrderID: "order-1",
			State: "CLAIMED", AssignedOperatorUserID: "operator-7",
			LeaseUntil: leaseUntil(at(11)), UpdatedAt: at(10)}},
		{Task: procurementdomain.ExecutionTask{ID: "task-no-lease", AgencyOrderID: "order-2",
			State: "IN_PROGRESS", AssignedOperatorUserID: "operator-7", UpdatedAt: at(10)}},
		{Task: procurementdomain.ExecutionTask{ID: "task-active", AgencyOrderID: "order-3",
			State: "CLAIMED", AssignedOperatorUserID: "operator-7",
			LeaseUntil: leaseUntil(at(13)), UpdatedAt: at(10)},
			MerchantOrder: procurementdomain.MerchantOrder{State: "PLACEMENT_PENDING"}},
	}}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).
		List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]WorkItem{}
	for _, item := range surface.Items {
		byID[item.ID] = item
	}
	for _, id := range []string{"task-expired", "task-no-lease"} {
		if item := byID[id]; len(item.Actions) != 1 || item.Actions[0] != "CLAIM" || item.AssignmentState != "EXPIRED" {
			t.Fatalf("%s item=%+v want expired + [CLAIM]", id, item)
		}
	}
	if item := byID["task-active"]; len(item.Actions) != 4 || item.Actions[0] != "REVEAL_SHIPPING" || item.AssignmentState != "ACTIVE" {
		t.Fatalf("active lease item=%+v", item)
	}
}

func TestPlannedProcurementDoesNotAdvertisePlacementEvidenceAction(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{{
		Task: procurementdomain.ExecutionTask{
			ID: "task-planned", AgencyOrderID: "order-1", State: "CLAIMED",
			AssignedOperatorUserID: "operator-7", LeaseUntil: leaseUntil(at(13)),
		},
		MerchantOrder: procurementdomain.MerchantOrder{State: "PLANNED"},
	}}}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).
		List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	actions := surface.Items[0].Actions
	for _, action := range actions {
		if action == "RECORD_PLACED" {
			t.Fatalf("PLANNED MerchantOrder must not advertise placement evidence: %v", actions)
		}
	}
	if len(actions) != 3 || actions[2] != "RECORD_FAILURE" {
		t.Fatalf("planned actions=%v", actions)
	}
}

func TestSucceededProcurementKeepsAssignmentUntilMerchantOrderDone(t *testing.T) {
	ports := &fakePorts{queue: []procurementapp.QueueItem{
		{
			Task: procurementdomain.ExecutionTask{
				ID: "task-logistics", AgencyOrderID: "order-1", State: "SUCCEEDED",
				AssignedOperatorUserID: "operator-7", LeaseUntil: leaseUntil(at(13)),
			},
			MerchantOrder: procurementdomain.MerchantOrder{State: "PLACED"},
			LogisticsSummary: procurementapp.LogisticsSummary{
				ExpectedUnits: 1, AwaitingUnits: 1,
			},
		},
		{
			Task: procurementdomain.ExecutionTask{
				ID: "task-done", AgencyOrderID: "order-2", State: "SUCCEEDED",
				AssignedOperatorUserID: "operator-7", LeaseUntil: leaseUntil(at(13)),
			},
			MerchantOrder: procurementdomain.MerchantOrder{State: "PLACED"},
			LogisticsSummary: procurementapp.LogisticsSummary{
				ExpectedUnits: 1, DeliveredUnits: 1,
			},
		},
	}}
	surface, err := NewService(ports, ports, ports, fakeClock{now: at(12)}).
		List(context.Background(), WorkViewOpen, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]WorkItem{}
	for _, item := range surface.Items {
		byID[item.ID] = item
	}
	if got := byID["task-logistics"]; got.AssignmentState != "ACTIVE" ||
		got.Operational.WorkStage != agencydomain.MOWorkLogistics ||
		len(got.Actions) != 1 || got.Actions[0] != "CREATE_SHIPMENT" {
		t.Fatalf("logistics item=%+v", got)
	}
	if got := byID["task-done"]; got.AssignmentState != "COMPLETED" ||
		got.Operational.WorkStage != agencydomain.MOWorkDone || len(got.Actions) != 0 {
		t.Fatalf("done item=%+v", got)
	}
}

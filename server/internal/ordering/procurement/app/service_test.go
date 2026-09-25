package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type placedCall struct {
	evidence    domain.PlacementEvidence
	liveEnabled bool
}

type fakeRepository struct {
	Repository
	ManualReviewRepository
	placedCalls  []placedCall
	item         QueueItem
	sequence     []string
	resolveState string
	resolveErr   error
}

func (r *fakeRepository) GetQueueItem(_ context.Context, _ string) (QueueItem, error) {
	return r.item, nil
}

func (r *fakeRepository) RecordPlaced(_ context.Context, _, _, _ string, evidence domain.PlacementEvidence, liveEnabled bool, _ time.Time) (QueueItem, bool, error) {
	r.placedCalls = append(r.placedCalls, placedCall{evidence, liveEnabled})
	return QueueItem{}, false, nil
}

func resultEvidence(kind domain.PlacementEvidenceKind) domain.PlacementEvidence {
	return domain.PlacementEvidence{
		Kind: kind, ExternalOrderRef: "SHOP-ORDER-1",
		ReceiptSafeRef: "receipt-safe-ref-1", ActualAmountMinor: 3030,
		Currency: "USD", EvidenceSource: domain.EvidenceReceipt,
		ClaimsExternalLive: kind == domain.PlacementEvidenceLiveEffect,
	}
}

func (r *fakeRepository) PrepareMerchantEffectFunding(
	_ context.Context,
	_, _, _ string,
	_ bool,
	_ PurchasePreparation,
	_ time.Time,
) (QueueItem, bool, error) {
	r.sequence = append(r.sequence, "prepare")
	return r.item, false, nil
}

func (r *fakeRepository) ResolveMerchantEffectFunding(
	_ context.Context,
	_, _, _, _, fundingState string,
	_ PurchasePreparation,
	_ time.Time,
) (QueueItem, bool, error) {
	r.sequence = append(r.sequence, "resolve")
	r.resolveState = fundingState
	return r.item, false, r.resolveErr
}

type fakeFundingActivator struct {
	sequence *[]string
	result   MerchantOrderFundingActivation
	err      error
}

func (a fakeFundingActivator) ActivateMerchantOrderFunding(
	_ context.Context,
	_, _ string,
) (MerchantOrderFundingActivation, error) {
	*a.sequence = append(*a.sequence, "activate")
	return a.result, a.err
}

type fakeTransactor struct{}

func (fakeTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type fakeLiveMerchantGate struct {
	allowed bool
	err     error
}

func (g fakeLiveMerchantGate) AllowLiveMerchantEffect(context.Context) (bool, error) {
	return g.allowed, g.err
}

// Kill switch는 새 Begin만 차단한다. 이미 시작된 effect인지 판별할 수 있는
// repository까지 결과 증거를 보내야 외부 주문을 미기록 상태로 만들지 않는다.
func TestLiveResultReachesStartedEffectCheckAfterKillSwitch(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)},
		false)
	_, _, err := service.RecordResult(context.Background(), "task-1", "operator-1",
		"idem-key-0001", true, "", resultEvidence(domain.PlacementEvidenceLiveEffect))
	if err != nil {
		t.Fatalf("started live result must reach repository after kill switch: %v", err)
	}
	if len(repository.placedCalls) != 1 || repository.placedCalls[0].liveEnabled {
		t.Fatalf("gate-off result call mismatch: %+v", repository.placedCalls)
	}
}

// done은 모드 무관 단일 RecordPlaced로 수렴한다(ADR-0053). evidence 유무와
// activation 여부는 그대로 전달돼 repository가 모드-일치를 재검사한다.
func TestDoneRoutesToUnifiedRecordPlaced(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)},
		true)
	if _, _, err := service.RecordResult(context.Background(), "task-1", "operator-1",
		"idem-key-0001", true, "", resultEvidence(domain.PlacementEvidenceLiveEffect)); err != nil {
		t.Fatalf("RecordResult live: %v", err)
	}
	if _, _, err := service.RecordResult(context.Background(), "task-1", "operator-1",
		"idem-key-0002", true, "", resultEvidence(domain.PlacementEvidenceSandboxTest)); err != nil {
		t.Fatalf("RecordResult sandbox: %v", err)
	}
	if len(repository.placedCalls) != 2 {
		t.Fatalf("placed calls = %d", len(repository.placedCalls))
	}
	if repository.placedCalls[0].evidence.Kind != domain.PlacementEvidenceLiveEffect ||
		!repository.placedCalls[0].liveEnabled {
		t.Fatalf("live call mismatch: %+v", repository.placedCalls[0])
	}
	if repository.placedCalls[1].evidence.Kind != domain.PlacementEvidenceSandboxTest ||
		!repository.placedCalls[1].liveEnabled {
		t.Fatalf("sandbox call mismatch: %+v", repository.placedCalls[1])
	}
}

// 운영정합 3차 D-f: funding coverage는 view다 — 부족해도 지출 기록은
// 차단되지 않는다(경고는 화면·간이 회계가 담당).
func TestDoneRecordsWithoutFundingGate(t *testing.T) {
	repository := &fakeRepository{item: QueueItem{Task: domain.ExecutionTask{
		AgencyOrderID: "order-1",
	}}}
	service := NewService(repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, true)

	if _, _, err := service.RecordResult(context.Background(), "task-1", "operator-1",
		"idem-key-0001", true, "", resultEvidence(domain.PlacementEvidenceSandboxTest)); err != nil {
		t.Fatalf("RecordResult must not consult funding: %v", err)
	}
	if len(repository.placedCalls) != 1 {
		t.Fatalf("placed calls=%d, want 1", len(repository.placedCalls))
	}
}

func TestOwnerPurchaseStepsActivatesExactMOFundingBeforeSellerEffect(t *testing.T) {
	repository := &fakeRepository{item: QueueItem{
		MerchantOrder: domain.MerchantOrder{ID: "mo-1"},
	}}
	service := newOwnerPurchaseTestDriver(NewService(
		repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, true,
	), fakeFundingActivator{})
	service.activator = (fakeFundingActivator{
		sequence: &repository.sequence,
		result: MerchantOrderFundingActivation{
			PositionID: "position-1", State: "ACTIVE",
		},
	})

	if _, _, err := service.runOwnerPurchaseSteps(
		context.Background(), "task-1", "operator-1", "idem-key-0001",
	); err != nil {
		t.Fatalf("BeginMerchantEffect: %v", err)
	}
	if got := strings.Join(repository.sequence, ","); got != "prepare,activate,resolve" {
		t.Fatalf("effect order=%s", got)
	}
	if repository.resolveState != "ACTIVE" {
		t.Fatalf("resolved funding state=%s", repository.resolveState)
	}
}

func TestOwnerPurchaseStepsRuntimeKillBlocksOnlyLivePreparation(t *testing.T) {
	repository := &fakeRepository{item: QueueItem{
		MerchantOrder: domain.MerchantOrder{
			ID: "mo-live-1", ExecutionMode: domain.ModeLiveMerchantEffect,
		},
	}}
	service := newOwnerPurchaseTestDriver(NewService(
		repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, true,
	), fakeFundingActivator{})
	service.EnableLiveMerchantGate(fakeLiveMerchantGate{})
	service.activator = (fakeFundingActivator{sequence: &repository.sequence})

	if _, _, err := service.runOwnerPurchaseSteps(
		context.Background(), "task-live-1", "operator-1", "idem-key-0001",
	); !errors.Is(err, domain.ErrLiveModeClosed) {
		t.Fatalf("killed Live begin err=%v", err)
	}
	if len(repository.sequence) != 0 {
		t.Fatalf("killed Live begin reached effect preparation: %v", repository.sequence)
	}

	repository.item.MerchantOrder.ExecutionMode = domain.ModeSimulatedNoEffect
	if _, _, err := service.runOwnerPurchaseSteps(
		context.Background(), "task-sandbox-1", "operator-1", "idem-key-0002",
	); err != nil {
		t.Fatalf("Sandbox begin was affected by Live kill: %v", err)
	}
}

func TestOwnerPurchaseStepsDoesNotResolveWhenFundingActivationFails(t *testing.T) {
	repository := &fakeRepository{item: QueueItem{
		MerchantOrder: domain.MerchantOrder{ID: "mo-1"},
	}}
	service := newOwnerPurchaseTestDriver(NewService(
		repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, true,
	), fakeFundingActivator{})
	service.activator = (fakeFundingActivator{
		sequence: &repository.sequence, err: domain.ErrFundingNotReady,
	})

	if _, _, err := service.runOwnerPurchaseSteps(
		context.Background(), "task-1", "operator-1", "idem-key-0001",
	); err == nil {
		t.Fatal("BeginMerchantEffect unexpectedly succeeded")
	}
	if got := strings.Join(repository.sequence, ","); got != "prepare,activate" {
		t.Fatalf("seller effect resolved after failed funding: %s", got)
	}
}

func TestOwnerPurchaseStepsResolvesDefinitiveFundingFailure(t *testing.T) {
	repository := &fakeRepository{item: QueueItem{
		MerchantOrder: domain.MerchantOrder{ID: "mo-1"},
	}}
	service := newOwnerPurchaseTestDriver(NewService(
		repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, true,
	), fakeFundingActivator{})
	service.activator = (fakeFundingActivator{
		sequence: &repository.sequence,
		result: MerchantOrderFundingActivation{
			PositionID: "position-1", State: "FAILED",
		},
		err: domain.ErrFundingNotReady,
	})

	if _, _, err := service.runOwnerPurchaseSteps(
		context.Background(), "task-1", "operator-1", "idem-key-0001",
	); !errors.Is(err, domain.ErrFundingNotReady) {
		t.Fatalf("BeginMerchantEffect error=%v", err)
	}
	if got := strings.Join(repository.sequence, ","); got != "prepare,activate,resolve" {
		t.Fatalf("definitive failure was not resolved: %s", got)
	}
	if repository.resolveState != "FAILED" {
		t.Fatalf("resolved funding state=%s", repository.resolveState)
	}
}

// Unit driver supplies the already-verified Workflow contract. Real admission,
// claim/generation and commit ordering are exercised in the PostgreSQL suite.
type MerchantOrderFundingActivation struct{ PositionID, State string }
type ownerPurchaseTestDriver struct {
	*Service
	activator fakeFundingActivator
}

func newOwnerPurchaseTestDriver(s *Service, a fakeFundingActivator) *ownerPurchaseTestDriver {
	return &ownerPurchaseTestDriver{s, a}
}
func (s *ownerPurchaseTestDriver) runOwnerPurchaseSteps(ctx context.Context, task, actor, key string) (QueueItem, bool, error) {
	item, _ := s.repository.GetQueueItem(ctx, task)
	scope := procmsg.ExecutionScope{AgencyOrderID: "order", MerchantOrderID: item.MerchantOrder.ID, FlowID: "op", EffectID: "command", ClaimVersion: 1, Action: procmsg.EffectReservePurchase}
	a := PurchasePreparation{FundingPositionID: "position-1", FundingState: "AVAILABLE"}
	if _, _, err := s.PreparePurchase(procmsg.WithExecutionScope(ctx, scope), task, actor, key, a); err != nil {
		return QueueItem{}, false, err
	}
	f, err := s.activator.ActivateMerchantOrderFunding(ctx, item.MerchantOrder.ID, key)
	if err != nil && f.State != "FAILED" {
		return QueueItem{}, false, err
	}
	a.FundingPositionID = f.PositionID
	a.FundingState = f.State
	scope.Action = procmsg.EffectGrantMerchantPurchase
	item, replay, resolveErr := s.AuthorizeMerchant(procmsg.WithExecutionScope(ctx, scope), task, actor, key, a)
	if resolveErr == nil {
		resolveErr = err
	}
	return item, replay, resolveErr
}

func (r *fakeRepository) GetPurchaseSubject(ctx context.Context, id string) (QueueItem, error) {
	return r.GetQueueItem(ctx, id)
}

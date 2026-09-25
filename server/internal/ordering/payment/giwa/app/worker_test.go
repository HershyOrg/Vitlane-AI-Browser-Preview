package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

type workerClock struct{ now time.Time }

func (c workerClock) Now() time.Time { return c.now }

type workerChain struct {
	heads         ChainHeads
	observation   TransactionObservation
	observeErr    map[string]error
	canonical     string
	events        []FinalizedEvent
	from          uint64
	to            uint64
	contractState ContractPaymentState
	observeCalls  int
	waitForHeads  bool
}

func (c *workerChain) Heads(ctx context.Context) (ChainHeads, error) {
	if c.waitForHeads {
		<-ctx.Done()
		return ChainHeads{}, ctx.Err()
	}
	return c.heads, nil
}
func (c *workerChain) CanonicalBlockHashes(_ context.Context, blocks []uint64) ([]CanonicalBlockHashResult, error) {
	results := make([]CanonicalBlockHashResult, len(blocks))
	for index, block := range blocks {
		results[index] = CanonicalBlockHashResult{BlockNumber: block, BlockHash: c.canonical}
	}
	return results, nil
}
func (c *workerChain) ObserveTransactions(_ context.Context, hashes []string) ([]TransactionObservationResult, error) {
	c.observeCalls++
	results := make([]TransactionObservationResult, len(hashes))
	for index, hash := range hashes {
		results[index] = TransactionObservationResult{
			TxHash: hash, Observation: c.observation, Err: c.observeErr[hash],
		}
	}
	return results, nil
}
func (c *workerChain) FinalizedEvents(_ context.Context, from, to uint64) ([]FinalizedEvent, error) {
	c.from, c.to = from, to
	return c.events, nil
}
func (c *workerChain) ContractPaymentStates(_ context.Context, hashes []string) ([]ContractPaymentStateResult, error) {
	results := make([]ContractPaymentStateResult, len(hashes))
	for index, hash := range hashes {
		results[index] = ContractPaymentStateResult{OrderHash: hash, State: c.contractState}
	}
	return results, nil
}

type workerRepository struct {
	items             []ReconcileItem
	finalized         int
	safe              int
	receipts          int
	failed            int
	reorged           int
	cursor            uint64
	cursorSet         bool
	projected         []FinalizedEvent
	projectTo         uint64
	scheduled         int
	unknown           int
	closureCandidates []SubmissionClosureCandidate
	closed            int
	listLimit         int
}

func (r *workerRepository) ListReconcileItems(_ context.Context, limit int, _ time.Time) ([]ReconcileItem, error) {
	r.listLimit = limit
	return r.items, nil
}
func (r *workerRepository) ScheduleTransactionObservation(context.Context, ReconcileItem, time.Time, string, time.Time) error {
	r.scheduled++
	return nil
}
func (r *workerRepository) MarkTransactionObservationUnknown(context.Context, ReconcileItem, string, time.Time) error {
	r.unknown++
	return nil
}
func (r *workerRepository) ListSubmissionClosureCandidates(context.Context, uint64, int) ([]SubmissionClosureCandidate, error) {
	return r.closureCandidates, nil
}
func (r *workerRepository) MarkPaymentSubmissionUnknown(context.Context, SubmissionClosureCandidate, string, time.Time) error {
	r.closed++
	return nil
}
func (r *workerRepository) MarkTransactionSafe(context.Context, ReconcileItem, TransactionObservation, time.Time) error {
	r.safe++
	return nil
}
func (r *workerRepository) MarkAuxTransactionFinalized(context.Context, ReconcileItem, TransactionObservation, time.Time) error {
	r.finalized++
	return nil
}
func (r *workerRepository) MarkTransactionFailed(context.Context, ReconcileItem, string, time.Time) error {
	r.failed++
	return nil
}
func (r *workerRepository) MarkTransactionReorged(context.Context, ReconcileItem, time.Time) error {
	r.reorged++
	return nil
}
func (r *workerRepository) GetFinalizedCursor(context.Context, uint64, string) (uint64, bool, error) {
	return r.cursor, r.cursorSet, nil
}
func (r *workerRepository) ProjectFinalizedEvents(_ context.Context, _ uint64, _ string, events []FinalizedEvent, to uint64, _ time.Time) error {
	r.projected = append(r.projected, events...)
	r.projectTo = to
	r.cursor, r.cursorSet = to, true
	return nil
}
func TestWorkerDoesNotUnderflowFutureBlockConfirmations(t *testing.T) {
	repository := &workerRepository{items: []ReconcileItem{{
		Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
		TxHash:  "0xtx", Purpose: PurposePay, TxState: "SUBMITTED",
	}}}
	chain := &workerChain{
		heads: ChainHeads{Safe: 9, Finalized: 8}, canonical: "0xblock",
		observation: TransactionObservation{Mined: true, Success: true, BlockNumber: 10, BlockHash: "0xblock", EventName: "PaymentEscrowed", OrderHash: "0xorder"},
	}
	repository.cursor, repository.cursorSet = 8, true
	worker := NewReconciler(repository, chain, workerClock{}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)), ReconcilePolicy{EventOverlapBlocks: 2})
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.safe != 0 || repository.finalized != 0 {
		t.Fatal("future block must not be interpreted as a huge confirmation count")
	}
}

func TestWorkerBoundsHeadRPCWithTimeout(t *testing.T) {
	worker := NewReconciler(
		&workerRepository{}, &workerChain{waitForHeads: true}, workerClock{},
		31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		ReconcilePolicy{TickTimeout: 50 * time.Millisecond, RPCTimeout: time.Millisecond},
	)
	err := worker.Tick(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("head RPC timeout was not propagated: %v", err)
	}
}

func TestWorkerProjectsOnlyFinalizedTerminalEvent(t *testing.T) {
	repository := &workerRepository{cursor: 9, cursorSet: true}
	chain := &workerChain{
		heads: ChainHeads{Safe: 12, Finalized: 12},
		events: []FinalizedEvent{{
			TxHash: "0xtx", LogIndex: 0, BlockNumber: 10, BlockHash: "0xblock",
			EventName: "PaymentRefunded", OrderHash: "0xorder",
		}},
	}
	worker := NewReconciler(repository, chain, workerClock{}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)), ReconcilePolicy{EventOverlapBlocks: 2})
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.projected) != 1 || repository.projectTo != 12 || chain.from != 8 || chain.to != 12 {
		t.Fatalf("expected finalized cursor projection, events=%d cursor=%d range=%d..%d",
			len(repository.projected), repository.projectTo, chain.from, chain.to)
	}
	if chain.observeCalls != 0 {
		t.Fatalf("event projection made %d follow-up receipt calls", chain.observeCalls)
	}
}

func TestWorkerRollsSafeTransactionBackWhenCanonicalBlockChanges(t *testing.T) {
	repository := &workerRepository{
		cursor: 20, cursorSet: true,
		items: []ReconcileItem{{
			Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			TxHash:  "0xtx", Purpose: PurposePay, TxState: "SAFE", TxBlockHash: "0xold",
		}},
	}
	chain := &workerChain{
		heads: ChainHeads{Safe: 20, Finalized: 20}, canonical: "0xnew",
		observation: TransactionObservation{
			Mined: true, Success: true, BlockNumber: 19, BlockHash: "0xnew",
			EventName: "PaymentEscrowed", OrderHash: "0xorder",
		},
	}
	worker := NewReconciler(repository, chain, workerClock{}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.reorged != 1 {
		t.Fatalf("expected safe rollback, got %d", repository.reorged)
	}
}

func TestWorkerIsolatesReceiptErrorsAndStillProjectsEvents(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repository := &workerRepository{
		cursor: 9, cursorSet: true,
		items: []ReconcileItem{
			{
				Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder-1"},
				TxHash:  "0xtx-1", Purpose: PurposePay, TxState: "SUBMITTED",
				TxSubmittedAt: now.Add(-time.Minute),
			},
			{
				Payment: settlementdomain.Payment{ID: "payment-2", OrderHash: "0xorder-2"},
				TxHash:  "0xtx-2", Purpose: PurposePay, TxState: "SUBMITTED",
				TxSubmittedAt: now.Add(-time.Minute),
			},
		},
	}
	chain := &workerChain{
		heads: ChainHeads{Safe: 12, Finalized: 12}, canonical: "0xblock",
		observation: TransactionObservation{
			Mined: true, Success: true, BlockNumber: 10, BlockHash: "0xblock",
			EventName: "PaymentEscrowed", OrderHash: "0xorder-2",
		},
		observeErr: map[string]error{"0xtx-1": errors.New("provider timeout")},
		events: []FinalizedEvent{{
			TxHash: "0xtx-event", LogIndex: 0, BlockNumber: 11,
			BlockHash: "0xblock", EventName: "PaymentEscrowed", OrderHash: "0xorder-3",
		}},
	}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		ReconcilePolicy{BatchSize: 2, EventOverlapBlocks: 2},
	)
	if err := worker.Tick(context.Background()); err == nil {
		t.Fatal("expected isolated item error to keep the tick degraded")
	}
	if repository.safe != 1 || repository.scheduled != 2 {
		t.Fatalf("expected one safe transition and two schedules, safe=%d scheduled=%d",
			repository.safe, repository.scheduled)
	}
	if len(repository.projected) != 1 {
		t.Fatalf("receipt error blocked finalized event projection: %#v", repository.projected)
	}
	if repository.listLimit != 2 {
		t.Fatalf("repository batch limit=%d, want 2", repository.listLimit)
	}
}

func TestWorkerExpiresUnminedSubmissionAsUnknownNotFailed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repository := &workerRepository{
		cursor: 10, cursorSet: true,
		items: []ReconcileItem{{
			Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			TxHash:  "0xtx", Purpose: PurposePay, TxState: "SUBMITTED",
			TxSubmittedAt: now.Add(-7 * time.Hour),
		}},
	}
	chain := &workerChain{heads: ChainHeads{Safe: 10, Finalized: 10}}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.unknown != 1 || repository.failed != 0 {
		t.Fatalf("timeout must become unknown, unknown=%d failed=%d",
			repository.unknown, repository.failed)
	}
}

func TestWorkerExpiresMinedSubmissionWhenFinalityStalls(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repository := &workerRepository{
		cursor: 8, cursorSet: true,
		items: []ReconcileItem{{
			Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			TxHash:  "0xtx", Purpose: PurposePay, TxState: "SUBMITTED",
			TxSubmittedAt: now.Add(-7 * time.Hour),
		}},
	}
	chain := &workerChain{
		heads: ChainHeads{Safe: 9, Finalized: 8}, canonical: "0xblock",
		observation: TransactionObservation{
			Mined: true, Success: true, BlockNumber: 10, BlockHash: "0xblock",
			EventName: "PaymentEscrowed", OrderHash: "0xorder",
		},
	}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.unknown != 1 || repository.safe != 0 || repository.failed != 0 {
		t.Fatalf("stalled finality must stop hot polling as unknown: %#v", repository)
	}
}

func TestWorkerNeverExpiresSafeTransactionByWallTimeAlone(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	block := uint64(9)
	repository := &workerRepository{
		cursor: 10, cursorSet: true,
		items: []ReconcileItem{{
			Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			TxHash:  "0xtx", Purpose: PurposePay, TxState: "SAFE",
			TxBlock: &block, TxBlockHash: "0xblock",
			TxSubmittedAt: now.Add(-24 * time.Hour),
		}},
	}
	chain := &workerChain{
		heads: ChainHeads{Safe: 10, Finalized: 8}, canonical: "0xblock",
	}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.unknown != 0 || repository.failed != 0 || repository.scheduled != 1 {
		t.Fatalf("safe transaction must remain observable, unknown=%d failed=%d scheduled=%d",
			repository.unknown, repository.failed, repository.scheduled)
	}
}

func TestWorkerDoesNotReorgSafeTransactionWhenCanonicalHeaderIsUnavailable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	block := uint64(9)
	repository := &workerRepository{
		cursor: 10, cursorSet: true,
		items: []ReconcileItem{{
			Payment: settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			TxHash:  "0xtx", Purpose: PurposePay, TxState: "SAFE",
			TxBlock: &block, TxBlockHash: "0xold", TxSubmittedAt: now.Add(-time.Hour),
		}},
	}
	chain := &workerChain{
		heads: ChainHeads{Safe: 10, Finalized: 8},
		observation: TransactionObservation{
			Mined: true, Success: true, BlockNumber: block, BlockHash: "0xold",
			EventName: "PaymentEscrowed", OrderHash: "0xorder",
		},
	}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err == nil {
		t.Fatal("missing canonical header must keep the tick degraded")
	}
	if repository.reorged != 0 || repository.scheduled != 1 {
		t.Fatalf("missing header is not reorg evidence: %#v", repository)
	}
}

func TestWorkerClosesExpiredNoHashPaymentOnlyAfterFinalizedContractNone(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repository := &workerRepository{
		cursor: 10, cursorSet: true,
		closureCandidates: []SubmissionClosureCandidate{{
			Payment:     settlementdomain.Payment{ID: "payment-1", OrderHash: "0xorder"},
			PayDeadline: now.Add(-time.Minute),
		}},
	}
	chain := &workerChain{heads: ChainHeads{
		Safe: 10, Finalized: 10, FinalizedTimestamp: uint64(now.Unix()),
	}}
	worker := NewReconciler(
		repository, chain, workerClock{now: now}, 31337, "0xsettlement", 0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.closed != 1 {
		t.Fatalf("expected expired submission closure, got %d", repository.closed)
	}
}

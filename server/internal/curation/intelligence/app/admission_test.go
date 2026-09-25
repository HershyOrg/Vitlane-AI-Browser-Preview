package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

func TestAcquireWaitsBoundedThenDefers(t *testing.T) {
	registry := ProviderRegistry{
		providers: map[intelligencedomain.ProviderKind]Provider{},
		slots:     map[intelligencedomain.ProviderKind]chan struct{}{intelligencedomain.ProviderManaged: make(chan struct{}, 1)},
	}
	release, err := registry.Acquire(context.Background(), intelligencedomain.ProviderManaged, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// The slot is taken: a second caller waits the bounded time and is told
	// to defer, with a short RetryAfter, instead of failing the job.
	started := time.Now()
	if _, err := registry.Acquire(context.Background(), intelligencedomain.ProviderManaged, 20*time.Millisecond); err == nil {
		t.Fatal("a busy slot must not be granted")
	} else if f, ok := fault.As(err); !ok || f.Code != fault.RateLimited || f.Reason != intelligencedomain.ReasonModelSlotBusy || f.RetryAfter <= 0 {
		t.Fatalf("busy slot fault = %v", err)
	} else if time.Since(started) < 20*time.Millisecond {
		t.Fatal("the wait must be honoured before deferring")
	}
	release()
	release, err = registry.Acquire(context.Background(), intelligencedomain.ProviderManaged, time.Millisecond)
	if err != nil {
		t.Fatalf("released slot must be free again: %v", err)
	}
	release()
	if _, err := registry.Acquire(context.Background(), "NOT_REGISTERED", time.Millisecond); fault.CodeOf(err) != fault.ProviderUnavailable {
		t.Fatalf("unknown provider = %v", err)
	}
}

func TestDeferralOfBoundsTheWait(t *testing.T) {
	if _, ok := deferralOf(errors.New("plain")); ok {
		t.Fatal("an unclassified error is not a deferral")
	}
	if _, ok := deferralOf(fault.New(fault.ProviderUnavailable, "X", true)); ok {
		t.Fatal("only RATE_LIMITED defers")
	}
	short := fault.New(fault.RateLimited, "CATALOG_ROUTES_DEFERRED", true)
	if wait, ok := deferralOf(short); !ok || wait != 5*time.Second {
		t.Fatalf("missing RetryAfter should wait the default: %v %v", wait, ok)
	}
	long := fault.New(fault.RateLimited, "PROVIDER_RATE_LIMITED", true)
	long.RetryAfter = time.Hour
	if wait, ok := deferralOf(long); !ok || wait != intelligencedomain.MaximumDeferralWait {
		t.Fatalf("an hour-long Retry-After must be capped: %v %v", wait, ok)
	}
}

// admissionRepository records how Execute closes a claimed job.
type admissionRepository struct {
	Repository
	deferred  []time.Time
	reasons   []string
	reopened  []*time.Time
	closedAs  []intelligencedomain.JobStatus
	closeCode string
}

func (r *admissionRepository) DeferJob(_ context.Context, _ intelligencedomain.Job, _ intelligencedomain.Attempt, notBefore time.Time, reason string, _ time.Time) error {
	r.deferred = append(r.deferred, notBefore)
	r.reasons = append(r.reasons, reason)
	return nil
}
func (r *admissionRepository) CloseAttempt(context.Context, intelligencedomain.Attempt) error {
	return nil
}
func (r *admissionRepository) CloseJob(_ context.Context, _ string, status intelligencedomain.JobStatus, code string, _ bool, _ time.Time) error {
	r.closedAs = append(r.closedAs, status)
	r.closeCode = code
	return nil
}
func (r *admissionRepository) ReopenJob(_ context.Context, _ string, notBefore *time.Time, _ time.Time) error {
	r.reopened = append(r.reopened, notBefore)
	return nil
}
func (r *admissionRepository) InsertStep(context.Context, intelligencedomain.Step) error { return nil }
func (r *admissionRepository) UpdateStep(context.Context, intelligencedomain.Step) error { return nil }

type admissionInterpreter struct{ err error }

func (i admissionInterpreter) RunActionInterpretation(context.Context, string, string, string, string, string, int64) error {
	return i.err
}

type admissionTransactor struct{}

func (admissionTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type admissionClock struct{ now time.Time }

func (c admissionClock) Now() time.Time { return c.now }

type admissionIDs struct{}

func (admissionIDs) NewID() string { return "00000000-0000-4000-8000-000000000001" }

type admissionProvider struct{}

func (admissionProvider) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}
func (admissionProvider) Available(context.Context, string) error { return nil }
func (admissionProvider) Complete(context.Context, CompletionRequest) (CompletionResult, error) {
	return CompletionResult{}, nil
}

func newAdmissionService(t *testing.T, repository Repository, interpreter ActionInterpretationRunner) *Service {
	t.Helper()
	registry, err := NewProviderRegistry(RegisteredProvider{Provider: admissionProvider{}, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, &cancelTestProducts{}, registry, admissionTransactor{}, admissionClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}, admissionIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), runtimepolicy.NewFinalizer(context.Background(), time.Second))
	if err != nil {
		t.Fatal(err)
	}
	service.SetActionInterpreter(interpreter)
	return service
}

func claimedInterpretation(deferrals int) ClaimedJob {
	job := intelligencedomain.Job{ID: "job", UserID: "user", CurationID: "curation", CurationActionID: "action", ExecutionActionID: "action", PlanID: "plan",
		Target:   intelligencedomain.JobTarget{Kind: intelligencedomain.TargetActionInterpretation, ID: "action", Revision: 1},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "gpt-5-nano", Status: intelligencedomain.JobRunning, AttemptCount: 1 + deferrals, DeferCount: deferrals}
	attempt := intelligencedomain.Attempt{ID: "attempt", JobID: "job", UserID: "user", Ordinal: 1 + deferrals, Provider: intelligencedomain.ProviderManaged, RequestKey: "key", Status: intelligencedomain.AttemptRunning, DeadlineAt: time.Now().Add(time.Minute), StartedAt: time.Now()}
	return ClaimedJob{Job: job, Attempt: attempt}
}

func TestExecuteDefersOnResourceWaitWithoutSpendingARetry(t *testing.T) {
	busy := fault.New(fault.RateLimited, "CATALOG_ROUTES_DEFERRED", true)
	busy.RetryAfter = 12 * time.Second
	repository := &admissionRepository{}
	service := newAdmissionService(t, repository, admissionInterpreter{err: busy})
	if err := service.Execute(context.Background(), claimedInterpretation(0)); err != nil {
		t.Fatal(err)
	}
	if len(repository.deferred) != 1 || len(repository.closedAs) != 0 || len(repository.reopened) != 0 {
		t.Fatalf("deferred=%d closed=%v reopened=%d", len(repository.deferred), repository.closedAs, len(repository.reopened))
	}
	if want := service.clock.Now().Add(12 * time.Second); !repository.deferred[0].Equal(want) || repository.reasons[0] != "CATALOG_ROUTES_DEFERRED" {
		t.Fatalf("deferred until %s reason %s", repository.deferred[0], repository.reasons[0])
	}
}

func TestExecuteReportsExhaustedResourceWaitAsRetryableFailure(t *testing.T) {
	busy := fault.New(fault.RateLimited, "MODEL_SLOT_BUSY", true)
	repository := &admissionRepository{}
	service := newAdmissionService(t, repository, admissionInterpreter{err: busy})
	if err := service.Execute(context.Background(), claimedInterpretation(intelligencedomain.MaximumDeferrals)); err != nil {
		t.Fatal(err)
	}
	// Twelve waits already: the job is closed as a retryable failure with its
	// own reason. Executed attempts are still 1, so automatic retry reopens
	// it after the first backoff instead of giving up.
	if len(repository.deferred) != 0 || len(repository.reopened) != 1 || repository.closeCode != intelligencedomain.ReasonResourceWaitExhausted {
		t.Fatalf("deferred=%d reopened=%d code=%s", len(repository.deferred), len(repository.reopened), repository.closeCode)
	}
	if want := service.clock.Now().Add(intelligencedomain.RetryBackoff(1)); repository.reopened[0] == nil || !repository.reopened[0].Equal(want) {
		t.Fatalf("reopen backoff = %v want %s", repository.reopened[0], want)
	}
}

func TestExecuteBacksOffAutomaticRetries(t *testing.T) {
	repository := &admissionRepository{}
	service := newAdmissionService(t, repository, admissionInterpreter{err: fault.New(fault.ProviderRejected, intelligencedomain.ReasonProviderResponse, true)})
	claimed := claimedInterpretation(3)
	claimed.Job.AttemptCount, claimed.Attempt.Ordinal = 5, 5 // two executed, three deferred
	if err := service.Execute(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	if len(repository.reopened) != 1 || repository.reopened[0] == nil || !repository.reopened[0].Equal(service.clock.Now().Add(20*time.Second)) {
		t.Fatalf("second executed attempt must back off 20s: %v", repository.reopened)
	}
	// Three executed attempts: no automatic retry regardless of deferrals.
	repository = &admissionRepository{}
	service = newAdmissionService(t, repository, admissionInterpreter{err: fault.New(fault.ProviderRejected, intelligencedomain.ReasonProviderResponse, true)})
	claimed = claimedInterpretation(3)
	claimed.Job.AttemptCount, claimed.Attempt.Ordinal = 6, 6
	if err := service.Execute(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	if len(repository.reopened) != 0 || len(repository.closedAs) != 1 || repository.closedAs[0] != intelligencedomain.JobFailed {
		t.Fatalf("reopened=%d closed=%v", len(repository.reopened), repository.closedAs)
	}
}

type exhaustedProvider struct{ admissionProvider }

func (exhaustedProvider) Available(_ context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	return fault.New(fault.QuotaExceeded, intelligencedomain.ReasonQuotaExceeded, false)
}

type untouchedProducts struct {
	ProductPort
	t      *testing.T
	failed []string
}

func (p *untouchedProducts) ReadResearchContext(context.Context, string, string, string) (ResearchContext, error) {
	p.t.Fatal("a round the allowance cannot finish must not read its context or call a catalog")
	return ResearchContext{}, nil
}
func (p *untouchedProducts) FailResearchTarget(_ context.Context, _ string, roundID string, reason string, retryable bool) error {
	p.failed = append(p.failed, roundID+":"+reason+":"+map[bool]string{true: "retryable", false: "final"}[retryable])
	return nil
}

// A research round is refused at the first step when the user's allowance
// cannot cover a query and one evaluation batch: no context read, no catalog
// call, and the Round closes with a final QUOTA_EXCEEDED.
func TestResearchRoundIsRefusedBeforeCatalogWhenAllowanceIsSpent(t *testing.T) {
	registry, err := NewProviderRegistry(RegisteredProvider{Provider: exhaustedProvider{}, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	repository := &admissionRepository{}
	products := &untouchedProducts{t: t}
	service, err := NewService(repository, products, registry, admissionTransactor{}, admissionClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}, admissionIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), runtimepolicy.NewFinalizer(context.Background(), time.Second))
	if err != nil {
		t.Fatal(err)
	}
	claimed := claimedInterpretation(0)
	claimed.Job.Target = intelligencedomain.JobTarget{Kind: intelligencedomain.TargetResearchRound, ID: "round-1"}
	if err := service.Execute(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	if len(repository.closedAs) != 1 || repository.closedAs[0] != intelligencedomain.JobFailed || repository.closeCode != intelligencedomain.ReasonQuotaExceeded || len(repository.reopened) != 0 {
		t.Fatalf("closed=%v code=%s reopened=%d", repository.closedAs, repository.closeCode, len(repository.reopened))
	}
	if len(products.failed) != 1 || products.failed[0] != "round-1:QUOTA_EXCEEDED:final" {
		t.Fatalf("research target closure = %v", products.failed)
	}
}

type orphanRepository struct {
	admissionRepository
	orphans []OrphanJob
}

func (r *orphanRepository) OrphanRunningJobs(context.Context, time.Time, int) ([]OrphanJob, error) {
	return r.orphans, nil
}

// The reconciler closes a job parked on an EFFECT_UNKNOWN attempt as a
// retryable failure and releases its Round, instead of leaving the curation
// blocked until the user cancels.
func TestReconcileOrphanJobsClosesParkedJobsAndReleasesTheRound(t *testing.T) {
	round := claimedInterpretation(0).Job
	round.Target = intelligencedomain.JobTarget{Kind: intelligencedomain.TargetResearchRound, ID: "round-9"}
	planning := claimedInterpretation(0).Job
	planning.ID, planning.Target = "job-p", intelligencedomain.JobTarget{Kind: intelligencedomain.TargetPlanningTask, ID: "task-1"}
	repository := &orphanRepository{orphans: []OrphanJob{
		{Job: round, LatestAttemptStatus: intelligencedomain.AttemptEffectUnknown, FailureCode: intelligencedomain.ReasonEffectUnknown},
		{Job: planning, LatestAttemptStatus: intelligencedomain.AttemptFailed, FailureCode: ""},
	}}
	products := &untouchedProducts{t: t}
	service := newAdmissionService(t, repository, admissionInterpreter{})
	service.products = products
	closed, err := service.ReconcileOrphanJobs(context.Background(), 10)
	if err != nil || closed != 2 {
		t.Fatalf("closed=%d err=%v", closed, err)
	}
	if len(repository.closedAs) != 2 || repository.closedAs[0] != intelligencedomain.JobFailed || repository.closedAs[1] != intelligencedomain.JobFailed {
		t.Fatalf("closed=%v", repository.closedAs)
	}
	if len(products.failed) != 1 || products.failed[0] != "round-9:EXTERNAL_EFFECT_UNKNOWN:retryable" {
		t.Fatalf("round release = %v", products.failed)
	}
}

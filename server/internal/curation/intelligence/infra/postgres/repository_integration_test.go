package postgres

import (
	"context"
	"errors"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	"os"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	planningpostgres "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessionpostgres "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"io"
	"log/slog"
)

type fixture struct {
	database   *sharedpostgres.Database
	repository *Repository
	userID     string
	job        intelligencedomain.Job
}

// newFixture seeds one real plan with its planning task, because
// intelligence_jobs carries real foreign keys and a fabricated UUID would
// bounce off them.
func newFixture(t *testing.T, ctx context.Context) fixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(ctx, "../../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lockConnection.Close() })
	if _, err := lockConnection.ExecContext(ctx,
		`SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lockConnection.ExecContext(context.Background(),
			`SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	})
	if _, err := database.DB.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}

	clock := sharedapp.SystemClock{}
	ids := sharedapp.UUIDGenerator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accountService := accountapp.NewService(
		accountpostgres.NewRepository(database), clock, ids,
	)
	user, err := accountService.CreateDevelopmentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessions := shoppingsessionapp.NewService(
		shoppingsessionpostgres.NewRepository(database), clock, ids,
	)
	curationService := curationapp.NewService(
		curationpostgres.NewRepository(
			database, planningpostgres.NewRepository(database),
		),
		sessions, database, clock, ids, logger,
	)
	created, err := curationService.CreatePlan(ctx, curationapp.CreatePlanInput{
		UserID: string(user.ID), OriginalIntent: "지능 통합 테스트 의자",
		PlanningMode: "SINGLE", ExecutionMode: "EXPERIMENT",
		TotalBudget: curationapp.MoneyInput{Amount: "100", Currency: "USD"},
		Country:     "KR", City: "서울", Category: "chair", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: ids.NewID(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The plan create recorded the initial curation action; jobs reference it
	// because a job's fan-out is projected through its user action.
	var actionID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT id FROM curation_actions WHERE actor_user_id = $1 LIMIT 1
	`, string(user.ID)).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database, ids)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: ids.NewID(), UserID: string(user.ID),
		CurationID:       string(created.Curation.ID),
		CurationActionID: actionID,
		PlanID:           string(created.Plan.ID),
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetPlanningTask,
			ID:   string(created.PlanningTask.ID),
		},
		Provider: intelligencedomain.ProviderManaged,
		ModelKey: "gpt-5-nano",
		Now:      clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		database: database, repository: repository,
		userID: string(user.ID), job: job,
	}
}

func TestPostgresOneJobPerTargetAndReplay(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}

	found, ok, err := f.repository.FindJobByTarget(ctx, f.userID, f.job.Target)
	if err != nil || !ok || found.ID != f.job.ID {
		t.Fatalf("find by target = (%v, %v, %v)", found.ID, ok, err)
	}
	// A second job for the same target must be impossible: the replayed user
	// command resolves to the stored job instead of dispatching twice.
	duplicate := f.job
	duplicate.ID = "11111111-2222-4333-8444-555555555555"
	if err := f.repository.InsertJob(ctx, duplicate); err == nil {
		t.Fatal("second job for the same target must be rejected")
	}
	progress, err := f.repository.ListCurationJobs(ctx, f.userID, f.job.CurationID)
	if err != nil || len(progress) != 1 ||
		progress[0].ActionID != f.job.CurationActionID {
		t.Fatalf("job action projection=%#v err=%v", progress, err)
	}
}

func TestPostgresActiveCurationWorkFollowsJobStatus(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}
	active, err := f.repository.CurationHasActiveJobs(
		ctx, f.userID, f.job.CurationID,
	)
	if err != nil || !active {
		t.Fatalf("pending job active=%v err=%v", active, err)
	}
	now := time.Now().UTC()
	if err := f.repository.CloseJob(
		ctx, f.job.ID, intelligencedomain.JobFailed,
		intelligencedomain.ReasonSubmissionInvalid, false, now,
	); err != nil {
		t.Fatal(err)
	}
	active, err = f.repository.CurationHasActiveJobs(
		ctx, f.userID, f.job.CurationID,
	)
	if err != nil || active {
		t.Fatalf("failed job active=%v err=%v", active, err)
	}
	var taskStatus string
	if err := f.database.DB.QueryRowContext(ctx, `
		SELECT status FROM planning_tasks WHERE id=$1
	`, f.job.Target.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "REQUESTED" {
		t.Fatalf("test no longer covers terminal Job + requested target: %s", taskStatus)
	}
}

func TestPostgresJobProgressIncludesLatestEffectUnknownAttempt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, time.Now().UTC())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%d, %v)", len(claimed), err)
	}
	attempt := claimed[0].Attempt
	attempt.Status = intelligencedomain.AttemptEffectUnknown
	attempt.FailureCode = intelligencedomain.ReasonEffectUnknown
	if err := f.repository.CloseAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}

	progress, err := f.repository.ListCurationJobs(ctx, f.userID, f.job.CurationID)
	if err != nil || len(progress) != 1 {
		t.Fatalf("progress = (%d, %v)", len(progress), err)
	}
	if progress[0].Status != string(intelligencedomain.JobRunning) ||
		progress[0].LatestAttemptStatus != intelligencedomain.AttemptEffectUnknown {
		t.Fatalf("effect-unknown progress = %#v", progress[0])
	}
}

func TestPostgresConcurrentClaimsProduceExactlyOneAttempt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	var wg sync.WaitGroup
	claims := make([][]byte, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, now)
			if err != nil || len(claimed) == 0 {
				return
			}
			claims[index] = []byte(claimed[0].Attempt.ID)
		}(worker)
	}
	wg.Wait()

	winners := 0
	for _, claim := range claims {
		if len(claim) > 0 {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, want exactly 1", winners)
	}
	// The database-level backstop: even if SKIP LOCKED were bypassed, the
	// partial unique index allows only one live attempt per job.
	var live int
	if err := f.database.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM intelligence_attempts
		WHERE job_id = $1 AND status IN ('RUNNING','EFFECT_UNKNOWN')
	`, f.job.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live attempts = %d, want 1", live)
	}
	var requestKeys int
	if err := f.database.DB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT request_key) FROM intelligence_attempts
		WHERE job_id = $1
	`, f.job.ID).Scan(&requestKeys); err != nil {
		t.Fatal(err)
	}
	if requestKeys != 1 {
		t.Fatalf("request keys = %d, want 1", requestKeys)
	}
}

func TestPostgresCancelledJobCannotBeCompletedOrReopenedByLateWorker(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%d, %v)", len(claimed), err)
	}
	cancelled, err := f.repository.CancelAction(
		ctx, f.userID, f.job.CurationActionID, now.Add(time.Second),
	)
	if err != nil || len(cancelled) != 1 {
		t.Fatalf("cancel = (%d, %v)", len(cancelled), err)
	}

	// A result racing behind the cancellation may close its append-only
	// attempt, but it must never resurrect the abandoned job.
	if err := f.repository.CloseJob(
		ctx, f.job.ID, intelligencedomain.JobSucceeded, "", false,
		now.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ReopenJob(ctx, f.job.ID, nil, now.Add(3*time.Second)); !errors.Is(err, intelligencedomain.ErrRetryExhausted) {
		t.Fatalf("reopen cancelled job error = %v", err)
	}

	var status string
	if err := f.database.DB.QueryRowContext(ctx, `
		SELECT status FROM intelligence_jobs WHERE id = $1
	`, f.job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(intelligencedomain.JobCancelled) {
		t.Fatalf("late worker changed cancelled job to %s", status)
	}
}

func TestPostgresActionAdmissionLockSerializesOneUser(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	firstLocked := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- f.database.WithinTransaction(ctx, func(txContext context.Context) error {
			if err := f.repository.LockActionAdmission(txContext, f.userID); err != nil {
				return err
			}
			close(firstLocked)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstLocked:
	case <-time.After(time.Second):
		t.Fatal("first admission lock was not acquired")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- f.database.WithinTransaction(ctx, func(txContext context.Context) error {
			return f.repository.LockActionAdmission(txContext, f.userID)
		})
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second admission passed before first commit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second admission did not continue after first commit")
	}
}

func TestPostgresReopenStopsAtTheAttemptCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for attempt := 0; attempt < intelligencedomain.MaximumAttempts; attempt++ {
		claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, now)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim %d = (%d, %v)", attempt, len(claimed), err)
		}
		record := claimed[0].Attempt
		record.Status = intelligencedomain.AttemptFailed
		record.FailureCode = intelligencedomain.ReasonProviderResponse
		record.Retryable = true
		completed := now.Add(time.Second)
		record.CompletedAt = &completed
		if err := f.repository.CloseAttempt(ctx, record); err != nil {
			t.Fatal(err)
		}
		if err := f.repository.CloseJob(
			ctx, f.job.ID, intelligencedomain.JobFailed,
			intelligencedomain.ReasonProviderResponse, true, now,
		); err != nil {
			t.Fatal(err)
		}
		if attempt < intelligencedomain.MaximumAttempts-1 {
			if err := f.repository.ReopenJob(ctx, f.job.ID, nil, now); err != nil {
				t.Fatalf("reopen %d: %v", attempt, err)
			}
		}
	}
	// The ceiling: reopening after the final attempt must refuse, so a failure
	// that repeats can never become an open-ended spend.
	if err := f.repository.ReopenJob(ctx, f.job.ID, nil, now); err == nil {
		t.Fatal("reopen past the ceiling must be refused")
	}
}

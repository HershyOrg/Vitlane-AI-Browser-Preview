package postgres

import (
	"context"
	"encoding/json"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	cp "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"sync"
	"testing"
	"time"
)

func TestThreadEnabledJobAdmissionAndCounting(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	f.repository.EnableThreads()
	if err := f.database.WithinTransaction(ctx, func(tx context.Context) error { return f.repository.InsertJob(tx, f.job) }); err != nil {
		t.Fatal(err)
	}
	repo := cp.NewRepository(f.database, nil)
	thread, err := repo.ReadThread(ctx, f.userID, f.job.CurationActionID)
	if err != nil || thread.Status != "RUNNING" || len(thread.Actions) != 2 || thread.Actions[0].Type != "AUTO_START" || thread.Actions[0].Status != "SUCCEEDED" || thread.Actions[len(thread.Actions)-1].Type != "INTENT_NEXT_STEP" {
		t.Fatalf("%+v %v", thread, err)
	}
	jobs, err := repo.ActionJobs(ctx, thread.ID, thread.Actions[len(thread.Actions)-1].ID)
	if err != nil || len(jobs) != 1 || jobs[0].JobID != f.job.ID {
		t.Fatalf("job receipt: %+v %v", jobs, err)
	}
	if n, e := f.repository.CountActiveActions(ctx, f.userID); e != nil || n != 1 {
		t.Fatalf("count=%d %v", n, e)
	}
	if n, e := f.repository.CountActiveActions(c.WithThreadExecution(ctx, thread.ID, thread.Actions[len(thread.Actions)-1].ID), f.userID); e != nil || n != 0 {
		t.Fatalf("self exclusion count=%d %v", n, e)
	}
}

func TestThreadRetryPreservesFailureAndAdmitsOneNewThread(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "conversation"}[admitted], func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, ctx)
			f.repository.EnableThreads()
			if err := f.database.WithinTransaction(ctx, func(tx context.Context) error { return f.repository.InsertJob(tx, f.job) }); err != nil {
				t.Fatal(err)
			}
			repo := cp.NewRepository(f.database, nil)
			old, err := repo.ReadThread(ctx, f.userID, f.job.CurationActionID)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.repository.CloseJob(ctx, f.job.ID, intelligence.JobFailed, "PROVIDER_RESPONSE_INVALID", true, time.Now()); err != nil {
				t.Fatal(err)
			}
			// A failed sibling cannot escape an active request while others are running.
			if err = f.repository.ReopenJob(ctx, f.job.ID, nil, time.Now()); err == nil {
				t.Fatal("active parent allowed retry")
			}
			old.Status = "FAILED"
			old.Origin = "REQUEST"
			old.Actions[len(old.Actions)-1].Status = "FAILED"
			old.Actions[len(old.Actions)-1].Jobs, err = repo.ActionJobs(ctx, old.ID, old.Actions[len(old.Actions)-1].ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.SaveThread(ctx, old); err != nil {
				t.Fatal(err)
			}
			old, err = repo.ReadThread(ctx, f.userID, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(old)
			retry := func() error {
				return f.database.WithinTransaction(ctx, func(tx context.Context) error {
					if admitted {
						rid := f.repository.ids.NewID()
						q := f.database.Queryer(tx)
						if _, err := q.ExecContext(tx, `SELECT curation_admit_request($1,$2,$3::uuid,'RETRY','',$3::text,'RESOLVING')`, f.userID, f.job.CurationID, rid); err != nil {
							return err
						}
						if _, err := q.ExecContext(tx, `SELECT set_config('vitlane.conversation_request',$1,true)`, rid); err != nil {
							return err
						}
					}
					return f.repository.ReopenJob(tx, f.job.ID, nil, time.Now())
				})
			}
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for n := 0; n < 2; n++ {
				wg.Add(1)
				go func() { defer wg.Done(); results <- retry() }()
			}
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else {
					t.Log(err)
				}
			}
			if successes != 1 {
				t.Fatalf("concurrent retry successes=%d", successes)
			}
			var id string
			if err = f.database.DB.QueryRowContext(ctx, `SELECT thread_id FROM curation_thread_jobs WHERE job_id=$1`, f.job.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			next, err := repo.ReadThread(ctx, f.userID, id)
			if err != nil {
				t.Fatal(err)
			}
			if id == old.ID || next.RetryOfThreadID != old.ID || next.RetryOfJobID != f.job.ID || next.Status != "RUNNING" || next.Actions[0].Type != "INTENT_NEXT_STEP" || next.Actions[0].RetryOfActionID != f.job.CurationActionID {
				t.Fatalf("retry=%+v", next)
			}
			after, err := repo.ReadThread(ctx, f.userID, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			// SaveThread enriches labels/timestamp. The frozen failure facts must match.
			after.UpdatedAt = old.UpdatedAt
			after.TargetLabels = old.TargetLabels
			frozen, _ := json.Marshal(after)
			if string(frozen) != string(before) {
				t.Fatalf("failure was rewritten: %s => %s", before, frozen)
			}
			if n, err := f.repository.CountActiveActions(ctx, f.userID); err != nil || n != 1 {
				t.Fatalf("slots=%d %v", n, err)
			}
			job, err := f.repository.GetJob(ctx, f.userID, f.job.ID)
			if err != nil || job.Status != intelligence.JobPending || job.AttemptCount != 0 {
				t.Fatalf("job=%+v %v", job, err)
			}
			execution, err := f.repository.ThreadExecutionContext(ctx, f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if c.ThreadExecutionFrom(execution).ThreadID != next.ID || c.ThreadExecutionFrom(execution).AllowFeedbackCriteria {
				t.Fatal("retry changed original Auto criteria authority")
			}
			// The old execution identity is fenced; the new parent controls cancellation.
			if err = repo.GuardThreadMutation(c.WithThreadExecution(ctx, old.ID, old.Actions[len(old.Actions)-1].ID), f.userID, f.job.CurationID); err == nil {
				t.Fatal("late original worker admitted")
			}
			if err = f.database.WithinTransaction(ctx, func(tx context.Context) error {
				next.Cancel()
				if err := repo.SaveThread(tx, next); err != nil {
					return err
				}
				_, err := f.repository.CancelAction(tx, f.userID, job.ExecutionActionID, time.Now())
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err = repo.GuardThreadMutation(execution, f.userID, f.job.CurationID); err == nil {
				t.Fatal("cancelled retry worker admitted")
			} else if f, ok := fault.As(err); !ok || f.Reason != "CURATION_THREAD_CANCELLED" {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticRetryKeepsActionThreadAndOneRequest(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	f.repository.EnableThreads()
	if e := f.database.WithinTransaction(ctx, func(tx context.Context) error { return f.repository.InsertJob(tx, f.job) }); e != nil {
		t.Fatal(e)
	}
	claimed, e := f.repository.ClaimPending(ctx, intelligence.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, time.Now())
	if e != nil || len(claimed) != 1 {
		t.Fatalf("claim %+v %v", claimed, e)
	}
	job, attempt := claimed[0].Job, claimed[0].Attempt
	now := time.Now()
	attempt.Status = intelligence.AttemptFailed
	attempt.FailureCode = "PROVIDER_RESPONSE_INVALID"
	attempt.Retryable = true
	attempt.CompletedAt = &now
	if e = f.repository.RetryFailedAttempt(ctx, job, attempt, nil, now); e != nil {
		t.Fatal(e)
	}
	repo := cp.NewRepository(f.database, nil)
	thread, e := repo.ReadThread(ctx, f.userID, f.job.CurationActionID)
	if e != nil {
		t.Fatal(e)
	}
	if thread.Status != "RUNNING" || thread.CurrentAction() == nil || thread.CurrentAction().ID != job.ExecutionActionID {
		t.Fatalf("auto retry moved Action: %+v", thread)
	}
	var n int
	if e = f.database.DB.QueryRowContext(ctx, `SELECT count(*) FROM curation_conversation_requests WHERE curation_id=$1`, job.CurationID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("duplicated request n=%d %v", n, e)
	}
	next, e := f.repository.ClaimPending(ctx, intelligence.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, time.Now())
	if e != nil || len(next) != 1 || next[0].Attempt.Ordinal != 2 || next[0].Job.ExecutionActionID != job.ExecutionActionID {
		t.Fatalf("claim next %+v %v", next, e)
	}
	if e = repo.GuardThreadMutation(c.WithActionAttempt(c.WithThreadExecution(ctx, thread.ID, job.ExecutionActionID), job.ID, attempt.ID), f.userID, job.CurationID); e == nil {
		t.Fatal("late automatic attempt admitted")
	}
}

func TestDevelopmentResetRemovesThreadJobActionDependencies(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	f.repository.EnableThreads()
	if e := f.database.WithinTransaction(ctx, func(tx context.Context) error { return f.repository.InsertJob(tx, f.job) }); e != nil {
		t.Fatal(e)
	}
	if e := accountpostgres.NewRepository(f.database).ResetDevelopmentUser(ctx, accountdomain.UserID(f.userID), time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"curation_threads", "intelligence_jobs"} {
		var n int
		if e := f.database.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", f.userID).Scan(&n); e != nil || n != 0 {
			t.Fatalf("%s n=%d %v", table, n, e)
		}
	}
}

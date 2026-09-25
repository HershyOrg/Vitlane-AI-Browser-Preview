package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// seedResearchJobs adds targets, sessions and rounds to the fixture's plan and
// returns one PENDING research job per target in target order.
func seedResearchJobs(t *testing.T, ctx context.Context, f fixture, count int) []intelligencedomain.Job {
	t.Helper()
	ids := sharedapp.UUIDGenerator{}
	now := time.Now().UTC()
	snapshot, _ := json.Marshal(map[string]any{"researchScope": map[string]any{"country": "KR"}})
	jobs := make([]intelligencedomain.Job, 0, count)
	for index := 0; index < count; index++ {
		target, session, round := ids.NewID(), ids.NewID(), ids.NewID()
		if _, err := f.database.DB.ExecContext(ctx, `
			INSERT INTO plan_targets(id,curation_id,user_id,plan_id,title,normalized_intent,category,allocated_amount,allocated_currency,country,city,url_mode,order_index,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$5,'test',10,'USD','KR','','NONE',$6,1,$7,$7)`,
			target, f.job.CurationID, f.userID, f.job.PlanID, "target "+string(rune('A'+index)), index, now); err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.DB.ExecContext(ctx, `
			INSERT INTO shopping_sessions(id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,status,version,created_at,updated_at)
			VALUES ($1,$2,$3,'{}','{}','RESEARCHING',1,$4,$4)`, session, target, f.userID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.DB.ExecContext(ctx, `
			INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,created_at)
			VALUES ($1,$2,$3,1,'test',1,'hash',$4,'REQUESTED',$5)`, round, session, f.userID, snapshot, now); err != nil {
			t.Fatal(err)
		}
		job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
			ID: ids.NewID(), UserID: f.userID, CurationID: f.job.CurationID,
			CurationActionID: f.job.CurationActionID, PlanID: f.job.PlanID,
			Target:   intelligencedomain.JobTarget{Kind: intelligencedomain.TargetResearchRound, ID: round},
			Provider: intelligencedomain.ProviderManaged, ModelKey: "gpt-5-nano", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.repository.InsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	return jobs
}

// A burst of research jobs is admitted up to the curation cap in target order;
// the rest wait as PENDING and report how many are ahead of them.
func TestPostgresClaimAdmitsResearchUpToTheCapsInTargetOrder(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	jobs := seedResearchJobs(t, ctx, f, 4)
	policy := intelligenceapp.ClaimPolicy{OtherLane: 4, ResearchPerUser: 3, ResearchPerCuration: 2, ResearchGlobal: 6}
	now := time.Now().UTC()

	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("first claim = (%d, %v), want the curation cap of 2", len(claimed), err)
	}
	if claimed[0].Job.ID != jobs[0].ID || claimed[1].Job.ID != jobs[1].ID {
		t.Fatalf("claim order must follow the target order: %s %s", claimed[0].Job.ID, claimed[1].Job.ID)
	}
	again, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second claim = (%d, %v), want 0 while the cap is full", len(again), err)
	}
	progress, err := f.repository.ListCurationJobs(ctx, f.userID, f.job.CurationID)
	if err != nil {
		t.Fatal(err)
	}
	ahead := map[string]int{}
	for _, item := range progress {
		if item.QueueAhead != nil {
			ahead[item.JobID] = *item.QueueAhead
		}
	}
	if ahead[jobs[2].ID] != 0 || ahead[jobs[3].ID] != 1 || len(ahead) != 2 {
		t.Fatalf("queue positions = %v", ahead)
	}

	// Closing one running job frees exactly one place, taken by the next target.
	if err := f.repository.CloseAttempt(ctx, closedAttempt(claimed[0].Attempt, now)); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.CloseJob(ctx, claimed[0].Job.ID, intelligencedomain.JobSucceeded, "", false, now); err != nil {
		t.Fatal(err)
	}
	next, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now)
	if err != nil || len(next) != 1 || next[0].Job.ID != jobs[2].ID {
		t.Fatalf("third claim = (%d, %v)", len(next), err)
	}
	// The global cap also holds: with a global cap of 3 and 2 running, only one more.
	tight := policy
	tight.ResearchGlobal, tight.ResearchPerUser, tight.ResearchPerCuration = 3, 4, 4
	last, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, tight, time.Minute, now)
	if err != nil || len(last) != 1 || last[0].Job.ID != jobs[3].ID {
		t.Fatalf("global cap claim = (%d, %v)", len(last), err)
	}
}

func closedAttempt(attempt intelligencedomain.Attempt, now time.Time) intelligencedomain.Attempt {
	attempt.Status = intelligencedomain.AttemptSucceeded
	attempt.CompletedAt = &now
	return attempt
}

// A deferral returns the job to the queue without spending an executed
// attempt: the next claim happens only after the wait, the automatic retry
// ceiling still counts executed attempts, and the projection explains the wait.
func TestPostgresDeferralQueuesWithoutSpendingAnAttempt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	jobs := seedResearchJobs(t, ctx, f, 1)
	policy := intelligenceapp.DefaultClaimPolicy()
	// PostgreSQL stores microseconds; truncate so stored times compare equal.
	now := time.Now().UTC().Truncate(time.Microsecond)

	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%d, %v)", len(claimed), err)
	}
	notBefore := now.Add(30 * time.Second)
	if err := f.repository.DeferJob(ctx, claimed[0].Job, claimed[0].Attempt, notBefore, "CATALOG_ROUTES_DEFERRED", now); err != nil {
		t.Fatal(err)
	}
	// A second deferral of the same closed attempt is rejected.
	if err := f.repository.DeferJob(ctx, claimed[0].Job, claimed[0].Attempt, notBefore, "CATALOG_ROUTES_DEFERRED", now); err != intelligencedomain.ErrJobClosed {
		t.Fatalf("late deferral = %v", err)
	}
	job, err := f.repository.GetJob(ctx, f.userID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != intelligencedomain.JobPending || job.DeferCount != 1 || job.AttemptCount != 1 || job.ExecutedAttempts() != 0 ||
		job.QueueReason != "CATALOG_ROUTES_DEFERRED" || job.NotBefore == nil || !job.NotBefore.Equal(notBefore) {
		t.Fatalf("deferred job = %+v", job)
	}
	var attemptStatus string
	if err := f.database.DB.QueryRowContext(ctx, `SELECT status FROM intelligence_attempts WHERE id=$1`, claimed[0].Attempt.ID).Scan(&attemptStatus); err != nil || attemptStatus != "DEFERRED" {
		t.Fatalf("attempt status = %s err=%v", attemptStatus, err)
	}
	progress, err := f.repository.ListCurationJobs(ctx, f.userID, f.job.CurationID)
	if err != nil {
		t.Fatal(err)
	}
	var shown *intelligenceapp.JobProgress
	for index := range progress {
		if progress[index].JobID == jobs[0].ID {
			shown = &progress[index]
		}
	}
	if shown == nil || shown.QueueReason != "CATALOG_ROUTES_DEFERRED" || shown.NotBefore == nil || shown.QueueAhead == nil || *shown.QueueAhead != 0 {
		t.Fatalf("projection = %+v", shown)
	}

	// Before the wait passes the job is invisible to the claim; after it, the
	// next attempt is ordinal 2 and the job has still executed nothing.
	if early, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now.Add(10*time.Second)); err != nil || len(early) != 0 {
		t.Fatalf("early claim = (%d, %v)", len(early), err)
	}
	later, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, notBefore.Add(time.Second))
	if err != nil || len(later) != 1 || later[0].Attempt.Ordinal != 2 || later[0].Job.ExecutedAttempts() != 1 || later[0].Job.QueueReason != "" {
		t.Fatalf("claim after wait = (%d, %v) %+v", len(later), err, later)
	}

	// A real retryable failure reopens with a backoff and counts one executed
	// attempt; the reopened job also waits out its not-before time.
	failed := later[0].Attempt
	failedAt := notBefore.Add(time.Second)
	failed.Status, failed.FailureCode, failed.Retryable, failed.CompletedAt = intelligencedomain.AttemptFailed, "PROVIDER_RESPONSE_INVALID", true, &failedAt
	backoff := notBefore.Add(20 * time.Second)
	if err := f.repository.RetryFailedAttempt(ctx, later[0].Job, failed, &backoff, notBefore.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	job, err = f.repository.GetJob(ctx, f.userID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != intelligencedomain.JobPending || job.QueueReason != intelligencedomain.ReasonRetryBackoff || job.NotBefore == nil || !job.NotBefore.Equal(backoff) || job.ExecutedAttempts() != 1 {
		t.Fatalf("reopened job = %+v", job)
	}
	if early, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, backoff.Add(-time.Second)); err != nil || len(early) != 0 {
		t.Fatalf("claim before backoff = (%d, %v)", len(early), err)
	}
	if third, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, backoff.Add(time.Second)); err != nil || len(third) != 1 || third[0].Attempt.Ordinal != 3 || third[0].Job.ExecutedAttempts() != 2 {
		t.Fatalf("claim after backoff = (%d, %v)", len(third), err)
	}
}

// Other job kinds keep their own lane: a full research cap never blocks them.
func TestPostgresOtherJobsHaveTheirOwnLane(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	seedResearchJobs(t, ctx, f, 2)
	if err := f.repository.InsertJob(ctx, f.job); err != nil {
		t.Fatal(err)
	}
	policy := intelligenceapp.ClaimPolicy{OtherLane: 1, ResearchPerUser: 1, ResearchPerCuration: 1, ResearchGlobal: 1}
	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, time.Now().UTC())
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim = (%d, %v), want one planning job and one research job", len(claimed), err)
	}
	kinds := map[intelligencedomain.TargetKind]int{}
	for _, item := range claimed {
		kinds[item.Job.Target.Kind]++
	}
	if kinds[intelligencedomain.TargetPlanningTask] != 1 || kinds[intelligencedomain.TargetResearchRound] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
}

// A job whose latest attempt is parked EFFECT_UNKNOWN, or whose closing write
// never landed, shows up as an orphan once the grace period has passed; a job
// with a live attempt never does.
func TestPostgresOrphanRunningJobsFindParkedAttempts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	jobs := seedResearchJobs(t, ctx, f, 2)
	policy := intelligenceapp.DefaultClaimPolicy()
	now := time.Now().UTC().Truncate(time.Microsecond)
	claimed, err := f.repository.ClaimPending(ctx, intelligencedomain.ProviderManaged, policy, time.Minute, now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim = (%d, %v)", len(claimed), err)
	}
	parked := claimed[0].Attempt
	parked.Status, parked.FailureCode = intelligencedomain.AttemptEffectUnknown, intelligencedomain.ReasonEffectUnknown
	if err := f.repository.CloseAttempt(ctx, parked); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.DB.ExecContext(ctx, `UPDATE intelligence_jobs SET updated_at = $2 WHERE id = $1`, claimed[0].Job.ID, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	orphans, err := f.repository.OrphanRunningJobs(ctx, now.Add(-2*time.Minute), 10)
	if err != nil || len(orphans) != 1 {
		t.Fatalf("orphans = (%d, %v)", len(orphans), err)
	}
	if orphans[0].Job.ID != jobs[0].ID || orphans[0].LatestAttemptStatus != intelligencedomain.AttemptEffectUnknown || orphans[0].FailureCode != intelligencedomain.ReasonEffectUnknown {
		t.Fatalf("orphan = %+v", orphans[0])
	}
	// Inside the grace period the same job is left alone.
	if recent, err := f.repository.OrphanRunningJobs(ctx, now.Add(-10*time.Minute), 10); err != nil || len(recent) != 0 {
		t.Fatalf("within grace = (%d, %v)", len(recent), err)
	}
}

package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// The operator summary reads durable ledgers only. Rounds outside the window,
// completions without outcomes and admission denials must each land in the
// right bucket, and a replayed completion must not duplicate its outcome row.
func TestResearchRoundSummaryAggregatesDurableLedgers(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	const (
		user     = "98000000-0000-4000-8000-000000000001"
		plan     = "98000000-0000-4000-8000-000000000002"
		curation = "98000000-0000-4000-8000-000000000003"
		target   = "98000000-0000-4000-8000-000000000004"
		session  = "98000000-0000-4000-8000-000000000010"
		ready    = "98000000-0000-4000-8000-000000000011"
		noResult = "98000000-0000-4000-8000-000000000012"
		failed   = "98000000-0000-4000-8000-000000000013"
		stale    = "98000000-0000-4000-8000-000000000014"
		action   = "98000000-0000-4000-8000-000000000020"
		job      = "98000000-0000-4000-8000-000000000021"
		attempt  = "98000000-0000-4000-8000-000000000022"
	)
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-10 * 24 * time.Hour)
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.DB.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("%s: %v", statement[:40], err)
		}
	}
	exec(`INSERT INTO shopping_sessions(id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,status,version,created_at,updated_at) VALUES ($1,$2,$3,'{}','{}','REVIEWING',1,$4,$4)`, session, target, user, now)
	round := func(id string, number int, country, status string, created time.Time, reason *string, retryable *bool) {
		snapshot, _ := json.Marshal(map[string]any{"researchScope": map[string]any{"country": country}})
		completed := &created
		if status == string(researchdomain.RoundStatusRequested) {
			completed = nil
		}
		exec(`INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,failure_reason_code,failure_retryable,created_at,completed_at) VALUES ($1,$2,$3,$4,'test',1,'hash',$5,$6,$7,$8,$9,$10)`, id, session, user, number, snapshot, status, reason, retryable, created, completed)
	}
	reason, notRetryable := "PROVIDER_RESPONSE_INVALID", false
	round(ready, 1, "KR", string(researchdomain.RoundStatusResultsReady), now.Add(-time.Hour), nil, nil)
	round(noResult, 2, "KR", string(researchdomain.RoundStatusNoResults), now.Add(-2*time.Hour), nil, nil)
	round(failed, 3, "KR", string(researchdomain.RoundStatusFailed), now.Add(-3*time.Hour), &reason, &notRetryable)
	round(stale, 4, "US", string(researchdomain.RoundStatusResultsReady), old, nil, nil)

	// A failed job whose last FAILED step was RANKING; the earlier catalog step succeeded.
	exec(`INSERT INTO curation_actions(id,curation_id,actor_user_id,action_type,phase_at_request,subject_type,subject_id,effect_kind,source_ref_type,source_ref_id,expected_curation_version,request_hash,created_at) VALUES ($1,$2,$3,'TARGET_RESEARCH_AGAIN','CURATING','TARGET',$4,'INTELLIGENCE','TEST','test',1,decode(repeat('ab',32),'hex'),$5)`, action, curation, user, target, now.Add(-3*time.Hour))
	exec(`INSERT INTO intelligence_jobs(id,user_id,curation_id,curation_action_id,plan_id,target_kind,research_round_id,provider,model_key,status,failure_code,retryable,attempt_count,created_at,updated_at,completed_at) VALUES ($1,$2,$3,$4,$5,'RESEARCH_ROUND',$6,'MANAGED','gpt-5.6-luna','FAILED',$7,false,1,$8,$8,$8)`, job, user, curation, action, plan, failed, reason, now.Add(-3*time.Hour))
	exec(`INSERT INTO intelligence_attempts(id,job_id,user_id,ordinal,provider,request_key,status,failure_code,retryable,deadline_at,started_at,completed_at) VALUES ($1,$2,$3,1,'MANAGED',gen_random_uuid(),'FAILED',$4,false,$5,$6,$7)`, attempt, job, user, reason, now.Add(-3*time.Hour).Add(5*time.Minute), now.Add(-3*time.Hour), now.Add(-3*time.Hour).Add(time.Minute))
	exec(`INSERT INTO intelligence_steps(id,job_id,attempt_id,user_id,ordinal,kind,status,started_at,completed_at) VALUES (gen_random_uuid(),$1,$2,$3,1,'SEARCHING_CATALOG','SUCCEEDED',$4,$5)`, job, attempt, user, now.Add(-3*time.Hour), now.Add(-3*time.Hour).Add(30*time.Second))
	exec(`INSERT INTO intelligence_steps(id,job_id,attempt_id,user_id,ordinal,kind,status,reason_code,started_at,completed_at) VALUES (gen_random_uuid(),$1,$2,$3,2,'RANKING','FAILED',$4,$5,$6)`, job, attempt, user, reason, now.Add(-3*time.Hour).Add(30*time.Second), now.Add(-3*time.Hour).Add(time.Minute))
	// A second attempt parked EFFECT_UNKNOWN never reaches the Round row; the
	// attempt ledger is the only place an operator can see it.
	exec(`INSERT INTO intelligence_attempts(id,job_id,user_id,ordinal,provider,request_key,status,failure_code,retryable,deadline_at,started_at) VALUES (gen_random_uuid(),$1,$2,2,'MANAGED',gen_random_uuid(),'EFFECT_UNKNOWN','EXTERNAL_EFFECT_UNKNOWN',false,$3,$4)`, job, user, now.Add(-2*time.Hour).Add(5*time.Minute), now.Add(-2*time.Hour))
	// Managed budget ledger: one settled call and one UNKNOWN reservation that
	// still holds its headroom; an old settled row stays outside the window.
	exec(`INSERT INTO managed_runner_reservations(id,usage_date,user_id,request_key,amount_micros,status,expires_at,created_at,completed_at) VALUES (gen_random_uuid(),$1::date,$2,'summary:settled',24000,'SETTLED',$3,$4,$4)`, now, user, now.Add(10*time.Minute), now.Add(-time.Hour))
	exec(`INSERT INTO managed_runner_reservations(id,usage_date,user_id,request_key,amount_micros,status,expires_at,created_at,completed_at) VALUES (gen_random_uuid(),$1::date,$2,'summary:unknown',70730,'UNKNOWN',$3,$4,$4)`, now, user, now.Add(10*time.Minute), now.Add(-2*time.Hour))
	exec(`INSERT INTO managed_runner_reservations(id,usage_date,user_id,request_key,amount_micros,status,expires_at,created_at,completed_at) VALUES (gen_random_uuid(),$1::date,$2,'summary:stale',1000,'SETTLED',$3,$4,$4)`, old, user, old.Add(10*time.Minute), old)

	outcome := researchapp.ResearchRoundOutcome{
		RoundID: ready, UserID: user, CurationID: curation, TargetID: target, AttemptID: attempt,
		Country: "KR", Mode: "APPEND", ObservedCount: 9, DuplicateCount: 2, RejectedCount: 1,
		AdmittedCount: 3, EvaluatedCount: 3, Duration: 20 * time.Second, CompletedAt: now.Add(-time.Hour),
		SourceCoverage: []researchapp.SourceCoverage{
			{Source: researchdomain.SourceCoupang, Status: "SUCCEEDED", CandidateCount: 3},
			{Source: researchdomain.SourceElevenStreet, Status: "FAILED", ReasonCode: "CATALOG_TIMEOUT"},
		},
	}
	if err := repo.SaveRoundOutcome(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	// A replayed completion converges on one row with the final numbers.
	outcome.AdmittedCount, outcome.EvaluatedCount, outcome.UnevaluatedCount = 5, 4, 1
	if err := repo.SaveRoundOutcome(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRoundOutcome(ctx, researchapp.ResearchRoundOutcome{
		RoundID: noResult, UserID: user, CurationID: curation, TargetID: target, Country: "KR", Mode: "APPEND",
		CompletedAt: now.Add(-2 * time.Hour), SourceCoverage: []researchapp.SourceCoverage{{Source: researchdomain.SourceElevenStreet, Status: "EMPTY"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRoundOutcome(ctx, researchapp.ResearchRoundOutcome{
		RoundID: stale, UserID: user, CurationID: curation, TargetID: target, Country: "US", Mode: "REPLACE",
		AdmittedCount: 16, EvaluatedCount: 16, CompletedAt: old,
		SourceCoverage: []researchapp.SourceCoverage{{Source: researchdomain.SourceShopify, Status: "SUCCEEDED", CandidateCount: 16}},
	}); err != nil {
		t.Fatal(err)
	}

	// Provider ledger: one local denial (quota unconfirmed) and one upstream success.
	if _, err := repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now); err == nil {
		t.Fatal("unconfirmed quota admitted a paid call")
	}
	if err := repo.SaveProviderQuota(ctx, "OWN_PRODUCT", researchapp.CatalogAPIQuota{Limit: 100, Remaining: 100, ResetAt: now.Add(time.Hour), ObservedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	call, err := repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteProviderCall(ctx, call, "SUCCESS", 200, 0, now); err != nil {
		t.Fatal(err)
	}

	summary, err := repo.ReadResearchRoundSummary(ctx, now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != researchapp.ResearchRoundSummarySchemaVersion {
		t.Fatalf("schema: %s", summary.SchemaVersion)
	}
	expectRounds := []researchapp.ResearchRoundStatusCount{{Country: "KR", Status: "FAILED", Count: 1}, {Country: "KR", Status: "NO_RESULTS", Count: 1}, {Country: "KR", Status: "RESULTS_READY", Count: 1}}
	if len(summary.Rounds) != len(expectRounds) {
		t.Fatalf("rounds: %+v", summary.Rounds)
	}
	for i, row := range expectRounds {
		if summary.Rounds[i] != row {
			t.Fatalf("rounds[%d]=%+v want %+v", i, summary.Rounds[i], row)
		}
	}
	if len(summary.Failures) != 1 || summary.Failures[0] != (researchapp.ResearchRoundFailureCount{FailureCode: reason, StepKind: "RANKING", Retryable: false, Count: 1}) {
		t.Fatalf("failures: %+v", summary.Failures)
	}
	expectSources := []researchapp.ResearchRoundSourceCount{
		{Country: "KR", Source: "COUPANG", Status: "SUCCEEDED", Count: 1},
		{Country: "KR", Source: "ELEVENST", Status: "EMPTY", Count: 1},
		{Country: "KR", Source: "ELEVENST", Status: "FAILED", ReasonCode: "CATALOG_TIMEOUT", Count: 1},
	}
	if len(summary.Sources) != len(expectSources) {
		t.Fatalf("sources: %+v", summary.Sources)
	}
	for i, row := range expectSources {
		if summary.Sources[i] != row {
			t.Fatalf("sources[%d]=%+v want %+v", i, summary.Sources[i], row)
		}
	}
	calls := map[string]int64{}
	for _, row := range summary.APICalls {
		calls[row.APIID+"/"+row.Outcome+"/"+map[bool]string{true: "billable", false: "local"}[row.Billable]] = row.Count
	}
	if calls["OWN_PRODUCT/CATALOG_QUOTA_UNCONFIRMED/local"] != 1 || calls["OWN_PRODUCT/SUCCESS/billable"] != 1 || len(calls) != 2 {
		t.Fatalf("api calls: %+v", summary.APICalls)
	}
	if summary.Admitted.Rounds != 2 || summary.Admitted.Median != 2.5 {
		t.Fatalf("admitted: %+v", summary.Admitted)
	}
	buckets := map[string]int64{}
	for _, bucket := range summary.Admitted.Buckets {
		buckets[bucket.Label] = bucket.Count
	}
	if buckets["0"] != 1 || buckets["4-7"] != 1 || buckets["1-3"] != 0 || buckets["8-15"] != 0 || buckets["16+"] != 0 {
		t.Fatalf("buckets: %+v", summary.Admitted.Buckets)
	}
	if summary.Evaluation.Evaluated != 4 || summary.Evaluation.Unevaluated != 1 {
		t.Fatalf("evaluation: %+v", summary.Evaluation)
	}
	expectSteps := []researchapp.ResearchStepDuration{
		{Kind: "RANKING", Count: 1, P50Seconds: 30, P95Seconds: 30},
		{Kind: "SEARCHING_CATALOG", Count: 1, P50Seconds: 30, P95Seconds: 30},
	}
	if len(summary.Steps) != len(expectSteps) {
		t.Fatalf("steps: %+v", summary.Steps)
	}
	for i, row := range expectSteps {
		if summary.Steps[i] != row {
			t.Fatalf("steps[%d]=%+v want %+v", i, summary.Steps[i], row)
		}
	}
	attempts := map[string]int64{}
	for _, row := range summary.Attempts {
		attempts[row.Status+"/"+row.FailureCode] = row.Count
	}
	if attempts["FAILED/"+reason] != 1 || attempts["EFFECT_UNKNOWN/EXTERNAL_EFFECT_UNKNOWN"] != 1 || len(attempts) != 2 {
		t.Fatalf("attempts: %+v", summary.Attempts)
	}
	expectReservations := []researchapp.ManagedReservationCount{
		{Status: "SETTLED", Count: 1, AmountMicros: 24000},
		{Status: "UNKNOWN", Count: 1, AmountMicros: 70730},
	}
	if len(summary.Reservations) != len(expectReservations) {
		t.Fatalf("reservations: %+v", summary.Reservations)
	}
	for i, row := range expectReservations {
		if summary.Reservations[i] != row {
			t.Fatalf("reservations[%d]=%+v want %+v", i, summary.Reservations[i], row)
		}
	}
	var rows int
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM research_round_outcomes WHERE round_id=$1`, ready).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("outcome rows=%d err=%v", rows, err)
	}
}

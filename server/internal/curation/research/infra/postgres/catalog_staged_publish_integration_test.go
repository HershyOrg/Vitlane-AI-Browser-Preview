package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// Staged publication: the observed products enter the pool unevaluated while
// the command stays RUNNING under the same fencing token; the assessments fill
// exactly once; the finalize releases the Round's pending marks and completes
// the command. A retried attempt of the same Round re-admits its pending rows
// instead of dropping them as duplicates, and no stage may write after an
// abort took the reservation away.
func TestCatalogStagedPublicationFillsOnceAndFinalizes(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	database := openCatalogPoolIntegrationDatabaseV2(t, ctx, databaseURL)
	seedCatalogPoolIntegrationTargetV2(t, ctx, database)
	repository := NewRepository(database)
	const (
		userID     = "98000000-0000-4000-8000-000000000001"
		curationID = "98000000-0000-4000-8000-000000000003"
		targetID   = "98000000-0000-4000-8000-000000000004"
		session    = "98000000-0000-4000-8000-000000000010"
		roundID    = "98000000-0000-4000-8000-000000000011"
		laterRound = "98000000-0000-4000-8000-000000000012"
	)
	now := time.Now().UTC()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("%s: %v", statement[:40], err)
		}
	}
	exec(`INSERT INTO shopping_sessions(id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,status,version,created_at,updated_at) VALUES ($1,$2,$3,'{}','{}','RESEARCHING',1,$4,$4)`, session, targetID, userID, now)
	exec(`INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,created_at) VALUES ($1,$2,$3,1,'test',1,'hash','{}','REQUESTED',$4)`, roundID, session, userID, now)
	exec(`INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,created_at,completed_at) VALUES ($1,$2,$3,2,'test',1,'hash','{}','CANCELLED',$4,$4)`, laterRound, session, userID, now)

	command := catalogPoolIntegrationCommandV2(userID, curationID, targetID, "attempt-1", 0, researchapp.CatalogResearchAppendV2, 'a')
	preflight, err := repository.PreflightCatalogSearchV2(ctx, command)
	if err != nil || preflight.Replay {
		t.Fatalf("preflight=%#v err=%v", preflight, err)
	}
	command = catalogPoolCommandWithReservationV2(command, preflight)
	reference := func(index int) researchapp.CatalogCandidateReferenceV2 {
		product := "product-" + string(rune('a'+index))
		value := catalogCandidateReferenceForPoolTestV2(userID, curationID, targetID, "candidate-"+string(rune('a'+index)), product, "shopify-product:"+product, "https://shop.example/products/"+string(rune('a'+index)), index, now)
		value.EvaluationRoundID = roundID
		return value
	}
	interim := researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now, DiscoveryOutcome: &researchapp.ResearchDiscoveryOutcome{SchemaVersion: "vitlane.research-discovery-outcome.v1", AddedCount: 2, UnevaluatedCount: 2, Status: "EVALUATING"}}
	published, err := repository.PublishObservedCandidatesV2(ctx, command, []researchapp.CatalogCandidateReferenceV2{reference(0), reference(1)}, interim)
	if err != nil || published.Version != 1 || len(published.AdmittedCandidateIDs) != 2 {
		t.Fatalf("publish=%#v err=%v", published, err)
	}
	var status string
	var leaseActive bool
	var pending int
	if err := database.DB.QueryRowContext(ctx, `SELECT status, lease_expires_at > now() FROM phase8_research_pool_commands WHERE user_id=$1 AND idempotency_key=$2`, userID, command.IdempotencyKey).Scan(&status, &leaseActive); err != nil || status != "RUNNING" || !leaseActive {
		t.Fatalf("command after publish status=%s lease=%v err=%v", status, leaseActive, err)
	}
	if err := database.DB.QueryRowContext(ctx, `SELECT count(*) FROM phase8_research_candidates WHERE plan_target_id=$1 AND evaluation_round_id=$2 AND axis_assessment IS NULL AND visible`, targetID, roundID).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending unevaluated=%d err=%v", pending, err)
	}

	// The workspace read exposes the pending mark so a retry can pick it up.
	stored, err := repository.LoadCatalogWorkspaceStateV2(ctx, userID, curationID)
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, candidate := range stored.Candidates {
		if candidate.EvaluationRoundID == roundID && candidate.Assessment.AxisAssessment == nil {
			marked++
		}
	}
	if marked != 2 {
		t.Fatalf("workspace pending marks=%d", marked)
	}

	// A second publish for the same Round (a retried attempt after a lost
	// evaluation) re-admits the pending rows rather than skipping them.
	again, err := repository.PublishObservedCandidatesV2(ctx, command, []researchapp.CatalogCandidateReferenceV2{reference(0), reference(1)}, interim)
	if err == nil {
		t.Fatalf("publish on a moved pool version must CAS-fail, got %#v", again)
	}
	assertCatalogPoolFaultV2(t, err, fault.Conflict, researchapp.CatalogCandidatePoolVersionConflictV2)
	retry := command
	retry.ExpectedPoolVersion = 1
	retry.IdempotencyKey = "attempt-2"
	retryPreflight, err := repository.PreflightCatalogSearchV2(ctx, retry)
	if err != nil {
		// The first command still holds its lease; the retry waits for it.
		assertCatalogPoolFaultV2(t, err, fault.Conflict, researchapp.CatalogCandidatePoolSearchInProgressV2)
	} else {
		t.Fatalf("a leased command must block a second reservation: %#v", retryPreflight)
	}

	// Stage two fills the assessments exactly once.
	axis := &researchdomain.AxisAssessmentV1{SchemaVersion: "test", TotalScore: 77}
	filledVersion, filled, err := repository.FillCandidateAssessmentsV2(ctx, command, published.Version, map[string]researchapp.LiveCandidateAssessmentV2{
		published.AdmittedCandidateIDs[0]: {AxisAssessment: axis, IntentPoint: "Observed fit.", Features: []string{"a"}, Specifications: []string{}},
	}, now.Add(time.Second))
	if err != nil || filledVersion != 2 || filled != 1 {
		t.Fatalf("fill version=%d filled=%d err=%v", filledVersion, filled, err)
	}
	var total int
	if err := database.DB.QueryRowContext(ctx, `SELECT (axis_assessment->>'totalScore')::int FROM phase8_research_candidates WHERE candidate_id=$1`, published.AdmittedCandidateIDs[0]).Scan(&total); err != nil || total != 77 {
		t.Fatalf("saved assessment total=%d err=%v", total, err)
	}
	// A second fill never overwrites a saved assessment.
	other := &researchdomain.AxisAssessmentV1{SchemaVersion: "test", TotalScore: 11}
	if _, filled, err := repository.FillCandidateAssessmentsV2(ctx, command, filledVersion, map[string]researchapp.LiveCandidateAssessmentV2{published.AdmittedCandidateIDs[0]: {AxisAssessment: other}}, now.Add(2*time.Second)); err != nil || filled != 0 {
		t.Fatalf("refill filled=%d err=%v", filled, err)
	}
	if err := database.DB.QueryRowContext(ctx, `SELECT (axis_assessment->>'totalScore')::int FROM phase8_research_candidates WHERE candidate_id=$1`, published.AdmittedCandidateIDs[0]).Scan(&total); err != nil || total != 77 {
		t.Fatalf("assessment overwritten: total=%d err=%v", total, err)
	}

	// Stage three finalizes: the pending marks clear, the outcome lands, the
	// command completes and the version moves once more.
	final := researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now.Add(3 * time.Second), Duration: 20 * time.Second, DiscoveryOutcome: &researchapp.ResearchDiscoveryOutcome{SchemaVersion: "vitlane.research-discovery-outcome.v1", AddedCount: 2, EvaluatedCount: 1, UnevaluatedCount: 1, Status: "ADDED"}, SourceCoverage: []researchapp.SourceCoverage{{Source: researchdomain.SourceCoupang, Status: "SUCCEEDED", CandidateCount: 2}}}
	done, err := repository.FinalizeStagedSearchV2(ctx, command, filledVersion+1, roundID, published, final)
	if err != nil || done.Version != 4 || len(done.AdmittedCandidateIDs) != 2 || done.LatestDurationMilliseconds != 20000 {
		t.Fatalf("finalize=%#v err=%v", done, err)
	}
	if err := database.DB.QueryRowContext(ctx, `SELECT count(*) FROM phase8_research_candidates WHERE plan_target_id=$1 AND evaluation_round_id IS NOT NULL`, targetID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending marks after finalize=%d err=%v", pending, err)
	}
	if err := database.DB.QueryRowContext(ctx, `SELECT status FROM phase8_research_pool_commands WHERE user_id=$1 AND idempotency_key=$2`, userID, command.IdempotencyKey).Scan(&status); err != nil || status != "COMPLETED" {
		t.Fatalf("command after finalize=%s err=%v", status, err)
	}
	var outcomeStatus string
	if err := database.DB.QueryRowContext(ctx, `SELECT discovery_outcome->>'status' FROM phase8_research_pools WHERE plan_target_id=$1`, targetID).Scan(&outcomeStatus); err != nil || outcomeStatus != "ADDED" {
		t.Fatalf("final outcome=%s err=%v", outcomeStatus, err)
	}
	// Nothing may write through a completed reservation.
	if _, _, err := repository.FillCandidateAssessmentsV2(ctx, command, done.Version, map[string]researchapp.LiveCandidateAssessmentV2{}, now); err == nil {
		t.Fatal("fill after completion must be refused")
	}

	// An aborted command keeps what it published (cancel never rolls back) but
	// takes the reservation away from every later stage.
	second := catalogPoolIntegrationCommandV2(userID, curationID, targetID, "attempt-3", done.Version, researchapp.CatalogResearchAppendV2, 'b')
	secondPreflight, err := repository.PreflightCatalogSearchV2(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	second = catalogPoolCommandWithReservationV2(second, secondPreflight)
	late := reference(2)
	late.EvaluationRoundID = laterRound
	secondPublished, err := repository.PublishObservedCandidatesV2(ctx, second, []researchapp.CatalogCandidateReferenceV2{late}, interim)
	if err != nil || len(secondPublished.AdmittedCandidateIDs) != 1 {
		t.Fatalf("second publish=%#v err=%v", secondPublished, err)
	}
	if err := repository.AbortCatalogSearchV2(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.FillCandidateAssessmentsV2(ctx, second, secondPublished.Version, map[string]researchapp.LiveCandidateAssessmentV2{secondPublished.AdmittedCandidateIDs[0]: {AxisAssessment: axis}}, now); err == nil {
		t.Fatal("fill after abort must be refused")
	} else {
		assertCatalogPoolFaultV2(t, err, fault.Conflict, researchapp.CatalogCandidatePoolReservationLostV2)
	}
	var visible int
	if err := database.DB.QueryRowContext(ctx, `SELECT count(*) FROM phase8_research_candidates WHERE plan_target_id=$1 AND visible`, targetID).Scan(&visible); err != nil || visible != 3 {
		t.Fatalf("published cards must survive the abort: visible=%d err=%v", visible, err)
	}
}

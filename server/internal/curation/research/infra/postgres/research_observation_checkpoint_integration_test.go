package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	planningpostgres "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/catalogstub"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessionpostgres "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// TestRoundRetryEvaluatesCheckpointedObservationsWithoutCallingSources is the
// PR 1b gate on real PostgreSQL: once the sources have answered, a retryable
// failure later in the Round does not make the next attempt pay them again.
//
//   - attempt A: sources answer, evaluation fails for good -> no checkpoint
//   - attempt B: sources answer again, evaluation fails retryably -> checkpoint
//   - attempt C: zero source calls, the same observations, Round completes
func TestRoundRetryEvaluatesCheckpointedObservationsWithoutCallingSources(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	const (
		user     = "98000000-0000-4000-8000-000000000001"
		plan     = "98000000-0000-4000-8000-000000000002"
		curation = "98000000-0000-4000-8000-000000000003"
		target   = "98000000-0000-4000-8000-000000000004"
		jobID    = "98000000-0000-4000-8000-0000000000b7"
		attemptA = "98000000-0000-4000-8000-0000000000c7"
		attemptB = "98000000-0000-4000-8000-0000000000c8"
		attemptC = "98000000-0000-4000-8000-0000000000c9"
	)
	if _, err := db.DB.ExecContext(ctx,
		`INSERT INTO curation_budgets(curation_id,currency,allocations) VALUES($1,'USD',$2::jsonb)`,
		curation, `[{"targetId":"`+target+`","quantity":1,"amount":null}]`,
	); err != nil {
		t.Fatal(err)
	}

	clock := amazonPipelineClock{}
	ids := sharedapp.UUIDGenerator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := shoppingsessionapp.NewService(shoppingsessionpostgres.NewRepository(db), clock, ids)
	curationService := curationapp.NewService(
		curationpostgres.NewRepository(db, planningpostgres.NewRepository(db)),
		sessions, db, clock, ids, logger,
	)
	repo := NewRepository(db)
	research := researchapp.NewService(repo, curationService, sessions, db, clock, ids, logger)
	stub, err := catalogstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	sources := &countingCatalogGateway{Searcher: stub, fill: 50}
	live, err := researchapp.NewLiveCatalogReviewServiceV2(sources, clock, researchapp.LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 20, Window: time.Minute, MaximumConcurrent: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.EnableWorkspaceRepositoryV2(repo); err != nil {
		t.Fatal(err)
	}
	if err := research.EnableLiveCatalogReviewV2(live); err != nil {
		t.Fatal(err)
	}

	targetSnapshot := shoppingsessionapp.TargetSnapshot{
		ID: target, CurationID: curation, PlanID: plan,
		Title: "test product", NormalizedIntent: "test product", Category: "test",
		AllocatedBudget: shareddomain.Money{Amount: "100", Currency: "USD"},
		TargetHash:      "target-hash", TargetHashSchema: "vitlane.plan-target.v1",
		ConfirmedAt: clock.Now().Format(time.RFC3339),
	}
	scope := shoppingsessionapp.ResearchScopeSnapshot{
		Category: "test", Country: "US", URLMode: "NONE",
		AllowedItems: []string{}, BlockedItems: []string{},
	}
	session, err := sessions.CreateReady(ctx, shoppingsessionapp.CreateReadyInput{
		UserID: user, TargetID: target, TargetSnapshot: targetSnapshot, ScopeSnapshot: scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	roundID := ids.NewID()
	snapshot, err := json.Marshal(researchapp.ResearchContext{
		RoundID: roundID, SessionID: string(session.ID), PlanID: plan,
		RoundNumber: 1, Target: targetSnapshot, ResearchScope: scope,
		CandidateMinimum: 1, CandidateMaximum: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	round, err := researchdomain.NewRound(roundID, string(session.ID), user, 1, snapshot, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRound(ctx, round); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.StartResearch(ctx, user, string(session.ID), roundID); err != nil {
		t.Fatal(err)
	}

	// Managed research always carries criteria; the first attempt saves them
	// with the query checkpoint and every later attempt reads that checkpoint.
	query := researchapp.CatalogIntelligenceCatalogQuery{
		ExecutionVersion: researchapp.DiscoveryPolicyVersion, QueryInputHash: "input-hash",
		QueryProjections: []researchapp.CatalogQueryProjection{{Policy: "english-latin.v1", Language: "en", Query: "refillable fountain pen", Seeds: []string{"refillable fountain pen"}}},
		Query:            "refillable fountain pen", QuerySeeds: []string{"refillable fountain pen"}, ModelKey: "gpt-5-nano",
		Criteria: &curationdomain.TargetCriteriaSetV1{
			SchemaVersion: curationdomain.CriteriaSchema, Version: 1,
			Subject: curationdomain.ResearchSubject{Label: "fountain pen", ProductType: "fountain pen"},
			Axes: []curationdomain.ResearchAxis{{
				AxisID: "writing", Label: "Writing feel", Definition: "How comfortably the nib writes",
				Importance: 5, Origin: "REQUEST",
			}},
			Exclusions: []string{},
		},
	}
	offered := func(observations []researchapp.CatalogIntelligenceCandidateObservation) []string {
		out := make([]string, 0, len(observations))
		for _, observation := range observations {
			out = append(out, observation.ObservationID)
		}
		return out
	}
	failWith := func(retryable bool, seen *[]string) researchapp.CatalogIntelligenceCandidateRanker {
		return func(_ context.Context, observations []researchapp.CatalogIntelligenceCandidateObservation, _ int) ([]researchapp.CatalogIntelligenceRankedCandidate, error) {
			*seen = offered(observations)
			return nil, fault.New(fault.ProviderUnavailable, "PROVIDER_TIMEOUT", retryable)
		}
	}

	// A: the failure closes the job, so nothing is kept for a retry.
	var seenA []string
	_, errA := research.RunCatalogResearchForIntelligence(ctx, user, jobID, attemptA, roundID, query, failWith(false, &seenA))
	if errA == nil {
		t.Fatal("attempt A evaluation failure was swallowed")
	}
	callsAfterA := sources.calls.Load()
	if callsAfterA == 0 || len(seenA) == 0 {
		t.Fatalf("attempt A sources=%d offered=%v err=%v cause=%v", callsAfterA, seenA, errA, errors.Unwrap(errA))
	}

	// B: collects again because A left no checkpoint, then fails retryably.
	var seenB []string
	if _, err := research.RunCatalogResearchForIntelligence(ctx, user, jobID, attemptB, roundID, query, failWith(true, &seenB)); err == nil {
		t.Fatal("attempt B evaluation failure was swallowed")
	} else if failure, ok := fault.As(err); !ok || !failure.Retryable {
		t.Fatalf("attempt B error lost its retry classification: %v", err)
	}
	callsAfterB := sources.calls.Load()
	if callsAfterB != 2*callsAfterA || !reflect.DeepEqual(seenB, seenA) {
		t.Fatalf("attempt B sources=%d (want %d) offered=%v", callsAfterB, 2*callsAfterA, seenB)
	}

	checkpoint, err := repo.ReadCriteriaCheckpoint(ctx, user, roundID)
	if err != nil || checkpoint == nil || checkpoint.ExecutionVersion != researchapp.DiscoveryPolicyVersion || checkpoint.QueryInputHash != "input-hash" || len(checkpoint.QueryProjections) != 1 {
		t.Fatalf("versioned projection checkpoint lost: %+v %v", checkpoint, err)
	}
	contextResult, err := research.GetContextForIntelligence(ctx, user, jobID, roundID)
	if err != nil || !contextResult.Context.ExecutionCheckpoint || len(contextResult.Context.DiscoveryRequirements) != 1 {
		t.Fatalf("full pool retry context lost declarations: %+v %v", contextResult.Context.DiscoveryRequirements, err)
	}

	// C: the retry evaluates B's observations without a single source call.
	var seenC []string
	result, err := research.RunCatalogResearchForIntelligence(ctx, user, jobID, attemptC, roundID, query,
		func(_ context.Context, observations []researchapp.CatalogIntelligenceCandidateObservation, _ int) ([]researchapp.CatalogIntelligenceRankedCandidate, error) {
			seenC = offered(observations)
			ranked := make([]researchapp.CatalogIntelligenceRankedCandidate, 0, len(observations))
			for _, observation := range observations {
				ranked = append(ranked, researchapp.CatalogIntelligenceRankedCandidate{
					ObservationID: observation.ObservationID, IntentPoint: "Matches the request",
					Features: []string{}, Specifications: []string{},
				})
			}
			return ranked, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls := sources.calls.Load(); calls != callsAfterB {
		t.Fatalf("retry called the sources again: %d -> %d", callsAfterB, calls)
	}
	if !reflect.DeepEqual(seenC, seenB) || result.CandidateCount != len(seenB) || result.NoResults {
		t.Fatalf("retry offered=%v want %v result=%+v", seenC, seenB, result)
	}

	closed, err := repo.GetRound(ctx, user, roundID, false)
	if err != nil || closed.Status != researchdomain.RoundStatusResultsReady {
		t.Fatalf("round=%+v err=%v", closed, err)
	}
	var observed, admitted int
	if err := db.DB.QueryRowContext(ctx,
		`SELECT observed_count, admitted_count FROM research_round_outcomes WHERE round_id=$1 AND attempt_id=$2`,
		roundID, attemptC,
	).Scan(&observed, &admitted); err != nil {
		t.Fatalf("round outcome row: %v", err)
	}
	if observed != len(seenB) || admitted != len(seenB) {
		t.Fatalf("outcome observed=%d admitted=%d want %d", observed, admitted, len(seenB))
	}
	statuses := map[string]string{}
	rows, err := db.DB.QueryContext(ctx,
		`SELECT idempotency_key, status FROM phase8_research_pool_commands WHERE user_id=$1 AND plan_target_id=$2`,
		user, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			t.Fatal(err)
		}
		statuses[key] = status
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if statuses[attemptC] != "COMPLETED" || statuses[attemptA] == "COMPLETED" || statuses[attemptB] == "COMPLETED" {
		t.Fatalf("pool commands=%v", statuses)
	}
}

// countingCatalogGateway counts every catalog search the Round pays for.
type countingCatalogGateway struct {
	*catalogstub.Searcher
	calls atomic.Int64
	fill  int
}

func (gateway *countingCatalogGateway) SearchProducts(
	ctx context.Context,
	request researchapp.CatalogProductSearchRequest,
) (researchapp.CatalogProductSearchResult, error) {
	gateway.calls.Add(1)
	result, err := gateway.Searcher.SearchProducts(ctx, request)
	if err == nil && gateway.fill > 0 && len(result.Products) > 0 {
		base := result.Products[0]
		result.Products = nil
		for i := 0; i < min(gateway.fill, request.Limit); i++ {
			p := base
			p.ProviderProductID = fmt.Sprintf("retry-product-%d", i)
			p.SourceProductRef = nil
			p.ProviderOrder = i
			p.Locator = &researchapp.CatalogProductLocator{Kind: researchapp.CatalogLocatorProductURL, ProductURL: &researchapp.CatalogProductURLLocator{CanonicalURL: fmt.Sprintf("https://shop.example/products/retry-%d", i)}}
			result.Products = append(result.Products, p)
		}
	}
	return result, err
}

package postgres

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	shoppingsessionpostgres "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// TestResearchAgainWithZeroResultsKeepsEditedCriteriaAndCompletesRound is the
// Step 4 "정상 0건" scenario end to end on real PostgreSQL. The user edits the
// axes, researches again with feedback, and the catalog admits nothing. The
// round still closes as a completed investigation, the edited criteria are
// exactly what the checkpoint captured and what the next round reads, and a
// model that proposes different criteria cannot rewrite them (2026-09-12
// 설정 변경 권한 원칙).
func TestResearchAgainWithZeroResultsKeepsEditedCriteriaAndCompletesRound(t *testing.T) {
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
		// UUID-shaped identifiers the services validate.
		revisingAction = "98000000-0000-4000-8000-0000000000a1"
		followUpAction = "98000000-0000-4000-8000-0000000000a2"
		jobID          = "98000000-0000-4000-8000-0000000000b1"
		attemptID      = "98000000-0000-4000-8000-0000000000c1"
	)
	// Migration 000110 backfills this ledger row for every existing curation;
	// the shared seed predates budgets.
	if _, err := db.DB.ExecContext(ctx,
		`INSERT INTO curation_budgets(curation_id,currency,allocations) VALUES($1,'USD',$2::jsonb)`,
		curation, `[{"targetId":"`+target+`","quantity":1,"amount":null}]`,
	); err != nil {
		t.Fatal(err)
	}

	clock := amazonPipelineClock{}
	ids := sharedapp.UUIDGenerator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := shoppingsessionapp.NewService(
		shoppingsessionpostgres.NewRepository(db), clock, ids,
	)
	curationService := curationapp.NewService(
		curationpostgres.NewRepository(db, planningpostgres.NewRepository(db)),
		sessions, db, clock, ids, logger,
	)
	repo := NewRepository(db)
	research := researchapp.NewService(
		repo, curationService, sessions, db, clock, ids, logger,
	)
	stub, err := catalogstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	live, err := researchapp.NewLiveCatalogReviewServiceV2(
		emptyCatalogGateway{Searcher: stub}, clock,
		researchapp.LiveCatalogReviewConfigV2{
			MaximumCallsPerWindow: 20, Window: time.Minute, MaximumConcurrent: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := live.EnableWorkspaceRepositoryV2(repo); err != nil {
		t.Fatal(err)
	}
	if err := research.EnableLiveCatalogReviewV2(live); err != nil {
		t.Fatal(err)
	}
	jobs := &zeroResultJobCreator{}
	research.EnableIntelligenceWork(jobs)

	// Initial criteria: one axis at version 1.
	initial := curationdomain.TargetCriteriaSetV1{
		SchemaVersion: curationdomain.CriteriaSchema, Version: 1,
		Subject: curationdomain.ResearchSubject{
			Label: "fountain pen", ProductType: "fountain pen",
		},
		Axes: []curationdomain.ResearchAxis{{
			AxisID: "writing", Label: "Writing feel",
			Definition: "How comfortably the nib writes",
			Importance: 5, Origin: "REQUEST",
		}},
		Exclusions: []string{},
	}
	if _, err := curationService.ChangeTargetCriteria(
		ctx, user, curation, target, curationapp.CriteriaCommand{
			SchemaVersion:           "vitlane.criteria-command.v1",
			ExpectedCriteriaVersion: 0, ExpectedCurationVersion: 1,
			IdempotencyKey: "initial", Criteria: initial,
		},
	); err != nil {
		t.Fatal(err)
	}

	// A REVIEWING session whose first round already closed without results.
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
		UserID: user, TargetID: target,
		TargetSnapshot: targetSnapshot, ScopeSnapshot: scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstRoundID := ids.NewID()
	firstContext, err := json.Marshal(researchapp.ResearchContext{
		RoundID: firstRoundID, SessionID: string(session.ID), PlanID: plan,
		RoundNumber: 1, Target: targetSnapshot, ResearchScope: scope,
		CandidateMinimum: 1, CandidateMaximum: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := researchdomain.NewRound(
		firstRoundID, string(session.ID), user, 1, firstContext, clock.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRound(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.StartResearch(ctx, user, string(session.ID), firstRoundID); err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteFromCandidatePool(false, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRound(ctx, first); err != nil {
		t.Fatal(err)
	}
	session, err = sessions.CompleteResearch(ctx, user, string(session.ID), firstRoundID)
	if err != nil {
		t.Fatal(err)
	}

	// The user edits the axes explicitly: this is the only path that changes
	// saved criteria.
	edited := initial
	edited.Axes = append([]curationdomain.ResearchAxis{}, initial.Axes...)
	edited.Axes = append(edited.Axes, curationdomain.ResearchAxis{
		AxisID: "durability", Label: "Durability",
		Definition: "How long the pen keeps working",
		Importance: 4, Origin: "USER_EDIT",
	})
	if _, err := curationService.ChangeTargetCriteria(
		ctx, user, curation, target, curationapp.CriteriaCommand{
			SchemaVersion:           "vitlane.criteria-command.v1",
			ExpectedCriteriaVersion: 1, ExpectedCurationVersion: 1,
			IdempotencyKey: "edit-durability", Criteria: edited,
		},
	); err != nil {
		t.Fatal(err)
	}

	// Research again with feedback against the edited criteria.
	editedVersion := int64(2)
	again, err := research.ResearchAgain(ctx, researchapp.ResearchAgainInput{
		UserID: user, CurationID: curation, TargetID: target,
		SessionID:        string(session.ID),
		CurationActionID: revisingAction, ClientRequestID: revisingAction,
		Feedback:                "더 튼튼한 걸로",
		ExpectedCriteriaVersion: &editedVersion,
		ExpectedCurationVersion: 1, ExpectedSessionVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Round.Status != researchdomain.RoundStatusRequested ||
		again.Feedback.Status != researchdomain.FeedbackStatusActive ||
		again.Session.Status != shoppingsessiondomain.SessionStatusResearching ||
		len(jobs.rounds) != 1 || jobs.rounds[0] != again.Round.ID {
		t.Fatalf("research again=%+v jobs=%+v", again, jobs.rounds)
	}

	// The worker runs the round. Its query step proposes different criteria,
	// which must not reach the saved settings; the catalog finds nothing.
	proposed := edited
	proposed.Axes = append([]curationdomain.ResearchAxis{}, edited.Axes...)
	proposed.Axes = append(proposed.Axes, curationdomain.ResearchAxis{
		AxisID: "price", Label: "Price", Definition: "Lower price is better",
		Importance: 3, UsesPrice: true, Origin: "FEEDBACK",
	})
	rankerCalls := 0
	result, err := research.RunCatalogResearchForIntelligence(
		ctx, user, jobID, attemptID, again.Round.ID,
		researchapp.CatalogIntelligenceCatalogQuery{
			Query: "durable fountain pen", ProductVertical: "GENERAL", QuerySeeds: []string{"durable fountain pen"},
			ModelKey: "gpt-5-nano", Criteria: &proposed,
		},
		func(
			_ context.Context,
			observations []researchapp.CatalogIntelligenceCandidateObservation,
			_ int,
		) ([]researchapp.CatalogIntelligenceRankedCandidate, error) {
			rankerCalls++
			if len(observations) != 0 {
				t.Fatalf("empty catalog offered %d observations", len(observations))
			}
			return []researchapp.CatalogIntelligenceRankedCandidate{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoResults || result.CandidateCount != 0 || result.PoolVersion != 1 ||
		rankerCalls != 1 {
		t.Fatalf("result=%+v rankerCalls=%d", result, rankerCalls)
	}

	// The edited criteria survived the empty result untouched, and the
	// checkpoint captured them rather than the model's proposal.
	current, err := curationService.TargetCriteria(ctx, user, curation, target)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Version != 2 || len(current.Axes) != 2 ||
		current.Axes[0].AxisID != "writing" || current.Axes[1].AxisID != "durability" ||
		current.Axes[1].Importance != 4 || current.Axes[1].Origin != "USER_EDIT" {
		t.Fatalf("criteria after empty result=%+v", current)
	}
	var checkpointVersion int64
	var checkpointAxes int
	if err := db.DB.QueryRowContext(ctx,
		`SELECT (execution->'Criteria'->>'version')::bigint,
		        jsonb_array_length(execution->'Criteria'->'axes')
		 FROM research_criteria_checkpoints WHERE user_id=$1 AND round_id=$2`,
		user, again.Round.ID,
	).Scan(&checkpointVersion, &checkpointAxes); err != nil {
		t.Fatal(err)
	}
	if checkpointVersion != 2 || checkpointAxes != 2 {
		t.Fatalf("checkpoint version=%d axes=%d", checkpointVersion, checkpointAxes)
	}

	// Round, feedback and session read as a completed investigation.
	round, err := repo.GetRound(ctx, user, again.Round.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if round.Status != researchdomain.RoundStatusNoResults ||
		round.FailureReasonCode != "" || round.FailureRetryable != nil ||
		round.CompletedAt == nil {
		t.Fatalf("round=%+v", round)
	}
	// The completion transaction also wrote the Round outcome ledger row that
	// the operator summary reads: nothing observed, nothing admitted, no
	// evaluation, coverage carried as-is.
	var outcomeMode, outcomeCountry string
	var observed, admitted, evaluated, unevaluated int
	if err := db.DB.QueryRowContext(ctx,
		`SELECT mode, country, observed_count, admitted_count, evaluated_count, unevaluated_count
		 FROM research_round_outcomes WHERE round_id=$1 AND user_id=$2 AND attempt_id=$3`,
		again.Round.ID, user, attemptID,
	).Scan(&outcomeMode, &outcomeCountry, &observed, &admitted, &evaluated, &unevaluated); err != nil {
		t.Fatalf("round outcome row: %v", err)
	}
	if outcomeMode != "APPEND" || outcomeCountry != "US" || observed != 0 || admitted != 0 ||
		evaluated != 0 || unevaluated != 0 {
		t.Fatalf("round outcome mode=%s country=%s observed=%d admitted=%d evaluated=%d unevaluated=%d",
			outcomeMode, outcomeCountry, observed, admitted, evaluated, unevaluated)
	}
	previous, err := repo.GetRound(ctx, user, firstRoundID, false)
	if err != nil || previous.Status != researchdomain.RoundStatusSuperseded {
		t.Fatalf("previous round=%+v err=%v", previous, err)
	}
	feedback, err := repo.GetFeedbackForRound(ctx, user, again.Round.ID, false)
	if err != nil || feedback.Status != researchdomain.FeedbackStatusActive ||
		feedback.CancelledAt != nil {
		t.Fatalf("feedback=%+v err=%v", feedback, err)
	}
	session, err = sessions.Get(ctx, user, string(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != shoppingsessiondomain.SessionStatusReviewing ||
		session.CurrentResearchRoundID == nil ||
		*session.CurrentResearchRoundID != again.Round.ID {
		t.Fatalf("session=%+v", session)
	}
	var candidates int
	var commandStatus string
	if err := db.DB.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM phase8_research_candidates
		        WHERE user_id=$1 AND plan_target_id=$2),
		       (SELECT status FROM phase8_research_pool_commands
		        WHERE user_id=$1 AND plan_target_id=$2 AND idempotency_key=$3)
	`, user, target, attemptID).Scan(&candidates, &commandStatus); err != nil {
		t.Fatal(err)
	}
	if candidates != 0 || commandStatus != "COMPLETED" {
		t.Fatalf("candidates=%d command=%s", candidates, commandStatus)
	}

	// A late worker cannot reopen the closed round.
	if _, err := research.GetContextForIntelligence(
		ctx, user, jobID, again.Round.ID,
	); err == nil {
		t.Fatal("closed round served a new execution context")
	} else if failure, ok := fault.As(err); !ok || failure.Reason != "RESEARCH_ROUND_CLOSED" {
		t.Fatal(err)
	}

	// The next research starts from the edited axes and reuses the stored
	// query once the routing category has also been classified.
	next, err := research.ResearchAgain(ctx, researchapp.ResearchAgainInput{
		UserID: user, CurationID: curation, TargetID: target,
		SessionID:        string(session.ID),
		CurationActionID: followUpAction, ClientRequestID: followUpAction,
		ExpectedCriteriaVersion: &editedVersion,
		ExpectedCurationVersion: 1, ExpectedSessionVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	nextContext, err := research.GetContextForIntelligence(ctx, user, jobID, next.Round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nextContext.Context.Criteria == nil || nextContext.Context.Criteria.Version != 2 ||
		len(nextContext.Context.Criteria.Axes) != 2 ||
		nextContext.Context.Criteria.Axes[1].AxisID != "durability" {
		t.Fatalf("next round criteria=%+v", nextContext.Context.Criteria)
	}
	if !nextContext.FeedbackRequired || nextContext.FeedbackSummary != "" ||
		nextContext.Context.CachedQuery == nil ||
		nextContext.Context.CachedQuery.Query != "durable fountain pen" ||
		nextContext.Context.ExecutionCheckpoint {
		t.Fatalf("next round context=%+v", nextContext)
	}
}

// emptyCatalogGateway keeps the stub's applied-filter proof but admits no
// product, which is how a live catalog answers a query nothing matches.
type emptyCatalogGateway struct {
	*catalogstub.Searcher
}

func (gateway emptyCatalogGateway) SearchProducts(
	ctx context.Context,
	request researchapp.CatalogProductSearchRequest,
) (researchapp.CatalogProductSearchResult, error) {
	result, err := gateway.Searcher.SearchProducts(ctx, request)
	if err != nil {
		return result, err
	}
	result.Products = []researchapp.CatalogProductObservation{}
	return result, nil
}

type zeroResultJobCreator struct {
	rounds []string
}

func (creator *zeroResultJobCreator) CreateResearchJob(
	_ context.Context,
	input researchapp.CreateResearchJobInput,
) error {
	creator.rounds = append(creator.rounds, input.ResearchRoundID)
	return nil
}

func (creator *zeroResultJobCreator) LockActionAdmission(context.Context, string) error {
	return nil
}

func (creator *zeroResultJobCreator) PlanHasActiveWork(
	context.Context, string, string,
) (bool, error) {
	return false, nil
}

func (creator *zeroResultJobCreator) ActiveActionCount(context.Context, string) (int, error) {
	return 0, nil
}

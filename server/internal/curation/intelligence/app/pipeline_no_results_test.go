package app

import (
	"context"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

// A research round that finds nothing is a completed investigation, not a
// failure. The attempt and job close SUCCEEDED, the ranking call is skipped
// because there is nothing to rank, and Research is never asked to fail the
// round — so the saved criteria and the previous candidates are left exactly
// as they were. Only the Round's own NO_RESULTS status tells the user that
// nothing matched.
func TestResearchJobWithNoResultsClosesSucceededWithoutFailingTheRound(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: "job-empty", UserID: "user-1", CurationID: "curation-1",
		CurationActionID: "action-1", PlanID: "plan-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetResearchRound, ID: "round-2",
		},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "managed-model",
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = intelligencedomain.JobRunning
	attempt, err := intelligencedomain.NewAttempt(
		"attempt-empty", job, "request-empty", 1, time.Minute, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := &pipelineResearchProviderV2{}
	registry, err := NewProviderRegistry(RegisteredProvider{
		Provider: provider, Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	products := &pipelineNoResultsProductsV2{}
	repository := &pipelineResearchRepositoryV2{}
	service := &Service{
		repository: repository, products: products, providers: registry,
		transactor: &cancelTestTransactor{},
		clock:      pipelineResearchClockV2{now: now},
		ids:        &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
		finalizer: runtimepolicy.NewFinalizer(context.Background(), time.Second),
	}

	if err := service.Execute(
		context.Background(), ClaimedJob{Job: job, Attempt: attempt},
	); err != nil {
		t.Fatal(err)
	}

	if repository.closedJobStatus != intelligencedomain.JobSucceeded ||
		repository.closedFailure != "" ||
		repository.closedAttempt.Status != intelligencedomain.AttemptSucceeded ||
		repository.closedAttempt.FailureCode != "" {
		t.Fatalf("zero results must close as success: repository=%#v", repository)
	}
	if products.failureCalls != 0 {
		t.Fatalf("zero results must never fail the research round: products=%#v", products)
	}
	if products.runCalls != 1 || products.rankerCalls != 1 ||
		products.rankerObservations != 0 || len(products.ranked) != 0 {
		t.Fatalf("catalog execution=%#v", products)
	}
	// The feedback-revised query went to the model once. Ranking nothing must
	// not spend a second call.
	if len(provider.schemas) != 1 || provider.schemas[0] != SchemaCatalogQuery {
		t.Fatalf("managed calls=%v, want only the catalog query", provider.schemas)
	}
	if products.sweepCalls != 1 || products.sweepActionID != "action-1" {
		t.Fatalf("ready sweep=%#v", products)
	}
	wantSteps := []intelligencedomain.StepKind{
		intelligencedomain.StepInterpreting,
		intelligencedomain.StepSearchingCatalog,
		intelligencedomain.StepRanking,
		intelligencedomain.StepSubmitting,
	}
	if len(repository.updated) != len(wantSteps) {
		t.Fatalf("steps=%#v", repository.updated)
	}
	for index, step := range repository.updated {
		if step.Kind != wantSteps[index] ||
			step.Status != intelligencedomain.StepSucceeded ||
			step.ReasonCode != nil {
			t.Fatalf("step[%d]=%#v", index, step)
		}
	}
}

// pipelineNoResultsProductsV2 is a feedback re-research whose catalog search
// admits nothing, so the ranker is offered zero observations.
type pipelineNoResultsProductsV2 struct {
	pipelineResearchProductsV2
	rankerCalls        int
	rankerObservations int
}

func (products *pipelineNoResultsProductsV2) ReadResearchContext(
	context.Context, string, string, string,
) (ResearchContext, error) {
	// Feedback is present, so the query step consults the model exactly as a
	// user re-research that revised the axes does.
	return ResearchContext{
		RoundID: "round-2", UserID: "user-1", PlanID: "plan-1",
		TargetTitle: "방수 트레일 러닝화", TargetIntent: "trail running shoes",
		Country:          "US",
		FeedbackRequired: true, FeedbackSummary: "내구성 중요도 4 추가",
		FeedbackVersion: 1, FeedbackHash: "feedback-hash",
	}, nil
}

func (products *pipelineNoResultsProductsV2) RunCatalogResearch(
	ctx context.Context,
	_, _, _, _ string,
	query CatalogQueryPayload,
	ranker ResearchCandidateRanker,
) (ResearchExecutionOutcome, error) {
	products.runCalls++
	products.query = query
	products.rankerCalls++
	observations := []ResearchCandidateObservation{}
	products.rankerObservations = len(observations)
	ranked, err := ranker(ctx, observations, 16)
	if err != nil {
		return ResearchExecutionOutcome{}, err
	}
	products.ranked = ranked
	return ResearchExecutionOutcome{
		CandidateCount: 0, NoResults: true, PoolVersion: 1,
	}, nil
}

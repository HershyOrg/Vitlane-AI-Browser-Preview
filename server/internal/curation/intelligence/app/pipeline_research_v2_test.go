package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

func TestResearchJobPreservesControlPlaneWhilePublishingOnlyCatalogCandidates(t *testing.T) {
	now := time.Date(2026, 8, 14, 2, 3, 4, 0, time.UTC)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: "job-1", UserID: "user-1", CurationID: "curation-1",
		CurationActionID: "action-1", PlanID: "plan-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetResearchRound, ID: "round-1",
		},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "managed-model",
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = intelligencedomain.JobRunning
	attempt, err := intelligencedomain.NewAttempt(
		"attempt-1", job, "request-1", 1, time.Minute, now,
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
	products := &pipelineResearchProductsV2{}
	repository := &pipelineResearchRepositoryV2{}
	service := &Service{
		repository: repository, products: products, providers: registry,
		clock: pipelineResearchClockV2{now: now},
		ids:   &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
	}
	claimed := ClaimedJob{Job: job, Attempt: attempt}
	tracker := &stepTracker{service: service, job: job, attempt: attempt}

	if err := service.runResearch(context.Background(), claimed, tracker); err != nil {
		t.Fatal(err)
	}
	if len(provider.schemas) != 2 || provider.schemas[0] != SchemaCatalogQuery ||
		provider.schemas[1] != SchemaCandidateRanking {
		t.Fatalf("managed calls=%v", provider.schemas)
	}
	if products.runCalls != 1 || products.query.Query != "waterproof trail shoes" ||
		len(products.query.MustInclude) != 1 || products.query.MustInclude[0] != "waterproof" ||
		len(products.ranked) != 2 || products.ranked[0].ObservationID != "shopify-b" {
		t.Fatalf("catalog execution=%#v", products)
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
		if step.Kind != wantSteps[index] || step.Status != intelligencedomain.StepSucceeded {
			t.Fatalf("step[%d]=%#v", index, step)
		}
	}
}

func TestPlanningSuccessContinuesToResearchUnderTheSameAction(t *testing.T) {
	now := time.Date(2026, 8, 14, 3, 4, 5, 0, time.UTC)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: "planning-job-1", UserID: "user-1", CurationID: "curation-1",
		CurationActionID: "action-1", PlanID: "plan-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetPlanningTask, ID: "task-1",
		},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "managed-model",
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = intelligencedomain.JobRunning
	attempt, err := intelligencedomain.NewAttempt(
		"planning-attempt-1", job, "request-1", 1, time.Minute, now,
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
	products := &pipelineResearchProductsV2{}
	repository := &pipelineResearchRepositoryV2{}
	service := &Service{
		repository: repository, products: products, providers: registry,
		clock: pipelineResearchClockV2{now: now},
		ids:   &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
	}
	tracker := &stepTracker{
		service: service, job: job, attempt: attempt,
	}
	if err := service.runPlanning(
		context.Background(), ClaimedJob{Job: job, Attempt: attempt}, tracker,
	); err != nil {
		t.Fatal(err)
	}
	if products.submitPlanningCalls != 1 || products.startCuratingCalls != 1 ||
		products.startCuratingPlanID != "plan-1" ||
		products.startCuratingActionID != "action-1" {
		t.Fatalf("planning continuation=%#v", products)
	}
	if len(products.plannedTargets) != 1 ||
		products.plannedTargets[0].SearchQuery != "ceramic coffee mug" {
		t.Fatalf("planned targets=%#v", products.plannedTargets)
	}
}

func TestFinalResearchFailureClosesJobAndProductProjectionTogether(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 6, 7, 0, time.UTC)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: "job-failed", UserID: "user-1", CurationID: "curation-1",
		CurationActionID: "action-1", PlanID: "plan-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetResearchRound, ID: "round-1",
		},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "managed-model",
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = intelligencedomain.JobRunning
	attempt, err := intelligencedomain.NewAttempt(
		"attempt-failed", job, "request-failed", 1, time.Minute, now,
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
	products := &pipelineResearchProductsV2{runErr: fault.New(
		fault.ProviderRejected, "CANDIDATE_RANKING_EMPTY", false,
	)}
	repository := &pipelineResearchRepositoryV2{}
	transactor := &cancelTestTransactor{}
	service := &Service{
		repository: repository, products: products, providers: registry,
		transactor: transactor, clock: pipelineResearchClockV2{now: now},
		ids: &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
		finalizer: runtimepolicy.NewFinalizer(context.Background(), time.Second),
	}

	if err := service.Execute(
		context.Background(), ClaimedJob{Job: job, Attempt: attempt},
	); err != nil {
		t.Fatal(err)
	}
	if repository.closedAttempt.Status != intelligencedomain.AttemptFailed ||
		repository.closedJobStatus != intelligencedomain.JobFailed ||
		repository.closedFailure != "CANDIDATE_RANKING_EMPTY" ||
		products.failureCalls != 1 ||
		products.failureReason != "CANDIDATE_RANKING_EMPTY" ||
		products.failureRetryable || !products.failureTransaction {
		t.Fatalf("repository=%#v products=%#v", repository, products)
	}
}

type pipelineResearchProviderV2 struct {
	schemas []string
}

func (provider *pipelineResearchProviderV2) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}

func (provider *pipelineResearchProviderV2) Available(context.Context, string) error {
	return nil
}

func (provider *pipelineResearchProviderV2) Complete(
	_ context.Context,
	request CompletionRequest,
) (CompletionResult, error) {
	provider.schemas = append(provider.schemas, request.SchemaName)
	switch request.SchemaName {
	case SchemaPlanningTargets:
		return CompletionResult{Content: `{"targets":[{"title":"세라믹 커피 머그","category":"Kitchen","searchQuery":"ceramic coffee mug","rationale":"Matches the request."}]}`}, nil
	case SchemaCatalogQuery:
		return CompletionResult{Content: `{"query":"waterproof trail shoes","mustInclude":["waterproof"],"mustExclude":["used"]}`}, nil
	case SchemaCandidateRanking:
		return CompletionResult{Content: `{"ranked":[{"observationId":"shopify-b","intentPoint":"Best fit.","features":["Feature B"],"specifications":["Spec B"]},{"observationId":"shopify-a","intentPoint":"Second fit.","features":[],"specifications":[]}]}`}, nil
	default:
		return CompletionResult{}, fmt.Errorf("unexpected schema %s", request.SchemaName)
	}
}

type pipelineResearchProductsV2 struct {
	ProductPort
	runCalls      int
	query         CatalogQueryPayload
	ranked        []ResearchRankedCandidate
	sweepCalls    int
	sweepActionID string

	submitPlanningCalls   int
	plannedTargets        []ProposedTarget
	startCuratingCalls    int
	startCuratingPlanID   string
	startCuratingActionID string
	runErr                error
	failureCalls          int
	failureReason         string
	failureRetryable      bool
	failureTransaction    bool
}

func (products *pipelineResearchProductsV2) ReadPlanningContext(
	context.Context, string, string,
) (PlanningContext, error) {
	return PlanningContext{
		TaskID: "task-1", PlanID: "plan-1", OriginalIntent: "세라믹 커피 머그",
		PlanningMode: "SINGLE", TotalBudget: Money{Amount: "100", Currency: "USD"},
		Country: "US", MinimumTargets: 1, MaximumTargets: 1, InitialRun: true,
	}, nil
}

func (products *pipelineResearchProductsV2) SubmitPlanning(
	_ context.Context,
	_, _ string,
	_ PlanningContext,
	targets []ProposedTarget,
) (SubmissionOutcome, error) {
	products.submitPlanningCalls++
	products.plannedTargets = append([]ProposedTarget(nil), targets...)
	return SubmissionOutcome{Accepted: true, ResultID: "proposal-1"}, nil
}

func (products *pipelineResearchProductsV2) StartCurating(
	_ context.Context,
	_ string,
	planID, actionID string,
) (bool, error) {
	products.startCuratingCalls++
	products.startCuratingPlanID = planID
	products.startCuratingActionID = actionID
	return true, nil
}

func (products *pipelineResearchProductsV2) ReadResearchContext(
	context.Context, string, string, string,
) (ResearchContext, error) {
	return ResearchContext{
		RoundID: "round-1", UserID: "user-1", PlanID: "plan-1",
		TargetTitle: "방수 트레일 러닝화", TargetIntent: "trail running shoes",
	}, nil
}

func (products *pipelineResearchProductsV2) RunCatalogResearch(
	ctx context.Context,
	_, _, _, _ string,
	query CatalogQueryPayload,
	ranker ResearchCandidateRanker,
) (ResearchExecutionOutcome, error) {
	products.runCalls++
	products.query = query
	if products.runErr != nil {
		return ResearchExecutionOutcome{}, products.runErr
	}
	ranked, err := ranker(ctx, []ResearchCandidateObservation{
		{ObservationID: "shopify-a", Name: "Observed A"},
		{ObservationID: "shopify-b", Name: "Observed B"},
	}, 8)
	if err != nil {
		return ResearchExecutionOutcome{}, err
	}
	products.ranked = ranked
	return ResearchExecutionOutcome{
		CandidateCount: len(ranked), PoolVersion: 2,
	}, nil
}

func (products *pipelineResearchProductsV2) FailResearchTarget(
	ctx context.Context,
	_, _ string,
	reasonCode string,
	retryable bool,
) error {
	products.failureCalls++
	products.failureReason = reasonCode
	products.failureRetryable = retryable
	products.failureTransaction, _ = ctx.Value(cancelTransactionKey{}).(bool)
	return nil
}

func (products *pipelineResearchProductsV2) StartReadySessions(
	_ context.Context,
	_, _, actionID string,
) (int, error) {
	products.sweepCalls++
	products.sweepActionID = actionID
	return 0, nil
}

type pipelineResearchRepositoryV2 struct {
	Repository
	updated         []intelligencedomain.Step
	closedAttempt   intelligencedomain.Attempt
	closedJobStatus intelligencedomain.JobStatus
	closedFailure   string
}

func (repository *pipelineResearchRepositoryV2) CloseAttempt(
	_ context.Context,
	attempt intelligencedomain.Attempt,
) error {
	repository.closedAttempt = attempt
	return nil
}

func (repository *pipelineResearchRepositoryV2) CloseJob(
	_ context.Context,
	_ string,
	status intelligencedomain.JobStatus,
	failureCode string,
	_ bool,
	_ time.Time,
) error {
	repository.closedJobStatus = status
	repository.closedFailure = failureCode
	return nil
}

func (repository *pipelineResearchRepositoryV2) InsertStep(
	context.Context,
	intelligencedomain.Step,
) error {
	return nil
}

func (repository *pipelineResearchRepositoryV2) UpdateStep(
	_ context.Context,
	step intelligencedomain.Step,
) error {
	repository.updated = append(repository.updated, step)
	return nil
}

type pipelineResearchClockV2 struct{ now time.Time }

func (clock pipelineResearchClockV2) Now() time.Time { return clock.now }

type pipelineResearchIDsV2 struct{ next int }

func (ids *pipelineResearchIDsV2) NewID() string {
	ids.next++
	return fmt.Sprintf("step-%d", ids.next)
}

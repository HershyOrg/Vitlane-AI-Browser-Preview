package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestEvaluationBatchesFollowTheLimitAndParallelCap(t *testing.T) {
	policy := DefaultEvaluationPolicy()
	for count, want := range map[int]int{1: 1, 18: 1, 25: 1, 26: 2, 50: 2} {
		if got := evaluationBatches(count, policy); got != want {
			t.Fatalf("batches(%d) = %d, want %d", count, got, want)
		}
	}
	wide := EvaluationPolicy{BatchLimit: 13, Parallel: 4, ExtraSlotWait: time.Second, BatchTimeout: time.Minute}
	if got := evaluationBatches(50, wide); got != 4 {
		t.Fatalf("wide policy batches(50) = %d, want 4", got)
	}
	if got := fmt.Sprint(balancedBatches(50, 2)); got != "[25 25]" {
		t.Fatalf("balanced(50,2) = %s", got)
	}
	if got := fmt.Sprint(balancedBatches(50, 4)); got != "[13 13 12 12]" {
		t.Fatalf("balanced(50,4) = %s", got)
	}
	if got := fmt.Sprint(balancedBatches(3, 4)); got != "[1 1 1]" {
		t.Fatalf("balanced(3,4) = %s", got)
	}
	if !DefaultEvaluationPolicy().Valid() || (EvaluationPolicy{BatchLimit: 4, Parallel: 2, ExtraSlotWait: time.Second, BatchTimeout: time.Minute}).Valid() {
		t.Fatal("policy validation")
	}
}

var observationIDPattern = regexp.MustCompile(`observationId=(\S+)`)

// batchProvider answers every ranking call with exactly the products offered
// in its prompt, records which request keys ran and how many ran at once, and
// can fail a given batch key once or always.
type batchProvider struct {
	mu        sync.Mutex
	keys      []string
	peak      int
	inFlight  int
	failOnce  map[string]bool
	failEvery map[string]bool
	hold      time.Duration
}

func (p *batchProvider) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}
func (p *batchProvider) Available(context.Context, string) error { return nil }
func (p *batchProvider) Complete(ctx context.Context, request CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	p.keys = append(p.keys, request.RequestKey)
	p.inFlight++
	p.peak = max(p.peak, p.inFlight)
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.inFlight--; p.mu.Unlock() }()
	if request.SchemaName == SchemaCatalogQuery {
		return CompletionResult{Content: `{"query":"trail shoes","mustInclude":[],"mustExclude":[]}`}, nil
	}
	if p.hold > 0 {
		select {
		case <-ctx.Done():
			return CompletionResult{}, ctx.Err()
		case <-time.After(p.hold):
		}
	}
	suffix := request.RequestKey[strings.Index(request.RequestKey, ":"+SchemaCandidateRanking)+len(":"+SchemaCandidateRanking):]
	p.mu.Lock()
	failEvery, failOnce := p.failEvery[suffix], p.failOnce[suffix]
	if failOnce {
		p.failOnce[suffix] = false
	}
	p.mu.Unlock()
	if failEvery || failOnce {
		return CompletionResult{}, fault.New(fault.ProviderUnavailable, "PROVIDER_HTTP_5XX", true)
	}
	ranked := []map[string]any{}
	for _, match := range observationIDPattern.FindAllStringSubmatch(request.UserPrompt, -1) {
		ranked = append(ranked, map[string]any{"observationId": match[1], "intentPoint": "Observed fit.", "features": []string{}, "specifications": []string{}})
	}
	raw, _ := json.Marshal(map[string]any{"ranked": ranked})
	return CompletionResult{Content: string(raw)}, nil
}

// batchProducts offers count observations to the ranker and keeps the answer.
type batchProducts struct {
	pipelineResearchProductsV2
	count  int
	ranked []ResearchRankedCandidate
}

func (p *batchProducts) ReadResearchContext(context.Context, string, string, string) (ResearchContext, error) {
	return ResearchContext{RoundID: "round-1", UserID: "user-1", PlanID: "plan-1", Country: "US", ContentLocale: "en-US",
		TargetTitle: "trail shoes", TargetIntent: "trail running shoes", Criteria: &ResearchCriteria{}}, nil
}
func (p *batchProducts) RunCatalogResearch(ctx context.Context, _, _, _, _ string, _ CatalogQueryPayload, ranker ResearchCandidateRanker) (ResearchExecutionOutcome, error) {
	observations := make([]ResearchCandidateObservation, p.count)
	for index := range observations {
		observations[index] = ResearchCandidateObservation{ObservationID: fmt.Sprintf("obs-%02d", index), Name: fmt.Sprintf("Product %d", index)}
	}
	ranked, err := ranker(ctx, observations, len(observations))
	if err != nil {
		return ResearchExecutionOutcome{}, err
	}
	p.ranked = ranked
	return ResearchExecutionOutcome{CandidateCount: len(ranked), PoolVersion: 2}, nil
}

func runBatchRound(t *testing.T, provider *batchProvider, slots int, count int, policy EvaluationPolicy) (*batchProducts, error) {
	t.Helper()
	now := time.Date(2026, 9, 17, 3, 4, 5, 0, time.UTC)
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: "job-1", UserID: "user-1", CurationID: "curation-1", CurationActionID: "action-1", PlanID: "plan-1",
		Target:   intelligencedomain.JobTarget{Kind: intelligencedomain.TargetResearchRound, ID: "round-1"},
		Provider: intelligencedomain.ProviderManaged, ModelKey: "managed-model", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = intelligencedomain.JobRunning
	attempt, err := intelligencedomain.NewAttempt("attempt-1", job, "request-1", 1, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewProviderRegistry(RegisteredProvider{Provider: provider, Concurrency: slots})
	if err != nil {
		t.Fatal(err)
	}
	products := &batchProducts{count: count}
	service := &Service{
		repository: &pipelineResearchRepositoryV2{}, products: products, providers: registry,
		clock: pipelineResearchClockV2{now: now}, ids: &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
		modelSlotWait: 50 * time.Millisecond, evaluation: policy,
	}
	claimed := ClaimedJob{Job: job, Attempt: attempt}
	return products, service.runResearch(context.Background(), claimed, &stepTracker{service: service, job: job, attempt: attempt})
}

func rankingKeys(keys []string) []string {
	out := []string{}
	for _, key := range keys {
		if strings.Contains(key, SchemaCandidateRanking) {
			out = append(out, key[strings.Index(key, SchemaCandidateRanking):])
		}
	}
	return out
}

// Fifty candidates split into two balanced halves that run side by side under
// their own request keys; every product comes back evaluated and no
// supplement is needed.
func TestLargeRoundEvaluatesInTwoParallelBatches(t *testing.T) {
	policy := DefaultEvaluationPolicy()
	policy.ExtraSlotWait = 50 * time.Millisecond
	provider := &batchProvider{hold: 30 * time.Millisecond}
	products, err := runBatchRound(t, provider, 4, 50, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rankingKeys(provider.keys), ","); got != SchemaCandidateRanking+":b1,"+SchemaCandidateRanking+":b2" && got != SchemaCandidateRanking+":b2,"+SchemaCandidateRanking+":b1" {
		t.Fatalf("ranking keys = %s", got)
	}
	if provider.peak != 2 {
		t.Fatalf("batches must run side by side, peak in flight = %d", provider.peak)
	}
	if len(products.ranked) != 50 {
		t.Fatalf("ranked %d of 50", len(products.ranked))
	}
}

// With only one model slot obtainable the round evaluates in a single call
// instead of splitting sequentially, which the measurement showed is slower
// than not splitting at all.
func TestRoundDoesNotSplitWithoutASecondSlot(t *testing.T) {
	policy := DefaultEvaluationPolicy()
	policy.ExtraSlotWait = 20 * time.Millisecond
	provider := &batchProvider{}
	products, err := runBatchRound(t, provider, 1, 40, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rankingKeys(provider.keys), ","); got != SchemaCandidateRanking {
		t.Fatalf("expected one unsuffixed ranking call, got %s", got)
	}
	if len(products.ranked) != 40 {
		t.Fatalf("ranked %d of 40", len(products.ranked))
	}
	// Small rounds never split even with slots to spare.
	provider = &batchProvider{}
	if _, err := runBatchRound(t, provider, 4, 18, policy); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rankingKeys(provider.keys), ","); got != SchemaCandidateRanking {
		t.Fatalf("18 candidates must be one call, got %s", got)
	}
}

// A batch that fails once is retried under a fresh request key; a batch that
// keeps failing leaves its products for the bounded supplement, and the Round
// still completes with the other half evaluated.
func TestFailedBatchIsRetriedOnceThenLeftToTheSupplement(t *testing.T) {
	policy := DefaultEvaluationPolicy()
	policy.ExtraSlotWait = 50 * time.Millisecond
	provider := &batchProvider{failOnce: map[string]bool{":b2": true}}
	products, err := runBatchRound(t, provider, 2, 30, policy)
	if err != nil {
		t.Fatal(err)
	}
	keys := strings.Join(rankingKeys(provider.keys), ",")
	if !strings.Contains(keys, SchemaCandidateRanking+":b2:retry") || len(products.ranked) != 30 {
		t.Fatalf("retry expected: keys=%s ranked=%d", keys, len(products.ranked))
	}

	provider = &batchProvider{failEvery: map[string]bool{":b2": true, ":b2:retry": true}}
	products, err = runBatchRound(t, provider, 2, 30, policy)
	if err != nil {
		t.Fatal(err)
	}
	keys = strings.Join(rankingKeys(provider.keys), ",")
	if !strings.Contains(keys, ":supplement") || len(products.ranked) != 30 {
		t.Fatalf("supplement must recover the failed batch: keys=%s ranked=%d", keys, len(products.ranked))
	}

	// Every batch failing is the round's failure, retryable for the job.
	provider = &batchProvider{failEvery: map[string]bool{":b1": true, ":b1:retry": true, ":b2": true, ":b2:retry": true}}
	if _, err := runBatchRound(t, provider, 2, 30, policy); err == nil || !fault.Retryable(err) {
		t.Fatalf("all batches failing must fail the attempt retryably, got %v", err)
	}
}

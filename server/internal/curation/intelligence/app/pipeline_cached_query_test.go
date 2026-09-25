package app

import (
	"context"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

type cachedQueryProducts struct {
	pipelineResearchProductsV2
	context ResearchContext
}

func (products *cachedQueryProducts) ReadResearchContext(context.Context, string, string, string) (ResearchContext, error) {
	return products.context, nil
}

// A saved query is reused only while it fits the Round's market. A Korean
// phrase saved for a KR Round must not be replayed into the English-only US
// providers, where it would fail before any search ran.
func TestCachedQueryIsReusedOnlyWhenItFitsTheRoundMarket(t *testing.T) {
	for _, test := range []struct {
		name        string
		country     string
		cached      string
		wantQuery   string
		modelCalled bool
	}{
		{"english cache on US round is reused", "US", "trail running shoes", "trail running shoes", false},
		{"korean cache on US round asks the model again", "US", "방수 트레일 러닝화", "waterproof trail shoes", true},
		{"korean cache on KR round is reused", "KR", "방수 트레일 러닝화", "방수 트레일 러닝화", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC)
			job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
				ID: "job-1", UserID: "user-1", CurationID: "curation-1",
				CurationActionID: "action-1", PlanID: "plan-1",
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
			provider := &pipelineResearchProviderV2{}
			registry, err := NewProviderRegistry(RegisteredProvider{Provider: provider, Concurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			products := &cachedQueryProducts{context: ResearchContext{
				RoundID: "round-1", UserID: "user-1", PlanID: "plan-1", Country: test.country,
				TargetTitle: "방수 트레일 러닝화", TargetIntent: "trail running shoes",
				CachedQuery: &CatalogQueryPayload{Query: test.cached, QuerySeeds: []string{test.cached}}, ExecutionCheckpoint: true,
			}}
			repository := &pipelineResearchRepositoryV2{}
			service := &Service{
				repository: repository, products: products, providers: registry,
				clock: pipelineResearchClockV2{now: now}, ids: &pipelineResearchIDsV2{}, logger: cancelTestLogger(),
			}
			claimed := ClaimedJob{Job: job, Attempt: attempt}
			if err := service.runResearch(context.Background(), claimed, &stepTracker{service: service, job: job, attempt: attempt}); err != nil {
				t.Fatal(err)
			}
			queryCalls := 0
			for _, schema := range provider.schemas {
				if schema == SchemaCatalogQuery {
					queryCalls++
				}
			}
			if (queryCalls == 1) != test.modelCalled || products.runCalls != 1 || products.query.Query != test.wantQuery {
				t.Fatalf("schemas=%v runCalls=%d query=%q", provider.schemas, products.runCalls, products.query.Query)
			}
		})
	}
}

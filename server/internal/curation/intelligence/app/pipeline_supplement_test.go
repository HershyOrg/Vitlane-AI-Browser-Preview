package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

// supplementProvider answers the first ranking call with one product and the
// supplementary call with the other, or fails the supplement on request.
type supplementProvider struct {
	keys           []string
	schemas        []string
	failSupplement bool
}

func (p *supplementProvider) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}
func (p *supplementProvider) Available(context.Context, string) error { return nil }
func (p *supplementProvider) Complete(_ context.Context, request CompletionRequest) (CompletionResult, error) {
	p.keys = append(p.keys, request.RequestKey)
	p.schemas = append(p.schemas, request.SchemaName)
	switch request.SchemaName {
	case SchemaCatalogQuery:
		return CompletionResult{Content: `{"query":"waterproof trail shoes","mustInclude":[],"mustExclude":[]}`}, nil
	case SchemaCandidateRanking:
		if strings.HasSuffix(request.RequestKey, ":supplement") {
			if p.failSupplement {
				return CompletionResult{}, fmt.Errorf("provider down")
			}
			if !strings.Contains(request.UserPrompt, "shopify-a") || strings.Contains(request.UserPrompt, "shopify-b") {
				return CompletionResult{}, fmt.Errorf("supplement must offer only the missing product: %s", request.UserPrompt)
			}
			return CompletionResult{Content: `{"ranked":[{"observationId":"shopify-a","intentPoint":"Recovered fit.","features":[],"specifications":[]}]}`}, nil
		}
		return CompletionResult{Content: `{"ranked":[{"observationId":"shopify-b","intentPoint":"Best fit.","features":[],"specifications":[]},{"observationId":"shopify-ghost","intentPoint":"Invented.","features":[],"specifications":[]}]}`}, nil
	}
	return CompletionResult{}, fmt.Errorf("unexpected schema %s", request.SchemaName)
}

// With explicit criteria, a product the first answer skipped gets exactly one
// supplementary evaluation call under its own request key. A failed supplement
// leaves that product unevaluated and never fails the Round.
func TestRankingSupplementsSkippedProductsOnce(t *testing.T) {
	for _, test := range []struct {
		name           string
		failSupplement bool
		wantRanked     []string
	}{
		{"supplement recovers the skipped product", false, []string{"shopify-b", "shopify-a"}},
		{"failed supplement keeps the partial answer", true, []string{"shopify-b"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 9, 15, 3, 4, 5, 0, time.UTC)
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
			provider := &supplementProvider{failSupplement: test.failSupplement}
			registry, err := NewProviderRegistry(RegisteredProvider{Provider: provider, Concurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			products := &cachedQueryProducts{context: ResearchContext{
				RoundID: "round-1", UserID: "user-1", PlanID: "plan-1", Country: "US",
				TargetTitle: "trail shoes", TargetIntent: "trail running shoes", ContentLocale: "en-US",
				Criteria: &ResearchCriteria{},
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
			if len(provider.schemas) != 3 || provider.schemas[1] != SchemaCandidateRanking || provider.schemas[2] != SchemaCandidateRanking {
				t.Fatalf("calls=%v", provider.schemas)
			}
			if provider.keys[1] == provider.keys[2] || !strings.HasSuffix(provider.keys[2], ":supplement") {
				t.Fatalf("supplement must use its own request key: %v", provider.keys)
			}
			got := []string{}
			for _, r := range products.ranked {
				got = append(got, r.ObservationID)
			}
			if strings.Join(got, ",") != strings.Join(test.wantRanked, ",") {
				t.Fatalf("ranked=%v want %v", got, test.wantRanked)
			}
		})
	}
}

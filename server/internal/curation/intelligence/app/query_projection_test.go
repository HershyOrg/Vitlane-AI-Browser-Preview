package app

import (
	"context"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"testing"
)

func TestResearchQueryGroupsReuseAndInvalidate(t *testing.T) {
	provider := &pipelineResearchProviderV2{}
	registry, err := NewProviderRegistry(RegisteredProvider{Provider: provider, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{providers: registry}
	claimed := ClaimedJob{Job: intelligencedomain.Job{Provider: intelligencedomain.ProviderManaged}}
	c := ResearchContext{Country: "US", TargetIntent: "등산화", ProductVertical: "FASHION", CatalogLanguages: []CatalogLanguageRequirement{{"english-latin.v1", "en"}, {"english-latin.v1", "en"}}}
	query, err := service.generateResearchQuery(context.Background(), claimed, c)
	if err != nil || len(provider.schemas) != 1 || len(query.Projections) != 1 {
		t.Fatalf("one model call per language policy: %v %+v %v", provider.schemas, query, err)
	}
	c.CachedQuery = &query
	if _, err = service.generateResearchQuery(context.Background(), claimed, c); err != nil || len(provider.schemas) != 1 {
		t.Fatalf("unchanged input not reused: %v", err)
	}
	c.FeedbackSummary = "더 가볍게"
	if _, err = service.generateResearchQuery(context.Background(), claimed, c); err != nil || len(provider.schemas) != 2 {
		t.Fatalf("changed input not regenerated: %v", err)
	}
	c.ExecutionCheckpoint = true
	if _, err = service.generateResearchQuery(context.Background(), claimed, c); err != nil || len(provider.schemas) != 2 {
		t.Fatalf("pinned execution regenerated: %v", err)
	}
	c.CatalogLanguages = append(c.CatalogLanguages, CatalogLanguageRequirement{"korean-mixed.v1", "ko"})
	if q, err := service.generateResearchQuery(context.Background(), claimed, c); err != nil || len(provider.schemas) != 3 || len(q.Projections) != 2 {
		t.Fatalf("new language group not generated independently: %v", err)
	}
}
func TestQueryProjectionLatinRuleIncludesAllTerms(t *testing.T) {
	for _, word := range []string{"café", "jalapeño", "écran 27"} {
		if !validQueryProjection(CatalogQueryProjection{Language: "en", Query: word, Seeds: []string{word}}) {
			t.Fatal(word)
		}
	}
	for _, p := range []CatalogQueryProjection{{Language: "en", Query: "chair용"}, {Language: "en", Query: "chair", Seeds: []string{"의자"}}, {Language: "en", Query: "chair", MustExclude: []string{"중고"}}} {
		if validQueryProjection(p) {
			t.Fatalf("non-Latin term accepted: %+v", p)
		}
	}
	if !validQueryProjection(CatalogQueryProjection{Language: "ko", Query: "라미 Safari 만년필"}) {
		t.Fatal("KR mixed query rejected")
	}
}

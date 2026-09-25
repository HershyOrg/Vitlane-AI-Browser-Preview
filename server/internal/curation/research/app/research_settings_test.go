package app

import (
	"context"
	"encoding/json"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"testing"
)

type mutableResearchSettings struct {
	testPlanningService
	settings curationdomain.ResearchSettings
	reads    int
}

func (p *mutableResearchSettings) ResearchSettingsForPlan(context.Context, string, string) (curationdomain.ResearchSettings, error) {
	p.reads++
	return p.settings, nil
}

func TestResearchSettingsFreezeCountryAndPreservePriceConstraint(t *testing.T) {
	p := &mutableResearchSettings{settings: curationdomain.ResearchSettings{SchemaVersion: "vitlane.research-settings.v1", Version: 1, Country: "KR"}}
	s := &Service{plans: p}
	original := ResearchContext{}
	original.ResearchScope.Country = "US"
	original.ResearchScope.MinPrice = &shareddomain.Money{Amount: "25.00", Currency: "USD"}
	raw, _ := json.Marshal(original)
	first, err := s.attachResearchSettings(context.Background(), "alice", "plan", raw)
	if err != nil {
		t.Fatal(err)
	}
	firstHash, _ := shareddomain.CanonicalJSONHash(json.RawMessage(first))
	p.settings = curationdomain.ResearchSettings{SchemaVersion: "vitlane.research-settings.v1", Version: 2, Country: "US"}
	second, err := s.attachResearchSettings(context.Background(), "alice", "plan", raw)
	if err != nil {
		t.Fatal(err)
	}
	var kr, us ResearchContext
	if err = json.Unmarshal(first, &kr); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(second, &us); err != nil {
		t.Fatal(err)
	}
	afterHash, _ := shareddomain.CanonicalJSONHash(json.RawMessage(first))
	if firstHash != afterHash || kr.ResearchScope.Country != "KR" || kr.ResearchSettings.Version != 1 || us.ResearchScope.Country != "US" || us.ResearchSettings.Version != 2 {
		t.Fatal("new setting rewrote a prior snapshot")
	}
	if *kr.ResearchScope.MinPrice != *original.ResearchScope.MinPrice || *us.ResearchScope.MinPrice != *original.ResearchScope.MinPrice {
		t.Fatal("country changed monetary meaning")
	}
	if original.ResearchScope.Country != "US" || p.reads != 2 {
		t.Fatal("seed or original scope was mutated")
	}
}

type capturedKoreanQuery struct{ query string }

func (g *capturedKoreanQuery) SearchExternalMalls(_ context.Context, request KoreanSearchRequest) (ExternalCatalogSearchResult, error) {
	g.query = request.Query
	return ExternalCatalogSearchResult{}, nil
}
func (*capturedKoreanQuery) Usage(context.Context, string, bool) (CatalogProviderUsage, error) {
	return CatalogProviderUsage{}, nil
}
func (*capturedKoreanQuery) Configured(string) bool { return true }
func TestAppendPersistsTheSavedProviderQueryItActuallyUsed(t *testing.T) {
	g := &capturedKoreanQuery{}
	s := &LiveCatalogReviewServiceV2{korean: g}
	profile := CatalogTargetSearchProfileV2{NormalizedIntent: "라미 사파리 만년필"}
	profile.Market.Country, profile.Market.Currency = "KR", "KRW"
	result, err := s.searchWorkspacePlanForProfileV2(context.Background(), CatalogWorkspaceSearchInputV2{TargetID: "target", Mode: CatalogResearchAppendV2, Search: LiveCatalogReviewSearchInputV2{Query: "ignored browser query"}}, profile)
	if err != nil || g.query != profile.NormalizedIntent || result.Metrics.ProviderQuery != g.query || result.Metrics.ProviderCountry != "KR" {
		t.Fatalf("append changed persisted provider context: query=%s metrics=%+v err=%v", g.query, result.Metrics, err)
	}
}

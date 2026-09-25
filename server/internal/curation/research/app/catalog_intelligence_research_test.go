package app

import (
	"context"
	"fmt"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCatalogManagedRankingCanOnlyOrderObservedShopifyProducts(t *testing.T) {
	result := catalogManagedRankingFixtureV2()
	ranked, err := catalogApplyManagedRankingV2(
		context.Background(), result, 2,
		func(
			_ context.Context,
			observations []CatalogIntelligenceCandidateObservation,
			maximum int,
		) ([]CatalogIntelligenceRankedCandidate, error) {
			if maximum != 2 || len(observations) != 2 ||
				observations[0].ObservationID != "shopify-a" ||
				observations[0].PriceMinimumAmount != "12.34" ||
				observations[1].ObservationID != "shopify-b" {
				t.Fatalf("managed observation boundary=%#v maximum=%d", observations, maximum)
			}
			return []CatalogIntelligenceRankedCandidate{
				{ObservationID: "shopify-b", IntentPoint: "Best fit", Features: []string{"Feature B"}, Specifications: []string{"Spec B"}},
				{ObservationID: "shopify-a", IntentPoint: "Second fit", Features: []string{"Feature A"}, Specifications: []string{"Spec A"}},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked.Search.Products) != 2 ||
		ranked.Search.Products[0].ProviderProductID != "shopify-b" ||
		ranked.Search.Products[0].ProviderOrder != 0 ||
		ranked.Search.Products[1].ProviderProductID != "shopify-a" ||
		ranked.Search.Products[1].ProviderOrder != 1 ||
		ranked.CandidateAssessments["shopify-b"].IntentPoint != "Best fit" ||
		ranked.Metrics.AICallCount != result.Metrics.AICallCount+1 {
		t.Fatalf("ranked result=%#v", ranked)
	}
}

func TestCatalogManagedRankingRejectsInventedDuplicateAndEmptyOutput(t *testing.T) {
	tests := []struct {
		name   string
		output []CatalogIntelligenceRankedCandidate
		reason string
	}{
		{
			name:   "unobserved product",
			output: []CatalogIntelligenceRankedCandidate{{ObservationID: "invented"}},
			reason: "PHASE8_MANAGED_RANKING_UNOBSERVED_PRODUCT",
		},
		{
			name: "duplicate product",
			output: []CatalogIntelligenceRankedCandidate{
				{ObservationID: "shopify-a"}, {ObservationID: "shopify-a"},
			},
			reason: "PHASE8_MANAGED_RANKING_DUPLICATE_PRODUCT",
		},
		{
			name:   "empty output for usable observations",
			output: []CatalogIntelligenceRankedCandidate{},
			reason: "PHASE8_MANAGED_RANKING_INVALID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := catalogApplyManagedRankingV2(
				context.Background(), catalogManagedRankingFixtureV2(), 2,
				func(context.Context, []CatalogIntelligenceCandidateObservation, int) ([]CatalogIntelligenceRankedCandidate, error) {
					return test.output, nil
				},
			)
			failure, ok := fault.As(err)
			if !ok || failure.Reason != test.reason {
				t.Fatalf("failure=%v want reason=%s", err, test.reason)
			}
		})
	}
}

func TestCatalogExpandCannotRaceActiveCurationWorker(t *testing.T) {
	foreground := &catalogForegroundWorkFakeV2{active: true}
	service := &LiveCatalogReviewServiceV2{
		workspace:      &catalogPlannedSearchProfileRepositoryV2{},
		foregroundWork: foreground,
	}
	_, err := service.SearchWorkspaceV2(
		context.Background(), CatalogWorkspaceSearchInputV2{
			UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
			Mode: CatalogResearchAppendV2,
		},
	)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.Conflict ||
		failure.Reason != "CURATION_EXPANSION_IN_PROGRESS" ||
		foreground.calls != 1 {
		t.Fatalf("failure=%v foreground=%#v", err, foreground)
	}
}

type catalogForegroundWorkFakeV2 struct {
	active bool
	err    error
	calls  int
}

func (fake *catalogForegroundWorkFakeV2) HasActiveCurationWork(
	_ context.Context,
	userID string,
	curationID string,
) (bool, error) {
	fake.calls++
	if userID != "user-1" || curationID != "curation-1" {
		return false, fault.New(fault.InternalFailure, "TEST_SCOPE_MISMATCH", false)
	}
	return fake.active, fake.err
}

func catalogManagedRankingFixtureV2() LiveCatalogReviewResultV2 {
	return LiveCatalogReviewResultV2{
		Search: CatalogProductSearchResult{Products: []CatalogProductObservation{
			{
				ProviderProductID: "shopify-a", Title: "Observed A",
				PriceRange: CatalogPriceRange{
					Minimum: CatalogMoney{AmountMinor: 1234, Currency: "USD"},
					Maximum: CatalogMoney{AmountMinor: 1299, Currency: "USD"},
				},
			},
			{
				ProviderProductID: "shopify-b", Title: "Observed B",
				PriceRange: CatalogPriceRange{
					Minimum: CatalogMoney{AmountMinor: 2200, Currency: "USD"},
					Maximum: CatalogMoney{AmountMinor: 2200, Currency: "USD"},
				},
			},
		}},
		CandidateAssessments: map[string]LiveCandidateAssessmentV2{
			"shopify-a": {IntentPoint: "Server A"},
			"shopify-b": {IntentPoint: "Server B"},
		},
		Metrics: LiveCatalogReviewMetricsV2{AICallCount: 1},
	}
}

func TestNewAxisAssessmentTravelsWithCandidateAndKeepsUnassessedProductsAsCandidates(t *testing.T) {
	criteria := curationdomain.TargetCriteriaSetV1{SchemaVersion: curationdomain.CriteriaSchema, Version: 1, Subject: curationdomain.ResearchSubject{Label: "펜", ProductType: "pen"}, Axes: []curationdomain.ResearchAxis{{AxisID: "writing", Label: "필기감", Definition: "쓰기 편안함", Importance: 5, Origin: "REQUEST"}}, Exclusions: []string{}}
	evaluation := catalogAssessmentContext{Criteria: &criteria, Locale: "ko-KR", RoundID: "r", ModelKey: "gpt-5.6-luna", Now: time.Now()}
	for _, test := range []struct {
		mode                   string
		evaluated, unevaluated int
	}{
		{"valid", 2, 0},
		// A skipped product is admitted unevaluated; it is never dropped.
		{"missing", 1, 1},
		// A score built on a fact the product never carried loses only its
		// assessment; the product stays a candidate.
		{"invented_fact", 0, 2},
		{"price_without_axis", 0, 2},
	} {
		t.Run(test.mode, func(t *testing.T) {
			mode := test.mode
			result, err := catalogApplyManagedRankingV2(context.Background(), catalogManagedRankingFixtureV2(), 2, func(_ context.Context, observations []CatalogIntelligenceCandidateObservation, max int) ([]CatalogIntelligenceRankedCandidate, error) {
				out := []CatalogIntelligenceRankedCandidate{}
				for _, o := range observations {
					score := researchdomain.AxisScoreV1{AxisID: "writing", ScorePercent: 97, Basis: "UNKNOWN", Explanation: "필기감은 직접 확인하지 못했습니다."}
					if mode == "invented_fact" {
						score.Basis = "PROVIDED"
						score.FactIDs = []string{"review"}
					}
					out = append(out, CatalogIntelligenceRankedCandidate{ObservationID: o.ObservationID, IntentPoint: "생성 당시 설명", AxisScores: []researchdomain.AxisScoreV1{score}})
				}
				if mode == "price_without_axis" {
					for i := range out {
						out[i].AxisScores[0].Basis = "INFERRED"
						out[i].AxisScores[0].FactIDs = []string{"price"}
					}
				}
				if mode == "missing" {
					out = out[:1]
				}
				return out, nil
			}, evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Search.Products) != 2 || result.EvaluatedCount != test.evaluated || result.UnevaluatedCount != test.unevaluated || len(result.CandidateAssessments) != 2 {
				t.Fatalf("products=%d evaluated=%d unevaluated=%d assessments=%d", len(result.Search.Products), result.EvaluatedCount, result.UnevaluatedCount, len(result.CandidateAssessments))
			}
			assessed := 0
			for _, a := range result.CandidateAssessments {
				if a.AxisAssessment == nil {
					if a.Features == nil || a.Specifications == nil {
						t.Fatalf("unevaluated assessment must serialize collections as []: %+v", a)
					}
					continue
				}
				assessed++
				if a.AxisAssessment.TotalScore != 97 || a.AxisAssessment.ObservationHash == "" || a.AxisAssessment.ContentLocale != "ko-KR" {
					t.Fatalf("assessment lost %+v", a)
				}
			}
			if assessed != test.evaluated {
				t.Fatalf("assessed=%d want %d", assessed, test.evaluated)
			}
			// Assessed products lead; unevaluated ones keep provider order after them.
			for i := range result.Search.Products {
				if result.Search.Products[i].ProviderOrder != i {
					t.Fatalf("provider order not contiguous: %+v", result.Search.Products)
				}
			}
			if mode == "missing" && result.CandidateAssessments[result.Search.Products[0].ProviderProductID].AxisAssessment == nil {
				t.Fatal("the assessed product must come first")
			}
		})
	}
}

func TestManagedAxisEvaluationUsesActualCandidateUpdateSize(t *testing.T) {
	for _, available := range []int{1, 15, 50, 65} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			expected := min(available, CandidateUpdateSize)
			result := catalogManagedRankingFixtureV2()
			base := result.Search.Products[0]
			products := make([]CatalogProductObservation, available)
			for i := range products {
				products[i] = base
				products[i].ProviderProductID = fmt.Sprintf("new-%02d", i)
			}
			result.Search.Products = catalogSelectNewProducts(products, CandidateUpdateSize)
			criteria := curationdomain.TargetCriteriaSetV1{SchemaVersion: curationdomain.CriteriaSchema, Version: 1, Subject: curationdomain.ResearchSubject{Label: "pen", ProductType: "pen"}, Axes: []curationdomain.ResearchAxis{{AxisID: "a", Label: "a", Definition: "a", Importance: 3, Origin: "REQUEST"}}}
			ranked, err := catalogApplyManagedRankingV2(context.Background(), result, CandidateUpdateSize, func(_ context.Context, observations []CatalogIntelligenceCandidateObservation, maximum int) ([]CatalogIntelligenceRankedCandidate, error) {
				if len(observations) != expected || maximum != expected {
					t.Fatalf("batch %d maximum %d", len(observations), maximum)
				}
				out := []CatalogIntelligenceRankedCandidate{}
				for _, o := range observations {
					out = append(out, CatalogIntelligenceRankedCandidate{ObservationID: o.ObservationID, IntentPoint: "Not directly verified.", AxisScores: []researchdomain.AxisScoreV1{{AxisID: "a", ScorePercent: 50, Basis: "UNKNOWN", Explanation: "Not directly verified."}}})
				}
				return out, nil
			}, catalogAssessmentContext{Criteria: &criteria, Locale: "en-US", RoundID: "r", ModelKey: "m", Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if len(ranked.Search.Products) != expected || len(ranked.CandidateAssessments) != expected {
				t.Fatal("new candidate batch was truncated")
			}
		})
	}
}

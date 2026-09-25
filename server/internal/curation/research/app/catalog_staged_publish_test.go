package app

import (
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// A candidate this Round already published unevaluated is fresh again for a
// retried attempt; a candidate another Round left unevaluated, or one with a
// saved assessment, stays a duplicate.
func TestFreshProductsForRoundReadmitPendingRowsOfTheSameRound(t *testing.T) {
	stored := []CatalogCandidateReferenceV2{
		{PlanTargetID: "target", SourceKind: "SHOPIFY_LIVE", ProviderProductID: "pending", EvaluationRoundID: "round-1"},
		{PlanTargetID: "target", SourceKind: "SHOPIFY_LIVE", ProviderProductID: "other-round", EvaluationRoundID: "round-0"},
		{PlanTargetID: "target", SourceKind: "SHOPIFY_LIVE", ProviderProductID: "scored", EvaluationRoundID: "round-1", Assessment: LiveCandidateAssessmentV2{AxisAssessment: &researchdomain.AxisAssessmentV1{}}},
		{PlanTargetID: "elsewhere", SourceKind: "SHOPIFY_LIVE", ProviderProductID: "foreign"},
	}
	products := []CatalogProductObservation{
		{ProviderProductID: "pending"},
		{ProviderProductID: "other-round"},
		{ProviderProductID: "scored"},
		{ProviderProductID: "new"},
		{ProviderProductID: "new"},
	}
	fresh, duplicates, count := catalogFreshProductsForRoundV2(stored, products, "target", "round-1")
	if count != 3 {
		t.Fatalf("target holds %d candidates, want 3", count)
	}
	got := []string{}
	for _, p := range fresh {
		got = append(got, p.ProductRef().ProductID)
	}
	if len(got) != 2 || got[0] != "pending" || got[1] != "new" || duplicates != 3 {
		t.Fatalf("fresh=%v duplicates=%d", got, duplicates)
	}
}

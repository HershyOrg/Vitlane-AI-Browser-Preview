package app

import (
	"context"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// TestCatalogResearchV2ContractVertical proves that the immutable identity and
// hash references required by each feature are sufficient input for the next
// feature. It is intentionally provider- and persistence-independent while
// the V2 runtime remains dormant until the R6 activation manifest.
func TestCatalogResearchV2ContractVertical(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	searchPlan := mustCatalogSearchPlanV2(
		t, shareddomain.NoResearchPriceConstraint(),
	)
	for _, request := range searchPlan.Requests {
		if request.Filters.Price != nil {
			t.Fatalf("NONE gained a provider price filter: %+v", request.Filters.Price)
		}
	}
	rankingObservation := candidateRankingObservedV2(
		"product-1",
		"Black lightweight waterproof trail running shoes",
		"New rubber footwear for wet weather.",
		0,
		7600,
		"USD",
		now.Add(-2*time.Minute),
	)
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{
		response: catalogSearchResponseV2(
			searchPlan, rankingObservation.Admission.Observation,
		),
	}}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), searchPlan)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != CatalogSearchExecutionResultsV2 ||
		len(execution.AdmissionInputs) != 1 {
		t.Fatalf("search execution=%+v", execution)
	}
	observation := execution.AdmissionInputs[0]
	rankingObservation.Admission = observation
	candidateRankingRebindEvidenceRefsV2(&rankingObservation)
	ranking, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint()).Intent,
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{rankingObservation},
		TrustedNow:   now,
	})
	if err != nil || len(ranking.Ranked) != 1 {
		t.Fatalf("ranking=%+v err=%v", ranking, err)
	}
	assessment := ranking.Ranked[0].Assessment
	assessment.ID = "assessment-1"

	pool, err := researchdomain.NewCandidatePoolV2("target-1")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := researchdomain.FinalizeCandidateBatchV2(
		researchdomain.CandidatePoolSnapshotV2{Pool: pool},
		researchdomain.CandidateBatchFinalizeInputV2{
			BatchID: "batch-1", ExpectedPoolVersion: pool.Version,
			ProcessSourceKind: researchdomain.CandidateBatchResearchRoundV2,
			ProcessSourceID:   "round-1",
			Outcome:           researchdomain.CandidateBatchFinalizeSuccessV2,
			CompletedAt:       now,
			Admissions: []researchdomain.CandidateAdmissionInputV2{{
				CandidateID:       "candidate-1",
				IdentityKey:       "SHOPIFY_UCP:product-1",
				InitialLocatorRef: observation.Locator.ProductURL.CanonicalURL,
				DiscoveryID:       "discovery-1",
				LocatorRef:        observation.Locator.ProductURL.CanonicalURL,
				PreviewVariantRef: "variant-preview-1",
				ObservationEvidenceRefs: []string{
					"sha256:catalog-observation-1",
				},
				RankPosition:         ranking.Ranked[0].RankPosition,
				RankingPolicyVersion: ranking.Ranked[0].RankingPolicyVersion,
				DiscoveredAt:         now.Add(-time.Minute),
				Assessment:           assessment,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Candidates) != 1 || len(commit.Discoveries) != 1 ||
		commit.Discoveries[0].CandidateID != commit.Candidates[0].ID {
		t.Fatalf("candidate commit=%+v", commit)
	}

	// Research ends with a fallible CartView item. It deliberately has no
	// choice authority token, OfferResolution, ResolvedOffer or expiry. Fresh
	// Shopify resolution is exercised by PrepareAgencyOrder service tests.
	draft, err := curationdomain.NewCartItemV2(curationdomain.CartItemV2{
		ID: "draft-1", CandidateID: commit.Candidates[0].ID,
		ProductTitleSnapshot: rankingObservation.Admission.Observation.Title,
		ProductURL:           observation.Locator.ProductURL.CanonicalURL,
		VariantID:            "gid://shopify/ProductVariant/1",
		VariantTitleSnapshot: "Graphite", SelectedOptions: []string{"Graphite"},
		PreviewPriceMinor: 7600, PreviewCurrency: "USD", Quantity: 2,
		ObservedAt: now, AddedAt: now.Add(time.Minute),
	})
	if err != nil || draft.VariantID == "" || draft.Quantity != 2 {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
}

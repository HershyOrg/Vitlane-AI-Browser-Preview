package app

import (
	"context"
	"fmt"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"reflect"
	"testing"
	"time"
)

func discoveryTestAmazon(i int) CatalogProductObservation {
	asin := fmt.Sprintf("B%09d", i)
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: asin}
	variant := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: asin}
	url, _ := variant.ExternalURL()
	return CatalogProductObservation{ProviderProductID: ref.IdentityKey(), SourceProductRef: &ref, Title: "Trail shoes", ProviderOrder: i, Locator: &CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: url}}, VariantObservation: &researchdomain.VariantObservation{VariantRef: variant, Price: researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}, ProductURL: url, ObservationID: "obs-" + asin, ObservedAt: time.Unix(100, 0), RefreshAfter: time.Unix(200, 0), Availability: "UNKNOWN", DeliveryEligibility: "UNCONFIRMED", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, PurchaseRoute: "EXTERNAL"}}
}
func TestDiscoveryFairMergeIndependentOfInputOrder(t *testing.T) {
	products := []CatalogProductObservation{}
	for i := 0; i < 50; i++ {
		p := catalogProductWithURLV2(fmt.Sprint(i), 1000)
		p.ProviderOrder = i
		products = append(products, p)
	}
	for i := 0; i < 16; i++ {
		products = append(products, discoveryTestAmazon(i))
	}
	want := catalogSelectNewProductsForRound(products, "round", 50)
	reversed := append([]CatalogProductObservation{}, products...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	got := catalogSelectNewProductsForRound(reversed, "round", 50)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("adapter/input order changed capped result")
	}
	amazon := 0
	for _, p := range got {
		if p.Source() == researchdomain.SourceAmazon {
			amazon++
		}
	}
	if len(got) != 50 || amazon != 16 {
		t.Fatalf("total=%d amazon=%d", len(got), amazon)
	}
	// Duplicate discovery routes never multiply one original product's allocation.
	duplicate := products[0]
	duplicate.DiscoveryRoute = "another-api"
	if n := len(mergeDiscoveryProducts(append(products, duplicate), "round")); n != 66 {
		t.Fatalf("duplicate routes inflated pool: %d", n)
	}
}
func TestDiscoveryCommonAdmissionAndPartialFailure(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{TargetID: "t", NormalizedIntent: "shoes", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
	input := CatalogWorkspaceSearchInputV2{TargetID: "t", Search: LiveCatalogReviewSearchInputV2{Query: "shoes", Limit: 50}}
	plan, err := BuildSearchPlanV3(input, profile)
	if err != nil {
		t.Fatal(err)
	}
	s := &LiveCatalogReviewServiceV2{}
	s.RegisterDiscoveryAdapter(discoveryBinding{EnglishDiscoveryDescriptor("AMAZON"), func(context.Context, DiscoveryRequest) (DiscoveryPage, error) {
		p := emptyDiscoveryResult()
		p.Search.Products = []CatalogProductObservation{discoveryTestAmazon(1)}
		p.NextProgress["AMAZON"] = SourceSearchProgress{Query: "shoes", Page: 2}
		return p, nil
	}})
	s.RegisterDiscoveryAdapter(discoveryBinding{EnglishDiscoveryDescriptor("SHOPIFY"), func(context.Context, DiscoveryRequest) (DiscoveryPage, error) {
		return DiscoveryPage{}, fault.New(fault.ProviderUnavailable, "CATALOG_API_DISABLED", false)
	}})
	got, err := s.executeDiscovery(context.Background(), DiscoveryRequest{Input: input, Profile: profile, Plan: plan})
	if err != nil || len(got.Search.Products) != 1 || !got.Search.Partial {
		t.Fatalf("unbounded UNKNOWN price / partial failure: %+v %v", got, err)
	}
	maxPrice := int64(5000)
	plan.Required.MaximumMinor = &maxPrice
	got, err = s.executeDiscovery(context.Background(), DiscoveryRequest{Input: input, Profile: profile, Plan: plan})
	if err != nil || len(got.Search.Products) != 0 || got.NextProgress["AMAZON"].Page != 2 || got.ProviderRejectedCount != 1 {
		t.Fatalf("bounded UNKNOWN must reject without losing progress: %+v %v", got, err)
	}
}

type discoveryManyAmazon struct {
	amazonTestGateway
	products []CatalogProductObservation
}

func (g *discoveryManyAmazon) SearchAmazon(context.Context, AmazonSearchRequest) (AmazonSearchResult, error) {
	return AmazonSearchResult{Products: g.products, PaginationKnown: true, HasNextPage: true, RawCount: len(g.products)}, nil
}
func TestAmazonDiscoveryNoSixteenCapAndRawPagination(t *testing.T) {
	g := &discoveryManyAmazon{}
	for i := 0; i < 32; i++ {
		g.products = append(g.products, discoveryTestAmazon(i))
	}
	s := &LiveCatalogReviewServiceV2{amazon: g}
	profile := CatalogTargetSearchProfileV2{TargetID: "t", NormalizedIntent: "shoes", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
	input := CatalogWorkspaceSearchInputV2{TargetID: "t", Search: LiveCatalogReviewSearchInputV2{Query: "shoes", Limit: 50}}
	got, err := s.searchWorkspacePlanForProfileV2(context.Background(), input, profile)
	if err != nil || len(got.Search.Products) != 32 || got.NextProgress["AMAZON"].Page != 2 {
		t.Fatalf("products=%d progress=%+v err=%v", len(got.Search.Products), got.NextProgress, err)
	}
}
func TestFullPoolRetryResumesPendingWithoutAddingNewCandidates(t *testing.T) {
	stored := []CatalogCandidateReferenceV2{}
	products := []CatalogProductObservation{}
	for i := 0; i < 50; i++ {
		p := catalogProductWithURLV2(fmt.Sprint(i), 1000)
		products = append(products, p)
		stored = append(stored, CatalogCandidateReferenceV2{PlanTargetID: "t", ProviderProductID: p.ProviderProductID, Locator: *p.Locator, EvaluationRoundID: "round"})
	}
	products = append(products, catalogProductWithURLV2("new", 1000))
	fresh, _, count := catalogFreshProductsForRoundV2(stored, products, "t", "round")
	got := catalogSelectRoundProducts(stored, fresh, "t", "round", count)
	if len(got) != 50 {
		t.Fatalf("pending rows lost at full capacity: %d", len(got))
	}
	for _, p := range got {
		if p.ProviderProductID == "new" {
			t.Fatal("retry exceeded pool capacity")
		}
	}
}
func TestDiscoveryResponseHandoffIsOwnerBoundOneShotAndExpires(t *testing.T) {
	now := time.Now().UTC()
	input := CatalogWorkspaceSearchInputV2{UserID: "owner", CurationID: "c", TargetID: "t"}
	result, refs := catalogCandidateReferencesForSearchResultV2(input, DiscoveryPage{Search: CatalogProductSearchResult{Products: []CatalogProductObservation{catalogProductWithURLV2("shoe", 1234)}}, CandidateAssessments: map[string]LiveCandidateAssessmentV2{}}, now)
	h := &discoveryResponseHandoff{}
	h.put(input, result, nil, now)
	wrong := refs[0]
	wrong.UserID = "other"
	if _, ok := h.take(wrong, now); ok {
		t.Fatal("cross-owner handoff")
	}
	got, ok := h.take(refs[0], now)
	if !ok || got.PriceRange.Minimum.AmountMinor != 1234 {
		t.Fatal("lost original facts")
	}
	if _, ok = h.take(refs[0], now); ok {
		t.Fatal("not one-shot")
	}
	h.put(input, result, nil, now)
	if _, ok = h.take(refs[0], now.Add(time.Minute)); ok {
		t.Fatal("expired handoff")
	}
}
func TestUnicodeLatinAndSemanticExclusionPolicy(t *testing.T) {
	terms, err := catalogManagedSearchTermsV2([]string{"café", "jalapeño"})
	if err != nil || terms[0] != "café" {
		t.Fatalf("Latin normalized incorrectly: %v %v", terms, err)
	}
	if _, err = catalogManagedSearchTermsV2([]string{"cafe용"}); err == nil {
		t.Fatal("non-Latin letter accepted")
	}
	profile := CatalogTargetSearchProfileV2{TargetID: "t", NormalizedIntent: "shoes", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
	plan, err := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{TargetID: "t", Search: LiveCatalogReviewSearchInputV2{Query: "shoes", PreferredExclusions: []string{"leather"}}}, profile)
	p := catalogProductWithURLV2("shoe", 1000)
	p.Title = "Leather-free shoes"
	if err != nil || len(plan.Preferred.Exclusions) != 1 || AdmitPlannedObservationV3(plan, p, nil) != "" {
		t.Fatal("semantic exclusion became literal hard gate")
	}
}
func TestDiscoveryRepeatedCursorMovesToNextSeed(t *testing.T) {
	p := SourceSearchProgress{Query: "first", Cursor: "same", Page: 2, SeedIndex: 0}
	got := AdvanceSearchProgress(p, []string{"first", "second"}, "same", true, false)
	if got.Query != "second" || got.Page != 1 || got.SchemaVersion != DiscoveryPolicyVersion {
		t.Fatalf("cursor loop: %+v", got)
	}
}

func TestFirstVisibleHydrationUsesResponseAndRefreshLooksUpAgain(t *testing.T) {
	now := time.Now().UTC()
	clock := &liveReviewClockV2{now: now}
	gateway := &liveReviewGatewayV2{clock: clock}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := CatalogWorkspaceSearchInputV2{UserID: "owner", CurationID: "c", TargetID: "t"}
	result, refs := catalogCandidateReferencesForSearchResultV2(input, DiscoveryPage{Search: CatalogProductSearchResult{Products: []CatalogProductObservation{catalogProductWithURLV2("shoe", 1234)}}, CandidateAssessments: map[string]LiveCandidateAssessmentV2{}}, now)
	service.workspace = &liveReviewWorkspaceRepositoryV2{state: CatalogWorkspaceStoredStateV2{Candidates: refs, Pools: []CatalogPoolMetadataV2{{TargetID: "t", Version: 1}}}}
	service.responseHandoff.put(input, result, nil, now)
	view, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{UserID: "owner", CurationID: "c", TargetID: "t", Scope: CatalogResearchHydrationVisibleTargetV2})
	if err != nil || gateway.lookupCalls != 0 || len(view.Pools) != 1 || view.Pools[0].Products[0].PriceRange.Minimum.AmountMinor != 1234 {
		t.Fatalf("first view discarded search facts: %+v %v calls=%d", view, err, gateway.lookupCalls)
	}
	_, err = service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{UserID: "owner", CurationID: "c", TargetID: "t", CandidateID: refs[0].CandidateID, Scope: CatalogResearchHydrationCandidateV2})
	if err != nil || gateway.lookupCalls != 1 {
		t.Fatalf("explicit refresh reused response: %v calls=%d", err, gateway.lookupCalls)
	}
}

func TestDiscoveryPreservesSourceCallMetrics(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{TargetID: "t", NormalizedIntent: "shoes", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
	input := CatalogWorkspaceSearchInputV2{TargetID: "t", Search: LiveCatalogReviewSearchInputV2{Query: "shoes"}}
	s := &LiveCatalogReviewServiceV2{}
	s.RegisterDiscoveryAdapter(discoveryBinding{EnglishDiscoveryDescriptor("SHOPIFY"), func(context.Context, DiscoveryRequest) (DiscoveryPage, error) {
		p := emptyDiscoveryResult()
		p.Metrics = LiveCatalogReviewMetricsV2{ShopifyCallCount: 1, LocalCallsUsed: 1, LocalCallsRemaining: 9, LocalRateLimit: 10, ProviderCostStatus: "NOT_REPORTED_BY_PROVIDER"}
		return p, nil
	}})
	s.RegisterDiscoveryAdapter(discoveryBinding{EnglishDiscoveryDescriptor("AMAZON"), func(context.Context, DiscoveryRequest) (DiscoveryPage, error) { return emptyDiscoveryResult(), nil }})
	got, err := s.searchWorkspacePlanForProfileV2(context.Background(), input, profile)
	if err != nil || got.Metrics.ShopifyCallCount != 1 || got.Metrics.LocalCallsRemaining != 9 || got.Metrics.LocalRateLimit != 10 || got.Metrics.ProviderCostStatus == "" {
		t.Fatalf("later source erased metrics: %+v %v", got.Metrics, err)
	}
}

package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestPrepareAgencyOrderReadsShopifyOnceAndDetectsPriceChange(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	available := true
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess,
		Matches: []CatalogOfferMatch{{
			DraftID: "cart-item-1", RequestedIdentifier: "gid://shopify/ProductVariant/1",
			Match:   "exact",
			Product: CatalogProductObservation{Title: "Fresh Pack"},
			Variant: CatalogPreviewVariant{
				ID: "gid://shopify/ProductVariant/1", Title: "Black",
				Price:        CatalogMoney{AmountMinor: 8200, Currency: "USD"},
				Availability: CatalogAvailability{Available: &available},
			},
		}},
	}}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareAgencyOrder(context.Background(), PrepareAgencyOrderPreviewInputV2{
		Country: "US", Currency: "USD", Items: []CartItemInputV2{{
			CartItemID: "cart-item-1", CandidateID: "candidate-1", ProductTitle: "Pack",
			VariantID: "gid://shopify/ProductVariant/1", VariantTitle: "Black",
			PreviewPriceMinor: 7600, PreviewCurrency: "USD", Quantity: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateway.lookupCalls != 1 || result.Status != "ACTION_REQUIRED" ||
		len(result.Lines) != 1 || result.Lines[0].Status != PreparedCartItemPriceChangedV2 ||
		!result.Lines[0].PriceChanged || result.SubtotalMinor != 16400 ||
		result.Metrics.ShopifyCallCount != 1 || result.Metrics.AICallCount != 0 {
		t.Fatalf("result=%#v lookupCalls=%d", result, gateway.lookupCalls)
	}
}

func TestPrepareAgencyOrderAccountsOfferSplitRetriesPerProviderAttempt(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	available := true
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		ProviderCallCount: 3, Outcome: CatalogOutcomeSuccess,
		Matches: []CatalogOfferMatch{{
			DraftID: "cart-item-1", RequestedIdentifier: "gid://shopify/ProductVariant/1",
			Match: "exact", Product: CatalogProductObservation{Title: "Fresh Pack"},
			Variant: CatalogPreviewVariant{
				ID: "gid://shopify/ProductVariant/1", Title: "Black",
				Price:        CatalogMoney{AmountMinor: 7600, Currency: "USD"},
				Availability: CatalogAvailability{Available: &available},
			},
		}},
	}}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareAgencyOrder(
		context.Background(), catalogPrepareAgencyOrderInputV2(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.lookupCalls != 1 || gateway.lookupProviderCalls != 3 ||
		result.Metrics.ShopifyCallCount != 3 || result.Metrics.LocalCallsUsed != 3 ||
		result.Metrics.LocalCallsRemaining != 0 || service.activeCalls != 0 {
		t.Fatalf(
			"logical=%d provider=%d metrics=%+v active=%d",
			gateway.lookupCalls, gateway.lookupProviderCalls, result.Metrics,
			service.activeCalls,
		)
	}
}

func TestPrepareAgencyOrderStopsOfferSplitBeforeRateDeniedAttempt(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		ProviderCallCount: 3, Outcome: CatalogOutcomeSuccess,
	}}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 2, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PrepareAgencyOrder(
		context.Background(), catalogPrepareAgencyOrderInputV2(),
	)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.RateLimited ||
		failure.Reason != "LIVE_CATALOG_REVIEW_RATE_LIMITED" {
		t.Fatalf("failure=%v", err)
	}
	if gateway.lookupProviderCalls != 2 || service.windowCalls != 2 ||
		service.activeCalls != 0 {
		t.Fatalf(
			"provider=%d window=%d active=%d",
			gateway.lookupProviderCalls, service.windowCalls, service.activeCalls,
		)
	}
}

func TestProviderOperationKeepsLogicalConcurrencySlotAcrossSequentialCalls(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	service, err := NewLiveCatalogReviewServiceV2(
		&liveReviewGatewayV2{clock: clock}, clock, LiveCatalogReviewConfigV2{
			MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.beginProviderOperationV2(clock.now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, competingErr := service.acquire(clock.now, 1); competingErr == nil {
		t.Fatal("competing logical operation entered while the first operation was active")
	} else if failure, ok := fault.As(competingErr); !ok ||
		failure.Reason != "LIVE_CATALOG_REVIEW_BUSY" {
		t.Fatalf("competing failure=%v", competingErr)
	}
	for range 2 {
		release, acquireErr := operation.AcquireCatalogProviderCall(context.Background())
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		release()
	}
	calls, used, remaining := operation.Snapshot()
	operation.Close()
	if calls != 2 || used != 2 || remaining != 1 || service.activeCalls != 0 {
		t.Fatalf(
			"calls=%d used=%d remaining=%d active=%d",
			calls, used, remaining, service.activeCalls,
		)
	}
}

func catalogPrepareAgencyOrderInputV2() PrepareAgencyOrderPreviewInputV2 {
	return PrepareAgencyOrderPreviewInputV2{
		Country: "US", Currency: "USD", Items: []CartItemInputV2{{
			CartItemID: "cart-item-1", CandidateID: "candidate-1", ProductTitle: "Pack",
			VariantID: "gid://shopify/ProductVariant/1", VariantTitle: "Black",
			PreviewPriceMinor: 7600, PreviewCurrency: "USD", Quantity: 1,
		}},
	}
}

func TestCatalogConfiguredVariantTitleUsesPersistedSelectedOptionValues(t *testing.T) {
	t.Parallel()
	if got := catalogConfiguredVariantTitleV2(
		[]string{"Color: Black", "Size: Medium"}, "Provider product title",
	); got != "Black / Medium" {
		t.Fatalf("configured title=%q", got)
	}
	if got := catalogConfiguredVariantTitleV2(nil, " Provider product title "); got != "Provider product title" {
		t.Fatalf("fallback title=%q", got)
	}
}

func TestLoadWorkspaceKeepsFreshLookupDisclosureAndRebindsItToCandidate(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	available := true
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess, ProviderCallCount: 1,
		Matches: []CatalogOfferMatch{{
			DraftID: "candidate:candidate-1", RequestedIdentifier: "https://shop.example/products/pack",
			Match: "exact",
			Product: CatalogProductObservation{
				ProviderProductID: "provider-product-1", Title: "Fresh Pack",
			},
			Variant: CatalogPreviewVariant{
				ID: "variant-1", Title: "Default", Price: CatalogMoney{AmountMinor: 8200, Currency: "USD"},
				Availability: CatalogAvailability{Available: &available},
			},
		}},
		Messages: []CatalogProviderMessage{
			{Type: "warning", Path: "$.products[0]", SubjectKind: "PRODUCT", SubjectRef: "provider-product-1", Presentation: "disclosure", Content: "Merchant disclosure"},
			{Type: "warning", SubjectKind: "SEARCH", Presentation: "notice", Content: "Search notice"},
		},
	}}
	repository := &liveReviewWorkspaceRepositoryV2{state: CatalogWorkspaceStoredStateV2{
		Pools: []CatalogPoolMetadataV2{{TargetID: "target-1", Version: 1}},
		Candidates: []CatalogCandidateReferenceV2{{
			PlanTargetID: "target-1", CandidateID: "candidate-1", Visible: true,
			Assessment: LiveCandidateAssessmentV2{
				IntentPoint: "Matches a compact daily-carry target.",
				Features:    []string{"Padded straps"}, Specifications: []string{"20 L"},
			},
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{
				CanonicalURL: "https://shop.example/products/pack",
			}},
		}, {
			PlanTargetID: "target-1", CandidateID: "candidate-hidden", Visible: false,
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{
				CanonicalURL: "https://shop.example/products/hidden",
			}},
		}, {
			PlanTargetID: "target-2", CandidateID: "candidate-other-hidden", Visible: false,
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{
				CanonicalURL: "https://shop.example/products/other-hidden",
			}},
		}},
		Configurations: []CatalogCandidateConfigurationV2{{
			TargetID: "target-1", CandidateID: "candidate-1", VariantID: "variant-1",
			SelectedOptions: []string{"Color: Black"}, ObservedAt: clock.now,
		}},
	}}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableWorkspaceRepositoryV2(repository); err != nil {
		t.Fatal(err)
	}
	storedView, err := service.LoadWorkspaceV2(
		context.Background(), "user-1", "curation-1", "US", "USD", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.lookupCalls != 0 || len(storedView.Pools) != 1 ||
		len(storedView.Pools[0].Products) != 1 ||
		storedView.Pools[0].Products[0].Title != "" ||
		storedView.Pools[0].Products[0].ProviderProductID != "candidate-1" ||
		storedView.Pools[0].Assessments["candidate-1"].IntentPoint == "" ||
		len(storedView.Configurations) != 1 ||
		storedView.Configurations[0].Variant.VariantID != "variant-1" {
		t.Fatalf("stored view=%+v lookupCalls=%d", storedView, gateway.lookupCalls)
	}
	view, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationVisibleTargetV2, TargetID: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pools) != 1 || len(view.Pools[0].Messages) != 1 ||
		view.Pools[0].Messages[0].SubjectRef != "candidate-1" ||
		view.Pools[0].Messages[0].Path != "$.products[0]" {
		t.Fatalf("product disclosure=%+v", view.Pools)
	}
	assessment := view.Pools[0].Assessments["candidate-1"]
	if assessment.IntentPoint != "Matches a compact daily-carry target." ||
		len(assessment.Features) != 1 || assessment.Specifications[0] != "20 L" {
		t.Fatalf("candidate assessment=%+v", assessment)
	}
	if len(view.Messages) != 1 || view.Messages[0].Content != "Search notice" {
		t.Fatalf("global messages=%+v", view.Messages)
	}
	if len(gateway.lookupRequests) != 1 || len(gateway.lookupRequests[0].Inputs) != 1 ||
		gateway.lookupRequests[0].Inputs[0].DraftID != "candidate:candidate-1" {
		t.Fatalf("visible hydration inputs=%+v", gateway.lookupRequests)
	}
	_, err = service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationHiddenTargetV2, TargetID: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.lookupRequests) != 2 || len(gateway.lookupRequests[1].Inputs) != 1 ||
		gateway.lookupRequests[1].Inputs[0].DraftID != "candidate:candidate-hidden" {
		t.Fatalf("hidden target hydration inputs=%+v", gateway.lookupRequests)
	}
}

func TestCatalogProviderMessageSubjectRebindKeepsPathAndGlobalNotice(t *testing.T) {
	messages := []CatalogProviderMessage{
		{Type: "warning", Path: "$.products[0]", SubjectKind: "PRODUCT", SubjectRef: "provider-product-1", Content: "Product disclosure"},
		{Type: "warning", SubjectKind: "SEARCH", Content: "Search notice"},
	}
	rebindCatalogProviderMessageSubjectsV2(messages, map[string]string{
		"provider-product-1": "candidate-1",
	})
	if messages[0].SubjectRef != "candidate-1" || messages[0].Path != "$.products[0]" ||
		messages[1].SubjectRef != "" || messages[1].Content != "Search notice" {
		t.Fatalf("messages=%+v", messages)
	}
}

func TestVisibleHydrationIsTargetScopedAndDefensivelyCapsFiftyCandidates(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	gateway := &liveReviewGatewayV2{clock: clock}
	state := CatalogWorkspaceStoredStateV2{Pools: []CatalogPoolMetadataV2{
		{TargetID: "target-1", Version: 52}, {TargetID: "target-2", Version: 8},
	}}
	for index := 0; index < 52; index++ {
		candidateID := fmt.Sprintf("target-1-candidate-%02d", index)
		url := fmt.Sprintf("https://shop.example/products/%02d", index)
		state.Candidates = append(state.Candidates, CatalogCandidateReferenceV2{
			PlanTargetID: "target-1", CandidateID: candidateID, Visible: true,
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: url}},
		})
	}
	for index := 0; index < 8; index++ {
		candidateID := fmt.Sprintf("target-2-candidate-%02d", index)
		url := fmt.Sprintf("https://shop.example/products/other-%02d", index)
		state.Candidates = append(state.Candidates, CatalogCandidateReferenceV2{
			PlanTargetID: "target-2", CandidateID: candidateID, Visible: true,
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: url}},
		})
	}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableWorkspaceRepositoryV2(&liveReviewWorkspaceRepositoryV2{state: state}); err != nil {
		t.Fatal(err)
	}
	view, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationVisibleTargetV2, TargetID: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pools) != 1 || view.Pools[0].Metadata.TargetID != "target-1" ||
		len(view.Pools[0].Products) != 50 || len(gateway.lookupRequests) != 1 ||
		len(gateway.lookupRequests[0].Inputs) != 50 {
		t.Fatalf("target-scoped view=%+v lookup inputs=%d", view.Pools,
			len(gateway.lookupRequests[0].Inputs))
	}
}

// A saved candidate with nothing Shopify could be asked with stays unresolved,
// and says so: a retry that would send nothing again is not offered.
func TestHydrationDoesNotOfferARetryForACandidateWithoutALocator(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)}
	state := CatalogWorkspaceStoredStateV2{
		Pools: []CatalogPoolMetadataV2{{TargetID: "target-1", Version: 2}},
		Candidates: []CatalogCandidateReferenceV2{
			{PlanTargetID: "target-1", CandidateID: "candidate-ok", Visible: true, DisplayOrder: 1,
				Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL,
					ProductURL: &CatalogProductURLLocator{CanonicalURL: "https://shop.example/products/ok"}}},
			{PlanTargetID: "target-1", CandidateID: "candidate-bare", Visible: true, DisplayOrder: 2,
				Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL}},
		},
	}
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess, ProviderCallCount: 1,
		Matches: []CatalogOfferMatch{{DraftID: "candidate:candidate-ok", Product: CatalogProductObservation{
			ProviderProductID: "provider-ok", Title: "Camp chair",
			PriceRange: CatalogPriceRange{Minimum: CatalogMoney{AmountMinor: 2499, Currency: "USD"}, Maximum: CatalogMoney{AmountMinor: 3999, Currency: "USD"}},
		}}},
	}}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableWorkspaceRepositoryV2(&liveReviewWorkspaceRepositoryV2{state: state}); err != nil {
		t.Fatal(err)
	}
	view, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationVisibleTargetV2, TargetID: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	pool := view.Pools[0]
	ok, bare := pool.Hydrations["candidate-ok"], pool.Hydrations["candidate-bare"]
	if ok.Status != CatalogCandidateHydrationReadyV2 || pool.Products[0].PriceRange.Minimum.AmountMinor != 2499 ||
		pool.Products[0].PriceRange.Maximum.AmountMinor != 3999 || pool.Products[0].PriceRange.Minimum.Currency != "USD" {
		t.Fatalf("a read product states the lowest and highest price Shopify returned: %+v %+v", ok, pool.Products[0].PriceRange)
	}
	if bare.Status != CatalogCandidateHydrationUnresolvedV2 || bare.Retryable ||
		bare.ReasonCode != CatalogCandidateHydrationNoLocatorReasonV2 || pool.Products[1].PriceRange.Minimum.Currency != "" {
		t.Fatalf("a candidate without a locator is unresolved, unpriced and not retryable: %+v %+v", bare, pool.Products[1])
	}
	if len(gateway.lookupRequests) != 1 || len(gateway.lookupRequests[0].Inputs) != 1 {
		t.Fatalf("only the candidate that can be asked about is sent: %+v", gateway.lookupRequests)
	}
}

func TestVisibleHydrationKeepsElevenReadyCandidatesWhenOneIsUnresolvedAndRetriesOnlyThatCandidate(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)}
	state := CatalogWorkspaceStoredStateV2{
		Pools: []CatalogPoolMetadataV2{{TargetID: "target-1", Version: 12}},
	}
	lookup := CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess, ProviderCallCount: 1,
	}
	for index := 1; index <= 12; index++ {
		candidateID := fmt.Sprintf("candidate-%02d", index)
		url := fmt.Sprintf("https://shop.example/products/%02d", index)
		state.Candidates = append(state.Candidates, CatalogCandidateReferenceV2{
			PlanTargetID: "target-1", CandidateID: candidateID, Visible: true,
			DisplayOrder: index,
			Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: url}},
		})
		if index < 12 {
			lookup.Matches = append(lookup.Matches, CatalogOfferMatch{
				DraftID: "candidate:" + candidateID,
				Product: CatalogProductObservation{
					ProviderProductID: fmt.Sprintf("provider-%02d", index),
					Title:             fmt.Sprintf("Product %02d", index),
				},
			})
		} else {
			lookup.UnresolvedIDs = append(lookup.UnresolvedIDs, "candidate:"+candidateID)
		}
	}
	gateway := &liveReviewGatewayV2{clock: clock, lookupResult: lookup}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableWorkspaceRepositoryV2(
		&liveReviewWorkspaceRepositoryV2{state: state},
	); err != nil {
		t.Fatal(err)
	}

	view, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationVisibleTargetV2, TargetID: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pools) != 1 || len(view.Pools[0].Products) != 12 {
		t.Fatalf("pools=%+v", view.Pools)
	}
	ready := 0
	unresolved := 0
	for _, product := range view.Pools[0].Products {
		hydration := view.Pools[0].Hydrations[product.ProviderProductID]
		switch hydration.Status {
		case CatalogCandidateHydrationReadyV2:
			ready++
		case CatalogCandidateHydrationUnresolvedV2:
			unresolved++
			if product.ProviderProductID != "candidate-12" || product.Title != "" ||
				hydration.ReasonCode != CatalogCandidateHydrationUnresolvedReasonV2 ||
				!hydration.Retryable {
				t.Fatalf("unresolved product=%+v hydration=%+v", product, hydration)
			}
		}
	}
	if ready != 11 || unresolved != 1 {
		t.Fatalf("ready=%d unresolved=%d", ready, unresolved)
	}

	gateway.lookupResult = CatalogOfferLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess, ProviderCallCount: 1,
		Matches: []CatalogOfferMatch{{
			DraftID: "candidate:candidate-12",
			Product: CatalogProductObservation{
				ProviderProductID: "provider-12", Title: "Recovered Product 12",
			},
		}},
	}
	retried, err := service.HydrateWorkspaceV2(context.Background(), CatalogResearchHydrationInputV2{
		UserID: "user-1", CurationID: "curation-1",
		Scope: CatalogResearchHydrationCandidateV2, TargetID: "target-1",
		CandidateID: "candidate-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.lookupRequests) != 2 || len(gateway.lookupRequests[1].Inputs) != 1 ||
		gateway.lookupRequests[1].Inputs[0].DraftID != "candidate:candidate-12" ||
		len(retried.Pools) != 1 || len(retried.Pools[0].Products) != 1 ||
		retried.Pools[0].Products[0].Title != "Recovered Product 12" ||
		retried.Pools[0].Hydrations["candidate-12"].Status != CatalogCandidateHydrationReadyV2 {
		t.Fatalf("requests=%+v retried=%+v", gateway.lookupRequests, retried.Pools)
	}
}

type liveReviewClockV2 struct{ now time.Time }

func (clock *liveReviewClockV2) Now() time.Time { return clock.now }

type liveReviewGatewayV2 struct {
	clock               *liveReviewClockV2
	calls               int
	request             CatalogProductSearchRequest
	lookupCalls         int
	lookupRequests      []CatalogOfferLookupRequest
	lookupProviderCalls int
	lookupResult        CatalogOfferLookupResult
}

type liveReviewWorkspaceRepositoryV2 struct {
	CatalogWorkspaceRepositoryV2
	state CatalogWorkspaceStoredStateV2
}

func (repository *liveReviewWorkspaceRepositoryV2) LoadCatalogWorkspaceStateV2(
	context.Context, string, string,
) (CatalogWorkspaceStoredStateV2, error) {
	return repository.state, nil
}

func (*liveReviewWorkspaceRepositoryV2) CatalogCurationMarketContextV2(
	context.Context, string, string,
) (CatalogMarketContextV2, error) {
	return CatalogMarketContextV2{Country: "US", Currency: "USD"}, nil
}

func (gateway *liveReviewGatewayV2) SearchProducts(
	_ context.Context,
	request CatalogProductSearchRequest,
) (CatalogProductSearchResult, error) {
	gateway.calls++
	gateway.request = request
	gateway.clock.now = gateway.clock.now.Add(275 * time.Millisecond)
	return CatalogProductSearchResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess,
		Products: []CatalogProductObservation{{
			ProviderProductID: "gid://shopify/p/test", Title: "Test Backpack",
			Locator: &CatalogProductLocator{
				Kind:       CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: "https://example.myshopify.com/products/test"},
			},
		}},
	}, nil
}

func (*liveReviewGatewayV2) LookupMedia(
	context.Context,
	CatalogMediaLookupRequest,
) (CatalogMediaLookupResult, error) {
	return CatalogMediaLookupResult{}, nil
}

func (gateway *liveReviewGatewayV2) LookupOffers(
	ctx context.Context,
	request CatalogOfferLookupRequest,
) (CatalogOfferLookupResult, error) {
	gateway.lookupCalls++
	gateway.lookupRequests = append(gateway.lookupRequests, request)
	providerCalls := gateway.lookupResult.ProviderCallCount
	if providerCalls < 1 {
		providerCalls = 1
	}
	result := gateway.lookupResult
	result.ProviderCallCount = 0
	for range providerCalls {
		release, err := request.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
		if err != nil {
			return result, err
		}
		gateway.lookupProviderCalls++
		result.ProviderCallCount++
		release()
	}
	gateway.clock.now = gateway.clock.now.Add(180 * time.Millisecond)
	return result, nil
}

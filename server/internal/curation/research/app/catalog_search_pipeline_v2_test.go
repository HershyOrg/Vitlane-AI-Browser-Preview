package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// shopifyTranslationForTestV2 does what a Round does for the Shopify catalog:
// build the plan once, take the adapter's proof view of it, translate it.
func shopifyTranslationForTestV2(
	t *testing.T, profile CatalogTargetSearchProfileV2, input CatalogWorkspaceSearchInputV2,
) (researchdomain.SearchPlan, researchdomain.TargetSearchIntentV2, researchdomain.CatalogSearchPlanV2) {
	t.Helper()
	plan, err := BuildSearchPlanV3(input, profile)
	if err != nil {
		t.Fatalf("search plan: %v", err)
	}
	query := plan.QueriesFor(researchdomain.SearchLanguageEnglish)[0]
	intent, exponent, err := shopifyProofIntentV2(profile, plan, query)
	if err != nil {
		t.Fatalf("proof intent: %v", err)
	}
	envelope, err := TranslateCatalogSearchPlanV2(TranslateCatalogSearchPlanV2Input{
		PlanID: "plan-1", Plan: plan, Query: query, Intent: intent,
		Capability: CatalogSearchCapabilityV2{
			ProtocolVersion: catalogProtocolVersionV2, SchemaFingerprint: "schema-1",
			MaximumLimit: 8, CurrencyExponents: []CatalogCurrencyExponentV2{{Currency: "USD", Exponent: exponent}},
		},
	})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	return plan, intent, envelope
}

// A Round asks the Shopify catalog once: the plan's phrase, this source's page.
// There is no second request with a reshaped query.
func TestCatalogShopifySearchAsksThePlansPhraseOnce(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)}
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "lightweight trail running shoes",
		Category: "shopify:footwear", TargetHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Market: CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}
	product := catalogProductWithURLV2("product-3", 8200)
	product.Media = nil // Optional media must never trigger a second discovery request.
	basePlan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	scripted := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(basePlan, product)},
	}}
	gateway := &catalogPlannedSearchGatewayV2{scripted: scripted}
	service, err := NewLiveCatalogReviewServiceV2(gateway, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := CatalogWorkspaceSearchInputV2{
		UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
		Mode:       CatalogResearchAppendV2,
		QuerySeeds: []string{"trail running shoes", "lightweight running shoes"},
		Search:     LiveCatalogReviewSearchInputV2{Query: "lightweight trail running shoes", Limit: 8},
	}
	result, err := service.searchWorkspacePlanForProfileV2(context.Background(), input, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripted.calls) != 1 || result.Metrics.ShopifyCallCount != 1 {
		t.Fatalf("calls=%d metrics=%+v", len(scripted.calls), result.Metrics)
	}
	request := scripted.calls[0]
	if request.Query != "lightweight trail running shoes" || request.Cursor != "" || request.Filters.Price != nil {
		t.Fatalf("the request is the plan's phrase, first page, no price: %+v", request)
	}
	if len(result.Search.Products) != 1 || result.Search.Products[0].ProviderProductID != "product-3" {
		t.Fatalf("products=%+v", result.Search.Products)
	}
	// This phrase had no further page, so the next Round reads the plan's next phrase.
	next := result.NextProgress["SHOPIFY"]
	if next.Query != "trail running shoes" || next.Page != 1 {
		t.Fatalf("next progress=%+v", next)
	}
	if result.Metrics.ProviderQuery != "lightweight trail running shoes" || result.Metrics.ProviderCountry != "US" {
		t.Fatalf("metrics=%+v", result.Metrics)
	}
}

// One word is a complete query. This used to fail with
// CATALOG_SEARCH_PLAN_COMPILE_INVALID before any Shopify request was sent.
func TestCatalogShopifySearchAcceptsAOneWordQuery(t *testing.T) {
	for _, phrase := range []string{"backpack", "sunscreen", "camping chair"} {
		profile := CatalogTargetSearchProfileV2{
			TargetID: "target-1", NormalizedIntent: phrase, TargetHash: "profile-hash",
			Market: CatalogMarketContextV2{Country: "US", Currency: "USD"},
		}
		_, intent, envelope := shopifyTranslationForTestV2(t, profile, CatalogWorkspaceSearchInputV2{
			TargetID: "target-1", Mode: CatalogResearchAppendV2,
			Criteria: &curationdomain.TargetCriteriaSetV1{Subject: curationdomain.ResearchSubject{ProductType: phrase}},
			Search:   LiveCatalogReviewSearchInputV2{Query: phrase},
		})
		if len(envelope.Requests) != 1 || envelope.Requests[0].Query != phrase {
			t.Fatalf("%q: envelope=%+v", phrase, envelope.Requests)
		}
		if intent.ProductType != phrase {
			t.Fatalf("%q: provider proof changed product type to %q", phrase, intent.ProductType)
		}
	}
}

func TestCatalogManagedQueryTermsBecomeServerPostFilters(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "lightweight trail running shoes",
		TargetHash: "profile-hash",
		Market:     CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}
	plan, intent, envelope := shopifyTranslationForTestV2(t, profile, CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Mode: CatalogResearchAppendV2,
		Search: LiveCatalogReviewSearchInputV2{
			Query:            "trail running shoes",
			HardLexicalTerms: []string{"water-resistant", "reflective trim"},
			Exclusions:       []string{"used", "open box"},
		},
	})
	if strings.Join(plan.Required.MustContain, "|") != "water-resistant|reflective trim" ||
		strings.Join(plan.Required.Exclusions, "|") != "used|open box" {
		t.Fatalf("the plan states the required conditions once: %+v", plan.Required)
	}
	if strings.Join(intent.HardLexicalTerms, "|") != "water-resistant|reflective trim" ||
		strings.Join(intent.Exclusions, "|") != "used|open box" {
		t.Fatalf("intent terms=%+v exclusions=%+v", intent.HardLexicalTerms, intent.Exclusions)
	}
	postFilters := strings.Join(envelope.Requests[0].PostFilters, "|")
	for _, expected := range []string{
		"require water-resistant", "require reflective trim",
		"exclude used", "exclude open box",
	} {
		if !strings.Contains(postFilters, expected) {
			t.Fatalf("postFilters=%q missing %q", postFilters, expected)
		}
	}
}

func TestCatalogManagedQueryRejectsNonEnglishHardTerms(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "trail running shoes",
		TargetHash: "profile-hash",
		Market:     CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}
	plan, err := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Mode: CatalogResearchAppendV2,
		Search: LiveCatalogReviewSearchInputV2{
			Query: "trail running shoes", HardLexicalTerms: []string{"방수"},
		},
	}, profile)
	if err != nil {
		t.Fatalf("the plan keeps the user's words as given: %v", err)
	}
	_, _, err = shopifyProofIntentV2(profile, plan, "trail running shoes")
	failure, ok := fault.As(err)
	if !ok || failure.Reason != "PHASE8_MANAGED_SEARCH_TERMS_INVALID" {
		t.Fatalf("an English catalog cannot enforce a Korean term: %v", err)
	}
}

func TestCatalogSearchPlanUsesServerOwnedPriceProfile(t *testing.T) {
	minimum, err := shareddomain.NewMoney("12.50", "USD")
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := shareddomain.NewMoney("80", "USD")
	if err != nil {
		t.Fatal(err)
	}
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "compact travel adapter",
		TargetHash:   "profile-hash",
		Market:       CatalogMarketContextV2{Country: "US", Currency: "USD"},
		MinimumPrice: &minimum, MaximumPrice: &maximum,
	}
	browserMinimum := int64(1)
	browserMaximum := int64(999999)
	input := CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Mode: CatalogResearchAppendV2,
		Search: LiveCatalogReviewSearchInputV2{
			MinimumMinor: &browserMinimum, MaximumMinor: &browserMaximum,
		},
	}
	plan, _, envelope := shopifyTranslationForTestV2(t, profile, input)
	if plan.Required.MinimumMinor == nil || *plan.Required.MinimumMinor != 1250 ||
		plan.Required.MaximumMinor == nil || *plan.Required.MaximumMinor != 8000 {
		t.Fatalf("the plan's bounds come from the server's profile: %+v", plan.Required)
	}
	price := envelope.Requests[0].Filters.Price
	if price == nil || price.MinimumMinor == nil || price.MaximumMinor == nil ||
		*price.MinimumMinor != 1250 || *price.MaximumMinor != 8000 {
		t.Fatalf("browser price escaped server profile: %+v", price)
	}

	profile.MinimumPrice = nil
	profile.MaximumPrice = nil
	plan, intent, _ := shopifyTranslationForTestV2(t, profile, input)
	if plan.HasPriceBound() || intent.PriceConstraint.Kind != shareddomain.ResearchPriceConstraintNone {
		t.Fatalf("NONE profile accepted browser price: plan=%+v intent=%+v", plan.Required, intent)
	}
}

func TestCatalogMissingMediaHydratesFiftyProductsInOneProviderCall(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)}
	provider := &catalogMediaLookupGatewayV2{}
	service, err := NewLiveCatalogReviewServiceV2(provider, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 1, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rateGateway := &catalogRateLimitedCatalogGatewayV2{service: service}
	products := catalogProductsWithoutMediaV2(50)
	messages := []CatalogProviderMessage{}
	if err := catalogLookupMissingMediaV2(
		context.Background(), rateGateway,
		CatalogMarketContextV2{Country: "US", Currency: "USD"},
		products, &messages,
	); err != nil {
		t.Fatal(err)
	}
	if provider.actualCalls != 1 || rateGateway.calls != 1 ||
		rateGateway.lastUsed != 1 || rateGateway.lastRemaining != 0 {
		t.Fatalf(
			"providerCalls=%d accounted=%d used=%d remaining=%d",
			provider.actualCalls, rateGateway.calls,
			rateGateway.lastUsed, rateGateway.lastRemaining,
		)
	}
	metrics := catalogPlannedSearchMetricsV2(service, rateGateway, clock.now, clock.now)
	if metrics.ShopifyCallCount != 1 || metrics.LocalCallsUsed != 1 ||
		metrics.LocalCallsRemaining != 0 {
		t.Fatalf("metrics=%+v", metrics)
	}
	for index, product := range products {
		if len(product.Media) != 1 || product.Media[0].URL == "" {
			t.Fatalf("product %d media=%+v", index, product.Media)
		}
	}
}

func TestCatalogMissingMediaDoesNotOvercountFiftyProductBatch(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)}
	provider := &catalogMediaLookupGatewayV2{}
	service, err := NewLiveCatalogReviewServiceV2(provider, clock, LiveCatalogReviewConfigV2{
		MaximumCallsPerWindow: 1, Window: time.Minute, MaximumConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rateGateway := &catalogRateLimitedCatalogGatewayV2{service: service}
	products := catalogProductsWithoutMediaV2(50)
	err = catalogLookupMissingMediaV2(
		context.Background(), rateGateway,
		CatalogMarketContextV2{Country: "US", Currency: "USD"},
		products, &[]CatalogProviderMessage{},
	)
	if err != nil {
		t.Fatalf("lookup media: %v", err)
	}
	if provider.actualCalls != 1 || rateGateway.calls != 1 ||
		service.windowCalls != 1 || service.activeCalls != 0 {
		t.Fatalf(
			"providerCalls=%d accounted=%d window=%d active=%d",
			provider.actualCalls, rateGateway.calls,
			service.windowCalls, service.activeCalls,
		)
	}
}

type catalogMediaLookupGatewayV2 struct {
	actualCalls int
}

func (*catalogMediaLookupGatewayV2) SearchProducts(
	context.Context,
	CatalogProductSearchRequest,
) (CatalogProductSearchResult, error) {
	return CatalogProductSearchResult{}, nil
}

func (gateway *catalogMediaLookupGatewayV2) LookupMedia(
	ctx context.Context,
	request CatalogMediaLookupRequest,
) (CatalogMediaLookupResult, error) {
	result := CatalogMediaLookupResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "2026-04-08",
		Outcome: CatalogOutcomeSuccess, Matches: []CatalogMediaMatch{},
	}
	for start := 0; start < len(request.Inputs); start += CatalogLookupDefaultBatchSize {
		end := min(start+CatalogLookupDefaultBatchSize, len(request.Inputs))
		release, err := request.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
		if err != nil {
			return result, err
		}
		gateway.actualCalls++
		result.ProviderCallCount++
		for _, input := range request.Inputs[start:end] {
			result.Matches = append(result.Matches, CatalogMediaMatch{
				CorrelationKey: input.CorrelationKey,
				Media: []CatalogMedia{{
					URL: "https://cdn.example/" + input.CorrelationKey + ".jpg",
				}},
			})
		}
		release()
	}
	return result, nil
}

func (*catalogMediaLookupGatewayV2) LookupOffers(
	context.Context,
	CatalogOfferLookupRequest,
) (CatalogOfferLookupResult, error) {
	return CatalogOfferLookupResult{}, nil
}

func catalogProductsWithoutMediaV2(count int) []CatalogProductObservation {
	products := make([]CatalogProductObservation, 0, count)
	for index := 0; index < count; index++ {
		productID := fmt.Sprintf("product-%d", index)
		products = append(products, CatalogProductObservation{
			ProviderProductID: productID,
			Locator: &CatalogProductLocator{
				Kind: CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{
					CanonicalURL: "https://merchant.example/products/" + productID,
				},
			},
		})
	}
	return products
}

type catalogPlannedSearchGatewayV2 struct {
	scripted *scriptedCatalogGatewayV2
}

func (gateway *catalogPlannedSearchGatewayV2) SearchProducts(
	ctx context.Context,
	request CatalogProductSearchRequest,
) (CatalogProductSearchResult, error) {
	return gateway.scripted.SearchProducts(ctx, request)
}

func (gateway *catalogPlannedSearchGatewayV2) LookupMedia(
	ctx context.Context,
	request CatalogMediaLookupRequest,
) (CatalogMediaLookupResult, error) {
	return gateway.scripted.LookupMedia(ctx, request)
}

func (*catalogPlannedSearchGatewayV2) LookupOffers(
	context.Context,
	CatalogOfferLookupRequest,
) (CatalogOfferLookupResult, error) {
	return CatalogOfferLookupResult{}, nil
}

type catalogPlannedSearchProfileRepositoryV2 struct {
	CatalogWorkspaceRepositoryV2
	profile CatalogTargetSearchProfileV2
}

func (repository *catalogPlannedSearchProfileRepositoryV2) CatalogTargetSearchProfileV2(
	context.Context,
	string,
	string,
	string,
) (CatalogTargetSearchProfileV2, error) {
	return repository.profile, nil
}

func TestCatalogPreferredTermsStaySoftAndNeverHardFilter(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "lightweight trail running shoes",
		TargetHash: "profile-hash",
		Market:     CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}
	plan, intent, envelope := shopifyTranslationForTestV2(t, profile, CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Mode: CatalogResearchAppendV2,
		Search: LiveCatalogReviewSearchInputV2{
			Query:          "trail running shoes",
			PreferredTerms: []string{"cheaper", "red colorway"},
		},
	})
	if len(plan.Required.MustContain) != 0 || len(intent.HardLexicalTerms) != 0 {
		t.Fatalf("preferred terms escalated to required conditions: %+v %+v", plan.Required, intent.HardLexicalTerms)
	}
	if strings.Join(plan.Preferred.Terms, "|") != "cheaper|red colorway" {
		t.Fatalf("the plan keeps what is merely preferred: %+v", plan.Preferred)
	}
	if envelope.Requests[0].Query != "lightweight trail running shoes" {
		t.Fatalf("a preference never rewrites the Round's phrase: %q", envelope.Requests[0].Query)
	}
	// A product that mentions none of the preferred terms is still admitted.
	if reason := AdmitPlannedObservationV3(plan, catalogProductWithURLV2("product-1", 8200), nil); reason != "" {
		t.Fatalf("a preference rejected a product: %s", reason)
	}
}

// The phrase the query step wrote is what is asked, word for word — one word included.
func TestAxisSearchAsksTheSourceQueryUnchanged(t *testing.T) {
	for _, query := range []string{"classic fountain pen", "fountain pen", "pen", "a ceramic coffee mug and a stainless steel water bottle"} {
		profile := CatalogTargetSearchProfileV2{TargetID: "target", NormalizedIntent: query, Category: "문구", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
		input := CatalogWorkspaceSearchInputV2{TargetID: "target", Mode: CatalogResearchAppendV2, Criteria: &curationdomain.TargetCriteriaSetV1{Subject: curationdomain.ResearchSubject{ProductType: "fountain pen"}}, Search: LiveCatalogReviewSearchInputV2{Query: query}}
		_, intent, envelope := shopifyTranslationForTestV2(t, profile, input)
		if envelope.Requests[0].Query != query || intent.IntentSummaryEnglish != query || len(intent.SoftLexicalTerms) != 0 || len(intent.HardLexicalTerms) != 0 {
			t.Fatalf("hidden scoring factors or a rewritten phrase: %q %+v", envelope.Requests[0].Query, intent)
		}
	}
}

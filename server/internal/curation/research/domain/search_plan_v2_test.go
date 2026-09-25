package domain

import (
	"testing"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func TestTargetSearchIntentV2RequiresEnglishBoundMarketInput(t *testing.T) {
	t.Parallel()
	intent := validTargetSearchIntentV2(t)
	if err := intent.Validate(); err != nil {
		t.Fatalf("valid intent: %v", err)
	}
	intent.IntentSummaryEnglish = "방수 트레일 러닝화"
	if err := intent.Validate(); err == nil {
		t.Fatal("non-English semantic input should fail")
	}
	intent = validTargetSearchIntentV2(t)
	intent.ShippingCountry = "KR"
	if err := intent.Validate(); err == nil {
		t.Fatal("shipping country must remain bound to market context")
	}
}

func TestTargetSearchIntentV2AcceptsReviewedNumericTaxonomy(t *testing.T) {
	t.Parallel()
	intent := validTargetSearchIntentV2(t)
	intent.VerifiedCategories = []string{"187"}
	if err := intent.Validate(); err != nil {
		t.Fatalf("numeric verified taxonomy should remain valid filter data: %v", err)
	}
}

// The Shopify request envelope holds exactly one request, and the only rules
// about its phrase are the provider's: English, and at most twelve words.
func TestCatalogSearchPlanV2IsOneRequestWithoutAWordMinimum(t *testing.T) {
	t.Parallel()
	build := func(requests []CatalogSearchRequestV2) (CatalogSearchPlanV2, error) {
		return NewCatalogSearchPlanV2(NewCatalogSearchPlanV2Input{
			PlanID: "search-plan-1", TargetID: "target-1",
			SourceProfileHash:           "sha256:profile",
			CompilerPolicyVersion:       SearchPlanSchemaV3,
			ProviderProtocolVersion:     "2026-04-08",
			CapabilitySchemaFingerprint: "sha256:schema",
			Requests:                    requests,
		})
	}
	plan, err := build(validCatalogSearchRequestsV2())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("validate plan: %v", err)
	}

	// "sunscreen" is a complete query. A minimum word count once failed every one-word search
	// before any request was sent; the envelope states no such rule.
	oneWord := validCatalogSearchRequestsV2()
	oneWord[0].Query = "sunscreen"
	if _, err := build(oneWord); err != nil {
		t.Fatalf("a one-word query is valid: %v", err)
	}
	for name, mutate := range map[string]func([]CatalogSearchRequestV2) []CatalogSearchRequestV2{
		"an empty query": func(r []CatalogSearchRequestV2) []CatalogSearchRequestV2 { r[0].Query = " "; return r },
		"a Korean query": func(r []CatalogSearchRequestV2) []CatalogSearchRequestV2 { r[0].Query = "선크림"; return r },
		"thirteen words": func(r []CatalogSearchRequestV2) []CatalogSearchRequestV2 {
			r[0].Query = "a b c d e f g h i j k l m"
			return r
		},
		"two requests": func(r []CatalogSearchRequestV2) []CatalogSearchRequestV2 { return append(r, r[0]) },
		"no request":   func([]CatalogSearchRequestV2) []CatalogSearchRequestV2 { return nil },
		"lowercase market": func(r []CatalogSearchRequestV2) []CatalogSearchRequestV2 {
			r[0].Context.AddressCountry = "us"
			r[0].Filters.ShipsTo.Country = "us"
			return r
		},
	} {
		if _, err := build(mutate(validCatalogSearchRequestsV2())); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}

	changed := plan
	changed.Requests = validCatalogSearchRequestsV2()
	changed.Requests[0].Cursor = "opaque:next"
	if err := changed.Validate(); err == nil {
		t.Fatal("the content hash binds the page that was asked")
	}
}

func validTargetSearchIntentV2(t *testing.T) TargetSearchIntentV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	return TargetSearchIntentV2{
		ProductType: "trail running shoes", UseCaseTerms: []string{"wet weather"},
		HardLexicalTerms: []string{"waterproof"}, SoftLexicalTerms: []string{"lightweight"},
		Exclusions: []string{"used"}, Colors: []string{"Black"},
		Sizes:         []CatalogSearchSizeV2{{Value: "10", SizingSystem: "US"}},
		TargetGenders: []string{"Unisex"}, Conditions: []string{"NEW"},
		PriceConstraint: shareddomain.NoResearchPriceConstraint(),
		MarketContext:   market, ShippingCountry: market.Country,
		IntentSummaryEnglish: "Lightweight trail shoes for wet-weather running.",
		SourceProfileHash:    "sha256:profile",
	}
}

func validCatalogSearchRequestsV2() []CatalogSearchRequestV2 {
	return []CatalogSearchRequestV2{{
		Query: "waterproof trail running shoes",
		Context: CatalogSearchContextV2{
			AddressCountry: "US", Currency: "USD", Language: "en",
			Intent: "Trail shoes for wet-weather running.",
		},
		Filters: CatalogSearchFiltersV2{
			Available: true, ShipsTo: CatalogSearchDestinationV2{Country: "US"},
			Conditions: []string{"new"},
		},
		PostFilters:     []string{"require waterproof", "exclude used"},
		RankPreferences: []string{"lightweight"},
		Limit:           8,
	}}
}

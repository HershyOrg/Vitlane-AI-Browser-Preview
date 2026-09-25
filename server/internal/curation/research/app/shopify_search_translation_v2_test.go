package app

import (
	"slices"
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// The Shopify adapter asks the phrase the plan prepared, with the plan's
// required conditions as the filters this provider can apply, in one request.
func TestTranslateCatalogSearchPlanV2AsksThePlansPhraseWithItsConditions(t *testing.T) {
	t.Parallel()
	input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
	plan, err := TranslateCatalogSearchPlanV2(input)
	if err != nil {
		t.Fatalf("translate plan: %v", err)
	}
	if len(plan.Requests) != 1 {
		t.Fatalf("one phrase, one page, one request: got %d", len(plan.Requests))
	}
	request := plan.Requests[0]
	if request.Query != "waterproof trail running shoes" || request.Cursor != "" {
		t.Fatalf("the adapter asks the plan's phrase and nothing else: %q", request.Query)
	}
	if request.Filters.Price != nil {
		t.Fatalf("NONE gained a price filter: %+v", request.Filters.Price)
	}
	if request.Context.Language != "en" || request.Context.Currency != "USD" ||
		request.Context.AddressCountry != "US" {
		t.Fatalf("context is not the English US/USD contract: %+v", request.Context)
	}
	if !slices.Equal(request.Filters.Conditions, []string{"new"}) ||
		len(request.Filters.Attributes) != 3 || request.Limit != 20 {
		t.Fatalf("structured filters were not mapped: %+v", request.Filters)
	}
	if !slices.Equal(request.PostFilters, []string{"require waterproof", "exclude used"}) {
		t.Fatalf("hard/exclusion post filters were mixed: %v", request.PostFilters)
	}
	if !slices.Equal(request.RankPreferences, []string{"lightweight", "price tier premium"}) {
		t.Fatalf("soft rank preferences were not isolated: %v", request.RankPreferences)
	}
	second, err := TranslateCatalogSearchPlanV2(input)
	if err != nil {
		t.Fatalf("translate same input again: %v", err)
	}
	if second.ContentHash != plan.ContentHash {
		t.Fatalf("same input must be deterministic: %s != %s", second.ContentHash, plan.ContentHash)
	}
	// The next page of the same phrase is a different request.
	input.Cursor = "opaque:next"
	paged, err := TranslateCatalogSearchPlanV2(input)
	if err != nil || paged.Requests[0].Cursor != "opaque:next" || paged.ContentHash == plan.ContentHash {
		t.Fatalf("the adapter's page travels in the request: %v %+v", err, paged.Requests)
	}
}

// The regression this structure exists to prevent: a one-word phrase failed a
// three-word check before any Shopify request was sent. The adapter has no word
// minimum; its only length rule is the provider's maximum, kept by cutting.
func TestTranslateCatalogSearchPlanV2AcceptsOneWordAndCutsLongPhrases(t *testing.T) {
	t.Parallel()
	for phrase, want := range map[string]string{
		"sunscreen":     "sunscreen",
		"camping chair": "camping chair",
		"one two three four five six seven eight nine ten eleven twelve thirteen": "one two three four five six seven eight nine ten eleven twelve",
	} {
		input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
		input.Plan = catalogSearchPlanV3(t, phrase)
		input.Query = phrase
		plan, err := TranslateCatalogSearchPlanV2(input)
		if err != nil {
			t.Fatalf("%q must translate: %v", phrase, err)
		}
		if plan.Requests[0].Query != want {
			t.Fatalf("%q became %q", phrase, plan.Requests[0].Query)
		}
	}
}

func TestTranslateCatalogSearchPlanV2AsksOnlyWhatThePlanPrepared(t *testing.T) {
	t.Parallel()
	input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
	input.Query = "hiking boots"
	_, err := TranslateCatalogSearchPlanV2(input)
	assertCompilerFaultV2(t, err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2)

	// A plan with Korean phrases only has nothing for an English catalog.
	korean, planErr := researchdomain.NewSearchPlan(researchdomain.NewSearchPlanInput{
		TargetID: "target-1", Limit: 20,
		Queries:  []researchdomain.SearchPlanQuery{{Language: researchdomain.SearchLanguageKorean, Text: "트레일 러닝화"}},
		Required: researchdomain.SearchPlanRequired{Market: input.Intent.MarketContext},
	})
	if planErr != nil {
		t.Fatalf("korean plan: %v", planErr)
	}
	input.Plan, input.Query = korean, "트레일 러닝화"
	_, err = TranslateCatalogSearchPlanV2(input)
	assertCompilerFaultV2(t, err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2)
}

func TestTranslateCatalogSearchPlanV2PreservesExplicitZeroMinorBound(t *testing.T) {
	t.Parallel()
	zero := mustCompilerMoneyV2(t, "0", "USD")
	maximum := mustCompilerMoneyV2(t, "150", "USD")
	constraint, err := shareddomain.NewExplicitResearchPriceConstraint(&zero, &maximum)
	if err != nil {
		t.Fatalf("constraint: %v", err)
	}
	plan, err := TranslateCatalogSearchPlanV2(catalogCompilerInputV2(t, constraint))
	if err != nil {
		t.Fatalf("translate plan: %v", err)
	}
	price := plan.Requests[0].Filters.Price
	if price == nil || price.MinimumMinor == nil || *price.MinimumMinor != 0 ||
		price.MaximumMinor == nil || *price.MaximumMinor != 15000 ||
		price.Currency != "USD" {
		t.Fatalf("explicit zero was collapsed or mis-scaled: %+v", price)
	}
}

func TestTranslateCatalogSearchPlanV2NoneDoesNotRequireCurrencyExponent(t *testing.T) {
	t.Parallel()
	input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
	input.Capability.CurrencyExponents = nil
	plan, err := TranslateCatalogSearchPlanV2(input)
	if err != nil {
		t.Fatalf("NONE should not require irrelevant currency exponent metadata: %v", err)
	}
	if plan.Requests[0].Filters.Price != nil {
		t.Fatalf("NONE gained price: %+v", plan.Requests[0].Filters.Price)
	}
}

func TestTranslateCatalogSearchPlanV2RejectsUnreviewedLimitsAndSchema(t *testing.T) {
	t.Parallel()
	t.Run("above provider maximum", func(t *testing.T) {
		t.Parallel()
		input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
		input.Capability.MaximumLimit = input.Plan.Limit - 1
		_, err := TranslateCatalogSearchPlanV2(input)
		assertCompilerFaultV2(t, err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2)
	})
	t.Run("missing market exponent", func(t *testing.T) {
		t.Parallel()
		maximum := mustCompilerMoneyV2(t, "150", "USD")
		constraint, err := shareddomain.NewExplicitResearchPriceConstraint(nil, &maximum)
		if err != nil {
			t.Fatalf("constraint: %v", err)
		}
		input := catalogCompilerInputV2(t, constraint)
		input.Capability.CurrencyExponents = []CatalogCurrencyExponentV2{{Currency: "JPY", Exponent: 0}}
		_, err = TranslateCatalogSearchPlanV2(input)
		assertCompilerFaultV2(
			t, err, fault.ProviderRejected, string(CatalogFailureSchemaMismatch),
		)
	})
	t.Run("non English context", func(t *testing.T) {
		t.Parallel()
		input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
		input.Intent.IntentSummaryEnglish = "가벼운 트레일 러닝화"
		_, err := TranslateCatalogSearchPlanV2(input)
		assertCompilerFaultV2(t, err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2)
	})
	t.Run("another market than the plan's", func(t *testing.T) {
		t.Parallel()
		input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
		other, err := shareddomain.NewMarketContext("CA", "CAD")
		if err != nil {
			t.Fatalf("market: %v", err)
		}
		input.Intent.MarketContext, input.Intent.ShippingCountry = other, other.Country
		_, err = TranslateCatalogSearchPlanV2(input)
		assertCompilerFaultV2(t, err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2)
	})
}

// catalogSearchPlanV3 is a Round's plan with one English phrase for the US market.
func catalogSearchPlanV3(t *testing.T, phrases ...string) researchdomain.SearchPlan {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	queries := make([]researchdomain.SearchPlanQuery, 0, len(phrases))
	for _, phrase := range phrases {
		queries = append(queries, researchdomain.SearchPlanQuery{Language: researchdomain.SearchLanguageEnglish, Text: phrase})
	}
	plan, err := researchdomain.NewSearchPlan(researchdomain.NewSearchPlanInput{
		TargetID: "target-1", ProductType: "trail running shoes", Limit: 20, Queries: queries,
		Required: researchdomain.SearchPlanRequired{
			Market: market, Exclusions: []string{"used"}, MustContain: []string{"waterproof"}, Conditions: []string{"new"},
		},
		Preferred: researchdomain.SearchPlanPreferred{Terms: []string{"lightweight"}},
	})
	if err != nil {
		t.Fatalf("search plan: %v", err)
	}
	return plan
}

func catalogCompilerInputV2(
	t *testing.T,
	constraint shareddomain.ResearchPriceConstraint,
) TranslateCatalogSearchPlanV2Input {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	return TranslateCatalogSearchPlanV2Input{
		PlanID: "search-plan-1",
		Plan:   catalogSearchPlanV3(t, "waterproof trail running shoes"),
		Query:  "waterproof trail running shoes",
		Intent: researchdomain.TargetSearchIntentV2{
			ProductType:   "trail running shoes",
			UseCaseTerms:  []string{"wet weather"},
			MaterialTerms: []string{"rubber"}, StyleTerms: []string{"minimalist"},
			HardLexicalTerms: []string{"waterproof"},
			SoftLexicalTerms: []string{"lightweight"},
			Exclusions:       []string{"used"}, Colors: []string{"Black"},
			Sizes: []researchdomain.CatalogSearchSizeV2{
				{Value: "10", SizingSystem: "US"},
				{Value: "10.5", SizingSystem: "US"},
			},
			TargetGenders: []string{"Unisex"}, Conditions: []string{"NEW"},
			VerifiedCategories:  []string{"Footwear"},
			PriceTierPreference: "premium", PriceConstraint: constraint,
			MarketContext: market, ShippingCountry: market.Country,
			IntentSummaryEnglish: "Lightweight trail shoes for wet-weather running.",
			SourceProfileHash:    "sha256:profile",
		},
		Capability: CatalogSearchCapabilityV2{
			ProtocolVersion: "2026-04-08", SchemaFingerprint: "sha256:schema",
			MaximumLimit: 20,
			CurrencyExponents: []CatalogCurrencyExponentV2{
				{Currency: "USD", Exponent: 2},
			},
		},
	}
}

func mustCompilerMoneyV2(t *testing.T, amount, currency string) shareddomain.Money {
	t.Helper()
	money, err := shareddomain.NewMoney(amount, currency)
	if err != nil {
		t.Fatalf("money: %v", err)
	}
	return money
}

func assertCompilerFaultV2(
	t *testing.T,
	err error,
	wantCode fault.Code,
	wantReason string,
) {
	t.Helper()
	classified, ok := fault.As(err)
	if !ok || classified.Code != wantCode || classified.Reason != wantReason {
		t.Fatalf("fault=%v want code=%s reason=%s", err, wantCode, wantReason)
	}
}

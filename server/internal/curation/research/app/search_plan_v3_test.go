package app

import (
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// A Round builds one plan, in the language of its market, from what the Round
// already decided. Nothing in it depends on which source will be asked.
func TestBuildSearchPlanV3PreparesPhrasesInTheMarketsLanguage(t *testing.T) {
	maximum, err := shareddomain.NewMoney("80", "USD")
	if err != nil {
		t.Fatal(err)
	}
	us := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "sunscreen", Category: "shopify:skincare",
		Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}, MaximumPrice: &maximum,
	}
	plan, err := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", ProductVertical: "BEAUTY",
		QuerySeeds: []string{"sunscreen", "mineral sunscreen spf 50", "선크림", " "},
		Criteria:   &curationdomain.TargetCriteriaSetV1{Subject: curationdomain.ResearchSubject{ProductType: "sunscreen"}},
		Search: LiveCatalogReviewSearchInputV2{
			Query: "sunscreen", Limit: 8, PreferredTerms: []string{"reef safe"}, Exclusions: []string{"spray"},
		},
	}, us)
	if err != nil {
		t.Fatalf("a one-word phrase builds a plan: %v", err)
	}
	english := plan.QueriesFor(researchdomain.SearchLanguageEnglish)
	if len(english) != 2 || english[0] != "sunscreen" || english[1] != "mineral sunscreen spf 50" ||
		len(plan.QueriesFor(researchdomain.SearchLanguageKorean)) != 0 {
		t.Fatalf("US phrases are English, primary first, and a Korean seed is left out: %+v", plan.Queries)
	}
	if plan.Required.MaximumMinor == nil || *plan.Required.MaximumMinor != 8000 || plan.Required.MinimumMinor != nil ||
		len(plan.Required.Exclusions) != 1 || len(plan.Required.MustContain) != 0 ||
		len(plan.Preferred.Terms) != 1 || plan.Preferred.ProductVertical != "BEAUTY" ||
		len(plan.Preferred.Categories) != 1 || plan.ProductType != "sunscreen" || plan.Limit != 8 {
		t.Fatalf("required and preferred conditions: %+v", plan)
	}

	kr := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "라미 사파리 만년필",
		Market: CatalogMarketContextV2{Country: "KR", Currency: "KRW"},
	}
	// A cumulative Round searches its own saved phrase, whatever the request carries.
	plan, err = BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Mode: CatalogResearchAppendV2, QuerySeeds: []string{"라미 사파리 만년필", "LAMY safari 만년필"},
		Search: LiveCatalogReviewSearchInputV2{Query: "ignored request query"},
	}, kr)
	if err != nil {
		t.Fatal(err)
	}
	korean := plan.QueriesFor(researchdomain.SearchLanguageKorean)
	if len(korean) != 2 || korean[0] != "라미 사파리 만년필" || len(plan.QueriesFor(researchdomain.SearchLanguageEnglish)) != 0 {
		t.Fatalf("a Korean market is asked in Korean, with the Round's own phrase first: %+v", plan.Queries)
	}
}

func TestBuildSearchPlanV3NeverSendsAKoreanPhraseToAnEnglishCatalog(t *testing.T) {
	profile := CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "캠핑 의자",
		Market: CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}
	_, err := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{TargetID: "target-1"}, profile)
	if failure, ok := fault.As(err); !ok || failure.Reason != "RESEARCH_INPUT_NORMALIZATION_REQUIRED" {
		t.Fatalf("want normalization required, got %v", err)
	}
	// The query step's normalised phrase stands in for a Target written in Korean.
	plan, err := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{
		TargetID: "target-1", Search: LiveCatalogReviewSearchInputV2{Query: "camping chair"},
	}, profile)
	if err != nil || plan.Queries[0].Text != "camping chair" {
		t.Fatalf("plan=%+v err=%v", plan.Queries, err)
	}
}

// Every source's normalised results meet the same required conditions.
func TestAdmitPlannedObservationV3HoldsEverySourceToTheSameConditions(t *testing.T) {
	minimum, maximum := int64(2000), int64(9000)
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := researchdomain.NewSearchPlan(researchdomain.NewSearchPlanInput{
		TargetID: "target-1", Limit: 8,
		Queries: []researchdomain.SearchPlanQuery{{Language: researchdomain.SearchLanguageEnglish, Text: "trail running shoes"}},
		Required: researchdomain.SearchPlanRequired{
			Market: market, MinimumMinor: &minimum, MaximumMinor: &maximum,
			Exclusions: []string{"used"}, MustContain: []string{"trail"},
		},
		Preferred: researchdomain.SearchPlanPreferred{Terms: []string{"red colorway"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shopify := func(title string, low, high int64, currency string) CatalogProductObservation {
		product := catalogProductWithURLV2("product", low)
		product.Title = title
		product.PriceRange = CatalogPriceRange{Minimum: CatalogMoney{AmountMinor: low, Currency: currency}, Maximum: CatalogMoney{AmountMinor: high, Currency: currency}}
		return product
	}
	amazonRef := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B000000001"}
	amazon := func(kind string, amount int64) CatalogProductObservation {
		observation := &researchdomain.VariantObservation{}
		observation.Price.Kind = kind
		if kind == "OBSERVED" {
			observation.Price.AmountMinor, observation.Price.Currency = &amount, "USD"
		}
		return CatalogProductObservation{ProviderProductID: "amazon:US:B000000001", SourceProductRef: &amazonRef, Title: "Trail Running Shoes", VariantObservation: observation}
	}
	toUSD := func(amountMinor int64, currency string) (int64, bool) { return amountMinor / 13, currency == "KRW" }
	for name, test := range map[string]struct {
		product CatalogProductObservation
		convert PlannedPriceConverterV3
		want    PlannedObservationRejectionV3
	}{
		"fits":                                 {shopify("Trail Running Shoes", 8200, 8200, "USD"), nil, ""},
		"a preference is never required":       {shopify("Trail Running Shoes in blue", 8200, 8200, "USD"), nil, ""},
		"an excluded word":                     {shopify("Used Trail Running Shoes", 8200, 8200, "USD"), nil, PlannedObservationExcludedV3},
		"a required word is missing":           {shopify("Road Running Shoes", 8200, 8200, "USD"), nil, PlannedObservationTermMissingV3},
		"above the budget":                     {shopify("Trail Running Shoes", 9100, 9100, "USD"), nil, PlannedObservationPriceOutOfRangeV3},
		"below the floor":                      {shopify("Trail Running Shoes", 500, 1500, "USD"), nil, PlannedObservationPriceOutOfRangeV3},
		"one of several prices fits":           {shopify("Trail Running Shoes", 1500, 2500, "USD"), nil, ""},
		"an unread price is not zero":          {shopify("Trail Running Shoes", 0, 0, ""), nil, PlannedObservationPriceUnknownV3},
		"another currency without a rate":      {shopify("Trail Running Shoes", 60000, 60000, "KRW"), nil, PlannedObservationPriceForeignV3},
		"another currency with the day's rate": {shopify("Trail Running Shoes", 60000, 60000, "KRW"), toUSD, ""},
		"Amazon observed":                      {amazon("OBSERVED", 8200), nil, ""},
		"Amazon without a price":               {amazon("UNKNOWN", 0), nil, PlannedObservationPriceUnknownV3},
	} {
		if got := AdmitPlannedObservationV3(plan, test.product, test.convert); got != test.want {
			t.Fatalf("%s: got %q want %q", name, got, test.want)
		}
	}
	// Without a price bound an unknown price decides nothing.
	unbounded, err := researchdomain.NewSearchPlan(researchdomain.NewSearchPlanInput{
		TargetID: "target-1", Limit: 8,
		Queries:  []researchdomain.SearchPlanQuery{{Language: researchdomain.SearchLanguageEnglish, Text: "trail running shoes"}},
		Required: researchdomain.SearchPlanRequired{Market: market},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := AdmitPlannedObservationV3(unbounded, amazon("UNKNOWN", 0), nil); got != "" {
		t.Fatalf("no bound, no price rule: %q", got)
	}
}

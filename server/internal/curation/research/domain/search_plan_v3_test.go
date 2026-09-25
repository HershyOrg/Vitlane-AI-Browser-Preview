package domain

import (
	"testing"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func searchPlanMarket(t *testing.T, country, currency string) shareddomain.MarketContext {
	t.Helper()
	market, err := shareddomain.NewMarketContext(country, currency)
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	return market
}

// The plan prepares phrases per language, keeps their order, and states every
// query rule once for all sources — without a minimum word count.
func TestSearchPlanPreparesPhrasesPerLanguageWithoutAWordMinimum(t *testing.T) {
	t.Parallel()
	maximum := int64(5000)
	plan, err := NewSearchPlan(NewSearchPlanInput{
		TargetID: "target-1", ProductType: "sunscreen", Limit: 8,
		Queries: []SearchPlanQuery{
			{Language: SearchLanguageEnglish, Text: " sunscreen "},
			{Language: SearchLanguageEnglish, Text: "mineral  sunscreen spf 50"},
			{Language: SearchLanguageEnglish, Text: "Sunscreen"},
			{Language: SearchLanguageKorean, Text: "선크림"},
		},
		Required: SearchPlanRequired{
			Market: searchPlanMarket(t, "US", "USD"), MaximumMinor: &maximum,
			Exclusions: []string{"used", " Used ", ""}, Conditions: []string{"new"},
		},
		Preferred: SearchPlanPreferred{Terms: []string{"reef safe"}},
	})
	if err != nil {
		t.Fatalf("a one-word phrase is a complete query: %v", err)
	}
	if got := plan.QueriesFor(SearchLanguageEnglish); len(got) != 2 || got[0] != "sunscreen" || got[1] != "mineral sunscreen spf 50" {
		t.Fatalf("English phrases keep their order, trimmed and without duplicates: %q", got)
	}
	if got := plan.QueriesFor(SearchLanguageKorean); len(got) != 1 || got[0] != "선크림" {
		t.Fatalf("a Korean source reads the Korean phrases only: %q", got)
	}
	if len(plan.Required.Exclusions) != 1 || !plan.HasPriceBound() {
		t.Fatalf("required conditions are normalised once: %+v", plan.Required)
	}
	maximum = 1
	if *plan.Required.MaximumMinor != 5000 {
		t.Fatal("the plan owns its bounds")
	}
}

func TestSearchPlanRejectsWhatNoSourceCouldSearch(t *testing.T) {
	t.Parallel()
	valid := func() NewSearchPlanInput {
		return NewSearchPlanInput{
			TargetID: "target-1", Limit: 8,
			Queries:  []SearchPlanQuery{{Language: SearchLanguageEnglish, Text: "camping chair"}},
			Required: SearchPlanRequired{Market: searchPlanMarket(t, "US", "USD")},
		}
	}
	if _, err := NewSearchPlan(valid()); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
	low, high := int64(10), int64(5)
	for name, mutate := range map[string]func(*NewSearchPlanInput){
		"no phrase": func(in *NewSearchPlanInput) { in.Queries = nil },
		"only blank phrases": func(in *NewSearchPlanInput) {
			in.Queries = []SearchPlanQuery{{Language: SearchLanguageEnglish, Text: "  "}}
		},
		"a Korean phrase as English":   func(in *NewSearchPlanInput) { in.Queries[0].Text = "캠핑 의자" },
		"punctuation only":             func(in *NewSearchPlanInput) { in.Queries[0].Text = "--" },
		"an unknown language":          func(in *NewSearchPlanInput) { in.Queries[0].Language = "ja" },
		"no target":                    func(in *NewSearchPlanInput) { in.TargetID = " " },
		"no limit":                     func(in *NewSearchPlanInput) { in.Limit = 0 },
		"a limit above the provider's": func(in *NewSearchPlanInput) { in.Limit = CatalogSearchPlanMaximumLimit + 1 },
		"crossed price bounds":         func(in *NewSearchPlanInput) { in.Required.MinimumMinor, in.Required.MaximumMinor = &low, &high },
		"no market":                    func(in *NewSearchPlanInput) { in.Required.Market = shareddomain.MarketContext{} },
	} {
		input := valid()
		mutate(&input)
		if _, err := NewSearchPlan(input); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	// More phrases than a Round ever prepares for one language.
	input := valid()
	for _, text := range []string{"a1", "a2", "a3", "a4", "a5"} {
		input.Queries = append(input.Queries, SearchPlanQuery{Language: SearchLanguageEnglish, Text: text})
	}
	if _, err := NewSearchPlan(input); err == nil {
		t.Fatal("six phrases of one language must be rejected")
	}
}

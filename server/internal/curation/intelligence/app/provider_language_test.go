package app

import (
	"strings"
	"testing"
)

func TestProviderLanguagePreservesUSContractAndKoreanMixedQueries(t *testing.T) {
	if providerPlanningPrompt("US") != planningSystemPrompt || providerCatalogQueryPrompt("US") != catalogQuerySystemPrompt {
		t.Fatal("US harness prompt changed")
	}
	if !validProviderCatalogPhrase("KR", "삼성 Galaxy Buds3 Pro") || !validProviderCatalogPhrase("KR", "라미 사파리") || validProviderCatalogPhrase("KR", "\n") || validProviderCatalogPhrase("US", "라미 사파리") {
		t.Fatal("provider query language contract")
	}
	if !strings.Contains(providerPlanningPrompt("KR"), "do not normalize through English") || !strings.Contains(providerCatalogQueryPrompt("KR"), "Do not translate existing Korean through English") {
		t.Fatal("Korean query roundtripped through English")
	}
	kr := PlanningTargetsSchemaForCountry(4, "KR")
	us := PlanningTargetsSchemaForCountry(4, "US")
	query := func(s map[string]any) map[string]any {
		return s["properties"].(map[string]any)["targets"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["searchQuery"].(map[string]any)
	}
	if query(kr)["pattern"] != nil || query(us)["pattern"] == nil {
		t.Fatal("KR schema mutated US schema")
	}
}

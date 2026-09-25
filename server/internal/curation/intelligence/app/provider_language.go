package app

import (
	"strings"
	"unicode"
)

func providerInputLanguage(country string, values ...string) inputLanguagePath {
	if country == "KR" {
		return "KOREAN_CATALOG_V1"
	}
	return classifyInputLanguagePath(values...)
}

func providerPlanningPrompt(country string) string {
	if country != "KR" {
		return planningSystemPrompt
	}
	start := strings.Index(planningSystemPrompt, "- Follow languageHandling")
	end := strings.Index(planningSystemPrompt, "- Do not mention prices")
	return planningSystemPrompt[:start] + `- languageHandling=KOREAN_CATALOG_V1 selects Korean-market search providers.
- searchQuery is a short Korean retail product phrase. Keep brand names,
  model numbers and existing Korean terms intact. Normalize directly to the
  provider's Korean query language; do not normalize through English first.
` + planningSystemPrompt[end:]
}

func providerCatalogQueryPrompt(country string) string {
	if country != "KR" {
		return catalogQuerySystemPrompt
	}
	return `You turn one shopping item into a product catalog search query.
Rules:
- languageHandling=KOREAN_CATALOG_V1 selects Korean-market search providers.
- Use a Korean retail query directly from the supplied request and feedback.
  Keep brand names and model numbers intact; Korean/Latin mixtures are valid.
  Do not translate existing Korean through English.
- productVertical names the product family so the right Korean malls are asked:
  FASHION (clothing, shoes, bags), BEAUTY (cosmetics, skincare), FOOD (groceries,
  drinks), LIVING (home, kitchen, stationery, daily goods), ELECTRONICS, or
  GENERAL when none fits.
- query contains product words only: no prices, no seller names, no adjectives
  about availability. Use the common retail name for the product.
- mustInclude holds preferred features distilled from the user's request or
  feedback. They guide ranking; subjective wishes (cheaper, nicer) belong in
  query wording, not mustInclude.
- mustExclude holds features the user rejected.
- Never invent constraints the item does not state.`
}

func validProviderCatalogPhrase(country, value string) bool {
	if country != "KR" {
		return isEnglishCatalogPhrase(value)
	}
	if strings.TrimSpace(value) == "" || len(value) > 2000 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func PlanningTargetsSchemaForCountry(maximum int, country string) map[string]any {
	schema := PlanningTargetsSchema(maximum)
	if country == "KR" {
		query := schema["properties"].(map[string]any)["targets"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["searchQuery"].(map[string]any)
		delete(query, "pattern")
		query["description"] = "Korean-market retail search phrase. Preserve brand/model tokens; Korean and Latin mixtures are valid."
		query["maxLength"] = 500
	}
	return schema
}

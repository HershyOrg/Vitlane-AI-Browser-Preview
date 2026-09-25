package app

import (
	"strings"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const researchSearchPlanInvalidV3 = "RESEARCH_SEARCH_PLAN_INVALID"

// searchLanguageForMarketV3 is the language a market's catalogs are indexed in.
// Korean malls are asked in Korean; every other market's sources in English.
func searchLanguageForMarketV3(country string) researchdomain.SearchLanguage {
	if strings.EqualFold(strings.TrimSpace(country), "KR") {
		return researchdomain.SearchLanguageKorean
	}
	return researchdomain.SearchLanguageEnglish
}

// BuildSearchPlanV3 builds the one search plan of a Round from what the Round
// already decided: the phrases the query step wrote for this market, the
// criteria's product type, the market and the budget's price bounds. It runs
// once, before any source is asked, and it is the only place a search rule is
// stated. Sources translate the plan; they never build a query themselves.
func BuildSearchPlanV3(
	input CatalogWorkspaceSearchInputV2,
	profile CatalogTargetSearchProfileV2,
) (researchdomain.SearchPlan, error) {
	market, err := shareddomain.NewMarketContext(profile.Market.Country, profile.Market.Currency)
	if err != nil {
		return researchdomain.SearchPlan{}, fault.Wrap(
			err, fault.InvalidInput, "PHASE8_TARGET_SEARCH_PROFILE_INVALID", false,
		)
	}
	language := input.SearchLanguage
	if !language.Valid() {
		language = searchLanguageForMarketV3(profile.Market.Country)
	}
	// The Round's own phrase is the primary one: the worker sets the profile's
	// intent to what the query step wrote for this market. A request-side query
	// stands in only when the profile has none, or when a Target written in
	// Korean is searched in an English market and the query step normalised it.
	primary := strings.TrimSpace(profile.NormalizedIntent)
	normalised := strings.TrimSpace(input.Search.Query)
	// A cumulative (append) Round searches its own saved phrase, whatever the
	// request carries. Any other search states the phrase it asks for.
	if primary == "" || (input.Mode != CatalogResearchAppendV2 && normalised != "") ||
		(language == researchdomain.SearchLanguageEnglish &&
			containsNonLatinCatalogLetter(primary) && normalised != "") {
		primary = normalised
	}
	if primary == "" || len(primary) > 2000 {
		return researchdomain.SearchPlan{}, fault.New(fault.InvalidInput, "CATALOG_QUERY_INVALID", false)
	}
	// An English catalog cannot be searched with a Korean phrase. The query step
	// normalises for the Round's market; a phrase that did not is not sent.
	if language == researchdomain.SearchLanguageEnglish && containsNonLatinCatalogLetter(primary) {
		return researchdomain.SearchPlan{}, fault.New(
			fault.InvalidInput, "RESEARCH_INPUT_NORMALIZATION_REQUIRED", false,
		)
	}
	queries := []researchdomain.SearchPlanQuery{{Language: language, Text: primary}}
	for _, seed := range input.QuerySeeds {
		seed = strings.TrimSpace(seed)
		// A seed in the wrong script finds nothing; it is left out rather than
		// failing a Round whose primary phrase is fine.
		if seed == "" || len(seed) > researchdomain.SearchPlanMaximumQueryLength ||
			(language == researchdomain.SearchLanguageEnglish && containsNonLatinCatalogLetter(seed)) ||
			len(queries) >= researchdomain.SearchPlanMaximumQueries {
			continue
		}
		queries = append(queries, researchdomain.SearchPlanQuery{Language: language, Text: seed})
	}
	exponent := workspaceCurrencyExponentV2(profile.Market.Currency)
	minimum, err := catalogProfileMinorBoundV2(profile.MinimumPrice, exponent)
	if err != nil {
		return researchdomain.SearchPlan{}, err
	}
	maximum, err := catalogProfileMinorBoundV2(profile.MaximumPrice, exponent)
	if err != nil {
		return researchdomain.SearchPlan{}, err
	}
	productType := ""
	if input.Criteria != nil {
		productType = input.Criteria.Subject.ProductType
	}
	limit := input.Search.Limit
	if limit == 0 {
		limit = 8
	}
	plan, err := researchdomain.NewSearchPlan(researchdomain.NewSearchPlanInput{
		TargetID:    input.TargetID,
		ProductType: productType,
		Queries:     queries,
		Required: researchdomain.SearchPlanRequired{
			Market: market, MinimumMinor: minimum, MaximumMinor: maximum,
			Exclusions:  input.Search.Exclusions,
			MustContain: input.Search.HardLexicalTerms,
			Conditions:  []string{"new"},
		},
		Preferred: researchdomain.SearchPlanPreferred{
			Terms:           input.Search.PreferredTerms,
			Exclusions:      input.Search.PreferredExclusions,
			ProductVertical: input.ProductVertical,
			Categories:      catalogVerifiedCategoriesV2(profile.Category),
		},
		Limit: limit,
	})
	if err != nil {
		return researchdomain.SearchPlan{}, fault.Wrap(
			err, fault.InvalidInput, researchSearchPlanInvalidV3, false,
		)
	}
	return plan, nil
}

// PlannedObservationRejectionV3 says which required condition a product failed.
type PlannedObservationRejectionV3 string

const (
	PlannedObservationExcludedV3        PlannedObservationRejectionV3 = "EXCLUSION_MATCHED"
	PlannedObservationTermMissingV3     PlannedObservationRejectionV3 = "REQUIRED_TERM_MISSING"
	PlannedObservationPriceUnknownV3    PlannedObservationRejectionV3 = "PRICE_UNKNOWN"
	PlannedObservationPriceForeignV3    PlannedObservationRejectionV3 = "PRICE_NOT_COMPARABLE"
	PlannedObservationPriceOutOfRangeV3 PlannedObservationRejectionV3 = "PRICE_OUT_OF_RANGE"
)

// PlannedPriceConverterV3 converts a minor amount into the plan's market
// currency, or reports that it cannot (no rate for the day).
type PlannedPriceConverterV3 func(amountMinor int64, currency string) (int64, bool)

// plannedObservedPriceV3 reads the price range a normalised observation states,
// whichever source wrote it. known is false when the source saw no price; an
// absent price is never read as zero.
func plannedObservedPriceV3(product CatalogProductObservation) (low, high int64, currency string, known bool) {
	switch {
	case product.ExternalObservation != nil:
		price := product.ExternalObservation.Price
		if price.Kind != "OBSERVED" || price.AmountMinor == nil {
			return 0, 0, "", false
		}
		return *price.AmountMinor, *price.AmountMinor, price.Currency, true
	case product.Source() == researchdomain.SourceAmazon:
		if product.VariantObservation == nil || product.VariantObservation.Price.Kind != "OBSERVED" ||
			product.VariantObservation.Price.AmountMinor == nil {
			return 0, 0, "", false
		}
		price := product.VariantObservation.Price
		return *price.AmountMinor, *price.AmountMinor, price.Currency, true
	default:
		minimum, maximum := product.PriceRange.Minimum, product.PriceRange.Maximum
		if minimum.Currency == "" {
			return 0, 0, "", false
		}
		if maximum.Currency != minimum.Currency || maximum.AmountMinor < minimum.AmountMinor {
			maximum = minimum
		}
		return minimum.AmountMinor, maximum.AmountMinor, minimum.Currency, true
	}
}

// AdmitPlannedObservationV3 holds one normalised observation to the plan's
// required conditions. Every source's results pass through it, so a source that
// cannot filter by price or by wording is held to the same conditions as one
// that can. It returns "" for a product that meets them all.
func AdmitPlannedObservationV3(
	plan researchdomain.SearchPlan,
	product CatalogProductObservation,
	convert PlannedPriceConverterV3,
) PlannedObservationRejectionV3 {
	fields := candidateRankingLexicalFieldsV2(product)
	for _, term := range plan.Required.Exclusions {
		if candidateRankingFieldsContainV2(fields, term) {
			return PlannedObservationExcludedV3
		}
	}
	for _, term := range plan.Required.MustContain {
		if !candidateRankingFieldsContainV2(fields, term) {
			return PlannedObservationTermMissingV3
		}
	}
	if !plan.HasPriceBound() {
		return ""
	}
	low, high, currency, known := plannedObservedPriceV3(product)
	if !known {
		return PlannedObservationPriceUnknownV3
	}
	if currency != string(plan.Required.Market.Currency) {
		if convert == nil {
			return PlannedObservationPriceForeignV3
		}
		var lowOK, highOK bool
		low, lowOK = convert(low, currency)
		high, highOK = convert(high, currency)
		if !lowOK || !highOK {
			return PlannedObservationPriceForeignV3
		}
	}
	// A product with several prices fits when any of them does.
	if (plan.Required.MinimumMinor != nil && high < *plan.Required.MinimumMinor) ||
		(plan.Required.MaximumMinor != nil && low > *plan.Required.MaximumMinor) {
		return PlannedObservationPriceOutOfRangeV3
	}
	return ""
}

package app

import (
	"math/big"
	"slices"
	"strings"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const catalogSearchPlanCompileInvalidV2 = "CATALOG_SEARCH_PLAN_COMPILE_INVALID"

// CatalogSearchCapabilityV2 is a reviewed snapshot of the provider's live
// catalog capability. Its fingerprint and protocol version are copied into
// the immutable request envelope; the translation never guesses an absent
// currency exponent or a provider maximum.
type CatalogSearchCapabilityV2 struct {
	ProtocolVersion   string
	SchemaFingerprint string
	MaximumLimit      int
	CurrencyExponents []CatalogCurrencyExponentV2
}

type CatalogCurrencyExponentV2 struct {
	Currency string
	Exponent uint8
}

// TranslateCatalogSearchPlanV2Input is what the Shopify catalog adapter needs
// to turn the Round's SearchPlan into its own request: which of the plan's
// English phrases to ask now and from which page (the adapter's own progress),
// and its proof-side view of the plan's conditions.
type TranslateCatalogSearchPlanV2Input struct {
	PlanID     string
	Plan       researchdomain.SearchPlan
	Query      string
	Cursor     string
	Intent     researchdomain.TargetSearchIntentV2
	Capability CatalogSearchCapabilityV2
}

// TranslateCatalogSearchPlanV2 is the Shopify catalog's translation of a
// SearchPlan. It chooses no phrase and states no condition of its own: the
// query is one of the plan's English phrases, the filters are the plan's
// required conditions in the provider's vocabulary (price in minor units,
// ships-to, availability, condition, a reviewed category), and the page is the
// adapter's position in that phrase. The provider's one length rule, at most
// twelve words, is kept by cutting the phrase, never by failing the Round.
func TranslateCatalogSearchPlanV2(
	input TranslateCatalogSearchPlanV2Input,
) (researchdomain.CatalogSearchPlanV2, error) {
	if err := input.Plan.Validate(); err != nil {
		return researchdomain.CatalogSearchPlanV2{}, fault.Wrap(
			err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
	}
	if err := input.Intent.Validate(); err != nil {
		return researchdomain.CatalogSearchPlanV2{}, fault.Wrap(
			err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
	}
	if err := validateCatalogCapabilityV2(input.Capability); err != nil {
		return researchdomain.CatalogSearchPlanV2{}, err
	}
	if input.Plan.Limit > input.Capability.MaximumLimit ||
		input.Intent.MarketContext != input.Plan.Required.Market {
		return researchdomain.CatalogSearchPlanV2{}, fault.New(
			fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
	}
	query := strings.TrimSpace(input.Query)
	if !slices.Contains(input.Plan.QueriesFor(researchdomain.SearchLanguageEnglish), query) {
		// The adapter may only ask a phrase the plan prepared for its language.
		return researchdomain.CatalogSearchPlanV2{}, fault.New(
			fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
	}
	words := strings.Fields(query)
	if len(words) > researchdomain.CatalogSearchQueryMaximumWords {
		words = words[:researchdomain.CatalogSearchQueryMaximumWords]
	}
	price, err := compileCatalogPriceV2(input.Intent, input.Capability)
	if err != nil {
		return researchdomain.CatalogSearchPlanV2{}, err
	}
	request := researchdomain.CatalogSearchRequestV2{
		Cursor: strings.TrimSpace(input.Cursor),
		Query:  strings.Join(words, " "),
		Context: researchdomain.CatalogSearchContextV2{
			AddressCountry: string(input.Plan.Required.Market.Country),
			Currency:       string(input.Plan.Required.Market.Currency),
			Language:       researchdomain.CatalogSearchLanguageEnglish,
			Intent:         strings.TrimSpace(input.Intent.IntentSummaryEnglish),
		},
		Filters:         compileCatalogFiltersV2(input.Intent, price),
		PostFilters:     compileCatalogPostFiltersV2(input.Intent),
		RankPreferences: compileCatalogRankPreferencesV2(input.Intent),
		Limit:           input.Plan.Limit,
	}
	plan, err := researchdomain.NewCatalogSearchPlanV2(
		researchdomain.NewCatalogSearchPlanV2Input{
			PlanID: strings.TrimSpace(input.PlanID), TargetID: input.Plan.TargetID,
			SourceProfileHash:           strings.TrimSpace(input.Intent.SourceProfileHash),
			CompilerPolicyVersion:       researchdomain.SearchPlanSchemaV3,
			ProviderProtocolVersion:     strings.TrimSpace(input.Capability.ProtocolVersion),
			CapabilitySchemaFingerprint: strings.TrimSpace(input.Capability.SchemaFingerprint),
			Requests:                    []researchdomain.CatalogSearchRequestV2{request},
		},
	)
	if err != nil {
		return researchdomain.CatalogSearchPlanV2{}, fault.Wrap(
			err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
	}
	return plan, nil
}

func validateCatalogCapabilityV2(capability CatalogSearchCapabilityV2) error {
	if strings.TrimSpace(capability.ProtocolVersion) == "" ||
		strings.TrimSpace(capability.SchemaFingerprint) == "" ||
		capability.MaximumLimit <= 0 ||
		capability.MaximumLimit > researchdomain.CatalogSearchPlanMaximumLimit {
		return fault.New(
			fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
		)
	}
	seen := make(map[string]struct{}, len(capability.CurrencyExponents))
	for _, currency := range capability.CurrencyExponents {
		code := strings.TrimSpace(currency.Currency)
		if len(code) != 3 || code != strings.ToUpper(code) || currency.Exponent > 6 {
			return fault.New(
				fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
			)
		}
		if _, exists := seen[code]; exists {
			return fault.New(
				fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
			)
		}
		seen[code] = struct{}{}
	}
	return nil
}

func compileCatalogPriceV2(
	intent researchdomain.TargetSearchIntentV2,
	capability CatalogSearchCapabilityV2,
) (*researchdomain.CatalogSearchPriceV2, error) {
	if intent.PriceConstraint.Kind == shareddomain.ResearchPriceConstraintNone {
		return nil, nil
	}
	currency := string(intent.MarketContext.Currency)
	exponent, exists := catalogCurrencyExponentV2(capability, currency)
	if !exists {
		return nil, fault.New(
			fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
		)
	}
	price := &researchdomain.CatalogSearchPriceV2{Currency: currency}
	var err error
	if intent.PriceConstraint.Min != nil {
		price.MinimumMinor, err = catalogMinorUnitsV2(*intent.PriceConstraint.Min, exponent)
		if err != nil {
			return nil, err
		}
	}
	if intent.PriceConstraint.Max != nil {
		price.MaximumMinor, err = catalogMinorUnitsV2(*intent.PriceConstraint.Max, exponent)
		if err != nil {
			return nil, err
		}
	}
	return price, nil
}

func catalogCurrencyExponentV2(
	capability CatalogSearchCapabilityV2,
	currency string,
) (uint8, bool) {
	for _, entry := range capability.CurrencyExponents {
		if entry.Currency == currency {
			return entry.Exponent, true
		}
	}
	return 0, false
}

func catalogMinorUnitsV2(money shareddomain.Money, exponent uint8) (*int64, error) {
	amount, ok := new(big.Rat).SetString(money.Amount)
	if !ok || amount.Sign() < 0 {
		return nil, fault.New(fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
	scaled := new(big.Rat).Mul(amount, new(big.Rat).SetInt(scale))
	if scaled.Denom().Cmp(big.NewInt(1)) != 0 || !scaled.Num().IsInt64() {
		return nil, fault.New(fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false)
	}
	minor := scaled.Num().Int64()
	return &minor, nil
}

func compileCatalogFiltersV2(
	intent researchdomain.TargetSearchIntentV2,
	price *researchdomain.CatalogSearchPriceV2,
) researchdomain.CatalogSearchFiltersV2 {
	attributes := make([]researchdomain.CatalogSearchAttributeV2, 0, 3)
	if len(intent.Colors) > 0 {
		attributes = append(attributes, researchdomain.CatalogSearchAttributeV2{
			Name: "Color", Values: cleanCompilerValuesV2(intent.Colors, false),
		})
	}
	if len(intent.Sizes) > 0 {
		values := make([]string, 0, len(intent.Sizes))
		for _, size := range intent.Sizes {
			value := strings.TrimSpace(size.Value)
			if system := strings.TrimSpace(size.SizingSystem); system != "" {
				value = system + " " + value
			}
			values = append(values, value)
		}
		attributes = append(attributes, researchdomain.CatalogSearchAttributeV2{
			Name: "Size", Values: cleanCompilerValuesV2(values, false),
		})
	}
	if len(intent.TargetGenders) > 0 {
		attributes = append(attributes, researchdomain.CatalogSearchAttributeV2{
			Name:   "Target gender",
			Values: cleanCompilerValuesV2(intent.TargetGenders, false),
		})
	}
	return researchdomain.CatalogSearchFiltersV2{
		Available: true,
		ShipsTo: researchdomain.CatalogSearchDestinationV2{
			Country: string(intent.ShippingCountry),
		},
		Categories: cleanCompilerValuesV2(intent.VerifiedCategories, false),
		Conditions: cleanCompilerValuesV2(intent.Conditions, true),
		Attributes: attributes,
		Price:      cloneDomainSearchPriceV2(price),
	}
}

func compileCatalogPostFiltersV2(
	intent researchdomain.TargetSearchIntentV2,
) []string {
	filters := make([]string, 0, len(intent.HardLexicalTerms)+len(intent.Exclusions))
	for _, requirement := range intent.HardLexicalTerms {
		filters = append(filters, "require "+strings.TrimSpace(requirement))
	}
	for _, exclusion := range intent.Exclusions {
		filters = append(filters, "exclude "+strings.TrimSpace(exclusion))
	}
	return cleanCompilerValuesV2(filters, false)
}

func compileCatalogRankPreferencesV2(
	intent researchdomain.TargetSearchIntentV2,
) []string {
	preferences := slices.Clone(intent.SoftLexicalTerms)
	if strings.TrimSpace(intent.PriceTierPreference) != "" {
		preferences = append(
			preferences, "price tier "+strings.TrimSpace(intent.PriceTierPreference),
		)
	}
	return cleanCompilerValuesV2(preferences, false)
}

func cleanCompilerValuesV2(values []string, lower bool) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func cloneDomainSearchPriceV2(
	price *researchdomain.CatalogSearchPriceV2,
) *researchdomain.CatalogSearchPriceV2 {
	if price == nil {
		return nil
	}
	cloned := *price
	cloned.MinimumMinor = cloneSearchMinorV2(price.MinimumMinor)
	cloned.MaximumMinor = cloneSearchMinorV2(price.MaximumMinor)
	return &cloned
}

func cloneDomainSearchFiltersV2(
	filters researchdomain.CatalogSearchFiltersV2,
) researchdomain.CatalogSearchFiltersV2 {
	cloned := filters
	cloned.Categories = slices.Clone(filters.Categories)
	cloned.Conditions = slices.Clone(filters.Conditions)
	cloned.Attributes = make(
		[]researchdomain.CatalogSearchAttributeV2, len(filters.Attributes),
	)
	for index, attribute := range filters.Attributes {
		cloned.Attributes[index] = researchdomain.CatalogSearchAttributeV2{
			Name: attribute.Name, Values: slices.Clone(attribute.Values),
		}
	}
	cloned.Price = cloneDomainSearchPriceV2(filters.Price)
	return cloned
}

func cloneSearchMinorV2(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

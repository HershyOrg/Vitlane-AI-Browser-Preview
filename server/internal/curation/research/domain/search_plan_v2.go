package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const (
	CatalogSearchPlanSchemaV2      = "vitlane.catalog-search-plan.v2"
	CatalogSearchPlanMaximumLimit  = 50
	CatalogSearchQueryMaximumWords = 12
	CatalogSearchLanguageEnglish   = "en"
)

var (
	ErrTargetSearchIntentV2Invalid = errors.New("TARGET_SEARCH_INTENT_V2_INVALID")
	ErrCatalogSearchPlanV2Invalid  = errors.New("CATALOG_SEARCH_PLAN_V2_INVALID")
)

// TargetSearchIntentV2 is the Shopify catalog adapter's English-only view of a
// SearchPlan: what its request filters were built from and what the provider's
// applied-filter proof is checked against. It states no query and no word rule;
// the phrases belong to the SearchPlan. SourceProfileHash binds these fields to
// the validated TargetRequirementProfile without importing another product's
// domain model into Research.
type TargetSearchIntentV2 struct {
	ProductType          string                               `json:"productType"`
	UseCaseTerms         []string                             `json:"useCaseTerms"`
	BrandTerms           []string                             `json:"brandTerms"`
	ModelTerms           []string                             `json:"modelTerms"`
	MaterialTerms        []string                             `json:"materialTerms"`
	StyleTerms           []string                             `json:"styleTerms"`
	HardLexicalTerms     []string                             `json:"hardLexicalTerms"`
	SoftLexicalTerms     []string                             `json:"softLexicalTerms"`
	Exclusions           []string                             `json:"exclusions"`
	Colors               []string                             `json:"colors"`
	Sizes                []CatalogSearchSizeV2                `json:"sizes"`
	TargetGenders        []string                             `json:"targetGenders"`
	Conditions           []string                             `json:"conditions"`
	VerifiedCategories   []string                             `json:"verifiedCategories"`
	PriceTierPreference  string                               `json:"priceTierPreference,omitempty"`
	PriceConstraint      shareddomain.ResearchPriceConstraint `json:"priceConstraint"`
	MarketContext        shareddomain.MarketContext           `json:"marketContext"`
	ShippingCountry      shareddomain.CountryCode             `json:"shippingCountry"`
	IntentSummaryEnglish string                               `json:"intentSummaryEnglish"`
	SourceProfileHash    string                               `json:"sourceProfileHash"`
}

type CatalogSearchSizeV2 struct {
	Value        string `json:"value"`
	SizingSystem string `json:"sizingSystem,omitempty"`
}

func (intent TargetSearchIntentV2) Validate() error {
	if strings.TrimSpace(intent.ProductType) == "" ||
		strings.TrimSpace(intent.IntentSummaryEnglish) == "" ||
		strings.TrimSpace(intent.SourceProfileHash) == "" ||
		intent.ShippingCountry != intent.MarketContext.Country {
		return ErrTargetSearchIntentV2Invalid
	}
	if err := intent.PriceConstraint.ValidateForMarket(intent.MarketContext); err != nil {
		return fmt.Errorf("%w: %v", ErrTargetSearchIntentV2Invalid, err)
	}
	semanticFields := [][]string{
		{intent.ProductType}, intent.UseCaseTerms, intent.BrandTerms,
		intent.ModelTerms, intent.MaterialTerms, intent.StyleTerms,
		intent.HardLexicalTerms, intent.SoftLexicalTerms, intent.Exclusions,
		intent.Colors, intent.TargetGenders, intent.Conditions,
		{intent.IntentSummaryEnglish},
	}
	if intent.PriceTierPreference != "" {
		semanticFields = append(semanticFields, []string{intent.PriceTierPreference})
	}
	for _, values := range semanticFields {
		if !uniqueTrimmedFoldV2(values) {
			return ErrTargetSearchIntentV2Invalid
		}
		for _, value := range values {
			if !isEnglishSemanticV2(value) {
				return ErrTargetSearchIntentV2Invalid
			}
		}
	}
	if !uniqueTrimmedFoldV2(intent.VerifiedCategories) {
		return ErrTargetSearchIntentV2Invalid
	}
	for _, category := range intent.VerifiedCategories {
		// A reviewed provider taxonomy may use an opaque numeric identifier.
		// It is still deterministic filter data, not user-language query text.
		if !isEnglishOrNumericSemanticV2(category) {
			return ErrTargetSearchIntentV2Invalid
		}
	}
	if !uniqueSearchSizesV2(intent.Sizes) {
		return ErrTargetSearchIntentV2Invalid
	}
	for _, size := range intent.Sizes {
		if !isEnglishOrNumericSemanticV2(size.Value) ||
			(size.SizingSystem != "" && !isEnglishSemanticV2(size.SizingSystem)) {
			return ErrTargetSearchIntentV2Invalid
		}
	}
	return nil
}

type CatalogSearchContextV2 struct {
	AddressCountry string `json:"addressCountry"`
	Currency       string `json:"currency"`
	Language       string `json:"language"`
	Intent         string `json:"intent"`
}

type CatalogSearchDestinationV2 struct {
	Country string `json:"country"`
}

type CatalogSearchAttributeV2 struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// CatalogSearchPriceV2 is optional at the request level. Non-nil zero bounds
// are explicit values and must never be collapsed into NONE.
type CatalogSearchPriceV2 struct {
	MinimumMinor *int64 `json:"minimumMinor,omitempty"`
	MaximumMinor *int64 `json:"maximumMinor,omitempty"`
	Currency     string `json:"currency"`
}

type CatalogSearchFiltersV2 struct {
	Available  bool                       `json:"available"`
	ShipsTo    CatalogSearchDestinationV2 `json:"shipsTo"`
	Categories []string                   `json:"categories"`
	Conditions []string                   `json:"conditions"`
	Attributes []CatalogSearchAttributeV2 `json:"attributes"`
	Price      *CatalogSearchPriceV2      `json:"price,omitempty"`
}

// CatalogSearchRequestV2 is the Shopify catalog's request format: one phrase of
// the SearchPlan, one page, and the plan's required conditions as the filters
// this provider can apply.
type CatalogSearchRequestV2 struct {
	Cursor          string                 `json:"cursor,omitempty"`
	Query           string                 `json:"query"`
	Context         CatalogSearchContextV2 `json:"context"`
	Filters         CatalogSearchFiltersV2 `json:"filters"`
	PostFilters     []string               `json:"postFilters"`
	RankPreferences []string               `json:"rankPreferences"`
	Limit           int                    `json:"limit"`
}

// CatalogSearchPlanV2 is the immutable envelope of that one request: what was
// asked, of which provider protocol, bound by a content hash the observation
// evidence refers to. It is a translation of a SearchPlan, never a plan of its
// own — it chooses no phrase and adds no condition.
type CatalogSearchPlanV2 struct {
	SchemaVersion               string                   `json:"schemaVersion"`
	PlanID                      string                   `json:"planId"`
	TargetID                    string                   `json:"targetId"`
	SourceProfileHash           string                   `json:"sourceProfileHash"`
	CompilerPolicyVersion       string                   `json:"compilerPolicyVersion"`
	ProviderProtocolVersion     string                   `json:"providerProtocolVersion"`
	CapabilitySchemaFingerprint string                   `json:"capabilitySchemaFingerprint"`
	Requests                    []CatalogSearchRequestV2 `json:"requests"`
	ContentHash                 string                   `json:"contentHash"`
}

type NewCatalogSearchPlanV2Input struct {
	PlanID                      string
	TargetID                    string
	SourceProfileHash           string
	CompilerPolicyVersion       string
	ProviderProtocolVersion     string
	CapabilitySchemaFingerprint string
	Requests                    []CatalogSearchRequestV2
}

func NewCatalogSearchPlanV2(
	input NewCatalogSearchPlanV2Input,
) (CatalogSearchPlanV2, error) {
	plan := CatalogSearchPlanV2{
		SchemaVersion:               CatalogSearchPlanSchemaV2,
		PlanID:                      strings.TrimSpace(input.PlanID),
		TargetID:                    strings.TrimSpace(input.TargetID),
		SourceProfileHash:           strings.TrimSpace(input.SourceProfileHash),
		CompilerPolicyVersion:       strings.TrimSpace(input.CompilerPolicyVersion),
		ProviderProtocolVersion:     strings.TrimSpace(input.ProviderProtocolVersion),
		CapabilitySchemaFingerprint: strings.TrimSpace(input.CapabilitySchemaFingerprint),
		Requests:                    cloneCatalogSearchRequestsV2(input.Requests),
	}
	if err := plan.validateShape(); err != nil {
		return CatalogSearchPlanV2{}, err
	}
	hash, err := plan.calculateHash()
	if err != nil {
		return CatalogSearchPlanV2{}, err
	}
	plan.ContentHash = hash
	return plan, nil
}

func (plan CatalogSearchPlanV2) Validate() error {
	if err := plan.validateShape(); err != nil {
		return err
	}
	hash, err := plan.calculateHash()
	if err != nil {
		return err
	}
	if plan.ContentHash != hash {
		return fmt.Errorf("%w: content hash mismatch", ErrCatalogSearchPlanV2Invalid)
	}
	return nil
}

func (plan CatalogSearchPlanV2) validateShape() error {
	if plan.SchemaVersion != CatalogSearchPlanSchemaV2 ||
		strings.TrimSpace(plan.PlanID) == "" || strings.TrimSpace(plan.TargetID) == "" ||
		strings.TrimSpace(plan.SourceProfileHash) == "" ||
		strings.TrimSpace(plan.CompilerPolicyVersion) == "" ||
		strings.TrimSpace(plan.ProviderProtocolVersion) == "" ||
		strings.TrimSpace(plan.CapabilitySchemaFingerprint) == "" ||
		len(plan.Requests) != 1 {
		return ErrCatalogSearchPlanV2Invalid
	}
	if err := validateCatalogSearchRequestV2(plan.Requests[0]); err != nil {
		return fmt.Errorf("%w: %v", ErrCatalogSearchPlanV2Invalid, err)
	}
	return nil
}

func validateCatalogSearchRequestV2(request CatalogSearchRequestV2) error {
	market, marketErr := shareddomain.NewMarketContext(
		request.Context.AddressCountry,
		request.Context.Currency,
	)
	if request.Limit <= 0 ||
		request.Limit > CatalogSearchPlanMaximumLimit ||
		request.Context.Language != CatalogSearchLanguageEnglish ||
		marketErr != nil ||
		string(market.Country) != request.Context.AddressCountry ||
		string(market.Currency) != request.Context.Currency ||
		!isEnglishSemanticV2(request.Context.Intent) {
		return ErrCatalogSearchPlanV2Invalid
	}
	// One word is a complete query ("sunscreen"). The only length rule is the
	// provider's upper bound, which the translation keeps by cutting, not failing.
	queryWords := strings.Fields(request.Query)
	if len(queryWords) == 0 ||
		len(queryWords) > CatalogSearchQueryMaximumWords ||
		!isEnglishOrNumericSemanticV2(request.Query) {
		return ErrCatalogSearchPlanV2Invalid
	}
	if !request.Filters.Available ||
		request.Filters.ShipsTo.Country != request.Context.AddressCountry {
		return ErrCatalogSearchPlanV2Invalid
	}
	if !validateEnglishListsV2(
		request.Filters.Categories,
		request.Filters.Conditions,
		request.PostFilters,
		request.RankPreferences,
	) {
		return ErrCatalogSearchPlanV2Invalid
	}
	for _, attribute := range request.Filters.Attributes {
		if !isEnglishSemanticV2(attribute.Name) || len(attribute.Values) == 0 ||
			!uniqueTrimmedFoldV2(attribute.Values) {
			return ErrCatalogSearchPlanV2Invalid
		}
		for _, value := range attribute.Values {
			if !isEnglishOrNumericSemanticV2(value) {
				return ErrCatalogSearchPlanV2Invalid
			}
		}
	}
	if request.Filters.Price != nil {
		price := request.Filters.Price
		if price.MinimumMinor == nil && price.MaximumMinor == nil ||
			price.Currency != request.Context.Currency ||
			(price.MinimumMinor != nil && *price.MinimumMinor < 0) ||
			(price.MaximumMinor != nil && *price.MaximumMinor < 0) ||
			(price.MinimumMinor != nil && price.MaximumMinor != nil &&
				*price.MinimumMinor > *price.MaximumMinor) {
			return ErrCatalogSearchPlanV2Invalid
		}
	}
	return nil
}

func (plan CatalogSearchPlanV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion               string                   `json:"schemaVersion"`
		PlanID                      string                   `json:"planId"`
		TargetID                    string                   `json:"targetId"`
		SourceProfileHash           string                   `json:"sourceProfileHash"`
		CompilerPolicyVersion       string                   `json:"compilerPolicyVersion"`
		ProviderProtocolVersion     string                   `json:"providerProtocolVersion"`
		CapabilitySchemaFingerprint string                   `json:"capabilitySchemaFingerprint"`
		Requests                    []CatalogSearchRequestV2 `json:"requests"`
	}{
		SchemaVersion: plan.SchemaVersion, PlanID: plan.PlanID,
		TargetID: plan.TargetID, SourceProfileHash: plan.SourceProfileHash,
		CompilerPolicyVersion:       plan.CompilerPolicyVersion,
		ProviderProtocolVersion:     plan.ProviderProtocolVersion,
		CapabilitySchemaFingerprint: plan.CapabilitySchemaFingerprint,
		Requests:                    plan.Requests,
	})
}

func cloneCatalogSearchRequestsV2(
	requests []CatalogSearchRequestV2,
) []CatalogSearchRequestV2 {
	cloned := make([]CatalogSearchRequestV2, len(requests))
	for index, request := range requests {
		cloned[index] = request
		cloned[index].Filters.Categories = slices.Clone(request.Filters.Categories)
		cloned[index].Filters.Conditions = slices.Clone(request.Filters.Conditions)
		cloned[index].Filters.Attributes = make(
			[]CatalogSearchAttributeV2, len(request.Filters.Attributes),
		)
		for attributeIndex, attribute := range request.Filters.Attributes {
			cloned[index].Filters.Attributes[attributeIndex] = CatalogSearchAttributeV2{
				Name: attribute.Name, Values: slices.Clone(attribute.Values),
			}
		}
		if request.Filters.Price != nil {
			price := *request.Filters.Price
			price.MinimumMinor = cloneInt64V2(price.MinimumMinor)
			price.MaximumMinor = cloneInt64V2(price.MaximumMinor)
			cloned[index].Filters.Price = &price
		}
		cloned[index].PostFilters = slices.Clone(request.PostFilters)
		cloned[index].RankPreferences = slices.Clone(request.RankPreferences)
	}
	return cloned
}

func cloneInt64V2(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validateEnglishListsV2(lists ...[]string) bool {
	for _, values := range lists {
		if !uniqueTrimmedFoldV2(values) {
			return false
		}
		for _, value := range values {
			if !isEnglishOrNumericSemanticV2(value) {
				return false
			}
		}
	}
	return true
}

func uniqueTrimmedFoldV2(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return false
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func uniqueSearchSizesV2(values []CatalogSearchSizeV2) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Value = strings.TrimSpace(value.Value)
		value.SizingSystem = strings.TrimSpace(value.SizingSystem)
		if value.Value == "" {
			return false
		}
		key := strings.ToLower(value.SizingSystem + "\x00" + value.Value)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func isEnglishSemanticV2(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasLatin := false
	for _, current := range value {
		if unicode.IsLetter(current) {
			if !unicode.In(current, unicode.Latin) {
				return false
			}
			hasLatin = true
		}
	}
	return hasLatin
}

func isEnglishOrNumericSemanticV2(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasLatinOrDigit := false
	for _, current := range value {
		if unicode.IsLetter(current) {
			if !unicode.In(current, unicode.Latin) {
				return false
			}
			hasLatinOrDigit = true
		}
		if unicode.IsDigit(current) {
			hasLatinOrDigit = true
		}
	}
	return hasLatinOrDigit
}

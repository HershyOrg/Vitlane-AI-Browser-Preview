package domain

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const TargetRequirementProfileSchemaV1 = "vitlane.target-requirement-profile.v1"

var (
	ErrTargetRequirementProfileInvalid = errors.New(
		"TARGET_REQUIREMENT_PROFILE_INVALID",
	)
	ErrTargetRequirementProfileNotEnglish = errors.New(
		"TARGET_REQUIREMENT_PROFILE_NOT_ENGLISH",
	)
	ErrTargetRequirementProvenanceInvalid = errors.New(
		"TARGET_REQUIREMENT_PROVENANCE_INVALID",
	)
	ErrTargetRequirementInvented = errors.New(
		"TARGET_REQUIREMENT_INVENTED",
	)
)

// MarketContextV2 is the Curation view of the shared market value. Country
// and currency remain independent from the optional price constraint.
type MarketContextV2 = shareddomain.MarketContext

func NewMarketContextV2(country, currency string) (MarketContextV2, error) {
	return shareddomain.NewMarketContext(country, currency)
}

type ConstraintProvenanceOriginV2 string

const (
	ConstraintOriginUserExplicitV2    ConstraintProvenanceOriginV2 = "USER_EXPLICIT"
	ConstraintOriginApprovedProfileV2 ConstraintProvenanceOriginV2 = "APPROVED_PROFILE"
	ConstraintOriginPolicyV2          ConstraintProvenanceOriginV2 = "POLICY"
	ConstraintOriginModelDerivedV2    ConstraintProvenanceOriginV2 = "MODEL_DERIVED"
)

func (o ConstraintProvenanceOriginV2) Valid() bool {
	return o == ConstraintOriginUserExplicitV2 ||
		o == ConstraintOriginApprovedProfileV2 ||
		o == ConstraintOriginPolicyV2 ||
		o == ConstraintOriginModelDerivedV2
}

type ConstraintProvenanceV2 struct {
	Field     string                       `json:"field"`
	Value     string                       `json:"value"`
	Origin    ConstraintProvenanceOriginV2 `json:"origin"`
	SourceRef string                       `json:"sourceRef"`
}

type TargetPriceConstraintSourceV2 string

const (
	TargetPriceConstraintPlanInheritedV2 TargetPriceConstraintSourceV2 = "PLAN_INHERITED"
	TargetPriceConstraintExplicitV2      TargetPriceConstraintSourceV2 = "TARGET_EXPLICIT"
)

type TargetSizeV2 struct {
	Value        string `json:"value"`
	SizingSystem string `json:"sizingSystem,omitempty"`
}

type TargetAttributesV2 struct {
	Colors       []string       `json:"colors"`
	Sizes        []TargetSizeV2 `json:"sizes"`
	TargetGender []string       `json:"targetGender"`
}

type TargetRequirementProfileV2 struct {
	SchemaVersion                    string                               `json:"schemaVersion"`
	ProductType                      string                               `json:"productType"`
	UseCases                         []string                             `json:"useCases"`
	BrandTerms                       []string                             `json:"brandTerms"`
	ModelTerms                       []string                             `json:"modelTerms"`
	Materials                        []string                             `json:"materials"`
	Styles                           []string                             `json:"styles"`
	HardRequirements                 []string                             `json:"hardRequirements"`
	SoftPreferences                  []string                             `json:"softPreferences"`
	Exclusions                       []string                             `json:"exclusions"`
	Attributes                       TargetAttributesV2                   `json:"attributes"`
	Condition                        []string                             `json:"condition"`
	PriceTierPreference              string                               `json:"priceTierPreference,omitempty"`
	EffectiveResearchPriceConstraint shareddomain.ResearchPriceConstraint `json:"effectiveResearchPriceConstraint"`
	PriceConstraintSource            TargetPriceConstraintSourceV2        `json:"priceConstraintSource"`
	PriceConstraintSourceRef         string                               `json:"priceConstraintSourceRef,omitempty"`
	MarketContext                    MarketContextV2                      `json:"marketContext"`
	ShippingCountry                  shareddomain.CountryCode             `json:"shippingCountry"`
	ReferenceURLs                    []string                             `json:"referenceUrls"`
	TaxonomyRef                      string                               `json:"taxonomyRef,omitempty"`
	Provenance                       []ConstraintProvenanceV2             `json:"provenance"`
	ProvenanceSourceCatalogHash      string                               `json:"provenanceSourceCatalogHash"`
	ProfileHash                      string                               `json:"profileHash"`
}

// NewTargetRequirementProfileV2Input contains model-normalized semantic
// fields but not an effective numeric price. The constructor derives price
// and market facts from trusted Plan/explicit-setting inputs.
type NewTargetRequirementProfileV2Input struct {
	ProductType                   string
	UseCases                      []string
	BrandTerms                    []string
	ModelTerms                    []string
	Materials                     []string
	Styles                        []string
	HardRequirements              []string
	SoftPreferences               []string
	Exclusions                    []string
	Attributes                    TargetAttributesV2
	Condition                     []string
	PriceTierPreference           string
	MarketContext                 MarketContextV2
	MarketContextSourceRef        string
	PlanPriceConstraint           shareddomain.ResearchPriceConstraint
	PlanPriceSourceRef            string
	TargetExplicitPriceConstraint *shareddomain.ResearchPriceConstraint
	TargetExplicitPriceSourceRef  string
	ReferenceURLs                 []string
	TaxonomyRef                   string
	Provenance                    []ConstraintProvenanceV2
	TrustedProvenanceSourceRefs   []string
}

func NewTargetRequirementProfileV2(
	input NewTargetRequirementProfileV2Input,
) (TargetRequirementProfileV2, error) {
	effectivePrice := input.PlanPriceConstraint.Clone()
	priceSource := TargetPriceConstraintPlanInheritedV2
	priceSourceRef := strings.TrimSpace(input.PlanPriceSourceRef)
	if input.TargetExplicitPriceConstraint != nil {
		effectivePrice = input.TargetExplicitPriceConstraint.Clone()
		priceSource = TargetPriceConstraintExplicitV2
		priceSourceRef = strings.TrimSpace(input.TargetExplicitPriceSourceRef)
	}
	profile := TargetRequirementProfileV2{
		SchemaVersion:                    TargetRequirementProfileSchemaV1,
		ProductType:                      strings.TrimSpace(input.ProductType),
		UseCases:                         cleanCatalogTargetValues(input.UseCases),
		BrandTerms:                       cleanCatalogTargetValues(input.BrandTerms),
		ModelTerms:                       cleanCatalogTargetValues(input.ModelTerms),
		Materials:                        cleanCatalogTargetValues(input.Materials),
		Styles:                           cleanCatalogTargetValues(input.Styles),
		HardRequirements:                 cleanCatalogTargetValues(input.HardRequirements),
		SoftPreferences:                  cleanCatalogTargetValues(input.SoftPreferences),
		Exclusions:                       cleanCatalogTargetValues(input.Exclusions),
		Attributes:                       cloneCatalogAttributes(input.Attributes),
		Condition:                        cleanCatalogTargetValues(input.Condition),
		PriceTierPreference:              strings.TrimSpace(input.PriceTierPreference),
		EffectiveResearchPriceConstraint: effectivePrice,
		PriceConstraintSource:            priceSource,
		PriceConstraintSourceRef:         priceSourceRef,
		MarketContext:                    input.MarketContext,
		ShippingCountry:                  input.MarketContext.Country,
		ReferenceURLs:                    cleanCatalogTargetValues(input.ReferenceURLs),
		TaxonomyRef:                      strings.TrimSpace(input.TaxonomyRef),
		Provenance:                       cloneCatalogProvenance(input.Provenance),
	}
	trustedSourceRefs := cleanCatalogTargetValues(input.TrustedProvenanceSourceRefs)
	trustedSourceRefs = append(
		trustedSourceRefs,
		strings.TrimSpace(input.MarketContextSourceRef),
		strings.TrimSpace(input.PlanPriceSourceRef),
		strings.TrimSpace(input.TargetExplicitPriceSourceRef),
	)
	trustedSourceRefs = cleanCatalogTargetValues(trustedSourceRefs)
	slices.Sort(trustedSourceRefs)
	sourceCatalogHash, err := shareddomain.CanonicalJSONHash(trustedSourceRefs)
	if err != nil {
		return TargetRequirementProfileV2{}, err
	}
	profile.ProvenanceSourceCatalogHash = sourceCatalogHash
	marketSourceRef := strings.TrimSpace(input.MarketContextSourceRef)
	if marketSourceRef != "" {
		profile.Provenance = append(profile.Provenance, ConstraintProvenanceV2{
			Field: "shippingCountry", Value: string(profile.ShippingCountry),
			Origin: ConstraintOriginPolicyV2, SourceRef: marketSourceRef,
		})
	}
	if priceSourceRef != "" &&
		(effectivePrice.Kind == shareddomain.ResearchPriceConstraintExplicit ||
			priceSource == TargetPriceConstraintExplicitV2) {
		value, err := catalogPriceProvenanceValue(effectivePrice)
		if err != nil {
			return TargetRequirementProfileV2{}, err
		}
		profile.Provenance = append(profile.Provenance, ConstraintProvenanceV2{
			Field: "effectiveResearchPriceConstraint", Value: value,
			Origin: ConstraintOriginUserExplicitV2, SourceRef: priceSourceRef,
		})
	}
	slices.SortFunc(profile.Provenance, compareCatalogProvenance)
	if err := validateTrustedCatalogProvenance(
		profile.Provenance, catalogSourceRefSet(trustedSourceRefs),
	); err != nil {
		return TargetRequirementProfileV2{}, err
	}
	if err := profile.validateShape(); err != nil {
		return TargetRequirementProfileV2{}, err
	}
	hash, err := profile.calculateHash()
	if err != nil {
		return TargetRequirementProfileV2{}, err
	}
	profile.ProfileHash = hash
	return profile, nil
}

func (p TargetRequirementProfileV2) Validate() error {
	if err := p.validateShape(); err != nil {
		return err
	}
	hash, err := p.calculateHash()
	if err != nil {
		return err
	}
	if p.ProfileHash != hash {
		return fmt.Errorf(
			"%w: profile hash mismatch",
			ErrTargetRequirementProfileInvalid,
		)
	}
	return nil
}

func (p TargetRequirementProfileV2) validateShape() error {
	if p.SchemaVersion != TargetRequirementProfileSchemaV1 ||
		strings.TrimSpace(p.ProductType) == "" ||
		strings.TrimSpace(p.ProvenanceSourceCatalogHash) == "" {
		return ErrTargetRequirementProfileInvalid
	}
	if err := p.EffectiveResearchPriceConstraint.ValidateForMarket(
		p.MarketContext,
	); err != nil {
		return fmt.Errorf("%w: %v", ErrTargetRequirementProfileInvalid, err)
	}
	if p.ShippingCountry != p.MarketContext.Country {
		return fmt.Errorf(
			"%w: shipping country must equal market country",
			ErrTargetRequirementProfileInvalid,
		)
	}
	if p.PriceConstraintSource != TargetPriceConstraintPlanInheritedV2 &&
		p.PriceConstraintSource != TargetPriceConstraintExplicitV2 {
		return ErrTargetRequirementProfileInvalid
	}
	if p.EffectiveResearchPriceConstraint.Kind ==
		shareddomain.ResearchPriceConstraintExplicit &&
		strings.TrimSpace(p.PriceConstraintSourceRef) == "" {
		return fmt.Errorf(
			"%w: explicit price source is required",
			ErrTargetRequirementProfileInvalid,
		)
	}
	if p.PriceConstraintSource == TargetPriceConstraintExplicitV2 &&
		strings.TrimSpace(p.PriceConstraintSourceRef) == "" {
		return fmt.Errorf(
			"%w: target override source is required",
			ErrTargetRequirementProfileInvalid,
		)
	}
	if p.PriceConstraintSource == TargetPriceConstraintPlanInheritedV2 &&
		p.EffectiveResearchPriceConstraint.Kind ==
			shareddomain.ResearchPriceConstraintNone &&
		strings.TrimSpace(p.PriceConstraintSourceRef) != "" {
		return fmt.Errorf(
			"%w: inherited NONE cannot claim a price source",
			ErrTargetRequirementProfileInvalid,
		)
	}
	for _, condition := range p.Condition {
		if !strings.EqualFold(condition, "NEW") {
			return fmt.Errorf(
				"%w: Phase 8 condition policy is NEW",
				ErrTargetRequirementProfileInvalid,
			)
		}
	}
	for _, referenceURL := range p.ReferenceURLs {
		parsed, err := url.ParseRequestURI(referenceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" {
			return fmt.Errorf(
				"%w: reference URL",
				ErrTargetRequirementProfileInvalid,
			)
		}
	}
	return validateCatalogProfileLanguageAndProvenance(p)
}

func (p TargetRequirementProfileV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion                    string                               `json:"schemaVersion"`
		ProductType                      string                               `json:"productType"`
		UseCases                         []string                             `json:"useCases"`
		BrandTerms                       []string                             `json:"brandTerms"`
		ModelTerms                       []string                             `json:"modelTerms"`
		Materials                        []string                             `json:"materials"`
		Styles                           []string                             `json:"styles"`
		HardRequirements                 []string                             `json:"hardRequirements"`
		SoftPreferences                  []string                             `json:"softPreferences"`
		Exclusions                       []string                             `json:"exclusions"`
		Attributes                       TargetAttributesV2                   `json:"attributes"`
		Condition                        []string                             `json:"condition"`
		PriceTierPreference              string                               `json:"priceTierPreference,omitempty"`
		EffectiveResearchPriceConstraint shareddomain.ResearchPriceConstraint `json:"effectiveResearchPriceConstraint"`
		PriceConstraintSource            TargetPriceConstraintSourceV2        `json:"priceConstraintSource"`
		PriceConstraintSourceRef         string                               `json:"priceConstraintSourceRef,omitempty"`
		MarketContext                    MarketContextV2                      `json:"marketContext"`
		ShippingCountry                  shareddomain.CountryCode             `json:"shippingCountry"`
		ReferenceURLs                    []string                             `json:"referenceUrls"`
		TaxonomyRef                      string                               `json:"taxonomyRef,omitempty"`
		Provenance                       []ConstraintProvenanceV2             `json:"provenance"`
		ProvenanceSourceCatalogHash      string                               `json:"provenanceSourceCatalogHash"`
	}{
		SchemaVersion: p.SchemaVersion, ProductType: p.ProductType,
		UseCases: p.UseCases, BrandTerms: p.BrandTerms, ModelTerms: p.ModelTerms,
		Materials: p.Materials, Styles: p.Styles,
		HardRequirements: p.HardRequirements,
		SoftPreferences:  p.SoftPreferences, Exclusions: p.Exclusions,
		Attributes: p.Attributes, Condition: p.Condition,
		PriceTierPreference:              p.PriceTierPreference,
		EffectiveResearchPriceConstraint: p.EffectiveResearchPriceConstraint,
		PriceConstraintSource:            p.PriceConstraintSource,
		PriceConstraintSourceRef:         p.PriceConstraintSourceRef,
		MarketContext:                    p.MarketContext, ShippingCountry: p.ShippingCountry,
		ReferenceURLs: p.ReferenceURLs, TaxonomyRef: p.TaxonomyRef,
		Provenance:                  p.Provenance,
		ProvenanceSourceCatalogHash: p.ProvenanceSourceCatalogHash,
	})
}

func validateCatalogProfileLanguageAndProvenance(
	profile TargetRequirementProfileV2,
) error {
	semanticFields := map[string][]string{
		"productType":             {profile.ProductType},
		"useCases":                profile.UseCases,
		"brandTerms":              profile.BrandTerms,
		"modelTerms":              profile.ModelTerms,
		"materials":               profile.Materials,
		"styles":                  profile.Styles,
		"hardRequirements":        profile.HardRequirements,
		"softPreferences":         profile.SoftPreferences,
		"exclusions":              profile.Exclusions,
		"attributes.colors":       profile.Attributes.Colors,
		"attributes.targetGender": profile.Attributes.TargetGender,
		"condition":               profile.Condition,
	}
	if profile.PriceTierPreference != "" {
		semanticFields["priceTierPreference"] = []string{
			profile.PriceTierPreference,
		}
	}
	for _, size := range profile.Attributes.Sizes {
		if !isCatalogEnglishOrNumericText(size.Value) ||
			(size.SizingSystem != "" && !isCatalogEnglishText(size.SizingSystem)) {
			return ErrTargetRequirementProfileNotEnglish
		}
		semanticFields["attributes.sizes"] = append(
			semanticFields["attributes.sizes"], catalogSizeProvenanceValue(size),
		)
	}
	for _, values := range semanticFields {
		for _, value := range values {
			if !isCatalogEnglishText(value) {
				return ErrTargetRequirementProfileNotEnglish
			}
		}
	}

	expected := make(map[string]struct{})
	for field, values := range semanticFields {
		for _, value := range values {
			expected[field+"\x00"+value] = struct{}{}
		}
	}
	for _, referenceURL := range profile.ReferenceURLs {
		expected["referenceUrls\x00"+referenceURL] = struct{}{}
	}
	if profile.TaxonomyRef != "" {
		expected["taxonomyRef\x00"+profile.TaxonomyRef] = struct{}{}
	}
	expected["shippingCountry\x00"+string(profile.ShippingCountry)] = struct{}{}
	if profile.EffectiveResearchPriceConstraint.Kind ==
		shareddomain.ResearchPriceConstraintExplicit ||
		profile.PriceConstraintSource == TargetPriceConstraintExplicitV2 {
		value, err := catalogPriceProvenanceValue(
			profile.EffectiveResearchPriceConstraint,
		)
		if err != nil {
			return err
		}
		expected["effectiveResearchPriceConstraint\x00"+value] = struct{}{}
	}

	observed := make(map[string]ConstraintProvenanceV2, len(profile.Provenance))
	for _, provenance := range profile.Provenance {
		provenance.Field = strings.TrimSpace(provenance.Field)
		provenance.Value = strings.TrimSpace(provenance.Value)
		provenance.SourceRef = strings.TrimSpace(provenance.SourceRef)
		if provenance.Field == "" || provenance.Value == "" ||
			!provenance.Origin.Valid() || provenance.SourceRef == "" ||
			len(provenance.SourceRef) > 200 {
			return ErrTargetRequirementProvenanceInvalid
		}
		key := provenance.Field + "\x00" + provenance.Value
		if _, exists := observed[key]; exists {
			return ErrTargetRequirementProvenanceInvalid
		}
		if _, exists := expected[key]; !exists {
			return fmt.Errorf(
				"%w: provenance does not match a profile value",
				ErrTargetRequirementProvenanceInvalid,
			)
		}
		observed[key] = provenance
	}
	for key := range expected {
		provenance, exists := observed[key]
		if !exists {
			return fmt.Errorf(
				"%w: missing provenance",
				ErrTargetRequirementProvenanceInvalid,
			)
		}
		field := strings.SplitN(key, "\x00", 2)[0]
		if catalogProtectedProfileField(field) &&
			provenance.Origin == ConstraintOriginModelDerivedV2 {
			return fmt.Errorf(
				"%w: %s",
				ErrTargetRequirementInvented,
				field,
			)
		}
		if field == "taxonomyRef" &&
			provenance.Origin != ConstraintOriginPolicyV2 {
			return fmt.Errorf(
				"%w: taxonomy must be server-validated",
				ErrTargetRequirementInvented,
			)
		}
		if field == "shippingCountry" &&
			provenance.Origin != ConstraintOriginPolicyV2 {
			return ErrTargetRequirementProvenanceInvalid
		}
	}
	return nil
}

func catalogProtectedProfileField(field string) bool {
	switch field {
	case "brandTerms", "modelTerms", "attributes.colors",
		"attributes.sizes", "attributes.targetGender", "condition",
		"priceTierPreference", "referenceUrls",
		"effectiveResearchPriceConstraint":
		return true
	default:
		return false
	}
}

func cloneCatalogAttributes(input TargetAttributesV2) TargetAttributesV2 {
	result := TargetAttributesV2{
		Colors:       cleanCatalogTargetValues(input.Colors),
		Sizes:        append([]TargetSizeV2(nil), input.Sizes...),
		TargetGender: cleanCatalogTargetValues(input.TargetGender),
	}
	for index := range result.Sizes {
		result.Sizes[index].Value = strings.TrimSpace(result.Sizes[index].Value)
		result.Sizes[index].SizingSystem = strings.TrimSpace(
			result.Sizes[index].SizingSystem,
		)
	}
	return result
}

func cloneCatalogProvenance(input []ConstraintProvenanceV2) []ConstraintProvenanceV2 {
	result := append([]ConstraintProvenanceV2(nil), input...)
	for index := range result {
		result[index].Field = strings.TrimSpace(result[index].Field)
		result[index].Value = strings.TrimSpace(result[index].Value)
		result[index].SourceRef = strings.TrimSpace(result[index].SourceRef)
	}
	return result
}

func cleanCatalogTargetValues(input []string) []string {
	if input == nil {
		return []string{}
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
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

func compareCatalogProvenance(left, right ConstraintProvenanceV2) int {
	leftKey := left.Field + "\x00" + left.Value + "\x00" +
		string(left.Origin) + "\x00" + left.SourceRef
	rightKey := right.Field + "\x00" + right.Value + "\x00" +
		string(right.Origin) + "\x00" + right.SourceRef
	return strings.Compare(leftKey, rightKey)
}

func validateTrustedCatalogProvenance(
	provenance []ConstraintProvenanceV2,
	trustedSourceRefs map[string]struct{},
) error {
	for _, source := range provenance {
		if source.Origin == ConstraintOriginModelDerivedV2 {
			continue
		}
		if _, trusted := trustedSourceRefs[source.SourceRef]; !trusted {
			return fmt.Errorf(
				"%w: untrusted source reference",
				ErrTargetRequirementInvented,
			)
		}
	}
	return nil
}

func catalogSourceRefSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func catalogPriceProvenanceValue(
	constraint shareddomain.ResearchPriceConstraint,
) (string, error) {
	if constraint.Kind == shareddomain.ResearchPriceConstraintNone {
		return "NONE", nil
	}
	return shareddomain.CanonicalJSONHash(constraint)
}

func catalogSizeProvenanceValue(value TargetSizeV2) string {
	size := strings.TrimSpace(value.Value)
	system := strings.TrimSpace(value.SizingSystem)
	if system == "" {
		return size
	}
	return system + ":" + size
}

func isCatalogEnglishText(value string) bool {
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

func isCatalogEnglishOrNumericText(value string) bool {
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

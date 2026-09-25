package app

import (
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const candidateRankingCompileInvalidV2 = "CANDIDATE_RANKING_COMPILE_INVALID"

type CandidateRankingRejectionReasonV2 string

const (
	CandidateRankingInvalidObservationV2    CandidateRankingRejectionReasonV2 = "INVALID_OBSERVATION"
	CandidateRankingInvalidLocatorV2        CandidateRankingRejectionReasonV2 = "UNRESOLVABLE_LOCATOR"
	CandidateRankingFreshEvidenceRequiredV2 CandidateRankingRejectionReasonV2 = "FRESH_EVIDENCE_REQUIRED"
	CandidateRankingCurrencyMismatchV2      CandidateRankingRejectionReasonV2 = "CURRENCY_MISMATCH"
	CandidateRankingPriceMismatchV2         CandidateRankingRejectionReasonV2 = "PRICE_MISMATCH"
	CandidateRankingHardLexicalMismatchV2   CandidateRankingRejectionReasonV2 = "HARD_LEXICAL_MISMATCH"
	CandidateRankingExclusionMatchedV2      CandidateRankingRejectionReasonV2 = "EXCLUSION_MATCHED"
)

// CandidateRankingObservationV2 binds one response-scoped product observation
// to fresh, durable-safe evidence references. The compiler may inspect raw
// display fields for matching, but never returns the observation or those
// fields in its output.
type CandidateRankingObservationV2 struct {
	Admission               CatalogAdmissionInputV2
	ObservationEvidenceID   string
	ObservationEvidenceHash string
	EvidenceRefs            []string
	ObservedAt              time.Time
	ExpiresAt               time.Time
}

type CompileCandidateRankingV2Input struct {
	Intent       researchdomain.TargetSearchIntentV2
	SourcePolicy researchdomain.CandidateAssessmentSourcePolicyV2
	Observations []CandidateRankingObservationV2
	TrustedNow   time.Time
	// CurrencyExponent is a reviewed provider capability value. NONE must not
	// carry one; EXPLICIT requires it to reproduce the immutable search band.
	CurrencyExponent *uint8
}

// RankedCandidateAssessmentV2 is deliberately pre-materialization. ProductKey
// correlates the result back to the caller's admission input; Candidate,
// Discovery and Assessment IDs are issued by the owning transaction later.
type RankedCandidateAssessmentV2 struct {
	ProductKey           string
	RankPosition         int
	TotalScore           int
	RankingPolicyVersion string
	Assessment           researchdomain.CandidateAssessmentInputV2
}

type CandidateRankingRejectionV2 struct {
	ProductKey string
	Reason     CandidateRankingRejectionReasonV2
}

type CandidateRankingCompilationV2 struct {
	Ranked   []RankedCandidateAssessmentV2
	Rejected []CandidateRankingRejectionV2
}

type candidateRankingScoredV2 struct {
	productKey    string
	providerOrder int
	totalScore    int
	assessment    researchdomain.CandidateAssessmentInputV2
}

// CompileCandidateRankingV2 applies server-owned hard filters, deterministic
// research-ranking.v1 (or the distinct expansion policy), and evidence-backed
// assessment templates. It performs no I/O and does not allocate IDs.
func CompileCandidateRankingV2(
	input CompileCandidateRankingV2Input,
) (CandidateRankingCompilationV2, error) {
	result := CandidateRankingCompilationV2{
		Ranked:   []RankedCandidateAssessmentV2{},
		Rejected: []CandidateRankingRejectionV2{},
	}
	if err := input.Intent.Validate(); err != nil || input.TrustedNow.IsZero() ||
		!candidateRankingCurrencyExponentValidV2(input.Intent, input.CurrencyExponent) ||
		!validCandidateAssessmentSourcePolicyV2(input.SourcePolicy) {
		return result, fault.Wrap(
			errOrCandidateRankingInvalidV2(err),
			fault.InvalidInput,
			candidateRankingCompileInvalidV2,
			false,
		)
	}

	seenProducts := make(map[string]struct{}, len(input.Observations))
	scored := make([]candidateRankingScoredV2, 0, len(input.Observations))
	for _, observed := range input.Observations {
		productKey := strings.TrimSpace(
			observed.Admission.Observation.ProviderProductID,
		)
		if productKey == "" || observed.Admission.Observation.ProviderOrder < 0 ||
			isHTTPURLV2(productKey) {
			result.Rejected = append(result.Rejected, CandidateRankingRejectionV2{
				ProductKey: productKey, Reason: CandidateRankingInvalidObservationV2,
			})
			continue
		}
		if _, exists := seenProducts[productKey]; exists {
			return CandidateRankingCompilationV2{
					Ranked:   []RankedCandidateAssessmentV2{},
					Rejected: []CandidateRankingRejectionV2{},
				}, fault.New(
					fault.InvalidInput, candidateRankingCompileInvalidV2, false,
				)
		}
		seenProducts[productKey] = struct{}{}

		reason, evidenceRefs, fields := candidateRankingHardFilterV2(
			input.Intent, observed, input.TrustedNow, input.CurrencyExponent,
		)
		if reason != "" {
			result.Rejected = append(result.Rejected, CandidateRankingRejectionV2{
				ProductKey: productKey, Reason: reason,
			})
			continue
		}

		components, total, matchedGroups, unmatchedSoft := candidateRankingScoreV2(
			input.Intent,
			fields,
			observed.Admission.Observation.ProviderOrder,
		)
		assessment := researchdomain.CandidateAssessmentInputV2{
			SourcePolicy: input.SourcePolicy,
			IntentPoint: researchdomain.CandidateAssessmentClaimV2{
				Text:         "Fresh catalog evidence matches the requested product type and explicit requirements.",
				EvidenceRefs: append([]string(nil), evidenceRefs...),
			},
			Features: candidateRankingFeatureLinesV2(
				matchedGroups, evidenceRefs,
			),
			Specifications: candidateRankingSpecificationLinesV2(
				observed.Admission.Locator.Kind, evidenceRefs,
			),
			Tradeoffs:        candidateRankingTradeoffsV2(unmatchedSoft, evidenceRefs),
			ScoreComponents:  components,
			EvidenceRefs:     append([]string(nil), evidenceRefs...),
			SourceObservedAt: observed.ObservedAt.UTC(),
		}
		scored = append(scored, candidateRankingScoredV2{
			productKey:    productKey,
			providerOrder: observed.Admission.Observation.ProviderOrder,
			totalScore:    total,
			assessment:    assessment,
		})
	}

	sort.Slice(scored, func(left, right int) bool {
		if scored[left].totalScore != scored[right].totalScore {
			return scored[left].totalScore > scored[right].totalScore
		}
		if scored[left].providerOrder != scored[right].providerOrder {
			return scored[left].providerOrder < scored[right].providerOrder
		}
		return scored[left].productKey < scored[right].productKey
	})
	for index, value := range scored {
		result.Ranked = append(result.Ranked, RankedCandidateAssessmentV2{
			ProductKey:           value.productKey,
			RankPosition:         index + 1,
			TotalScore:           value.totalScore,
			RankingPolicyVersion: string(input.SourcePolicy),
			Assessment:           value.assessment,
		})
	}
	sort.Slice(result.Rejected, func(left, right int) bool {
		if result.Rejected[left].ProductKey != result.Rejected[right].ProductKey {
			return result.Rejected[left].ProductKey < result.Rejected[right].ProductKey
		}
		return result.Rejected[left].Reason < result.Rejected[right].Reason
	})
	return result, nil
}

func validCandidateAssessmentSourcePolicyV2(
	policy researchdomain.CandidateAssessmentSourcePolicyV2,
) bool {
	return policy == researchdomain.CandidateAssessmentResearchRankingV1 ||
		policy == researchdomain.CandidateAssessmentExpansionV1
}

func errOrCandidateRankingInvalidV2(err error) error {
	if err != nil {
		return err
	}
	return fault.New(fault.InvalidInput, candidateRankingCompileInvalidV2, false)
}

func candidateRankingHardFilterV2(
	intent researchdomain.TargetSearchIntentV2,
	observed CandidateRankingObservationV2,
	trustedNow time.Time,
	currencyExponent *uint8,
) (CandidateRankingRejectionReasonV2, []string, [][]string) {
	admission := observed.Admission
	observation := admission.Observation
	if !candidateRankingLocatorValidV2(admission) {
		return CandidateRankingInvalidLocatorV2, nil, nil
	}
	evidenceRefs, valid := candidateRankingEvidenceRefsV2(observed.EvidenceRefs)
	if !valid || !candidateRankingEvidenceLeaseValidV2(observed, trustedNow) ||
		!candidateRankingHardFilterProofValidV2(intent, observed.Admission) ||
		!candidateRankingEvidenceLineageValidV2(observed, evidenceRefs) {
		return CandidateRankingFreshEvidenceRequiredV2, nil, nil
	}
	if observation.PriceRange.Minimum.AmountMinor < 0 ||
		observation.PriceRange.Maximum.AmountMinor <
			observation.PriceRange.Minimum.AmountMinor {
		return CandidateRankingInvalidObservationV2, nil, nil
	}
	wantCurrency := string(intent.MarketContext.Currency)
	if observation.PriceRange.Minimum.Currency != wantCurrency ||
		observation.PriceRange.Maximum.Currency != wantCurrency {
		return CandidateRankingCurrencyMismatchV2, nil, nil
	}
	if !candidateRankingPriceProofValidV2(
		intent, admission.HardFilters.AppliedFilters,
		observation.PriceRange, currencyExponent,
	) {
		return CandidateRankingPriceMismatchV2, nil, nil
	}

	fields := candidateRankingLexicalFieldsV2(observation)
	for _, exclusion := range intent.Exclusions {
		if candidateRankingFieldsContainV2(fields, exclusion) {
			return CandidateRankingExclusionMatchedV2, nil, nil
		}
	}
	hardTerms := make([]string, 0, len(intent.HardLexicalTerms)+1)
	hardTerms = append(hardTerms, intent.ProductType)
	hardTerms = append(hardTerms, intent.HardLexicalTerms...)
	for _, required := range hardTerms {
		if !candidateRankingFieldsContainV2(fields, required) {
			return CandidateRankingHardLexicalMismatchV2, nil, nil
		}
	}
	return "", evidenceRefs, fields
}

func candidateObservationProofV4(intent researchdomain.TargetSearchIntentV2, observed CandidateRankingObservationV2, now time.Time, exponent *uint8) (CandidateRankingRejectionReasonV2, []string, [][]string) {
	if !candidateRankingLocatorValidV2(observed.Admission) {
		return CandidateRankingInvalidLocatorV2, nil, nil
	}
	refs, valid := candidateRankingEvidenceRefsV2(observed.EvidenceRefs)
	if !valid || !candidateRankingEvidenceLeaseValidV2(observed, now) || !candidateRankingHardFilterProofValidV2(intent, observed.Admission) || !candidateRankingEvidenceLineageValidV2(observed, refs) {
		return CandidateRankingFreshEvidenceRequiredV2, nil, nil
	}
	return "", refs, candidateRankingLexicalFieldsV2(observed.Admission.Observation)
}

func candidateRankingCurrencyExponentValidV2(
	intent researchdomain.TargetSearchIntentV2,
	exponent *uint8,
) bool {
	switch intent.PriceConstraint.Kind {
	case shareddomain.ResearchPriceConstraintNone:
		return exponent == nil
	case shareddomain.ResearchPriceConstraintExplicit:
		return exponent != nil && *exponent <= 6
	default:
		return false
	}
}

func candidateRankingPriceProofValidV2(
	intent researchdomain.TargetSearchIntentV2,
	proof CatalogAppliedFilterProofV2,
	observed CatalogPriceRange,
	exponent *uint8,
) bool {
	if intent.PriceConstraint.Kind == shareddomain.ResearchPriceConstraintNone {
		return exponent == nil && proof.Price == nil && proof.PriceCurrency == ""
	}
	if exponent == nil || *exponent > 6 || proof.Price == nil ||
		proof.PriceCurrency != string(intent.MarketContext.Currency) {
		return false
	}
	want := CatalogPriceFilter{}
	var err error
	if intent.PriceConstraint.Min != nil {
		want.MinimumMinor, err = catalogMinorUnitsV2(
			*intent.PriceConstraint.Min, *exponent,
		)
		if err != nil {
			return false
		}
	}
	if intent.PriceConstraint.Max != nil {
		want.MaximumMinor, err = catalogMinorUnitsV2(
			*intent.PriceConstraint.Max, *exponent,
		)
		if err != nil {
			return false
		}
	}
	if !reflect.DeepEqual(*proof.Price, want) {
		return false
	}
	if want.MinimumMinor != nil &&
		observed.Maximum.AmountMinor < *want.MinimumMinor {
		return false
	}
	if want.MaximumMinor != nil &&
		observed.Minimum.AmountMinor > *want.MaximumMinor {
		return false
	}
	return true
}

func candidateRankingLocatorValidV2(admission CatalogAdmissionInputV2) bool {
	if admission.Observation.Locator == nil ||
		admission.Locator.Validate() != nil ||
		admission.Observation.Locator.Validate() != nil ||
		!reflect.DeepEqual(admission.Locator, *admission.Observation.Locator) {
		return false
	}
	locator := admission.Locator
	switch locator.Kind {
	case CatalogLocatorProductURL:
		return locator.ProductURL != nil &&
			locator.ProductURL.CanonicalURL ==
				strings.TrimSpace(locator.ProductURL.CanonicalURL)
	case CatalogLocatorMerchantVariant:
		preview := admission.Observation.PreviewVariant
		merchant := locator.MerchantVariant
		return merchant != nil && preview != nil && preview.Seller != nil &&
			preview.ID == merchant.VariantID &&
			strings.EqualFold(preview.Seller.Domain, merchant.SellerDomain)
	default:
		return false
	}
}

func candidateRankingEvidenceRefsV2(values []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || isHTTPURLV2(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return nil, false
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, false
	}
	sort.Strings(result)
	return result, true
}

func candidateRankingEvidenceLeaseValidV2(
	observed CandidateRankingObservationV2,
	trustedNow time.Time,
) bool {
	now := trustedNow.UTC()
	observedAt := observed.ObservedAt.UTC()
	expiresAt := observed.ExpiresAt.UTC()
	return !observed.ObservedAt.IsZero() && !observed.ExpiresAt.IsZero() &&
		!observedAt.After(now) && expiresAt.After(now) && expiresAt.After(observedAt)
}

func candidateRankingHardFilterProofValidV2(
	intent researchdomain.TargetSearchIntentV2,
	admission CatalogAdmissionInputV2,
) bool {
	applied := admission.HardFilters.AppliedFilters
	physical := admission.HardFilters.PhysicalEligibility
	if !applied.validateShape() ||
		applied.Provider == CatalogProviderShopifyGlobalV2 &&
			applied.ProviderCapability != CatalogShopifyGlobalCapabilityV2 ||
		!reflect.DeepEqual(applied.Filters, candidateRankingExpectedHardFiltersV2(intent)) ||
		!validCatalogPhysicalEligibilityProofV2(
			applied, admission.Observation, physical,
		) {
		return false
	}
	return true
}

func candidateRankingExpectedHardFiltersV2(
	intent researchdomain.TargetSearchIntentV2,
) CatalogHardFilterSetV2 {
	available := true
	attributes := make([]CatalogAttributeFilter, 0, 3)
	if len(intent.Colors) > 0 {
		attributes = append(attributes, CatalogAttributeFilter{
			Name: "Color", Values: append([]string(nil), intent.Colors...),
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
		attributes = append(attributes, CatalogAttributeFilter{
			Name: "Size", Values: values,
		})
	}
	if len(intent.TargetGenders) > 0 {
		attributes = append(attributes, CatalogAttributeFilter{
			Name: "Target gender", Values: append([]string(nil), intent.TargetGenders...),
		})
	}
	return normalizeCatalogHardFilterSetV2(CatalogProductSearchFilters{
		Available:  &available,
		ShipsTo:    &CatalogDestination{Country: string(intent.ShippingCountry)},
		Categories: append([]string(nil), intent.VerifiedCategories...),
		Conditions: append([]string(nil), intent.Conditions...),
		Attributes: attributes,
	})
}

func candidateRankingEvidenceLineageValidV2(
	observed CandidateRankingObservationV2,
	evidenceRefs []string,
) bool {
	if !validCatalogEvidenceRefV2(observed.ObservationEvidenceID) ||
		!validCatalogEvidenceHashV2(observed.ObservationEvidenceHash) {
		return false
	}
	expectedObservationHash, err := CatalogObservationEvidenceHashV2(
		observed.Admission, observed.ObservationEvidenceID,
		observed.ObservedAt, observed.ExpiresAt,
	)
	if err != nil || observed.ObservationEvidenceHash != expectedObservationHash {
		return false
	}
	applied := observed.Admission.HardFilters.AppliedFilters
	physical := observed.Admission.HardFilters.PhysicalEligibility
	required := []string{
		strings.TrimSpace(observed.ObservationEvidenceID),
		strings.TrimSpace(observed.ObservationEvidenceHash),
		strings.TrimSpace(applied.EvidenceRef),
		strings.TrimSpace(applied.EvidenceHash),
		strings.TrimSpace(physical.EvidenceRef),
		strings.TrimSpace(physical.EvidenceHash),
	}
	available := make(map[string]struct{}, len(evidenceRefs))
	for _, value := range evidenceRefs {
		available[value] = struct{}{}
	}
	for _, value := range required {
		if _, exists := available[value]; !exists {
			return false
		}
	}
	return true
}

// CatalogObservationEvidenceHashV2 binds the response-scoped product fact,
// exact locator, provider-applied filter proof, physical eligibility proof,
// and evidence lease. Persistence may store this safe hash, but not the raw
// provider display payload used to calculate it.
func CatalogObservationEvidenceHashV2(
	admission CatalogAdmissionInputV2,
	evidenceID string,
	observedAt time.Time,
	expiresAt time.Time,
) (string, error) {
	if !validCatalogEvidenceRefV2(evidenceID) || observedAt.IsZero() ||
		expiresAt.IsZero() || !expiresAt.After(observedAt) ||
		!candidateRankingLocatorValidV2(admission) ||
		!admission.HardFilters.AppliedFilters.validateShape() ||
		!validCatalogPhysicalEligibilityProofV2(
			admission.HardFilters.AppliedFilters, admission.Observation,
			admission.HardFilters.PhysicalEligibility,
		) {
		return "", fault.New(
			fault.InvalidInput, candidateRankingCompileInvalidV2, false,
		)
	}
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion        string                    `json:"schemaVersion"`
		EvidenceID           string                    `json:"evidenceId"`
		Observation          CatalogProductObservation `json:"observation"`
		Locator              CatalogProductLocator     `json:"locator"`
		AppliedEvidenceHash  string                    `json:"appliedEvidenceHash"`
		PhysicalEvidenceHash string                    `json:"physicalEvidenceHash"`
		ObservedAt           time.Time                 `json:"observedAt"`
		ExpiresAt            time.Time                 `json:"expiresAt"`
	}{
		SchemaVersion: "vitlane.catalog-product-observation-evidence.v2",
		EvidenceID:    strings.TrimSpace(evidenceID),
		Observation:   admission.Observation, Locator: admission.Locator,
		AppliedEvidenceHash:  admission.HardFilters.AppliedFilters.EvidenceHash,
		PhysicalEvidenceHash: admission.HardFilters.PhysicalEligibility.EvidenceHash,
		ObservedAt:           observedAt.UTC(), ExpiresAt: expiresAt.UTC(),
	})
}

func isHTTPURLV2(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") ||
		strings.HasPrefix(value, "https://")
}

func candidateRankingLexicalFieldsV2(
	observation CatalogProductObservation,
) [][]string {
	values := []string{
		observation.Title,
		observation.Description.Plain,
		observation.Description.Markdown,
	}
	for _, category := range observation.Categories {
		values = append(values, category.Value, category.Taxonomy)
	}
	if observation.PreviewVariant != nil {
		preview := observation.PreviewVariant
		values = append(
			values,
			preview.Title,
			preview.Description.Plain,
			preview.Description.Markdown,
		)
		if preview.Seller != nil {
			values = append(values, preview.Seller.Name)
		}
	}
	fields := make([][]string, 0, len(values))
	for _, value := range values {
		if tokens := candidateRankingTokensV2(value); len(tokens) > 0 {
			fields = append(fields, tokens)
		}
	}
	return fields
}

func candidateRankingTokensV2(value string) []string {
	var normalized strings.Builder
	normalized.Grow(len(value))
	previousSeparator := true
	for _, current := range strings.ToLower(value) {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			normalized.WriteRune(current)
			previousSeparator = false
			continue
		}
		if !previousSeparator {
			normalized.WriteByte(' ')
			previousSeparator = true
		}
	}
	return strings.Fields(normalized.String())
}

func candidateRankingFieldsContainV2(fields [][]string, phrase string) bool {
	want := candidateRankingTokensV2(phrase)
	if len(want) == 0 {
		return false
	}
	for _, field := range fields {
		if candidateRankingTokensContainV2(field, want) {
			return true
		}
	}
	return false
}

func candidateRankingTokensContainV2(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for start := 0; start <= len(haystack)-len(needle); start++ {
		matched := true
		for offset := range needle {
			if haystack[start+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func candidateRankingScoreV2(
	intent researchdomain.TargetSearchIntentV2,
	fields [][]string,
	providerOrder int,
) ([]researchdomain.CandidateScoreComponentV2, int, []string, bool) {
	semanticGroups := []struct {
		name  string
		terms []string
	}{
		{name: "product type", terms: []string{intent.ProductType}},
		{name: "use case", terms: intent.UseCaseTerms},
		{name: "brand", terms: intent.BrandTerms},
		{name: "model", terms: intent.ModelTerms},
		{name: "material", terms: intent.MaterialTerms},
		{name: "style", terms: intent.StyleTerms},
		{name: "soft preference", terms: intent.SoftLexicalTerms},
	}
	semanticMatched, semanticTotal := 0, 0
	matchedGroups := []string{"product type", "explicit requirements"}
	unmatchedSoft := false
	for _, group := range semanticGroups {
		groupMatched := false
		for _, term := range group.terms {
			semanticTotal++
			if candidateRankingFieldsContainV2(fields, term) {
				semanticMatched++
				groupMatched = true
			} else if group.name != "product type" {
				unmatchedSoft = true
			}
		}
		if groupMatched && group.name != "product type" {
			matchedGroups = append(matchedGroups, group.name)
		}
	}

	structuredTerms := make([]string, 0)
	structuredTerms = append(structuredTerms, intent.Colors...)
	for _, size := range intent.Sizes {
		structuredTerms = append(structuredTerms, size.Value)
	}
	structuredTerms = append(
		structuredTerms,
		intent.TargetGenders...,
	)
	structuredTerms = append(structuredTerms, intent.Conditions...)
	structuredTerms = append(structuredTerms, intent.VerifiedCategories...)
	structuredMatched := 0
	for _, term := range structuredTerms {
		if candidateRankingFieldsContainV2(fields, term) {
			structuredMatched++
		}
	}

	components := []researchdomain.CandidateScoreComponentV2{
		{Name: "attribute-fit", Score: candidateRankingWeightedScoreV2(
			structuredMatched, len(structuredTerms), 15,
		)},
		{Name: "evidence-quality", Score: 10},
		{Name: "hard-requirement-coverage", Score: 25},
		// Numeric price intersection was already enforced by the compiled search
		// request. NONE is intentionally the same neutral score for every row.
		{Name: "price-fit", Score: 10},
		{Name: "provider-order", Score: candidateRankingProviderOrderScoreV2(providerOrder)},
		{Name: "semantic-intent-fit", Score: candidateRankingWeightedScoreV2(
			semanticMatched, semanticTotal, 35,
		)},
	}
	total := 0
	for _, component := range components {
		total += component.Score
	}
	return components, total, matchedGroups, unmatchedSoft
}

func candidateRankingWeightedScoreV2(matched, total, weight int) int {
	if total == 0 {
		return weight
	}
	return (matched*weight + total/2) / total
}

func candidateRankingProviderOrderScoreV2(providerOrder int) int {
	if providerOrder >= 5 {
		return 0
	}
	return 5 - providerOrder
}

func candidateRankingFeatureLinesV2(
	matchedGroups []string,
	evidenceRefs []string,
) []researchdomain.CandidateAssessmentLineV2 {
	labels := make(map[string]string, len(matchedGroups))
	for _, group := range matchedGroups {
		switch group {
		case "product type":
			labels[group] = "Observed evidence supports the requested product type."
		case "explicit requirements":
			labels[group] = "Observed evidence covers every explicit lexical requirement."
		case "use case":
			labels[group] = "Observed evidence supports a requested use case."
		case "brand", "model":
			labels["brand or model"] = "Observed evidence supports a requested brand or model term."
		case "material", "style":
			labels["material or style"] = "Observed evidence supports a requested material or style term."
		case "soft preference":
			labels[group] = "Observed evidence supports a soft preference."
		}
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]researchdomain.CandidateAssessmentLineV2, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, researchdomain.CandidateAssessmentLineV2{
			Text:         labels[key],
			Origin:       researchdomain.CandidateClaimProviderInferredV2,
			EvidenceRefs: append([]string(nil), evidenceRefs...),
		})
	}
	return lines
}

func candidateRankingSpecificationLinesV2(
	locatorKind CatalogLocatorKind,
	evidenceRefs []string,
) []researchdomain.CandidateAssessmentLineV2 {
	locatorText := "A valid product-level URL locator is available."
	if locatorKind == CatalogLocatorMerchantVariant {
		locatorText = "A valid merchant Variant locator is available."
	}
	return []researchdomain.CandidateAssessmentLineV2{
		{
			Text:         locatorText,
			Origin:       researchdomain.CandidateClaimProviderExplicitV2,
			EvidenceRefs: append([]string(nil), evidenceRefs...),
		},
		{
			Text:         "The observed price range uses the requested market currency.",
			Origin:       researchdomain.CandidateClaimProviderExplicitV2,
			EvidenceRefs: append([]string(nil), evidenceRefs...),
		},
	}
}

func candidateRankingTradeoffsV2(
	unmatchedSoft bool,
	evidenceRefs []string,
) []researchdomain.CandidateAssessmentClaimV2 {
	if !unmatchedSoft {
		return []researchdomain.CandidateAssessmentClaimV2{}
	}
	return []researchdomain.CandidateAssessmentClaimV2{{
		Text:         "Some preferences were not evidenced by the fresh catalog observation.",
		EvidenceRefs: append([]string(nil), evidenceRefs...),
	}}
}

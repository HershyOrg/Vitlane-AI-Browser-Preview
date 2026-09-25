package app

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	catalogProtocolVersionV2 = "2026-04-08"
	catalogEvidenceTTLV2     = 5 * time.Minute
)

// CatalogTargetSearchProfileV2 is the server-owned Target projection used to
// compile Shopify requests. Browser-supplied country, currency, category, and
// normalized intent never become provider authority.
type CatalogTargetSearchProfileV2 struct {
	TargetID         string
	NormalizedIntent string
	Category         string
	TargetHash       string
	Market           CatalogMarketContextV2
	// Price bounds are read from the immutable Target research scope. They are
	// deliberately not accepted from the browser as provider authority.
	MinimumPrice *shareddomain.Money
	MaximumPrice *shareddomain.Money
}

type catalogTargetSearchProfileReaderV2 interface {
	CatalogTargetSearchProfileV2(
		context.Context,
		string,
		string,
		string,
	) (CatalogTargetSearchProfileV2, error)
}

type catalogRateLimitedCatalogGatewayV2 struct {
	service       *LiveCatalogReviewServiceV2
	calls         int
	lastUsed      int
	lastRemaining int
	lastSearch    CatalogProductSearchResult
}

func (gateway *catalogRateLimitedCatalogGatewayV2) SearchProducts(
	ctx context.Context,
	request CatalogProductSearchRequest,
) (CatalogProductSearchResult, error) {
	now := gateway.service.clock.Now()
	used, remaining, release, err := gateway.service.acquire(now, 1)
	if err != nil {
		return CatalogProductSearchResult{}, err
	}
	defer release()
	gateway.calls++
	gateway.lastUsed = used
	gateway.lastRemaining = remaining
	result, err := gateway.service.gateway.SearchProducts(ctx, request)
	if err == nil {
		gateway.lastSearch = result
	}
	return result, err
}

func (gateway *catalogRateLimitedCatalogGatewayV2) LookupMedia(
	ctx context.Context,
	request CatalogMediaLookupRequest,
) (CatalogMediaLookupResult, error) {
	if request.ProviderCallAdmission != nil {
		return CatalogMediaLookupResult{}, fault.New(
			fault.InternalFailure, "PHASE8_MEDIA_LOOKUP_ADMISSION_OVERRIDE", false,
		)
	}
	request.ProviderCallAdmission = gateway
	before := gateway.calls
	result, err := gateway.service.gateway.LookupMedia(ctx, request)
	if result.ProviderCallCount != gateway.calls-before {
		return result, fault.New(
			fault.InternalFailure, "PHASE8_MEDIA_LOOKUP_CALL_COUNT_MISMATCH", false,
		)
	}
	return result, err
}

func (gateway *catalogRateLimitedCatalogGatewayV2) AcquireCatalogProviderCall(
	_ context.Context,
) (func(), error) {
	now := gateway.service.clock.Now()
	used, remaining, release, err := gateway.service.acquire(now, 1)
	if err != nil {
		return nil, err
	}
	gateway.calls++
	gateway.lastUsed = used
	gateway.lastRemaining = remaining
	return release, nil
}

// searchShopifyPlanForProfileV2 asks the Shopify catalog for one page of one of
// the plan's English phrases. Which phrase and which page is this source's own
// progress; what to look for is the plan's and nobody else's.
func (service *LiveCatalogReviewServiceV2) searchShopifyPlanForProfileV2(
	ctx context.Context,
	input CatalogWorkspaceSearchInputV2,
	profile CatalogTargetSearchProfileV2,
	searchPlan researchdomain.SearchPlan,
) (LiveCatalogReviewResultV2, error) {
	queries := searchPlan.QueriesFor(researchdomain.SearchLanguageEnglish)
	if len(queries) == 0 {
		// The plan prepared no phrase in this catalog's language, so it is not asked.
		return LiveCatalogReviewResultV2{}, fault.New(
			fault.InvalidInput, "RESEARCH_INPUT_NORMALIZATION_REQUIRED", false,
		)
	}
	progress := SearchProgressFor(input.Progress, "SHOPIFY", queries[0], queries)
	if !slices.Contains(queries, progress.Query) {
		// A saved position of a phrase this plan no longer has starts over.
		progress = SearchProgressFor(nil, "SHOPIFY", queries[0], queries)
	}
	intent, exponent, err := shopifyProofIntentV2(profile, searchPlan, progress.Query)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	planHash, err := shareddomain.CanonicalJSONHash(struct {
		TargetID string                `json:"targetId"`
		Mode     CatalogResearchModeV2 `json:"mode"`
		Intent   string                `json:"intent"`
		Minimum  *int64                `json:"minimumMinor,omitempty"`
		Maximum  *int64                `json:"maximumMinor,omitempty"`
	}{
		TargetID: input.TargetID,
		Mode:     input.Mode,
		Intent:   intent.IntentSummaryEnglish,
		Minimum:  cloneCatalogMinorV2(input.Search.MinimumMinor),
		Maximum:  cloneCatalogMinorV2(input.Search.MaximumMinor),
	})
	if err != nil {
		return LiveCatalogReviewResultV2{}, fault.Wrap(
			err, fault.InternalFailure, "PHASE8_SEARCH_PLAN_HASH_FAILED", false,
		)
	}
	plan, err := TranslateCatalogSearchPlanV2(TranslateCatalogSearchPlanV2Input{
		PlanID: "phase8-search:" + strings.TrimPrefix(planHash, "0x"),
		Plan:   searchPlan,
		Query:  progress.Query,
		Cursor: progress.Cursor,
		Intent: intent,
		Capability: CatalogSearchCapabilityV2{
			ProtocolVersion:   catalogProtocolVersionV2,
			SchemaFingerprint: "shopify-global-catalog:2026-04-08",
			MaximumLimit:      CatalogSearchMaxLimit,
			CurrencyExponents: []CatalogCurrencyExponentV2{{
				Currency: profile.Market.Currency,
				Exponent: exponent,
			}},
		},
	})
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}

	startedAt := service.clock.Now()
	rateGateway := &catalogRateLimitedCatalogGatewayV2{service: service}
	coordinator, err := NewCatalogSearchCoordinatorV2(rateGateway)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	execution, err := coordinator.Execute(ctx, plan)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	if execution.Status == CatalogSearchExecutionNoResultsV2 {
		completedAt := service.clock.Now()
		return LiveCatalogReviewResultV2{NextProgress: SourceProgressSet{"SHOPIFY": AdvanceSearchProgress(progress, queries, rateGateway.lastSearch.Pagination.Cursor, rateGateway.lastSearch.Pagination.HasNextPage, false)},
			Search: CatalogProductSearchResult{
				Provider:        rateGateway.lastSearch.Provider,
				ProtocolVersion: rateGateway.lastSearch.ProtocolVersion,
				Outcome:         CatalogOutcomeSuccess,
				Products:        []CatalogProductObservation{},
				Messages:        execution.Messages,
			},
			CandidateAssessments: map[string]LiveCandidateAssessmentV2{},
			Metrics: catalogPlannedSearchMetricsV2(
				service, rateGateway, startedAt, completedAt,
			),
		}, nil
	}
	if execution.Status != CatalogSearchExecutionResultsV2 {
		return LiveCatalogReviewResultV2{}, fault.New(
			fault.ProviderRejected, "PHASE8_CATALOG_SEARCH_FAILED", false,
		)
	}

	observedAt := service.clock.Now().UTC()
	observations := make([]CandidateRankingObservationV2, 0, len(execution.AdmissionInputs))
	for _, admission := range execution.AdmissionInputs {
		evidenceID := catalogEvidenceIDV2(plan.ContentHash, admission.Observation.ProviderProductID)
		expiresAt := observedAt.Add(catalogEvidenceTTLV2)
		evidenceHash, hashErr := CatalogObservationEvidenceHashV2(
			admission, evidenceID, observedAt, expiresAt,
		)
		if hashErr != nil {
			return LiveCatalogReviewResultV2{}, hashErr
		}
		observations = append(observations, CandidateRankingObservationV2{
			Admission:               admission,
			ObservationEvidenceID:   evidenceID,
			ObservationEvidenceHash: evidenceHash,
			EvidenceRefs: []string{
				evidenceID,
				evidenceHash,
				admission.HardFilters.AppliedFilters.EvidenceRef,
				admission.HardFilters.AppliedFilters.EvidenceHash,
				admission.HardFilters.PhysicalEligibility.EvidenceRef,
				admission.HardFilters.PhysicalEligibility.EvidenceHash,
			},
			ObservedAt: observedAt,
			ExpiresAt:  expiresAt,
		})
	}
	var reviewedExponent *uint8
	if intent.PriceConstraint.Kind == shareddomain.ResearchPriceConstraintExplicit {
		value := exponent
		reviewedExponent = &value
	}
	// The provider's proof (locator, applied filters, fresh evidence) decides
	// whether a row is a usable observation; the plan's required conditions then
	// decide admission, exactly as they do for every other source. Ranking is the
	// evaluation step's work, not the search's.
	var ranking CandidateRankingCompilationV2
	for _, o := range observations {
		reason, _, _ := candidateObservationProofV4(intent, o, observedAt, reviewedExponent)
		if reason == "" {
			ranking.Ranked = append(ranking.Ranked, RankedCandidateAssessmentV2{ProductKey: o.Admission.Observation.ProviderProductID})
		}
	}
	if len(ranking.Ranked) == 0 && len(execution.AdmissionInputs) > 0 {
		// 검색은 성공했지만 명시적 제약(가격·제외어 등)이 전 후보를 걸렀다.
		// provider 오류가 아니라 조건에 맞는 결과가 없는 것이므로 검색-빈
		// 경로와 같은 정직한 NO_RESULTS 성공으로 종결한다. 빈 배치는
		// 도메인이 기존 pool을 보존한 채 NO_RESULTS로 닫는다.
		completedAt := service.clock.Now()
		return LiveCatalogReviewResultV2{NextProgress: SourceProgressSet{"SHOPIFY": AdvanceSearchProgress(progress, queries, rateGateway.lastSearch.Pagination.Cursor, rateGateway.lastSearch.Pagination.HasNextPage, false)},
			Search: CatalogProductSearchResult{
				Provider:        rateGateway.lastSearch.Provider,
				ProtocolVersion: rateGateway.lastSearch.ProtocolVersion,
				Outcome:         CatalogOutcomeSuccess,
				Products:        []CatalogProductObservation{},
				Messages:        execution.Messages,
			},
			CandidateAssessments: map[string]LiveCandidateAssessmentV2{},
			Metrics: catalogPlannedSearchMetricsV2(
				service, rateGateway, startedAt, completedAt,
			),
		}, nil
	}
	admissionByProduct := make(map[string]CatalogAdmissionInputV2, len(execution.AdmissionInputs))
	for _, admission := range execution.AdmissionInputs {
		admissionByProduct[admission.Observation.ProviderProductID] = admission
	}
	products := make([]CatalogProductObservation, 0, len(ranking.Ranked))
	assessments := make(map[string]LiveCandidateAssessmentV2, len(ranking.Ranked))
	for _, ranked := range ranking.Ranked {
		admission, exists := admissionByProduct[ranked.ProductKey]
		if !exists {
			return LiveCatalogReviewResultV2{}, fault.New(
				fault.InternalFailure, "CANDIDATE_RANKING_CORRELATION_FAILED", false,
			)
		}
		products = append(products, admission.Observation)
		assessments[ranked.ProductKey] = catalogLiveAssessmentV2(ranked)
	}

	messages := append([]CatalogProviderMessage(nil), execution.Messages...)
	// Search already carries price and optional media. Missing images are not
	// a reason to spend another request or fail a valid discovery page.
	completedAt := service.clock.Now()
	discarded := 0
	if len(execution.Attempts) > 0 {
		last := execution.Attempts[len(execution.Attempts)-1]
		discarded = max(0, last.RawCount-len(products))
	}
	search := rateGateway.lastSearch
	search.Outcome = CatalogOutcomeSuccess
	search.Products = products
	search.Partial = search.Partial || discarded > 0
	search.Messages = messages
	return LiveCatalogReviewResultV2{NextProgress: SourceProgressSet{"SHOPIFY": AdvanceSearchProgress(progress, queries, search.Pagination.Cursor, search.Pagination.HasNextPage, false)},
		Search: search,
		Metrics: catalogPlannedSearchMetricsV2(
			service, rateGateway, startedAt, completedAt,
		),
		CandidateEligibleCount:  len(products),
		DiscardedNoLocatorCount: discarded,
		CandidateAssessments:    assessments,
	}, nil
}

// shopifyProofIntentV2 is the Shopify adapter's English view of the Round's
// SearchPlan: the conditions its request filters are built from and the
// provider's applied-filter proof is checked against. Every field comes from
// the plan (or the profile the plan was built from); it carries no query words
// and therefore no rule about how many there are.
func shopifyProofIntentV2(
	profile CatalogTargetSearchProfileV2,
	plan researchdomain.SearchPlan,
	query string,
) (researchdomain.TargetSearchIntentV2, uint8, error) {
	query = strings.TrimSpace(query)
	if query == "" || containsNonLatinCatalogLetter(query) {
		return researchdomain.TargetSearchIntentV2{}, 0, fault.New(
			fault.InvalidInput, "RESEARCH_INPUT_NORMALIZATION_REQUIRED", false,
		)
	}
	hardLexicalTerms, err := catalogManagedSearchTermsV2(plan.Required.MustContain)
	if err != nil {
		return researchdomain.TargetSearchIntentV2{}, 0, err
	}
	exclusions, err := catalogManagedSearchTermsV2(plan.Required.Exclusions)
	if err != nil {
		return researchdomain.TargetSearchIntentV2{}, 0, err
	}
	// ProductType is retained for provider filter proof, not a Shopify-only
	// title/head-noun gate. Relevance is assessed against the common criteria.
	productType := strings.TrimSpace(plan.ProductType)
	if productType == "" || containsNonLatinCatalogLetter(productType) {
		productType = query
	}
	exponent := workspaceCurrencyExponentV2(profile.Market.Currency)
	constraint, err := catalogResearchPriceConstraintFromProfileV2(profile, exponent)
	if err != nil {
		return researchdomain.TargetSearchIntentV2{}, 0, err
	}
	sourceHash := strings.TrimSpace(profile.TargetHash)
	if sourceHash == "" {
		sourceHash, err = shareddomain.CanonicalJSONHash(struct {
			TargetID string `json:"targetId"`
			Intent   string `json:"intent"`
			Category string `json:"category"`
		}{profile.TargetID, strings.ToLower(query), strings.TrimSpace(profile.Category)})
		if err != nil {
			return researchdomain.TargetSearchIntentV2{}, 0, err
		}
	}
	intent := researchdomain.TargetSearchIntentV2{
		ProductType:          productType,
		UseCaseTerms:         []string{},
		SoftLexicalTerms:     []string{},
		HardLexicalTerms:     hardLexicalTerms,
		Exclusions:           exclusions,
		Conditions:           plan.Required.Conditions,
		VerifiedCategories:   plan.Preferred.Categories,
		PriceConstraint:      constraint,
		MarketContext:        plan.Required.Market,
		ShippingCountry:      plan.Required.Market.Country,
		IntentSummaryEnglish: query,
		SourceProfileHash:    sourceHash,
	}
	if err := intent.Validate(); err != nil {
		return researchdomain.TargetSearchIntentV2{}, 0, fault.Wrap(
			err, fault.InvalidInput, "PHASE8_TARGET_SEARCH_PROFILE_INVALID", false,
		)
	}
	return intent, exponent, nil
}

func catalogManagedSearchTermsV2(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, fault.New(
			fault.ProviderRejected, "PHASE8_MANAGED_SEARCH_TERMS_INVALID", false,
		)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		tokens := catalogEnglishSearchTokensV2(value)
		if len(tokens) == 0 {
			return nil, fault.New(
				fault.ProviderRejected, "PHASE8_MANAGED_SEARCH_TERMS_INVALID", false,
			)
		}
		if containsNonLatinCatalogLetter(value) {
			return nil, fault.New(fault.ProviderRejected, "PHASE8_MANAGED_SEARCH_TERMS_INVALID", false)
		}
		result = append(result, strings.Join(tokens, " "))
	}
	return catalogUniqueStringsV2(result), nil
}

func catalogResearchPriceConstraintFromProfileV2(
	profile CatalogTargetSearchProfileV2,
	exponent uint8,
) (shareddomain.ResearchPriceConstraint, error) {
	if profile.MinimumPrice == nil && profile.MaximumPrice == nil {
		return shareddomain.NoResearchPriceConstraint(), nil
	}
	for _, bound := range []*shareddomain.Money{profile.MinimumPrice, profile.MaximumPrice} {
		if bound == nil {
			continue
		}
		if string(bound.Currency) != strings.ToUpper(strings.TrimSpace(profile.Market.Currency)) {
			return shareddomain.ResearchPriceConstraint{}, fault.New(
				fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false,
			)
		}
		if _, err := catalogMinorUnitsV2(*bound, exponent); err != nil {
			return shareddomain.ResearchPriceConstraint{}, err
		}
	}
	constraint, err := shareddomain.NewExplicitResearchPriceConstraint(
		profile.MinimumPrice, profile.MaximumPrice,
	)
	if err != nil {
		return shareddomain.ResearchPriceConstraint{}, fault.Wrap(
			err, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false,
		)
	}
	return constraint, nil
}

func catalogResearchPriceConstraintV2(
	minimumMinor, maximumMinor *int64,
	currency string,
) (shareddomain.ResearchPriceConstraint, uint8, error) {
	exponent := workspaceCurrencyExponentV2(currency)
	if minimumMinor == nil && maximumMinor == nil {
		return shareddomain.NoResearchPriceConstraint(), exponent, nil
	}
	minimum, err := catalogMoneyFromMinorV2(minimumMinor, exponent, currency)
	if err != nil {
		return shareddomain.ResearchPriceConstraint{}, 0, err
	}
	maximum, err := catalogMoneyFromMinorV2(maximumMinor, exponent, currency)
	if err != nil {
		return shareddomain.ResearchPriceConstraint{}, 0, err
	}
	constraint, err := shareddomain.NewExplicitResearchPriceConstraint(minimum, maximum)
	if err != nil {
		return shareddomain.ResearchPriceConstraint{}, 0, fault.Wrap(
			err, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false,
		)
	}
	return constraint, exponent, nil
}

func catalogMoneyFromMinorV2(
	minor *int64,
	exponent uint8,
	currency string,
) (*shareddomain.Money, error) {
	if minor == nil {
		return nil, nil
	}
	if *minor < 0 {
		return nil, fault.New(fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false)
	}
	digits := strconv.FormatInt(*minor, 10)
	if exponent > 0 {
		for len(digits) <= int(exponent) {
			digits = "0" + digits
		}
		point := len(digits) - int(exponent)
		digits = digits[:point] + "." + digits[point:]
	}
	money, err := shareddomain.NewMoney(digits, currency)
	if err != nil {
		return nil, fault.Wrap(err, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false)
	}
	return &money, nil
}

func workspaceCurrencyExponentV2(currency string) uint8 {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "BHD", "JOD", "KWD", "OMR", "TND":
		return 3
	case "CLP", "ISK", "JPY", "KRW", "VND":
		return 0
	default:
		return 2
	}
}

var catalogEnglishSearchTokenV2 = regexp.MustCompile(`[\p{Latin}\p{N}\p{M}]+(?:[-'][\p{Latin}\p{N}\p{M}]+)*`)

func catalogEnglishSearchTokensV2(value string) []string {
	return catalogEnglishSearchTokenV2.FindAllString(strings.ToLower(strings.TrimSpace(value)), -1)
}

func catalogUniqueStringsV2(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func catalogVerifiedCategoriesV2(category string) []string {
	// The legacy Target category is Vitlane's internal taxonomy and must not be
	// silently promoted to Shopify's reviewed taxonomy. Only an explicitly
	// namespaced provider category may enter the structured provider filter.
	const prefix = "shopify:"
	value := strings.TrimSpace(category)
	if !strings.HasPrefix(strings.ToLower(value), prefix) {
		return []string{}
	}
	value = strings.TrimSpace(value[len(prefix):])
	if value == "" {
		return []string{}
	}
	return []string{value}
}

func catalogEvidenceIDV2(planHash, productID string) string {
	hash, err := shareddomain.CanonicalJSONHash(struct {
		PlanHash  string `json:"planHash"`
		ProductID string `json:"productId"`
	}{planHash, strings.TrimSpace(productID)})
	if err != nil {
		return "catalog-observation:invalid"
	}
	return "catalog-observation:" + strings.TrimPrefix(hash, "0x")
}

func catalogLiveAssessmentV2(
	ranked RankedCandidateAssessmentV2,
) LiveCandidateAssessmentV2 {
	features := make([]string, 0, len(ranked.Assessment.Features))
	for _, feature := range ranked.Assessment.Features {
		features = append(features, feature.Text)
	}
	specifications := make([]string, 0, len(ranked.Assessment.Specifications))
	for _, specification := range ranked.Assessment.Specifications {
		specifications = append(specifications, specification.Text)
	}
	return LiveCandidateAssessmentV2{
		IntentPoint:    ranked.Assessment.IntentPoint.Text,
		Features:       features,
		Specifications: specifications,
	}
}

func catalogLookupMissingMediaV2(
	ctx context.Context,
	gateway *catalogRateLimitedCatalogGatewayV2,
	market CatalogMarketContextV2,
	products []CatalogProductObservation,
	messages *[]CatalogProviderMessage,
) error {
	inputs := make([]CatalogMediaLookupInput, 0, len(products))
	for _, product := range products {
		if len(product.Media) > 0 || product.Locator == nil {
			continue
		}
		identifier := ""
		if product.Locator.ProductURL != nil {
			identifier = product.Locator.ProductURL.CanonicalURL
		} else if product.Locator.MerchantVariant != nil {
			identifier = product.Locator.MerchantVariant.VariantID
		}
		if identifier != "" {
			inputs = append(inputs, CatalogMediaLookupInput{
				CorrelationKey: product.ProviderProductID,
				Identifier:     identifier,
			})
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	lookup, err := gateway.LookupMedia(ctx, CatalogMediaLookupRequest{
		Inputs: inputs,
		Context: CatalogBuyerContext{
			Country:  market.Country,
			Currency: market.Currency,
			Language: "en",
			Intent:   "fill missing candidate media",
		},
	})
	if err != nil {
		return err
	}
	if lookup.Outcome == CatalogOutcomeBusinessError {
		return fault.New(fault.ProviderRejected, string(CatalogFailureBusinessOutcomeV2), false)
	}
	*messages = append(*messages, lookup.Messages...)
	mediaByProduct := make(map[string][]CatalogMedia, len(lookup.Matches))
	for _, match := range lookup.Matches {
		mediaByProduct[match.CorrelationKey] = append([]CatalogMedia(nil), match.Media...)
	}
	for index := range products {
		if media := mediaByProduct[products[index].ProviderProductID]; len(media) > 0 {
			products[index].Media = media
		}
	}
	return nil
}

func catalogPlannedSearchMetricsV2(
	service *LiveCatalogReviewServiceV2,
	gateway *catalogRateLimitedCatalogGatewayV2,
	startedAt, completedAt time.Time,
) LiveCatalogReviewMetricsV2 {
	return LiveCatalogReviewMetricsV2{
		PolicyVersion:             "phase8-search-plan.v2",
		StartedAt:                 startedAt,
		CompletedAt:               completedAt,
		Duration:                  completedAt.Sub(startedAt),
		AICallCount:               0,
		ShopifyCallCount:          gateway.calls,
		LocalCallsUsed:            gateway.lastUsed,
		LocalCallsRemaining:       gateway.lastRemaining,
		LocalRateLimit:            service.config.MaximumCallsPerWindow,
		LocalRateWindow:           service.config.Window,
		ProviderCostStatus:        "NOT_REPORTED_BY_PROVIDER",
		ProviderBillingCredential: false,
		ExternalEffect:            "CATALOG_READ_ONLY",
	}
}

var _ CatalogGatewayV2 = (*catalogRateLimitedCatalogGatewayV2)(nil)
var _ CatalogProviderCallAdmissionV2 = (*catalogRateLimitedCatalogGatewayV2)(nil)

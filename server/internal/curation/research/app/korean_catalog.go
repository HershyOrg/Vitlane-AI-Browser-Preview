package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

// US providers observe USD even when the user's immutable research bounds
// were entered in KRW. Only this request-local projection is converted.
func (s *LiveCatalogReviewServiceV2) usProviderProfile(ctx context.Context, profile CatalogTargetSearchProfileV2) (CatalogTargetSearchProfileV2, error) {
	if profile.Market.Currency == "USD" {
		return profile, nil
	}
	if profile.Market.Currency != "KRW" {
		return profile, fault.New(fault.InvalidInput, "CATALOG_CURRENCY_UNSUPPORTED", false)
	}
	var rate *researchdomain.DailyExchangeRate
	if profile.MinimumPrice != nil || profile.MaximumPrice != nil {
		if s.exchangeRate != nil {
			view, err := s.exchangeRate.View(ctx)
			if err != nil {
				return profile, err
			}
			rate = view.Rate
		}
		if rate == nil {
			return profile, fault.New(fault.ProviderUnavailable, "CATALOG_EXCHANGE_RATE_UNAVAILABLE", true)
		}
	}
	convert := func(value *shareddomain.Money) (*shareddomain.Money, error) {
		if value == nil {
			return nil, nil
		}
		minor, err := catalogProfileMinorBoundV2(value, 0)
		if err != nil {
			return nil, err
		}
		usd, err := researchdomain.ConvertResearchMinor(*minor, "KRW", "USD", *rate, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		money, err := shareddomain.NewMoney(catalogMinorAmountV2(usd, 2), "USD")
		return &money, err
	}
	var err error
	profile.MinimumPrice, err = convert(profile.MinimumPrice)
	if err != nil {
		return profile, err
	}
	profile.MaximumPrice, err = convert(profile.MaximumPrice)
	if err != nil {
		return profile, err
	}
	profile.Market.Currency = "USD"
	return profile, nil
}

type ExternalCatalogSearchResult struct {
	NextProgress SourceProgressSet
	Observations []researchdomain.ExternalProductObservation
	Coverage     []SourceCoverage
	Calls        int
	// RejectedCount counts provider results that were not products: documents
	// matched by site search that are not product pages, and product numbers
	// whose public page no longer carries a product. Neither is a failure.
	RejectedCount int
}
type ExternalCatalogGateway interface {
	SearchExternalMalls(context.Context, KoreanSearchRequest) (ExternalCatalogSearchResult, error)
	Usage(context.Context, string, bool) (CatalogProviderUsage, error)
	Configured(string) bool
}

// KoreanSearchRequest is the Korean malls' translation of a Round's SearchPlan:
// the plan's Korean phrases (Query is the primary one, Seeds the whole ordered
// list the routes rotate through), the vertical that picks which specialised
// malls are asked directly, and each route's own position in each phrase.
type KoreanSearchRequest struct {
	CollectionBudget            DiscoveryCollectionBudget
	Query                       string
	Country                     string
	Vertical                    string
	Progress                    SourceProgressSet
	Seeds                       []string
	ExistingExternalProductKeys map[string]bool
	ExistingBrowserSourceCounts map[researchdomain.Source]int
	// UserID and AttemptKey identify who the paid Actor run belongs to and
	// which attempt already paid for it. Without them the Actor paths are not
	// asked at all, because an unattributable run cannot be deduplicated.
	UserID     string
	AttemptKey string
}

func (s *LiveCatalogReviewServiceV2) EnableKoreanCatalog(g ExternalCatalogGateway, fx *ExchangeRateService) {
	s.korean = g
	s.exchangeRate = fx
}

func (s *LiveCatalogReviewServiceV2) searchKoreanProducts(ctx context.Context, input CatalogWorkspaceSearchInputV2, profile CatalogTargetSearchProfileV2, plan researchdomain.SearchPlan) (LiveCatalogReviewResultV2, error) {
	if s.korean == nil {
		return LiveCatalogReviewResultV2{}, fault.New(fault.ProviderUnavailable, "KOREAN_CATALOG_NOT_CONFIGURED", false)
	}
	queries := plan.QueriesFor(researchdomain.SearchLanguageKorean)
	if len(queries) == 0 {
		return LiveCatalogReviewResultV2{}, fault.New(fault.InvalidInput, "CATALOG_QUERY_INVALID", false)
	}
	started := time.Now().UTC()
	external, err := s.korean.SearchExternalMalls(ctx, KoreanSearchRequest{
		CollectionBudget: DefaultDiscoveryCollectionBudget(), Query: queries[0], Seeds: queries, Country: string(plan.Required.Market.Country),
		Vertical: plan.Preferred.ProductVertical, Progress: input.Progress,
		ExistingExternalProductKeys: input.ExistingExternalProductKeys,
		ExistingBrowserSourceCounts: input.ExistingBrowserSourceCounts,
		UserID:                      input.UserID, AttemptKey: input.IdempotencyKey,
	})
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	result := LiveCatalogReviewResultV2{NextProgress: external.NextProgress, ProviderRejectedCount: external.RejectedCount, Search: CatalogProductSearchResult{Outcome: CatalogOutcomeSuccess, Products: []CatalogProductObservation{}, Messages: []CatalogProviderMessage{}}, CandidateAssessments: map[string]LiveCandidateAssessmentV2{}, Metrics: LiveCatalogReviewMetricsV2{StartedAt: started, CompletedAt: time.Now().UTC(), ExternalEffect: "CATALOG_READ_ONLY", SourceCoverage: external.Coverage}}
	ranks := map[string]int{}
	for _, observation := range external.Observations {
		if observation.Validate() != nil {
			result.ProviderRejectedCount++
			continue
		}
		product, err := CatalogProductFromExternalObservation(observation)
		if err != nil {
			result.ProviderRejectedCount++
			continue
		}
		product.DiscoveryRoute = observation.Provenance.APIProvider + ":" + observation.Provenance.APIProduct
		rankKey := string(product.Source()) + ":" + product.DiscoveryRoute
		product.ProviderOrder = ranks[rankKey]
		ranks[rankKey]++
		result.Search.Products = append(result.Search.Products, product)
	}
	result.CandidateEligibleCount = len(result.Search.Products)
	result.Metrics.CompletedAt = time.Now().UTC()
	result.Metrics.Duration = result.Metrics.CompletedAt.Sub(started)
	for i := range result.Metrics.SourceCoverage {
		count := 0
		for _, p := range result.Search.Products {
			if p.Source() == result.Metrics.SourceCoverage[i].Source {
				count++
			}
		}
		result.Metrics.SourceCoverage[i].CandidateCount = count
	}
	return result, nil
}

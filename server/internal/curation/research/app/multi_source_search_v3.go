package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"slices"
	"strings"
	"unicode"
)

type SourceCoverage struct {
	Source         researchdomain.Source `json:"source"`
	Status         string                `json:"status"`
	ReasonCode     string                `json:"reasonCode,omitempty"`
	CandidateCount int                   `json:"candidateCount"`
}

func (service *LiveCatalogReviewServiceV2) EnableAmazon(gateway AmazonCatalogGateway) {
	service.amazon = gateway
}

// searchWorkspacePlanForProfileV2 executes every registered adapter through the
// same page contract. Adapter order and completion order are not admission order.
func (service *LiveCatalogReviewServiceV2) searchWorkspacePlanForProfileV2(ctx context.Context, input CatalogWorkspaceSearchInputV2, profile CatalogTargetSearchProfileV2) (LiveCatalogReviewResultV2, error) {
	if profile.Market.Country == "US" && profile.Market.Currency != "USD" {
		var err error
		profile, err = service.usProviderProfile(ctx, profile)
		if err != nil {
			return LiveCatalogReviewResultV2{}, err
		}
	}
	plan, err := BuildSearchPlanV3(input, profile)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	return service.executeDiscovery(ctx, DiscoveryRequest{Input: input, Profile: profile, Plan: plan})
}

// The Amazon driver only translates and validates provider facts. Candidate
// price/wording policy and merging are owned by executeDiscovery.
func (service *LiveCatalogReviewServiceV2) searchAmazonPage(ctx context.Context, request DiscoveryRequest) (LiveCatalogReviewResultV2, error) {
	queries := request.Plan.QueriesFor(researchdomain.SearchLanguageEnglish)
	if len(queries) == 0 {
		return LiveCatalogReviewResultV2{}, fault.New(fault.InvalidInput, "RESEARCH_INPUT_NORMALIZATION_REQUIRED", false)
	}
	progress := SearchProgressFor(request.Input.Progress, "AMAZON", queries[0], queries)
	if !slices.Contains(queries, progress.Query) {
		progress = SearchProgressFor(nil, "AMAZON", queries[0], queries)
	}
	found, err := service.amazon.SearchAmazon(ctx, AmazonSearchRequest{Query: progress.Query, Page: progress.Page, Marketplace: "US"})
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	result := emptyDiscoveryResult()
	result.NextProgress["AMAZON"] = AdvanceSearchProgress(progress, queries, "", found.HasNextPage, true)
	// Older test doubles do not supply raw pagination. Live adapters always do.
	if !found.PaginationKnown {
		result.NextProgress["AMAZON"] = AdvanceSearchProgress(progress, queries, "", len(found.Products) > 0, true)
	}
	for _, product := range found.Products {
		if product.Source() != researchdomain.SourceAmazon || product.ProductRef().Validate() != nil || product.VariantObservation == nil || product.VariantObservation.Validate() != nil {
			result.ProviderRejectedCount++
			continue
		}
		result.Search.Products = append(result.Search.Products, product)
	}
	result.Search.RawCount = found.RawCount
	result.Search.Partial = found.Partial
	return result, nil
}
func amazonFailureReason(err error) string {
	for _, code := range []string{"AMAZON_SOURCE_DISABLED", "AMAZON_AUTH_REJECTED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_QUOTA_UNCONFIRMED", "AMAZON_RATE_LIMITED", "AMAZON_TIMEOUT", "AMAZON_NETWORK_FAILED", "AMAZON_UPSTREAM_FAILED", "AMAZON_SCHEMA_MISMATCH", "AMAZON_ASIN_MISMATCH", "AMAZON_PRODUCT_UNRESOLVED", "AMAZON_MARKET_UNSUPPORTED"} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return "AMAZON_INTERNAL_ERROR"
}

func containsNonLatinCatalogLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return true
		}
	}
	return false
}

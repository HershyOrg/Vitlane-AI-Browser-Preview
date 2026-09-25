package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const DiscoveryPolicyVersion = "research-discovery.v4"

// Descriptors contain provider capabilities, never candidate admission policy.
// A gateway declares its catalog language; the query generator groups equal
// policy IDs before generating phrases. Countries are research markets.
type DiscoveryDescriptor struct {
	ID             string
	Countries      []string
	LanguagePolicy string
	Language       researchdomain.SearchLanguage
}
type DiscoveryDescriptorProvider interface{ DescribeDiscovery() DiscoveryDescriptor }
type DiscoveryRequest struct {
	Input   CatalogWorkspaceSearchInputV2
	Profile CatalogTargetSearchProfileV2
	Plan    researchdomain.SearchPlan
}

// The page contract preserves usable products, next state and partial failures
// independently. It reuses the existing response-scoped observation envelope.
type DiscoveryPage = LiveCatalogReviewResultV2
type DiscoveryAdapter interface {
	DescribeDiscovery() DiscoveryDescriptor
	SearchPage(context.Context, DiscoveryRequest) (DiscoveryPage, error)
}
type discoveryBinding struct {
	descriptor DiscoveryDescriptor
	search     func(context.Context, DiscoveryRequest) (DiscoveryPage, error)
}

func (a discoveryBinding) DescribeDiscovery() DiscoveryDescriptor { return a.descriptor }
func (a discoveryBinding) SearchPage(ctx context.Context, r DiscoveryRequest) (DiscoveryPage, error) {
	return a.search(ctx, r)
}

func EnglishDiscoveryDescriptor(id string) DiscoveryDescriptor {
	return DiscoveryDescriptor{ID: id, Countries: []string{"US"}, LanguagePolicy: "english-latin.v1", Language: researchdomain.SearchLanguageEnglish}
}
func KoreanDiscoveryDescriptor() DiscoveryDescriptor {
	return DiscoveryDescriptor{ID: "KOREAN_CATALOG", Countries: []string{"KR"}, LanguagePolicy: "korean-mixed.v1", Language: researchdomain.SearchLanguageKorean}
}
func discoveryDescriptor(gateway any, fallback DiscoveryDescriptor) DiscoveryDescriptor {
	if declared, ok := gateway.(DiscoveryDescriptorProvider); ok {
		return declared.DescribeDiscovery()
	}
	// Compatibility for in-process test gateways implementing the old transport
	// port. Production adapters and the accounting decorator declare capabilities.
	return fallback
}

// RegisterDiscoveryAdapter is a composition-time extension point. A new source
// does not add another branch to executeDiscovery or change common filters.
func (s *LiveCatalogReviewServiceV2) RegisterDiscoveryAdapter(adapter DiscoveryAdapter) {
	s.discoveryAdapters = append(s.discoveryAdapters, adapter)
}
func (s *LiveCatalogReviewServiceV2) catalogDiscoveryAdapters() []DiscoveryAdapter {
	adapters := []DiscoveryAdapter{}
	if s.gateway != nil {
		adapters = append(adapters, discoveryBinding{discoveryDescriptor(s.gateway, EnglishDiscoveryDescriptor("SHOPIFY")), func(ctx context.Context, r DiscoveryRequest) (DiscoveryPage, error) {
			return s.searchShopifyPlanForProfileV2(ctx, r.Input, r.Profile, r.Plan)
		}})
	}
	if s.amazon != nil {
		adapters = append(adapters, discoveryBinding{discoveryDescriptor(s.amazon, EnglishDiscoveryDescriptor("AMAZON")), s.searchAmazonPage})
	}
	if s.korean != nil {
		adapters = append(adapters, discoveryBinding{discoveryDescriptor(s.korean, KoreanDiscoveryDescriptor()), func(ctx context.Context, r DiscoveryRequest) (DiscoveryPage, error) {
			return s.searchKoreanProducts(ctx, r.Input, r.Profile, r.Plan)
		}})
	}
	return append(adapters, s.discoveryAdapters...)
}
func (s *LiveCatalogReviewServiceV2) DiscoveryRequirements(country string) []DiscoveryDescriptor {
	requirements := []DiscoveryDescriptor{}
	seen := map[string]bool{}
	for _, a := range s.catalogDiscoveryAdapters() {
		d := a.DescribeDiscovery()
		if slices.Contains(d.Countries, country) && !seen[d.LanguagePolicy] {
			seen[d.LanguagePolicy] = true
			requirements = append(requirements, d)
		}
	}
	sort.Slice(requirements, func(i, j int) bool { return requirements[i].LanguagePolicy < requirements[j].LanguagePolicy })
	return requirements
}
func emptyDiscoveryResult() DiscoveryPage {
	return DiscoveryPage{NextProgress: SourceProgressSet{}, Search: CatalogProductSearchResult{Outcome: CatalogOutcomeSuccess, Products: []CatalogProductObservation{}}, CandidateAssessments: map[string]LiveCandidateAssessmentV2{}}
}
func (s *LiveCatalogReviewServiceV2) executeDiscovery(ctx context.Context, r DiscoveryRequest) (DiscoveryPage, error) {
	started := s.discoveryNow()
	adapters := []DiscoveryAdapter{}
	for _, a := range s.catalogDiscoveryAdapters() {
		if slices.Contains(a.DescribeDiscovery().Countries, r.Profile.Market.Country) {
			adapters = append(adapters, a)
		}
	}
	if len(adapters) == 0 {
		return DiscoveryPage{}, fault.New(fault.ProviderUnavailable, "CATALOG_MARKET_UNAVAILABLE", false)
	}
	// Results are indexed only for collection; candidate ordering is a separate,
	// deterministic policy over source identities and provider relevance ranks.
	type outcome struct {
		plan researchdomain.SearchPlan
		page DiscoveryPage
		err  error
	}
	outcomes := make([]outcome, len(adapters))
	var wg sync.WaitGroup
	requests := make([]DiscoveryRequest, len(adapters))
	for index, adapter := range adapters {
		local := r
		descriptor := adapter.DescribeDiscovery()
		for _, projection := range r.Input.QueryProjections {
			if projection.Policy == descriptor.LanguagePolicy {
				local.Input.Search.Query = projection.Query
				local.Input.QuerySeeds = projection.Seeds
				local.Input.Search.PreferredExclusions = projection.MustExclude
				local.Input.Search.PreferredTerms = projection.MustInclude
				local.Profile.NormalizedIntent = projection.Query
				local.Input.SearchLanguage = descriptor.Language
				projected, err := BuildSearchPlanV3(local.Input, local.Profile)
				if err != nil {
					return DiscoveryPage{}, err
				}
				local.Plan = projected
				break
			}
		}
		outcomes[index].plan = local.Plan
		requests[index] = local
	}
	for index, adapter := range adapters {
		wg.Add(1)
		go func(index int, adapter DiscoveryAdapter, local DiscoveryRequest) {
			defer wg.Done()
			outcomes[index].page, outcomes[index].err = adapter.SearchPage(ctx, local)
		}(index, adapter, requests[index])
	}
	wg.Wait()
	result := emptyDiscoveryResult()
	successes := 0
	var firstError error
	for i, o := range outcomes {
		d := adapters[i].DescribeDiscovery()
		if o.err != nil {
			if firstError == nil {
				firstError = o.err
			}
			reason := "CATALOG_SOURCE_FAILED"
			if f, ok := fault.As(o.err); ok {
				reason = f.Reason
			} else if d.ID == "AMAZON" {
				reason = amazonFailureReason(o.err)
			}
			status := "FAILED"
			if CatalogAdmissionTransient(reason) || AmazonAdmissionUnavailable(reason) || reason == "CATALOG_API_DISABLED" || reason == "CATALOG_API_NOT_CONFIGURED" {
				status = "SKIPPED"
			}
			result.Search.Partial = true
			if !o.page.Search.Partial {
				result.Metrics.SourceCoverage = append(result.Metrics.SourceCoverage, SourceCoverage{Source: researchdomain.Source(d.ID), Status: status, ReasonCode: reason})
				continue
			}
			o.page.Metrics.SourceCoverage = append(o.page.Metrics.SourceCoverage, SourceCoverage{Source: researchdomain.Source(d.ID), Status: "PARTIAL", ReasonCode: reason})
		}
		successes++
		page := o.page
		for j := range page.Search.Products {
			page.Search.Products[j].SearchPolicy = d.LanguagePolicy
		}
		result.Search.RawCount += max(page.Search.RawCount, len(page.Search.Products)+page.Search.RejectedCount+page.ProviderRejectedCount)
		result.Search.Products = append(result.Search.Products, page.Search.Products...)
		result.Search.Messages = append(result.Search.Messages, page.Search.Messages...)
		result.Search.Partial = result.Search.Partial || page.Search.Partial
		result.ProviderRejectedCount += page.ProviderRejectedCount + page.Search.RejectedCount
		result.DiscardedNoLocatorCount += page.DiscardedNoLocatorCount
		for k, v := range page.NextProgress {
			result.NextProgress[k] = v
		}
		for k, v := range page.CandidateAssessments {
			result.CandidateAssessments[k] = v
		}
		result.Metrics.ShopifyCallCount += page.Metrics.ShopifyCallCount
		result.Metrics.LocalCallsUsed += page.Metrics.LocalCallsUsed
		if page.Metrics.LocalRateLimit > 0 {
			result.Metrics.LocalCallsRemaining = page.Metrics.LocalCallsRemaining
			result.Metrics.LocalRateLimit = page.Metrics.LocalRateLimit
			result.Metrics.LocalRateWindow = page.Metrics.LocalRateWindow
		}
		result.Metrics.ProviderBillingCredential = result.Metrics.ProviderBillingCredential || page.Metrics.ProviderBillingCredential
		if page.Metrics.ProviderCostStatus != "" {
			result.Metrics.ProviderCostStatus = page.Metrics.ProviderCostStatus
		}
		if len(page.Metrics.SourceCoverage) > 0 {
			result.Metrics.SourceCoverage = append(result.Metrics.SourceCoverage, page.Metrics.SourceCoverage...)
		} else {
			status := "SUCCEEDED"
			if len(page.Search.Products) == 0 {
				status = "EMPTY"
			}
			if page.Search.Partial {
				status = "PARTIAL"
			}
			result.Metrics.SourceCoverage = append(result.Metrics.SourceCoverage, SourceCoverage{Source: researchdomain.Source(d.ID), Status: status})
		}
	}
	if successes == 0 {
		return result, firstError
	}
	var rate *researchdomain.DailyExchangeRate
	rateRead := false
	convert := func(amount int64, currency string) (int64, bool) {
		if !rateRead {
			rateRead = true
			if s.exchangeRate != nil {
				if v, e := s.exchangeRate.View(ctx); e == nil {
					rate = v.Rate
				}
			}
		}
		if rate == nil {
			return 0, false
		}
		converted, e := researchdomain.ConvertResearchMinor(amount, currency, string(r.Plan.Required.Market.Currency), *rate, s.discoveryNow())
		return converted, e == nil
	}
	admitted := []CatalogProductObservation{}
	for _, p := range result.Search.Products {
		plan := r.Plan
		for index, adapter := range adapters {
			if adapter.DescribeDiscovery().LanguagePolicy == p.SearchPolicy {
				plan = outcomes[index].plan
				break
			}
		}
		if AdmitPlannedObservationV3(plan, p, convert) != "" {
			result.ProviderRejectedCount++
			continue
		}
		admitted = append(admitted, p)
	}
	result.Search.Products = mergeDiscoveryProducts(admitted, r.Input.IdempotencyKey)
	result.DiscoveryDuplicateCount = len(admitted) - len(result.Search.Products)
	result.CandidateEligibleCount = len(result.Search.Products)
	updateDiscoveryCoverage(&result)
	result.Metrics.PolicyVersion = DiscoveryPolicyVersion
	result.Metrics.ProviderCountry = r.Profile.Market.Country
	result.Metrics.ProviderQuery = r.Plan.Queries[0].Text
	result.Metrics.StartedAt = started
	result.Metrics.CompletedAt = s.discoveryNow()
	result.Metrics.Duration = result.Metrics.CompletedAt.Sub(started)
	result.Metrics.ExternalEffect = "CATALOG_READ_ONLY"
	return result, nil
}
func updateDiscoveryCoverage(r *DiscoveryPage) {
	for i := range r.Metrics.SourceCoverage {
		c := &r.Metrics.SourceCoverage[i]
		c.CandidateCount = 0
		for _, p := range r.Search.Products {
			if p.Source() == c.Source {
				c.CandidateCount++
			}
		}
	}
}
func discoveryIdentity(p CatalogProductObservation) string {
	if p.ProductRef().Validate() == nil {
		return p.ProductRef().IdentityKey()
	}
	return string(p.Source()) + ":" + p.ProviderProductID
}
func discoveryHash(seed, value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(seed+"|"+value)))
}
func mergeDiscoveryProducts(products []CatalogProductObservation, seed string) []CatalogProductObservation {
	// Resolve duplicate evidence independently of arrival order. Prefer a known
	// price and richer valid facts; never combine different variants' prices.
	unique := map[string]CatalogProductObservation{}
	quality := func(p CatalogProductObservation) int {
		n := len(p.Media)
		if _, _, _, known := plannedObservedPriceV3(p); known {
			n += 100
		}
		if p.Description.Plain != "" {
			n += 10
		}
		return n
	}
	stable := func(p CatalogProductObservation) string { raw, _ := json.Marshal(p); return string(raw) }
	for _, p := range products {
		key := discoveryIdentity(p)
		old, exists := unique[key]
		if !exists || quality(p) > quality(old) || quality(p) == quality(old) && stable(p) < stable(old) {
			unique[key] = p
		}
	}
	routes := map[string]map[string][]CatalogProductObservation{}
	for _, p := range unique {
		source := string(p.Source())
		if routes[source] == nil {
			routes[source] = map[string][]CatalogProductObservation{}
		}
		routes[source][p.DiscoveryRoute] = append(routes[source][p.DiscoveryRoute], p)
	}
	interleave := func(queues map[string][]CatalogProductObservation) []CatalogProductObservation {
		keys := make([]string, 0, len(queues))
		for key := range queues {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return discoveryHash(seed, keys[i]) < discoveryHash(seed, keys[j]) })
		merged := []CatalogProductObservation{}
		for rank := 0; ; rank++ {
			added := false
			for _, key := range keys {
				if rank < len(queues[key]) {
					merged = append(merged, queues[key][rank])
					added = true
				}
			}
			if !added {
				break
			}
		}
		return merged
	}
	sources := map[string][]CatalogProductObservation{}
	for source, queues := range routes {
		for route, queue := range queues {
			sort.Slice(queue, func(i, j int) bool {
				if queue[i].ProviderOrder != queue[j].ProviderOrder {
					return queue[i].ProviderOrder < queue[j].ProviderOrder
				}
				return discoveryHash(seed, discoveryIdentity(queue[i])) < discoveryHash(seed, discoveryIdentity(queue[j]))
			})
			queues[route] = queue
		}
		sources[source] = interleave(queues)
	}
	return interleave(sources)
}

func (s *LiveCatalogReviewServiceV2) discoveryNow() time.Time {
	if s.clock != nil {
		return s.clock.Now()
	}
	return time.Now()
}

// Collection limits protect actual external requests, unlike candidate caps.
type DiscoveryCollectionBudget struct{ ProductDetails, HTMLDetails, ActorItems int }

func DefaultDiscoveryCollectionBudget() DiscoveryCollectionBudget {
	return DiscoveryCollectionBudget{ProductDetails: 4, HTMLDetails: 2, ActorItems: 5}
}
func (b DiscoveryCollectionBudget) Validated() DiscoveryCollectionBudget {
	defaults := DefaultDiscoveryCollectionBudget()
	if b.ProductDetails <= 0 {
		b.ProductDetails = defaults.ProductDetails
	}
	if b.HTMLDetails <= 0 {
		b.HTMLDetails = defaults.HTMLDetails
	}
	if b.ActorItems <= 0 {
		b.ActorItems = defaults.ActorItems
	}
	b.ActorItems = min(b.ActorItems, defaults.ActorItems)
	return b
}

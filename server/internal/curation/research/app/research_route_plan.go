package app

import (
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"time"
)

// ResearchProviderResources is the read model used by route policy. It carries
// no SDK, database or scheduler dependency. Admission reserves against the same
// constraints immediately before each external call.
type ResearchProviderResources map[string]ProviderResourceState

type ResearchRoutePlan struct {
	Routes       []ResearchRoute
	Alternatives map[string][]ResearchRoute
}

func ResearchRouteAPIIDs(vertical string, actorAllowed bool) []string {
	ids := []string{"OWN_PRODUCT"}
	seen := map[string]bool{"OWN_PRODUCT": true}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, mall := range researchdomain.KoreanMallsToAsk(researchdomain.Vertical(vertical)) {
		if mall.BrowserSearch {
			add("BROWSER_MERCHANT")
		}
		if mall.Detail == researchdomain.DetailActor && !actorAllowed {
			continue
		}
		add(mall.DetailAPI)
		if mall.Detail == researchdomain.DetailPublicHTML {
			for _, id := range []string{"NAVER_WEBKR", "OWN_WEB", "SERP_GOOGLE"} {
				add(id)
			}
		}
	}
	return ids
}

// PlanResearchRoutes owns eligibility, optional enrichment, route alternatives
// and weighted ordering. Adapters only collect resources and execute the plan.
func PlanResearchRoutes(seed, vertical string, actorAllowed bool, resources ResearchProviderResources, now time.Time) ResearchRoutePlan {
	state := func(id string) ProviderResourceState {
		if s, ok := resources[id]; ok {
			return s
		}
		return ProviderResourceState{Reason: "CATALOG_API_NOT_CONFIGURED", CostClass: "FREE"}
	}
	// The source-independent OWN call is executed once for all registered malls.
	routes := []ResearchRoute{{ID: "OWN_PRODUCT", Source: "CROSS_MALL", APIIDs: []string{"OWN_PRODUCT"}, Resources: state("OWN_PRODUCT")}}
	alternatives := map[string][]ResearchRoute{}
	for _, mall := range researchdomain.KoreanMallsToAsk(researchdomain.Vertical(vertical)) {
		browserState := ProviderResourceState{}
		if mall.BrowserSearch {
			browserState = state("BROWSER_MERCHANT")
		}
		if mall.Detail == researchdomain.DetailActor && !actorAllowed &&
			(!mall.BrowserSearch || !browserState.CanStart) {
			continue
		}
		candidates := []ResearchRoute{}
		if mall.BrowserSearch {
			candidates = append(candidates, ResearchRoute{
				ID: "BROWSER_MERCHANT:" + string(mall.Source), Source: string(mall.Source),
				Specialized: len(mall.Verticals) > 0, APIIDs: []string{"BROWSER_MERCHANT"},
				Resources: browserState,
			})
		}
		if mall.Detail == researchdomain.DetailPublicHTML {
			for _, id := range []string{"NAVER_WEBKR", "OWN_WEB", "SERP_GOOGLE"} {
				detailState := state(mall.DetailAPI)
				combined := state(id)
				apiIDs := []string{id}
				if detailState.CanStart {
					combined = CombineRouteResources(now, combined, detailState)
					apiIDs = append(apiIDs, mall.DetailAPI)
				}

				candidates = append(candidates, ResearchRoute{ID: id + ":" + string(mall.Source), Source: string(mall.Source), Specialized: len(mall.Verticals) > 0, APIIDs: apiIDs, Resources: combined})
			}
		} else if mall.Detail != researchdomain.DetailActor || actorAllowed {
			candidates = append(candidates, ResearchRoute{ID: mall.DetailAPI, Source: string(mall.Source), Specialized: len(mall.Verticals) > 0, APIIDs: []string{mall.DetailAPI}, Resources: state(mall.DetailAPI)})
		}
		ranked := OrderResearchRoutes(seed, candidates)
		// When the local ephemeral browser is healthy, use it as the first
		// discovery path. It searches the merchant's own public UI and keeps
		// paid/API routes as one bounded recovery path.
		primary := 0
		for i, route := range ranked {
			if route.ID == "BROWSER_MERCHANT:"+string(mall.Source) && route.Resources.CanStart {
				primary = i
				break
			}
		}
		routes = append(routes, ranked[primary])
		for i, r := range ranked {
			if i == primary {
				continue
			}
			if r.Resources.CanStart {
				alternatives[r.Source] = append(alternatives[r.Source], r)
			}
		}
	}
	return ResearchRoutePlan{Routes: OrderResearchRoutes(seed, routes), Alternatives: alternatives}
}

// RouteDeferralWait decides whether a Round should wait instead of starting
// with most of its routes blocked. When less than half of the plan's fit-cost
// weight can start now and the earliest blocked route becomes ready within
// MaximumRouteDeferral, the Round is deferred until then: starting it would
// only skip the blocked malls and narrow the result, while a short wait keeps
// them. Permanently blocked routes (disabled, unconfigured, out of quota) have
// no ready time and never cause a wait.
const MaximumRouteDeferral = 90 * time.Second

func RouteDeferralWait(routes []ResearchRoute, now time.Time) (time.Duration, bool) {
	if len(routes) == 0 {
		return 0, false
	}
	total, startable := 0.0, 0.0
	var earliest *time.Time
	for _, route := range routes {
		weight := 1.0
		if route.Specialized {
			weight = 2
		}
		switch route.Resources.CostClass {
		case "FREE":
			weight *= 3
		case "INCLUDED":
			weight *= 2
		}
		total += weight
		if route.Resources.CanStart {
			startable += weight
			continue
		}
		if at := route.Resources.ReadyAt; at != nil && at.After(now) {
			if earliest == nil || at.Before(*earliest) {
				ready := *at
				earliest = &ready
			}
		}
	}
	if earliest == nil || startable*2 >= total {
		return 0, false
	}
	wait := earliest.Sub(now)
	if wait > MaximumRouteDeferral {
		return 0, false
	}
	if wait < 2*time.Second {
		wait = 2 * time.Second
	}
	return wait, true
}

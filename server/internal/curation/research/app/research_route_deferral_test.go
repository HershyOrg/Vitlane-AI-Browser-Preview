package app

import (
	"testing"
	"time"
)

func TestRouteDeferralWaitsOnlyWhenMostRoutesAreBlockedForAShortTime(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	soon, later := now.Add(10*time.Second), now.Add(40*time.Second)
	free := ProviderResourceState{CanStart: true, CostClass: "FREE"}
	held := func(at time.Time) ProviderResourceState {
		return ProviderResourceState{CanStart: false, CostClass: "FREE", Reason: "CATALOG_API_RATE_LIMITED", ReadyAt: &at}
	}
	routes := []ResearchRoute{
		{ID: "OWN_PRODUCT", Resources: ProviderResourceState{CanStart: true, CostClass: "INCLUDED"}},
		{ID: "NAVER_WEBKR:ELEVENST", Specialized: false, Resources: held(later)},
		{ID: "APIFY_MUSINSA", Specialized: true, Resources: ProviderResourceState{CanStart: false, CostClass: "METERED", Reason: "CATALOG_API_RATE_LIMITED", ReadyAt: &soon}},
		{ID: "APIFY_29CM", Specialized: true, Resources: ProviderResourceState{CanStart: false, CostClass: "METERED", Reason: "CATALOG_API_RATE_LIMITED", ReadyAt: &later}},
	}
	// Startable weight 2 (INCLUDED general) against 3 + 2 + 2 blocked: wait
	// for the earliest ready route, ten seconds away.
	if wait, ok := RouteDeferralWait(routes, now); !ok || wait != 10*time.Second {
		t.Fatalf("wait=%s ok=%v", wait, ok)
	}
	// With the specialised malls startable the plan may go: more than half
	// of the weight can start.
	routes[2].Resources, routes[3].Resources = free, free
	if _, ok := RouteDeferralWait(routes, now); ok {
		t.Fatal("a mostly startable plan must not wait")
	}
	// A permanently blocked route (no ready time) is not worth waiting for.
	routes[2].Resources = ProviderResourceState{CanStart: false, Reason: "CATALOG_API_DISABLED"}
	routes[3].Resources = ProviderResourceState{CanStart: false, Reason: "CATALOG_QUOTA_EXHAUSTED"}
	routes[1].Resources = ProviderResourceState{CanStart: false, Reason: "CATALOG_API_NOT_CONFIGURED"}
	if _, ok := RouteDeferralWait(routes, now); ok {
		t.Fatal("permanent blocks never defer")
	}
	// A ready time beyond the cap is a skip, not a wait.
	far := now.Add(5 * time.Minute)
	routes[1].Resources, routes[2].Resources, routes[3].Resources = held(far), held(far), held(far)
	if _, ok := RouteDeferralWait(routes, now); ok {
		t.Fatal("a five-minute wait exceeds the deferral cap")
	}
	// The floor keeps a nearly ready route from spinning the job.
	routes[1].Resources, routes[2].Resources, routes[3].Resources = held(now.Add(time.Millisecond)), held(later), held(later)
	if wait, ok := RouteDeferralWait(routes, now); !ok || wait != 2*time.Second {
		t.Fatalf("floor: wait=%s ok=%v", wait, ok)
	}
	if _, ok := RouteDeferralWait(nil, now); ok {
		t.Fatal("no routes, no wait")
	}
}

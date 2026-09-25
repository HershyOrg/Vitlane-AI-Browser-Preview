package app

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestRouteResourcesComposeSharedMaximum(t *testing.T) {
	now := time.Now()
	shared := ResourceConstraint{ID: "apify:account", Kind: "BUDGET", Used: 70, Limit: 100}
	a := ComposeProviderResources("METERED", now, []ResourceConstraint{{ID: "actor:a", Used: 40, Limit: 100}, shared})
	b := ComposeProviderResources("METERED", now, []ResourceConstraint{{ID: "actor:b", Used: 10, Limit: 100}, shared})
	combined := CombineRouteResources(now, a, b)
	if a.Pressure != .7 || b.Pressure != .7 || combined.Pressure != .7 || len(combined.Constraints) != 3 {
		t.Fatalf("%+v", combined)
	}
	shared.Used = 75
	b = ComposeProviderResources("METERED", now, []ResourceConstraint{{ID: "actor:b", Used: 10, Limit: 100}, shared})
	if b.Pressure != .75 {
		t.Fatal(b)
	}
}
func TestRouteWeightedPreferenceAndDeterministicReplay(t *testing.T) {
	routes := []ResearchRoute{
		{ID: "free", Resources: ProviderResourceState{CanStart: true, CostClass: "FREE"}},
		{ID: "paid", Resources: ProviderResourceState{CanStart: true, CostClass: "METERED"}},
		{ID: "disabled", Resources: ProviderResourceState{CanStart: false, CostClass: "FREE"}},
	}
	free := 0
	for i := 0; i < 10000; i++ {
		ranked := OrderResearchRoutes(fmt.Sprint(i), routes)
		if ranked[0].ID == "free" {
			free++
		}
		if ranked[2].ID != "disabled" {
			t.Fatal(ranked)
		}
	}
	if free < 7200 || free > 7800 {
		t.Fatalf("free picks=%d", free)
	}
	if !reflect.DeepEqual(OrderResearchRoutes("same", routes), OrderResearchRoutes("same", routes)) {
		t.Fatal("unstable")
	}
	routes[0].Resources.Pressure = .95
	for _, r := range OrderResearchRoutes("same", routes) {
		if r.ID == "free" && r.Weight > .31 {
			t.Fatal(r)
		}
	}
}
func TestResourceGateNotBypassedByPressureFloor(t *testing.T) {
	now := time.Now()
	at := now.Add(time.Minute)
	s := ComposeProviderResources("FREE", now, []ResourceConstraint{{ID: "rate", Used: 2, Limit: 2, Reason: "RATE", ReadyAt: &at}})
	if s.CanStart || s.Pressure != 1 || s.ReadyAt == nil {
		t.Fatal(s)
	}
	s = ComposeProviderResources("FREE", now, append(s.Constraints, ResourceConstraint{ID: "off", Reason: "OFF"}))
	if s.ReadyAt != nil {
		t.Fatal("permanent gate advertised a wakeup")
	}
}

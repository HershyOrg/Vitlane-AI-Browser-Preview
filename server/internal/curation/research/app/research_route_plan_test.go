package app

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestResearchRoutesChooseSpecialistsAndGeneralMalls(t *testing.T) {
	resources := ResearchProviderResources{}
	for _, api := range KoreanCatalogAPIs {
		resources[api.ID] = ProviderResourceState{CanStart: true, CostClass: "FREE"}
	}
	for _, tc := range []struct {
		vertical string
		want     []string
	}{
		{"FASHION", []string{"CROSS_MALL", "ELEVENST", "GMARKET", "MUSINSA", "TWENTYNINECM", "ZIGZAG"}},
		{"FOOD", []string{"CROSS_MALL", "ELEVENST", "GMARKET", "KURLY"}},
		{"LIVING", []string{"CROSS_MALL", "DAISOMALL", "ELEVENST", "GMARKET", "LOTTEON", "TWENTYNINECM"}},
		{"ELECTRONICS", []string{"CROSS_MALL", "ELEVENST", "GMARKET", "LOTTEON"}},
		{"BEAUTY", []string{"CROSS_MALL", "ELEVENST", "GMARKET", "LOTTEON"}},
		{"GENERAL", []string{"CROSS_MALL", "ELEVENST", "GMARKET"}},
		{"unknown", []string{"CROSS_MALL", "ELEVENST", "GMARKET"}},
	} {
		plan := PlanResearchRoutes("attempt", tc.vertical, true, resources, time.Now())
		got := []string{}
		for _, route := range plan.Routes {
			got = append(got, route.Source)
		}
		sort.Strings(got)
		sort.Strings(tc.want)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.vertical, got, tc.want)
		}
	}
}
func TestResearchRoutesKeepDiscoveryWhenOptionalDetailUnavailable(t *testing.T) {
	resources := ResearchProviderResources{"NAVER_WEBKR": {CanStart: true, CostClass: "FREE"}, "ELEVENST_HTML": {Reason: "CATALOG_API_DISABLED"}}
	plan := PlanResearchRoutes("attempt", "GENERAL", false, resources, time.Now())
	for _, route := range plan.Routes {
		if route.Source == "ELEVENST" && (!route.Resources.CanStart || len(route.APIIDs) != 1 || route.APIIDs[0] != "NAVER_WEBKR") {
			t.Fatal(route)
		}
		if route.ID == "APIFY_GMARKET" {
			t.Fatal("unconfigured actor was planned")
		}
	}
}

func TestResearchRoutesPreferConfiguredMerchantBrowserAndKeepOneRecoveryPath(t *testing.T) {
	resources := ResearchProviderResources{}
	for _, api := range KoreanCatalogAPIs {
		resources[api.ID] = ProviderResourceState{CanStart: true, CostClass: "FREE"}
	}
	plan := PlanResearchRoutes("browser-first", "FASHION", false, resources, time.Now())
	found := map[string]bool{}
	for _, route := range plan.Routes {
		if route.Source == "ELEVENST" || route.Source == "MUSINSA" || route.Source == "ZIGZAG" {
			found[route.Source] = true
			if route.Source != "ZIGZAG" && route.ID != "BROWSER_MERCHANT:"+route.Source {
				t.Fatalf("browser-capable source did not choose browser: %+v", route)
			}
		}
	}
	if !found["ELEVENST"] || !found["MUSINSA"] || !found["ZIGZAG"] {
		t.Fatalf("fashion routes missing: %+v", plan.Routes)
	}
	if len(plan.Alternatives["MUSINSA"]) != 0 {
		t.Fatalf("paid actor must not be planned without actor admission: %+v", plan.Alternatives["MUSINSA"])
	}
	if len(plan.Alternatives["ELEVENST"]) == 0 {
		t.Fatalf("11st API recovery path missing: %+v", plan.Alternatives["ELEVENST"])
	}
}
func TestCombineResourcesPreservesAvailabilityAndNewestSharedPressure(t *testing.T) {
	now := time.Now()
	a := ComposeProviderResources("FREE", now, []ResourceConstraint{{ID: "shared", Used: 20, Limit: 100}})
	b := ComposeProviderResources("FREE", now, []ResourceConstraint{{ID: "shared", Used: 70, Limit: 100}})
	b.CanStart = false
	b.Reason = "CATALOG_API_NOT_CONFIGURED"
	combined := CombineRouteResources(now, a, b)
	if combined.CanStart || combined.Pressure != .7 || combined.Reason != "CATALOG_API_NOT_CONFIGURED" {
		t.Fatal(combined)
	}
}

func TestResearchCommandHashIncludesRoutingCategory(t *testing.T) {
	input := CatalogWorkspaceSearchInputV2{UserID: "u", CurationID: "c", TargetID: "t", Mode: CatalogResearchAppendV2, ProductVertical: "FOOD"}
	first, err := NewCatalogCandidatePoolCommandV2(input, 0, "same")
	if err != nil {
		t.Fatal(err)
	}
	input.ProductVertical = "ELECTRONICS"
	second, err := NewCatalogCandidatePoolCommandV2(input, 0, "same")
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestHash == second.RequestHash {
		t.Fatal("different mall eligibility reused the same semantic command")
	}
}

package app

import (
	"context"
	"errors"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"testing"
	"time"
)

type controlTestRepository struct {
	AmazonControlRepository
	enabled bool
	err     error
}

func (r *controlTestRepository) ReadAmazonControl(context.Context) (AmazonSourceControl, error) {
	return AmazonSourceControl{Enabled: r.enabled, Version: 1}, r.err
}

type controlTestGateway struct {
	calls  int
	reason string
}

func (g *controlTestGateway) SearchAmazon(context.Context, AmazonSearchRequest) (AmazonSearchResult, error) {
	g.calls++
	if g.reason != "" {
		return AmazonSearchResult{}, fault.New(fault.ProviderUnavailable, g.reason, false)
	}
	return AmazonSearchResult{}, nil
}
func (g *controlTestGateway) LookupAmazon(context.Context, researchdomain.SourceVariantRef) (AmazonProductDetail, error) {
	g.calls++
	return AmazonProductDetail{}, nil
}
func (g *controlTestGateway) RefreshAmazonUsage(context.Context) (CatalogAPIUsage, error) {
	g.calls++
	return CatalogAPIUsage{}, nil
}
func TestAmazonOperatorOffGatesEveryGatewayOperation(t *testing.T) {
	repo := &controlTestRepository{}
	inner := &controlTestGateway{}
	g := &ControlledAmazonGateway{Gateway: inner, Control: repo}
	run := func(wantError bool) {
		_, search := g.SearchAmazon(context.Background(), AmazonSearchRequest{})
		_, detail := g.LookupAmazon(context.Background(), researchdomain.SourceVariantRef{})
		_, usage := g.RefreshAmazonUsage(context.Background())
		for _, err := range []error{search, detail, usage} {
			if (err != nil) != wantError {
				t.Fatalf("gateway guard: %v", err)
			}
		}
	}
	run(true)
	if inner.calls != 0 {
		t.Fatal("Off called provider")
	}
	repo.enabled = true
	run(false)
	if inner.calls != 3 {
		t.Fatal("On did not resume")
	}
	repo.enabled = false
	run(true)
	if inner.calls != 3 {
		t.Fatal("Off was cached incorrectly")
	}
	repo.enabled = true
	repo.err = errors.New("storage unavailable")
	run(true)
	if inner.calls != 3 {
		t.Fatal("storage failure allowed provider")
	}
}
func TestAmazonAdmissionUnavailableReturnsOtherSourceResults(t *testing.T) {
	for _, reason := range []string{"AMAZON_SOURCE_DISABLED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_RATE_LIMITED", "AMAZON_AUTH_REJECTED"} {
		t.Run(reason, func(t *testing.T) {
			clock := &liveReviewClockV2{now: time.Now()}
			profile := CatalogTargetSearchProfileV2{TargetID: "target", NormalizedIntent: "lightweight trail running shoes", Category: "shopify:footwear", TargetHash: "profile-hash", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}
			product := catalogProductWithURLV2("product-3", 8200)
			product.Media = []CatalogMedia{{URL: "https://cdn.shopify.com/product-3.jpg"}}
			plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
			scripted := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{response: catalogSearchResponseV2(plan, product)}}}
			service, err := NewLiveCatalogReviewServiceV2(&catalogPlannedSearchGatewayV2{scripted: scripted}, clock, LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1})
			if err != nil {
				t.Fatal(err)
			}
			service.EnableAmazon(&controlTestGateway{reason: reason})
			result, err := service.searchWorkspacePlanForProfileV2(context.Background(), CatalogWorkspaceSearchInputV2{UserID: "u", CurationID: "c", TargetID: "target", Mode: CatalogResearchReplaceV2, Search: LiveCatalogReviewSearchInputV2{Limit: 8}}, profile)
			if err != nil || len(result.Search.Products) != 1 || result.Search.Products[0].Source() != researchdomain.SourceShopify {
				t.Fatalf("lost Shopify result: %#v %v", result, err)
			}
			expected := "SKIPPED"
			if strings.Contains(reason, "AUTH") {
				expected = "FAILED"
			}
			if len(result.Metrics.SourceCoverage) != 2 || result.Metrics.SourceCoverage[1].Status != expected || result.Metrics.SourceCoverage[1].ReasonCode != reason {
				t.Fatalf("coverage: %#v", result.Metrics.SourceCoverage)
			}
		})
	}
}

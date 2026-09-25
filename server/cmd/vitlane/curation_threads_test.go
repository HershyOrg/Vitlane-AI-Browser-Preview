package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	research "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestApplicationWiresThreadsWithAndWithoutManagedRunner(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("APP_ENV", "test")
			t.Setenv("MANAGED_RUNNER_ENABLED", enabled)
			t.Setenv("MANAGED_RUNNER_MODEL_PROVIDER", "stub")
			t.Setenv("AGENCY_ORDER_ENABLED", "false")
			t.Setenv("PHASE5_SETTLEMENT_ENABLED", "false")
			t.Setenv("CURATION_CATALOG_RESEARCH_ENABLED", "false")
			t.Setenv("SHOPIFY_UCP_ENABLED", "false")
			t.Setenv("GOOGLE_OIDC_CLIENT_ID", "")
			t.Setenv("GOOGLE_OIDC_CLIENT_SECRET", "")
			config, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			// Wiring must not query storage or contact a model provider at startup.
			app, err := newApplication(context.Background(), config, &sharedpostgres.Database{}, http.DefaultTransport, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			if app.threadService == nil || app.threadHandler == nil {
				t.Fatal("thread routes are unavailable")
			}
			if (app.intelligenceService != nil) != (enabled == "true") {
				t.Fatal("Managed runner availability changed during wiring")
			}
		})
	}
}

func TestInterpretationWithoutManagedRunnerReturnsUnavailable(t *testing.T) {
	err := (threadPrimitives{}).StartInterpretation(context.Background(), curationdomain.CurationThread{}, curationdomain.CurationAction{})
	if !errors.Is(err, curationdomain.ErrCurationActionUnavailable) {
		t.Fatalf("got %v, want unavailable", err)
	}
}

// Vitlane saves no display facts of a Shopify product, so the DB-only read a reply
// is written from holds a skeleton: no title, and a zero price range that means
// "not read", never "free". The reply must see that product as unpriced.
func TestASavedShopifyCandidateReachesTheReplyUntitledAndUnpriced(t *testing.T) {
	assessment := research.LiveCandidateAssessmentV2{IntentPoint: "fits a small tent", AxisAssessment: &researchdomain.AxisAssessmentV1{TotalScore: 86, RoundID: "round-1"}}
	shopify := savedCandidate(research.CatalogProductObservation{ProviderProductID: "phase8-abc"}, assessment, true)
	if shopify.Title != "" || shopify.PriceMinor != nil || shopify.Source != "SHOPIFY" || !shopify.Assessed || shopify.TotalScore != 86 || shopify.IntentPoint != "fits a small tent" {
		t.Fatalf("a Shopify skeleton keeps its assessment and claims no name or price: %+v", shopify)
	}
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B000000001"}
	amazon := savedCandidate(research.CatalogProductObservation{ProviderProductID: "amazon:US:B000000001", SourceProductRef: &ref, Title: "Trail shoe"}, research.LiveCandidateAssessmentV2{}, false)
	if amazon.Title != "Trail shoe" || amazon.PriceMinor != nil || amazon.Assessed {
		t.Fatalf("an Amazon product without a price observation stays unpriced and unassessed: %+v", amazon)
	}
}

// Test the production adapter with controlled lookup results and failures.
type responseResearchFixture struct {
	base, fresh research.CatalogWorkspaceViewV2
	fail        bool
	calls       []research.CatalogResearchHydrationInputV2
}

func (f *responseResearchFixture) LoadWorkspaceV2(context.Context, string, string, string, string, bool) (research.CatalogWorkspaceViewV2, error) {
	return f.base, nil
}
func (f *responseResearchFixture) HydrateWorkspaceV2(_ context.Context, input research.CatalogResearchHydrationInputV2) (research.CatalogWorkspaceViewV2, error) {
	f.calls = append(f.calls, input)
	if f.fail {
		return research.CatalogWorkspaceViewV2{}, errors.New("lookup unavailable")
	}
	return f.fresh, nil
}

type responseFXFixture struct {
	rate researchdomain.DailyExchangeRate
}

func (f responseFXFixture) View(context.Context) (research.ExchangeRateView, error) {
	return research.ExchangeRateView{Rate: &f.rate}, nil
}
func TestReplyUsesVisibleShopifyListingRangeSelectedPriceAndUnknownOnFailure(t *testing.T) {
	now := time.Now().UTC()
	basePool := research.CatalogWorkspacePoolV2{
		Products:       []research.CatalogProductObservation{{ProviderProductID: "chosen"}, {ProviderProductID: "range"}},
		HiddenProducts: []research.CatalogProductObservation{{ProviderProductID: "hidden"}},
		Assessments:    map[string]research.LiveCandidateAssessmentV2{"chosen": {AxisAssessment: &researchdomain.AxisAssessmentV1{TotalScore: 90}}},
	}
	basePool.Metadata.TargetID = "target"
	current := basePool
	current.Products = []research.CatalogProductObservation{
		{ProviderProductID: "chosen", Title: "Original Trail Chair", PriceRange: research.CatalogPriceRange{Minimum: research.CatalogMoney{AmountMinor: 5000, Currency: "USD"}, Maximum: research.CatalogMoney{AmountMinor: 15000, Currency: "USD"}}},
		{ProviderProductID: "range", Title: "Original Pack", PriceRange: research.CatalogPriceRange{Minimum: research.CatalogMoney{AmountMinor: 8000, Currency: "USD"}, Maximum: research.CatalogMoney{AmountMinor: 12000, Currency: "USD"}}},
	}
	current.Hydrations = map[string]research.CatalogCandidateHydrationV2{"chosen": {Status: research.CatalogCandidateHydrationReadyV2}, "range": {Status: research.CatalogCandidateHydrationReadyV2}}
	f := &responseResearchFixture{
		base: research.CatalogWorkspaceViewV2{Pools: []research.CatalogWorkspacePoolV2{basePool}},
		fresh: research.CatalogWorkspaceViewV2{Pools: []research.CatalogWorkspacePoolV2{current}, Metrics: research.LiveCatalogReviewMetricsV2{CompletedAt: now},
			Configurations: []research.CatalogWorkspaceConfigurationViewV2{{CandidateID: "chosen", ObservedAt: "2020-01-01T00:00:00Z", Variant: research.LiveVariantReviewRowV2{PriceMinor: 15000, Currency: "USD", SelectedOptions: []research.LiveVariantOptionV2{{Name: "Color", Value: "Black"}}}}}},
	}
	source := threadResponseSource{research: f, fx: responseFXFixture{researchdomain.DailyExchangeRate{Base: "USD", Quote: "KRW", Rate: "1300", AsOf: now.Format("2006-01-02"), ObservedAt: now}}}
	out, err := source.SavedCandidates(context.Background(), "user", "curation", []string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	got := out["target"]
	if len(got) != 2 || len(f.calls) != 1 || f.calls[0].Scope != research.CatalogResearchHydrationVisibleTargetV2 || f.calls[0].Source != researchdomain.SourceShopify {
		t.Fatalf("visible target lookup only: %+v", f.calls)
	}
	if got[0].Title != "Original Trail Chair" || *got[0].PriceMinor != 15000 || got[0].Price.Basis != "SELECTED_VARIANT" || got[0].Price.SelectedOptions[0].Value != "Black" || got[0].Price.ConvertedMinor["KRW"] != 195000 || got[0].Price.ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("selected option, current timestamp and FX: %+v", got[0])
	}
	if got[1].Price.MinimumMinor != 8000 || got[1].Price.MaximumMinor != 12000 || got[1].Price.Basis != "PRODUCT_RANGE" {
		t.Fatalf("range must stay a range: %+v", got[1].Price)
	}
	f.fail = true
	out, err = source.SavedCandidates(context.Background(), "user", "curation", []string{"target"})
	if err != nil || len(out["target"]) != 2 || out["target"][0].PriceMinor != nil || out["target"][0].Title != "" || !out["target"][0].Assessed {
		t.Fatalf("failed lookup keeps assessments and no invented facts: %+v %v", out, err)
	}
}

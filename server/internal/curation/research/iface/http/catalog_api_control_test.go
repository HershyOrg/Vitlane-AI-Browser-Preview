package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

type stubRoundSummary struct {
	since, now time.Time
	err        error
}

func (s *stubRoundSummary) ReadResearchRoundSummary(_ context.Context, since, now time.Time) (researchapp.ResearchRoundSummary, error) {
	s.since, s.now = since, now
	if s.err != nil {
		return researchapp.ResearchRoundSummary{}, s.err
	}
	return researchapp.ResearchRoundSummary{
		SchemaVersion: researchapp.ResearchRoundSummarySchemaVersion, Since: since, GeneratedAt: now,
		Rounds:   []researchapp.ResearchRoundStatusCount{{Country: "KR", Status: "RESULTS_READY", Count: 3}},
		Failures: []researchapp.ResearchRoundFailureCount{{FailureCode: "PROVIDER_RESPONSE_INVALID", StepKind: "RANKING", Count: 1}},
		Sources:  []researchapp.ResearchRoundSourceCount{}, APICalls: []researchapp.CatalogAPICallCount{},
		Admitted: researchapp.ResearchAdmittedSummary{Rounds: 3, Median: 4, Buckets: []researchapp.ResearchAdmittedBucket{{Label: "4-7", Count: 3}}},
		Steps:    []researchapp.ResearchStepDuration{{Kind: "RANKING", Count: 2, P50Seconds: 61.5, P95Seconds: 118}},
		Attempts: []researchapp.ResearchAttemptCount{{Status: "FAILED", FailureCode: "QUOTA_EXCEEDED", Count: 1}},
	}, nil
}

func TestRoundSummaryWindowAndSchema(t *testing.T) {
	reader := &stubRoundSummary{}
	handler := &CatalogAPIHandler{Summary: reader}
	response := httptest.NewRecorder()
	handler.RoundSummary(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/catalog-apis/round-summary?days=3", nil))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	var body struct {
		SchemaVersion string `json:"schemaVersion"`
		WindowDays    int    `json:"windowDays"`
		Rounds        []struct {
			Country string `json:"country"`
			Count   int    `json:"count"`
		} `json:"rounds"`
		Failures []struct {
			StepKind string `json:"stepKind"`
		} `json:"failures"`
		Sources  []any `json:"sources"`
		APICalls []any `json:"apiCalls"`
		Admitted struct {
			Median float64 `json:"median"`
		} `json:"admitted"`
		Steps []struct {
			Kind       string  `json:"kind"`
			P95Seconds float64 `json:"p95Seconds"`
		} `json:"steps"`
		Attempts []struct {
			FailureCode string `json:"failureCode"`
		} `json:"attempts"`
		Reservations []any `json:"reservations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Steps) != 1 || body.Steps[0].Kind != "RANKING" || body.Steps[0].P95Seconds != 118 || len(body.Attempts) != 1 || body.Attempts[0].FailureCode != "QUOTA_EXCEEDED" || body.Reservations == nil {
		t.Fatalf("performance ledgers: %s", response.Body.String())
	}
	if body.SchemaVersion != "vitlane.research-round-summary.v1" || body.WindowDays != 3 || len(body.Rounds) != 1 || body.Rounds[0].Country != "KR" || body.Failures[0].StepKind != "RANKING" || body.Admitted.Median != 4 {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
	if body.Sources == nil || body.APICalls == nil {
		t.Fatal("empty collections must serialize as [] for the web")
	}
	if window := reader.now.Sub(reader.since); window != 3*24*time.Hour {
		t.Fatalf("window=%v", window)
	}

	// Default window is 7 days; the ledger read carries the same instant.
	response = httptest.NewRecorder()
	handler.RoundSummary(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/catalog-apis/round-summary", nil))
	if response.Code != 200 || reader.now.Sub(reader.since) != 7*24*time.Hour {
		t.Fatalf("default window: status=%d window=%v", response.Code, reader.now.Sub(reader.since))
	}

	for _, raw := range []string{"0", "31", "abc"} {
		response = httptest.NewRecorder()
		handler.RoundSummary(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/catalog-apis/round-summary?days="+raw, nil))
		if response.Code != 400 {
			t.Fatalf("days=%s status=%d", raw, response.Code)
		}
	}

	response = httptest.NewRecorder()
	(&CatalogAPIHandler{}).RoundSummary(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/catalog-apis/round-summary", nil))
	if response.Code != 503 {
		t.Fatalf("missing reader status=%d", response.Code)
	}
}

type usageGatewayFake struct{ asked []string }

func (g *usageGatewayFake) SearchExternalMalls(context.Context, researchapp.KoreanSearchRequest) (researchapp.ExternalCatalogSearchResult, error) {
	return researchapp.ExternalCatalogSearchResult{}, nil
}
func (g *usageGatewayFake) Usage(_ context.Context, id string, _ bool) (researchapp.CatalogProviderUsage, error) {
	g.asked = append(g.asked, id)
	definition, _ := researchapp.CatalogAPIDefinitionFor(id)
	return researchapp.CatalogProviderUsage{CatalogAPIDefinition: definition, Configured: true}, nil
}
func (*usageGatewayFake) Configured(string) bool { return true }

type usageControlFake struct {
	researchapp.CatalogProviderControlRepository
	read []string
}

func (c *usageControlFake) ReadProviderUsage(_ context.Context, id string) (researchapp.CatalogProviderUsage, error) {
	c.read = append(c.read, id)
	definition, _ := researchapp.CatalogAPIDefinitionFor(id)
	return researchapp.CatalogProviderUsage{CatalogAPIDefinition: definition, Requests24h: 41,
		Failures24h: []researchapp.CatalogAPIFailureCount{{ReasonCode: "RATE_LIMITED", Count: 2}}}, nil
}

// The operator's list has Shopify in it: read from the ledger, never through
// the Korean gateway, marked as a stub on a local review stack.
func TestCatalogAPIUsageListsShopifyFromTheLedger(t *testing.T) {
	gateway, control := &usageGatewayFake{}, &usageControlFake{}
	handler := &CatalogAPIHandler{Gateway: gateway, Control: control, ShopifyConfigured: true, ShopifyStub: true}
	response := httptest.NewRecorder()
	handler.Usage(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/catalog-apis", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		APIs []struct {
			ID          string `json:"id"`
			APIProvider string `json:"apiProvider"`
			APIProduct  string `json:"apiProduct"`
			Configured  bool   `json:"configured"`
			Requests24h int64  `json:"requests24h"`
			Failures24h []struct {
				ReasonCode string `json:"reasonCode"`
				Count      int64  `json:"count"`
			} `json:"failures24h"`
		} `json:"apis"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.APIs) != len(researchapp.KoreanCatalogAPIs)+2 {
		t.Fatalf("every research API and Shopify: got %d rows", len(body.APIs))
	}
	shopify := body.APIs[0]
	if shopify.ID != researchapp.ShopifyCatalogAPIID || shopify.APIProvider != "Shopify" || !shopify.Configured ||
		shopify.APIProduct != "Global Catalog search and lookup (UCP) [STUB]" || shopify.Requests24h != 41 ||
		len(shopify.Failures24h) != 1 || shopify.Failures24h[0].ReasonCode != "RATE_LIMITED" {
		t.Fatalf("shopify row=%+v", shopify)
	}
	if len(control.read) != 2 || control.read[0] != researchapp.ShopifyCatalogAPIID || control.read[1] != "TELEGRAM_JIRUM" {
		t.Fatalf("Shopify is read from the ledger: %v", control.read)
	}
	for _, id := range gateway.asked {
		if id == researchapp.ShopifyCatalogAPIID {
			t.Fatal("the Korean gateway was asked about Shopify")
		}
	}

	// Without a Shopify gateway the row says it cannot start, and cannot be switched on.
	unconfigured := &CatalogAPIHandler{Gateway: gateway, Control: control}
	if unconfigured.configured(researchapp.ShopifyCatalogAPIID) || !unconfigured.configured("NAVER_WEBKR") {
		t.Fatal("configuration is answered per API")
	}
}

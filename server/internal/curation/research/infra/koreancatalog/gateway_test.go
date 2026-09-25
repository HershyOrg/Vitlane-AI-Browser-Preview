package koreancatalog

import (
	"context"
	"encoding/json"
	"fmt"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	researchpostgres "github.com/vitlane/vitlane/server/internal/curation/research/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type memoryControl struct {
	mu sync.Mutex
	researchapp.CatalogProviderControlRepository
	codes         []string
	status, retry int
	disabled      bool
}

func (c *memoryControl) ReserveProviderCall(context.Context, string, string, time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return "", safeFailure("CATALOG_API_DISABLED")
	}
	return "call", nil
}
func (c *memoryControl) CompleteProviderCall(_ context.Context, _ string, code string, status, retry int, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, code)
	c.status = status
	c.retry = retry
	return nil
}
func response(status int, body, contentType string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestProviderTransportFailureAccountingAndRedaction(t *testing.T) {
	for _, test := range []struct {
		name, body, ctype, reason string
		status                    int
	}{
		{"json error", `{"status":"ERROR","message":"secret-test-key"}`, "application/json", "CATALOG_SCHEMA_MISMATCH", 200},
		{"invalid shape", `{"status":"OK","data":{}}`, "application/json", "CATALOG_SCHEMA_MISMATCH", 200},
		{"rate limit", `secret-test-key`, "application/json", "CATALOG_UPSTREAM_RATE_LIMITED", 429},
		{"auth", `secret-test-key`, "application/json", "CATALOG_AUTH_REJECTED", 403},
		{"redirect", ``, "application/json", "CATALOG_UPSTREAM_FAILED", 302},
		{"html instead of json", `<html>`, "text/html", "CATALOG_CONTENT_TYPE_INVALID", 200},
		{"body cap", strings.Repeat("a", (2<<20)+1), "application/json", "CATALOG_BODY_INVALID", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &memoryControl{}
			calls := 0
			g, _ := New(Config{Enabled: true, OWNKey: "secret-test-key", Control: c, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.openwebninja.com" || r.Header.Get("x-api-key") != "secret-test-key" || r.URL.Query().Get("language") != "ko" {
					t.Fatal("wrong provider request")
				}
				res := response(test.status, test.body, test.ctype)
				res.Header.Set("Retry-After", "120")
				res.Header.Set("Location", "https://elsewhere.invalid/?secret-test-key")
				return res, nil
			})}})
			var out any
			_, err := g.get(context.Background(), "OWN_PRODUCT", "SEARCH", "https://api.openwebninja.com", "/realtime-product-search/v2/search", url.Values{"q": {"라미 Safari"}, "language": {"ko"}, "country": {"kr"}}, &out)
			if err == nil || !strings.Contains(err.Error(), test.reason) || strings.Contains(err.Error(), "secret-test-key") || len(c.codes) != 1 || c.codes[0] != test.reason || calls != 1 {
				t.Fatalf("accounting=%v err=%v calls=%d", c.codes, err, calls)
			}
			if test.status == 429 && (c.retry != 120 || c.status != 429) {
				t.Fatal("Retry-After not recorded")
			}
		})
	}
}
func TestNaverHeadersAndPlainJSON(t *testing.T) {
	c := &memoryControl{}
	calls := 0
	g, _ := New(Config{Enabled: true, NaverClientID: "client-test", NaverClientSecret: "secret-test", Control: c, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "naverapihub.apigw.ntruss.com" || r.Header.Get("X-NCP-APIGW-API-KEY-ID") != "client-test" || r.Header.Get("X-NCP-APIGW-API-KEY") != "secret-test" || r.URL.Query().Get("query") != "라미 사파리 site:11st.co.kr/products" {
			t.Fatal("Naver query/auth mapping")
		}
		return response(200, `{"items":[]}`, "text/plain;charset=utf-8"), nil
	})}})
	obs, err := g.searchElevenStreet(context.Background(), "라미 사파리", "NAVER_WEBKR")
	if err != nil || len(obs.Products) != 0 || calls != 1 || c.codes[0] != "SUCCESS" {
		t.Fatalf("%v %v", obs, err)
	}
	c.disabled = true
	if _, err = g.searchElevenStreet(context.Background(), "라미 사파리", "NAVER_WEBKR"); err == nil || calls != 1 {
		t.Fatal("disabled API performed I/O")
	}
}
func TestProductMetadataRequiresOriginalIdentityAndKnownCurrency(t *testing.T) {
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceElevenStreet, Marketplace: "KR", ProductID: "5337333981"}
	canonical, _ := ref.ExternalProductURL()
	for _, test := range []struct {
		name, raw, kind string
		minor           int64
	}{
		{"matching original", `<script type="application/ld+json">{"@type":"Product","url":"https://www.11st.co.kr/products/5337333981","offers":{"price":23400,"priceCurrency":"KRW"}}</script>`, "OBSERVED", 23400},
		{"related product", `<script type="application/ld+json">{"@type":"Product","url":"https://www.11st.co.kr/products/111","offers":{"price":1,"priceCurrency":"KRW"}}</script>`, "UNKNOWN", 0},
		{"unidentified product", `<script type="application/ld+json">{"@type":"Product","offers":{"price":1,"priceCurrency":"KRW"}}</script>`, "UNKNOWN", 0},
		{"meta", `<meta property="product:price:amount" content="23,400"><meta property="product:price:currency" content="KRW">`, "OBSERVED", 23400},
		{"zero placeholder", `<meta property="product:price:amount" content="0"><meta property="product:price:currency" content="KRW">`, "UNKNOWN", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := researchdomain.ExternalProductObservation{ProductRef: ref, ProductURL: canonical, Price: observedPrice("", "")}
			applyProductMetadata(&o, []byte(test.raw))
			if o.Price.Kind != test.kind || (test.kind == "OBSERVED" && *o.Price.AmountMinor != test.minor) {
				t.Fatalf("price=%+v", o.Price)
			}
		})
	}
	if price := observedPrice("$12.34", ""); price.Kind != "OBSERVED" || *price.AmountMinor != 1234 {
		t.Fatal(price)
	}
	if price := observedPrice("₩12.34", ""); price.Kind != "UNKNOWN" {
		t.Fatal("fractional KRW accepted")
	}
}

// Explicitly opt-in. Uses only the named provider keys, a disposable PostgreSQL
// schema and a bounded number of requests. Evidence excludes bodies and secrets.
func TestLiveKoreanCatalogAdapters(t *testing.T) {
	if os.Getenv("VITLANE_STEP2_LIVE_TEST") != "1" {
		t.Skip("explicit VITLANE_STEP2_LIVE_TEST=1 required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dsn := os.Getenv("TEST_DATABASE_URL")
	base, err := sharedpostgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "step2_live_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err = base.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sharedpostgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx, "../../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	repo := researchpostgres.NewRepository(db)
	g, err := New(Config{Enabled: true, OWNKey: os.Getenv("OPEN_WEB_NINJA_API_KEY"), NaverClientID: os.Getenv("NAVER_API_HUB_CLIENT_ID"), NaverClientSecret: os.Getenv("NAVER_API_HUB_CLIENT_SECRET"), SerpKey: os.Getenv("SERP_API_KEY"), Control: repo, Client: &http.Client{Timeout: 36 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	entries := []map[string]any{}
	// Enable optional fallback paths in this isolated test schema only.
	if _, err = db.DB.ExecContext(ctx, `UPDATE research_catalog_source_control SET enabled=true WHERE source IN ('OWN_WEB','SERP_GOOGLE')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"OWN_PRODUCT", "OWN_WEB", "SERP_GOOGLE"} {
		started := time.Now()
		usage, e := g.Usage(ctx, id, true)
		entry := map[string]any{"api": id, "operation": "USAGE", "durationMs": time.Since(started).Milliseconds(), "success": e == nil}
		if e != nil {
			entry["reasonCode"] = reason(e)
		} else {
			entry["quota"] = usage.Quota
		}
		entries = append(entries, entry)
	}
	for _, id := range []string{"OWN_PRODUCT", "NAVER_WEBKR", "OWN_WEB", "SERP_GOOGLE"} {
		started := time.Now()
		var observations []researchdomain.ExternalProductObservation
		var e error
		if id == "OWN_PRODUCT" {
			observations, e = g.searchProducts(ctx, "삼성 갤럭시 버즈3 프로")
		} else {
			var search elevenStreetSearch
			search, e = g.searchElevenStreet(ctx, "라미 사파리 만년필", id)
			observations = search.Products
		}
		// Do not retain provider lookup tokens, response payloads, images or key-bearing URLs.
		projection := []map[string]any{}
		for _, o := range observations {
			if o.Validate() != nil {
				t.Error("adapter emitted invalid original identity")
			}
			projection = append(projection, map[string]any{"productRef": o.ProductRef, "productUrl": o.ProductURL, "title": o.Title, "price": o.Price, "seller": o.Seller, "priceScope": o.PriceScope, "observedAt": o.ObservedAt, "detailApiProvider": o.Provenance.DetailAPIProvider})
		}
		entry := map[string]any{"api": id, "operation": "SEARCH_AND_DETAIL", "durationMs": time.Since(started).Milliseconds(), "success": e == nil, "observations": projection}
		if e != nil {
			entry["reasonCode"] = reason(e)
		}
		entries = append(entries, entry)
		t.Logf("%s observations=%d reason=%s", id, len(observations), reason(e))
	}
	usage := []researchapp.CatalogProviderUsage{}
	for _, d := range researchapp.KoreanCatalogAPIs {
		u, e := g.Usage(ctx, d.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		usage = append(usage, u)
	}
	raw, _ := json.MarshalIndent(map[string]any{"observedAt": time.Now().UTC(), "implementation": "curation-step2", "entries": entries, "usage": usage}, "", "  ")
	path := os.Getenv("VITLANE_STEP2_EVIDENCE_PATH")
	if path != "" {
		path, _ = filepath.Abs(path)
		if !filepath.IsAbs(os.Getenv("VITLANE_STEP2_EVIDENCE_PATH")) {
			t.Fatal("evidence path must be absolute")
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A successful transport alone does not guarantee useful products. Assert
	// at least one validated original listing from each primary discovery path.
	for _, id := range []string{"OWN_PRODUCT", "NAVER_WEBKR"} {
		found := false
		for _, entry := range entries {
			if entry["api"] == id && entry["operation"] == "SEARCH_AND_DETAIL" {
				found = len(entry["observations"].([]map[string]any)) > 0
			}
		}
		if !found {
			t.Errorf("primary %s produced no validated original products; inspect sanitized evidence", id)
		}
	}
}
func reason(err error) string {
	if err == nil {
		return "SUCCESS"
	}
	if f, ok := fault.As(err); ok {
		return f.Reason
	}
	return fmt.Sprintf("%T", err)
}

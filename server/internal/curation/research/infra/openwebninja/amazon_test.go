package openwebninja

import (
	"context"
	"encoding/json"
	"fmt"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type testUsage struct {
	quota    *researchapp.CatalogAPIQuota
	calls    int
	outcomes []string
}

func (u *testUsage) ReserveAmazonCall(context.Context, string, time.Time) (string, error) {
	u.calls++
	return fmt.Sprint(u.calls), nil
}
func (u *testUsage) CompleteAmazonCall(_ context.Context, _ string, code string, _ time.Time) error {
	u.outcomes = append(u.outcomes, code)
	return nil
}
func (u *testUsage) SaveAmazonQuota(_ context.Context, q researchapp.CatalogAPIQuota, _ time.Time) error {
	u.quota = &q
	return nil
}
func (u *testUsage) RecordAmazonQuotaFailure(context.Context, string) error { return nil }
func (u *testUsage) ReadAmazonUsage(context.Context) (researchapp.CatalogAPIUsage, error) {
	return researchapp.CatalogAPIUsage{Quota: u.quota}, nil
}
func freshUsage() *testUsage {
	return &testUsage{quota: &researchapp.CatalogAPIQuota{Limit: 100, Remaining: 100, ObservedAt: time.Now(), ResetAt: time.Now().Add(time.Hour)}}
}
func TestAmazonExactPriceAndExplicitVariantParsing(t *testing.T) {
	for _, v := range []struct {
		raw, currency string
		amount        int64
		known         bool
	}{{"$1,234.56", "USD", 123456, true}, {"0.01", "USD", 1, true}, {"FREE", "USD", 0, false}, {"$12.345", "USD", 0, false}, {"-1", "USD", 0, false}, {"12", "KRW", 0, false}, {"99999999999999999999999", "USD", 0, false}} {
		p := observedPrice(&v.raw, v.currency)
		if (p.Kind == "OBSERVED") != v.known || (v.known && *p.AmountMinor != v.amount) {
			t.Fatalf("price %q: %#v", v.raw, p)
		}
		if !v.known && p.AmountMinor != nil {
			t.Fatal("unknown became zero")
		}
	}
	rows, status, truncated := parseVariants(json.RawMessage(`{"color":[{"asin":"B012345678","value":"Blue","is_available":true},{"asin":"B987654321","value":"Black"}],"size":[{"asin":"B012345678","value":"Small"},{"asin":"INVALID","value":"Large"}]}`))
	if len(rows) != 2 || len(rows[0].Labels) != 2 || status != "PARTIAL" || truncated {
		t.Fatalf("no Cartesian products: %#v %s", rows, status)
	}
	for _, raw := range []string{"null", "{}", "[]"} {
		rows, _, _ := parseVariants(json.RawMessage(raw))
		if len(rows) != 0 {
			t.Fatal("invented variant")
		}
	}
}
func TestAmazonAdapterMapsFailuresAndRejectsIdentityMismatch(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{401, `secret upstream response`, "AMAZON_AUTH_REJECTED"}, {429, `secret`, "AMAZON_RATE_LIMITED"}, {500, `secret`, "AMAZON_UPSTREAM_FAILED"}, {200, `{"status":"OK","data":{"asin":"B987654321","country":"US","product_title":"Headset","currency":"USD","product_price":"12.30","product_url":"https://www.amazon.com/dp/B987654321"}}`, "AMAZON_ASIN_MISMATCH"}, {200, `{"status":"OK","data":null}`, "AMAZON_SCHEMA_MISMATCH"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-api-key") != "test-only-key" || r.URL.Query().Get("asin") != "B012345678" {
					t.Error("request contract")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			u := freshUsage()
			g, err := New(Config{APIKey: "test-only-key", Client: srv.Client(), Usage: u, Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.LookupAmazon(context.Background(), researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"})
			if err == nil || !strings.Contains(err.Error(), tc.code) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("safe failure: %v", err)
			}
			if len(u.outcomes) != 1 || u.outcomes[0] != tc.code {
				t.Fatalf("not recorded: %#v", u.outcomes)
			}
		})
	}
}
func TestAmazonAdapterUsesOnlyExplicitASINAndCanonicalURL(t *testing.T) {
	u := freshUsage()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"OK","data":{"asin":"B012345678","country":"US","product_title":"Headset","currency":"USD","product_price":"199.99","product_url":"https://www.amazon.com/dp/B012345678?tracking=x","product_availability":"In Stock","product_variations":{"color":[{"asin":"B987654321","value":"Blue"}]}}}`)
	}))
	defer srv.Close()
	g, _ := New(Config{APIKey: "test-only", Client: srv.Client(), Usage: u, Endpoint: srv.URL})
	d, err := g.LookupAmazon(context.Background(), researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"})
	if err != nil {
		t.Fatal(err)
	}
	o := d.Product.VariantObservation
	if o.Validate() != nil || *o.Price.AmountMinor != 19999 || o.ProductURL != "https://www.amazon.com/dp/B012345678" || o.Seller.Kind != "UNKNOWN" || len(d.Variants) != 1 {
		t.Fatalf("wrong normalization: %#v", d)
	}
}

// Opt-in: at most one search and two detail calls, never prints the key or raw payload.
func TestAmazonLiveHeadsetVariant(t *testing.T) {
	if os.Getenv("VITLANE_AMAZON_LIVE_TEST") != "1" {
		t.Skip("explicit live audit only")
	}
	key := os.Getenv("AMAZON_PRODUCT_SEARCH_API")
	if key == "" {
		t.Fatal("key unavailable")
	}
	u := freshUsage()
	g, err := New(Config{APIKey: key, Client: &http.Client{Timeout: 10 * time.Second}, Usage: u})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := g.RefreshAmazonUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.SearchAmazon(ctx, researchapp.AmazonSearchRequest{Query: "Sony WH-1000XM5 wireless headphones", Marketplace: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Products) == 0 {
		t.Fatal("no headset")
	}
	ref := result.Products[0].ProductRef()
	detail, err := g.LookupAmazon(ctx, researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: ref.AnchorASIN})
	if err != nil {
		t.Fatal(err)
	}
	selected := ""
	for _, v := range detail.Variants {
		if v.ASIN != ref.AnchorASIN {
			selected = v.ASIN
			break
		}
	}
	if selected == "" {
		t.Fatal("no alternate direct ASIN")
	}
	alternate, err := g.LookupAmazon(ctx, researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: selected})
	if err != nil {
		t.Fatal(err)
	}
	if alternate.Product.VariantObservation.VariantRef.ASIN != selected || alternate.Product.VariantObservation.Validate() != nil {
		t.Fatal("selected variant mismatch")
	}
	after, err := g.RefreshAmazonUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live audit: products=%d directVariants=%d exactSelectedASIN=true priceKind=%s quotaBefore=%d quotaAfter=%d calls=%d", len(result.Products), len(detail.Variants), alternate.Product.VariantObservation.Price.Kind, before.Quota.Remaining, after.Quota.Remaining, u.calls)
}

type contendedUsage struct {
	*testUsage
	denials int
}

func (u *contendedUsage) ReserveAmazonCall(ctx context.Context, operation string, now time.Time) (string, error) {
	if u.denials > 0 {
		u.denials--
		f := fault.New(fault.RateLimited, "AMAZON_RATE_LIMITED", true)
		f.RetryAfter = time.Second
		return "", f
	}
	return u.testUsage.ReserveAmazonCall(ctx, operation, now)
}

func TestAmazonAdmissionWaitsOnceWhenAnotherRoundHoldsASlot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"OK","data":{"asin":"B012345678","country":"US","product_title":"Headset","currency":"USD","product_price":"199.99","product_url":"https://www.amazon.com/dp/B012345678","product_availability":"In Stock"}}`)
	}))
	defer server.Close()
	ref := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"}
	usage := &contendedUsage{testUsage: freshUsage(), denials: 1}
	g, err := New(Config{APIKey: "k", Client: server.Client(), Endpoint: server.URL, Usage: usage})
	if err != nil {
		t.Fatal(err)
	}
	waited := time.Duration(0)
	g.wait = func(_ context.Context, d time.Duration) error { waited += d; return nil }
	if _, err := g.LookupAmazon(context.Background(), ref); err != nil {
		t.Fatalf("second reservation should admit: %v", err)
	}
	if waited != time.Second || usage.calls != 1 || len(usage.outcomes) != 1 || usage.outcomes[0] != "SUCCESS" {
		t.Fatalf("waited=%v calls=%d outcomes=%v", waited, usage.calls, usage.outcomes)
	}
	// Two denials in a row mean the slots are genuinely busy: skip the source this Round.
	usage = &contendedUsage{testUsage: freshUsage(), denials: 2}
	g, _ = New(Config{APIKey: "k", Client: server.Client(), Endpoint: server.URL, Usage: usage})
	g.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := g.LookupAmazon(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "AMAZON_RATE_LIMITED") || usage.calls != 0 {
		t.Fatalf("busy slots: err=%v calls=%d", err, usage.calls)
	}
}

func TestMalformedAmazonRowDoesNotEraseNeighbor(t *testing.T) {
	var rows []product
	if err := json.Unmarshal([]byte(`[{"asin":42},{"asin":"B012345678","product_title":"Shoe","product_url":"https://www.amazon.com/dp/B012345678","currency":"USD"}]`), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("page lost: %v", err)
	}
	if _, err := normalize(rows[0], "US", time.Now()); err == nil {
		t.Fatal("invalid row admitted")
	}
	if _, err := normalize(rows[1], "US", time.Now()); err != nil {
		t.Fatalf("valid row lost: %v", err)
	}
}

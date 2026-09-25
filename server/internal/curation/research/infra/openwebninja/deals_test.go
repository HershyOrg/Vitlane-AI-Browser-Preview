package openwebninja

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedDealsUsesDocumentedV2AndPreservesUnknownPrice(t *testing.T) {
	usage := freshUsage()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/usage" {
			fmt.Fprintf(w, `{"status":"OK","data":{"plan":{"is_free":true},"quotas":[{"limit":100,"used":0,"remaining":100,"reset_at":%q}]}}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			return
		}
		if r.URL.Path != "/realtime-amazon-data/deals-v2" || r.URL.Query().Get("country") != "US" || r.URL.Query().Get("offset") != "0" {
			t.Errorf("unexpected request %s", r.URL)
			http.Error(w, "wrong path", 400)
			return
		}
		fmt.Fprintf(w, `{"status":"OK","data":{"deals":[{"deal_id":"d1","deal_title":"Headphones","product_asin":"B012345678","deal_state":"AVAILABLE","deal_ends_at":%q},{"deal_id":"d2","deal_title":"Ended","product_asin":"B987654321","deal_state":"EXPIRED"}]}}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	defer server.Close()
	gateway, e := New(Config{APIKey: "test-only", Client: server.Client(), Usage: usage, Endpoint: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	products, e := gateway.Poll(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(products) != 1 || products[0].ProductRef.AnchorASIN != "B012345678" || products[0].PriceMinor != nil || products[0].ShippingMinor != nil {
		t.Fatalf("%+v", products)
	}
	if usage.calls != 1 || len(usage.outcomes) != 1 || usage.outcomes[0] != "SUCCESS" {
		t.Fatalf("untracked %+v", usage)
	}
}

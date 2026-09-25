package koreancatalog

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// OWN Product details name Google-side offers on many Korean malls. Every
// registered mall becomes an original product identity without visiting the
// mall; unregistered merchants and Google's own URLs are left out. The detail
// limit bounds how many search hits pay for a detail call.
func TestOWNOffersAdmitEveryRegisteredMallWithinTheDetailLimit(t *testing.T) {
	details := 0
	var transportMu sync.Mutex
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		switch r.URL.Path {
		case "/usage":
			return response(200, `{"status":"OK","data":{"api_id":"realtime_product_search","plan":{"is_free":true},"quotas":[{"name":"Requests","limit":100,"used":0,"remaining":100,"reset_at":"2099-01-01T00:00:00Z"}]}}`, "application/json"), nil
		case "/realtime-product-search/v2/search":
			return response(200, `{"status":"OK","data":{"products":[{"product_id":"g1","product_title":"우유 A"},{"product_id":"g2","product_title":"우유 B"},{"product_id":"g3","product_title":"우유 C"},{"product_id":"g4","product_title":"우유 D"}]}}`, "application/json"), nil
		case "/realtime-product-search/v2/product-details":
			details++
			switch r.URL.Query().Get("product_id") {
			case "g1":
				return response(200, `{"status":"OK","data":{"product_title":"우유 A","offers":[{"offer_page_url":"https://www.kurly.com/goods/5063110","price":"₩3,900"},{"offer_page_url":"https://www.google.com/shopping/product/1","price":"₩1"}]}}`, "application/json"), nil
			case "g2":
				return response(200, `{"status":"OK","data":{"product_title":"우유 B","offers":[{"offer_page_url":"https://smartstore.naver.com/dairyfarm/products/7058693395","price":"₩4,200"},{"offer_page_url":"https://blueblack.co.kr/product/123","price":"₩4,000"}]}}`, "application/json"), nil
			case "g4":
				return response(200, `{"status":"OK","data":{"offers":[]}}`, "application/json"), nil
			case "g3":
				return response(200, `{"status":"OK","data":{"product_title":"우유 C","offers":[{"offer_page_url":"https://www.coupang.com/vp/products/8202544905?itemId=20106538968&vendorItemId=3000043960","price":"₩3,500"}]}}`, "application/json"), nil
			}
			t.Fatalf("detail beyond the limit: %s", r.URL.Query().Get("product_id"))
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})
	control := &admissionControl{}
	g, err := New(Config{Enabled: true, OWNKey: "k", Control: control, Client: &http.Client{Transport: transport}, DetailLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	products, err := g.searchProducts(context.Background(), "우유")
	if err != nil {
		t.Fatal(err)
	}
	if details != 3 || len(products) != 3 {
		t.Fatalf("details=%d products=%d", details, len(products))
	}
	want := map[researchdomain.Source]string{researchdomain.SourceKurly: "5063110", researchdomain.SourceNaverSmartstore: "dairyfarm/7058693395", researchdomain.SourceCoupang: "8202544905"}
	for _, p := range products {
		id, ok := want[p.ProductRef.Source]
		if !ok || p.ProductRef.ProductID != id || p.Validate() != nil || p.Price.Kind != "OBSERVED" || p.Provenance.DiscoveryChannel != "GOOGLE_SHOPPING" {
			t.Fatalf("unexpected observation %+v", p)
		}
		if p.ProductRef.Source == researchdomain.SourceCoupang && (p.OriginalItemID != "20106538968" || p.OriginalListingID != "3000043960") {
			t.Fatalf("Coupang item ids lost: %+v", p)
		}
		if p.ProductRef.Source != researchdomain.SourceCoupang && (p.OriginalItemID != "" || p.OriginalListingID != "") {
			t.Fatalf("item ids invented for %s", p.ProductRef.Source)
		}
		canonical, _ := p.ProductRef.ExternalProductURL()
		if p.ProductURL != canonical || !strings.HasPrefix(p.ProductURL, "https://") {
			t.Fatalf("product URL is not canonical: %s", p.ProductURL)
		}
	}

	// The default limit allows up to four details, without filling missing hits.
	details = 0
	g, _ = New(Config{Enabled: true, OWNKey: "k", Control: &admissionControl{}, Client: &http.Client{Transport: transport}})
	if _, err := g.searchProducts(context.Background(), "우유"); err != nil || details != 4 {
		t.Fatalf("default detail limit: details=%d err=%v", details, err)
	}
}

func TestSearchCoverageReportsEachRegisteredMall(t *testing.T) {
	var transportMu sync.Mutex
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		switch r.URL.Path {
		case "/usage":
			return response(200, `{"status":"OK","data":{"api_id":"realtime_product_search","plan":{"is_free":true},"quotas":[{"name":"Requests","limit":100,"used":0,"remaining":100,"reset_at":"2099-01-01T00:00:00Z"}]}}`, "application/json"), nil
		case "/realtime-product-search/v2/search":
			return response(200, `{"status":"OK","data":{"products":[{"product_id":"g1","product_title":"우유 A"}]}}`, "application/json"), nil
		case "/realtime-product-search/v2/product-details":
			return response(200, `{"status":"OK","data":{"product_title":"우유 A","offers":[{"offer_page_url":"https://www.kurly.com/goods/5063110","price":"₩3,900"},{"offer_page_url":"https://www.kurly.com/goods/5063111","price":"₩3,950"}]}}`, "application/json"), nil
		case "/search/v1/webkr":
			return response(200, `{"items":[]}`, "text/plain"), nil
		case "/realtime-web-search/search":
			// NAVER found nothing, so the Web fallback runs and also finds nothing.
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})
	g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", Control: &admissionControl{}, Client: &http.Client{Transport: transport}})
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "우유", Country: "KR"})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[researchdomain.Source]researchapp.SourceCoverage{}
	for _, c := range result.Coverage {
		rows[c.Source] = c
	}
	if rows[researchdomain.SourceKurly].Status != "SUCCEEDED" || rows[researchdomain.SourceKurly].CandidateCount != 2 {
		t.Fatalf("Kurly coverage missing: %+v", result.Coverage)
	}
	if rows[researchdomain.SourceCoupang].Status != "EMPTY" || rows[researchdomain.SourceElevenStreet].Status != "EMPTY" {
		t.Fatalf("legacy path rows changed: %+v", result.Coverage)
	}
	if len(result.Observations) != 2 {
		t.Fatalf("observations=%d", len(result.Observations))
	}
}

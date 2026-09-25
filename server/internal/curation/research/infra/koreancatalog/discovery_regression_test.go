package koreancatalog

import (
	"context"
	"encoding/json"
	"fmt"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"net/http"
	"testing"
)

// Discovery rows and next-page state are independent of optional detail success.
func TestPartialOWNPreservesProductsAndAdvancesSeed(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/realtime-product-search/v2/search":
			return response(200, `{"status":"OK","data":{"products":[{"product_id":"ok","product_title":"Milk"},{"product_id":"bad","product_title":"Milk"}]}}`, "application/json"), nil
		case "/realtime-product-search/v2/product-details":
			if r.URL.Query().Get("product_id") == "bad" {
				return response(503, `{}`, "application/json"), nil
			}
			return response(200, `{"status":"OK","data":{"offers":[{"offer_page_url":"https://www.coupang.com/vp/products/123456789","price":"1000원"}]}}`, "application/json"), nil
		}
		t.Errorf("unexpected request path %s", r.URL.Path)
		return response(500, `{}`, "application/json"), nil
	})
	g, err := New(Config{Enabled: true, OWNKey: "test", Control: &reviewControl{}, Client: &http.Client{Transport: transport}, DetailLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	out := g.executeRoute(context.Background(), researchapp.KoreanSearchRequest{Query: "우유", Seeds: []string{"우유", "저지방 우유"}}, researchapp.ResearchRoute{ID: "OWN_PRODUCT"})
	if len(out.products) != 1 || out.err == nil || out.progress["OWN_PRODUCT"].Query != "저지방 우유" {
		t.Fatalf("products=%d error=%v progress=%+v", len(out.products), out.err, out.progress)
	}

}
func TestKurlyRetainsAllTwentyValidItems(t *testing.T) {
	items := []map[string]any{}
	for i := 0; i < 20; i++ {
		items = append(items, map[string]any{"no": 5063110 + i, "name": "우유", "salesPrice": 2780, "discountedPrice": 2780, "isSoldOut": false})
	}
	body, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"listSections": []any{map[string]any{"data": map[string]any{"items": items}}}, "meta": map[string]any{"pagination": map[string]any{"currentPage": 1, "totalPages": 2}}}})
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, string(body), "application/json"), nil
	})
	g, err := New(Config{Enabled: true, Control: &reviewControl{}, Client: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.SourceKurly)
	got, err := g.searchMallJSON(context.Background(), mall, "우유", 1)
	if err != nil || got.Items != 20 || len(got.Products) != 20 || !got.HasNext {
		t.Fatalf("raw=%d products=%d next=%v err=%v", got.Items, len(got.Products), got.HasNext, err)
	}

}

func TestWebDiscoveryKeepsFiveHitsWhenDetailsFailAndAdvances(t *testing.T) {
	items := []map[string]string{}
	for i := 0; i < 5; i++ {
		items = append(items, map[string]string{"title": "상품", "link": fmt.Sprintf("https://www.11st.co.kr/products/%d", 123456+i)})
	}
	body, _ := json.Marshal(map[string]any{"items": items})
	details := 0
	g, err := New(Config{Enabled: true, NaverClientID: "test", NaverClientSecret: "test", Control: &reviewControl{}, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "naverapihub.apigw.ntruss.com" {
			return response(200, string(body), "application/json"), nil
		}
		details++
		return response(503, `{}`, "application/json"), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	out := g.executeRoute(context.Background(), researchapp.KoreanSearchRequest{Query: "상품", Seeds: []string{"상품"}}, researchapp.ResearchRoute{Source: string(researchdomain.SourceElevenStreet), APIIDs: []string{"NAVER_WEBKR", "ELEVEN_STREET_HTML"}})
	if len(out.products) != 5 || details != 2 || out.progress["NAVER_WEBKR"].Page != 2 {
		t.Fatalf("products=%d details=%d progress=%+v err=%v", len(out.products), details, out.progress, out.err)
	}
}
func TestSerpPaginationUsesProviderOffsetAndDoesNotFollowURL(t *testing.T) {
	starts := []string{}
	g, err := New(Config{Enabled: true, SerpKey: "test", Control: &reviewControl{}, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		starts = append(starts, r.URL.Query().Get("start"))
		return response(200, `{"organic_results":[],"serpapi_pagination":{"next":"https://serpapi.com/search.json?start=5&api_key=discard-me"}}`, "application/json"), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.SourceElevenStreet)
	got, err := g.searchViaWebPage(context.Background(), mall, "상품", "SERP_GOOGLE", 1, 2, "", true)
	if err != nil || !got.HasNext || got.NextCursor != "5" {
		t.Fatalf("pagination=%+v %v", got, err)
	}
	got, err = g.searchViaWebPage(context.Background(), mall, "상품", "SERP_GOOGLE", 2, 2, got.NextCursor, true)
	if err != nil || got.HasNext || len(starts) != 2 || starts[1] != "5" {
		t.Fatalf("offset loop or skipped rows: %v %+v %v", starts, got, err)
	}
}

func TestMalformedKoreanItemKeepsOriginalPageCardinality(t *testing.T) {
	var rows tolerantItems[productSummary]
	if err := json.Unmarshal([]byte(`[{"product_id":42},{"product_id":"valid","product_title":"상품"}]`), &rows); err != nil || len(rows) != 2 || rows[0].ID != "" || rows[1].ID != "valid" {
		t.Fatalf("page lost or malformed row admitted: %+v %v", rows, err)
	}
}

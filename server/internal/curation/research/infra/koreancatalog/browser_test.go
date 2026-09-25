package koreancatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func TestBrowserDiscoveryCanonicalizesMerchantProductsAndRejectsForgedRows(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/discover" ||
			r.Header.Get("Authorization") != "Bearer "+token ||
			r.Header.Get("Origin") != "" {
			t.Fatalf("unexpected browser request: %s %s", r.Method, r.URL.Path)
		}
		var request browserDiscoveryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil ||
			request.Source != "ELEVENST" || request.Query != "여성 운동화" || request.Limit != 4 || request.Offset != 0 {
			t.Fatalf("request: %+v err=%v", request, err)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": 1,
			"results": []map[string]any{
				{"source": "ELEVENST", "url": "https://www.11st.co.kr/products/9541368732?tracking=removed", "title": "스케쳐스 워킹화", "context": "할인 판매가 32900원 무료배송", "priceText": "₩32900"},
				{"source": "MUSINSA", "url": "https://www.musinsa.com/products/7132338", "title": "다른 출처", "context": ""},
				{"source": "ELEVENST", "url": "https://www.11st.co.kr/browsing/MallPlanDetail.tmall", "title": "상품 아닌 문서", "context": ""},
			},
			"coverage": map[string]any{"source": "ELEVENST", "status": "SUCCEEDED", "count": 3},
		})
	}))
	defer server.Close()

	control := &admissionControl{}
	gateway, err := New(Config{
		Enabled: true, BrowserBaseURL: server.URL, BrowserToken: token,
		Control: control, Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.SourceElevenStreet)
	result, err := gateway.searchViaBrowser(context.Background(), mall, "여성 운동화", 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DiscoveryCompleted || result.Items != 3 || result.Rejected != 2 || len(result.Products) != 1 {
		t.Fatalf("result: %+v", result)
	}
	product := result.Products[0]
	if product.ProductURL != "https://www.11st.co.kr/products/9541368732" ||
		product.Provenance.APIProvider != "Vitlane Browser Fork" ||
		product.Provenance.DiscoveryChannel != "SERVER_BROWSER_PUBLIC" ||
		product.Price.Kind != "OBSERVED" || product.Price.AmountMinor == nil ||
		*product.Price.AmountMinor != 32900 || len(control.codes) != 1 || control.codes[0] != "SUCCESS" {
		t.Fatalf("product=%+v completion=%v", product, control.codes)
	}
}

func TestBrowserRouteUsesDurableOffsetAndAdvancesWhilePageIsFull(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request browserDiscoveryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Offset != 2 || request.Limit != 2 {
			t.Fatalf("request: %+v err=%v", request, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": 1,
			"results": []map[string]any{
				{"source": "ELEVENST", "url": "https://www.11st.co.kr/products/33333", "title": "세 번째 상품", "context": ""},
				{"source": "ELEVENST", "url": "https://www.11st.co.kr/products/44444", "title": "네 번째 상품", "context": ""},
			},
			"coverage": map[string]any{"source": "ELEVENST", "status": "SUCCEEDED", "count": 2},
		})
	}))
	defer server.Close()

	gateway, err := New(Config{
		Enabled: true, BrowserBaseURL: server.URL, BrowserToken: token,
		Control: &admissionControl{}, Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	query := "여성 운동화"
	key := browserDiscoveryAPIID + ":ELEVENST"
	out := gateway.executeRoute(context.Background(), researchapp.KoreanSearchRequest{
		CollectionBudget: researchapp.DiscoveryCollectionBudget{ProductDetails: 2},
		Query:            query, Seeds: []string{query},
		ExistingBrowserSourceCounts: map[researchdomain.Source]int{
			researchdomain.SourceElevenStreet: 2,
		},
		ExistingExternalProductKeys: map[string]bool{"elevenst:KR:33333": true},
	}, researchapp.ResearchRoute{
		ID: "BROWSER_MERCHANT:ELEVENST", Source: "ELEVENST", APIIDs: []string{browserDiscoveryAPIID},
	})
	if out.err != nil || len(out.products) != 1 || out.products[0].ProductRef.ProductID != "44444" ||
		out.progress[key].Page != 2 || out.progress[key].Query != query {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestBrowserDiscoveryConfigRequiresPairedTokenAndSafeOrigin(t *testing.T) {
	control := &admissionControl{}
	for _, config := range []Config{
		{Enabled: true, BrowserBaseURL: "http://127.0.0.1:8787", Control: control, Client: http.DefaultClient},
		{Enabled: true, BrowserBaseURL: "http://merchant.example", BrowserToken: "0123456789abcdef0123456789abcdef", Control: control, Client: http.DefaultClient},
		{Enabled: true, BrowserBaseURL: "http://127.0.0.1:8787/path", BrowserToken: "0123456789abcdef0123456789abcdef", Control: control, Client: http.DefaultClient},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("unsafe browser config was accepted: %+v", config)
		}
	}
}

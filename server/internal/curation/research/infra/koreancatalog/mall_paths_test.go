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

const kurlySearchBody = `{"success":true,"message":null,"data":{"topSections":[],"listSections":[{"view":"list","data":{"items":[
 {"no":5063110,"name":"[연세우유 x 마켓컬리] 전용목장우유 900mL","salesPrice":2780,"discountedPrice":null,"isSoldOut":false,"listImageUrl":"https://product-image.kurly.com/product/image/a.jpg","shortDescription":"매일 아침 신선하게"},
 {"no":5063111,"name":"[서울우유] 나 100% 1L","salesPrice":3100,"discountedPrice":2790,"isSoldOut":false,"listImageUrl":"https://product-image.kurly.com/product/image/b.jpg"},
 {"no":5063112,"name":"품절 우유","salesPrice":1000,"discountedPrice":null,"isSoldOut":true,"listImageUrl":"https://product-image.kurly.com/product/image/c.jpg"}
]}}],"meta":{"pagination":{"total":608,"count":96,"perPage":96,"currentPage":1,"totalPages":7}}}}`

func ownEmpty() *http.Response {
	return response(200, `{"status":"OK","data":{"products":[]}}`, "application/json")
}

func usageBody() *http.Response {
	return response(200, `{"status":"OK","data":{"api_id":"realtime_product_search","plan":{"is_free":true},"quotas":[{"name":"Requests","limit":100,"used":0,"remaining":100,"reset_at":"2099-01-01T00:00:00Z"}]}}`, "application/json")
}

// The Round's vertical decides which malls are asked directly. A fashion
// Round adds Zigzag through web search plus its public page; the page's
// og price meta has no currency, which reads as KRW.
func TestFashionRoundAsksZigzagThroughWebSearchAndPublicPage(t *testing.T) {
	webQueries := []string{}
	details := []string{}
	var transportMu sync.Mutex
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		switch {
		case r.URL.Path == "/usage":
			return usageBody(), nil
		case r.URL.Path == "/realtime-product-search/v2/search":
			return ownEmpty(), nil
		case r.URL.Path == "/search/v1/webkr":
			q := r.URL.Query().Get("query")
			webQueries = append(webQueries, q)
			if strings.Contains(q, "site:zigzag.kr/catalog/products") {
				return response(200, `{"items":[{"title":"플리츠 스커트","link":"https://zigzag.kr/catalog/products/108087931","description":"지그재그 상품"},{"title":"검색 결과","link":"https://zigzag.kr/search?keyword=skirt","description":""}]}`, "text/plain"), nil
			}
			return response(200, `{"items":[]}`, "text/plain"), nil
		case r.URL.Host == "zigzag.kr" && r.URL.Path == "/catalog/products/108087931":
			details = append(details, r.URL.Host+r.URL.Path)
			return response(200, `<html><head><meta property="og:title" content="플리츠 스커트 · 지그재그"><meta property="og:image" content="https://cf.zigzag.kr/a.jpg"><meta property="product:price:amount" content="51,750"><script type="application/ld+json">{"@type":"Product","name":"플리츠 스커트","brand":"ZZ"}</script></head></html>`, "text/html"), nil
		case r.URL.Path == "/realtime-web-search/search":
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		}
		t.Fatalf("unexpected request %s%s", r.URL.Host, r.URL.Path)
		return nil, nil
	})
	g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", Control: &admissionControl{}, Client: &http.Client{Transport: transport}})
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "플리츠 스커트", Country: "KR", Vertical: "FASHION", Seeds: []string{"플리츠 스커트"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(webQueries) != 2 || !strings.Contains(strings.Join(webQueries, " "), "site:11st.co.kr/products") || !strings.Contains(strings.Join(webQueries, " "), "site:zigzag.kr/catalog/products") {
		t.Fatalf("web queries=%v", webQueries)
	}
	if len(details) != 1 || len(result.Observations) != 1 {
		t.Fatalf("details=%v observations=%+v", details, result.Observations)
	}
	o := result.Observations[0]
	if o.ProductRef.Source != researchdomain.SourceZigzag || o.ProductRef.ProductID != "108087931" || o.Price.Kind != "OBSERVED" || *o.Price.AmountMinor != 51750 || o.Price.Currency != "KRW" || o.Title != "플리츠 스커트 · 지그재그" || o.ImageURL == "" || o.Provenance.DetailAPIProvider != "Zigzag" {
		t.Fatalf("zigzag observation=%+v", o)
	}
	if result.RejectedCount != 1 {
		t.Fatalf("search page must be rejected, not a candidate: %d", result.RejectedCount)
	}
	if _, ok := result.NextProgress["NAVER_WEBKR:ZIGZAG"]; !ok || result.NextProgress["NAVER_WEBKR:ZIGZAG"].Page != 2 {
		t.Fatalf("zigzag progress not scoped: %+v", result.NextProgress)
	}
	rows := map[researchdomain.Source]researchapp.SourceCoverage{}
	for _, c := range result.Coverage {
		rows[c.Source] = c
	}
	if rows[researchdomain.SourceZigzag].Status != "SUCCEEDED" || rows[researchdomain.SourceZigzag].CandidateCount != 1 || rows[researchdomain.SourceElevenStreet].Status != "EMPTY" || rows[researchdomain.SourceCoupang].Status != "EMPTY" {
		t.Fatalf("coverage=%+v", result.Coverage)
	}
	if _, asked := rows[researchdomain.SourceKurly]; asked {
		t.Fatal("a fashion Round must not ask Kurly")
	}
}

// A food Round reads Kurly's public search JSON: discovery and detail in one
// call, sold-out items rejected, paging from the response's own pagination.
func TestFoodRoundReadsKurlySearchJSON(t *testing.T) {
	kurlyCalls := 0
	var transportMu sync.Mutex
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		switch {
		case r.URL.Path == "/usage":
			return usageBody(), nil
		case r.URL.Path == "/realtime-product-search/v2/search":
			return ownEmpty(), nil
		case r.URL.Path == "/search/v1/webkr":
			return response(200, `{"items":[]}`, "text/plain"), nil
		case r.URL.Path == "/realtime-web-search/search":
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		case r.URL.Host == "api.kurly.com" && r.URL.Path == "/search/v4/sites/market/normal-search":
			kurlyCalls++
			if r.URL.Query().Get("keyword") != "우유" || r.URL.Query().Get("page") != "1" || r.Header.Get("x-api-key") != "" {
				t.Fatalf("kurly request %s headers=%v", r.URL.RawQuery, r.Header)
			}
			return response(200, kurlySearchBody, "application/json"), nil
		}
		t.Fatalf("unexpected request %s%s", r.URL.Host, r.URL.Path)
		return nil, nil
	})
	g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", Control: &admissionControl{}, Client: &http.Client{Transport: transport}})
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "우유", Country: "KR", Vertical: "FOOD", Seeds: []string{"우유"}})
	if err != nil {
		t.Fatal(err)
	}
	if kurlyCalls != 1 || len(result.Observations) != 2 || result.RejectedCount != 1 {
		t.Fatalf("calls=%d observations=%d rejected=%d", kurlyCalls, len(result.Observations), result.RejectedCount)
	}
	byID := map[string]researchdomain.ExternalProductObservation{}
	for _, o := range result.Observations {
		if o.ProductRef.Source != researchdomain.SourceKurly || o.Validate() != nil || o.Provenance.DiscoveryChannel != "KURLY_SEARCH" || o.Provenance.APIProvider != "Kurly" {
			t.Fatalf("kurly observation=%+v", o)
		}
		byID[o.ProductRef.ProductID] = o
	}
	if *byID["5063110"].Price.AmountMinor != 2780 || *byID["5063111"].Price.AmountMinor != 2790 || byID["5063110"].ProductURL != "https://www.kurly.com/goods/5063110" || byID["5063110"].ImageURL == "" {
		t.Fatalf("kurly prices/urls: %+v", byID)
	}
	if p := result.NextProgress["KURLY_JSON"]; p.Page != 2 {
		t.Fatalf("kurly paging must follow the response pagination: %+v", result.NextProgress)
	}
	rows := map[researchdomain.Source]researchapp.SourceCoverage{}
	for _, c := range result.Coverage {
		rows[c.Source] = c
	}
	if rows[researchdomain.SourceKurly].Status != "SUCCEEDED" || rows[researchdomain.SourceKurly].CandidateCount != 2 {
		t.Fatalf("coverage=%+v", result.Coverage)
	}
}

// A living Round asks Lotte ON and Daiso Mall, whose pages carry JSON-LD
// Product with the id as sku; a mismatched JSON-LD product never lends its
// price. An unknown vertical asks only 11st.
func TestLivingRoundReadsJSONLDPagesAndUnknownVerticalStaysOnElevenStreet(t *testing.T) {
	webQueries := []string{}
	var transportMu sync.Mutex
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		switch {
		case r.URL.Path == "/usage":
			return usageBody(), nil
		case r.URL.Path == "/realtime-product-search/v2/search":
			return ownEmpty(), nil
		case r.URL.Path == "/search/v1/webkr":
			q := r.URL.Query().Get("query")
			webQueries = append(webQueries, q)
			if strings.Contains(q, "site:lotteon.com/p/product") {
				return response(200, `{"items":[{"title":"무선 청소기","link":"https://www.lotteon.com/p/product/LO1489459593","description":""}]}`, "text/plain"), nil
			}
			if strings.Contains(q, "site:daisomall.co.kr/pd/pdr") {
				return response(200, `{"items":[{"title":"미니 청소기","link":"https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=1058641","description":""}]}`, "text/plain"), nil
			}
			return response(200, `{"items":[]}`, "text/plain"), nil
		case r.URL.Host == "www.lotteon.com" && r.URL.Path == "/p/product/LO1489459593":
			return response(200, `<html><head><meta property="og:title" content="무선 청소기 | 롯데ON"><script type="application/ld+json">{"@type":"Product","name":"무선 청소기","sku":"LO1489459593","offers":{"@type":"Offer","price":25900,"priceCurrency":"KRW","seller":{"name":"롯데ON"}}}</script></head></html>`, "text/html"), nil
		case r.URL.Host == "www.daisomall.co.kr" && r.URL.Path == "/pd/pdr/SCR_PDR_0001":
			if r.URL.Query().Get("pdNo") != "1058641" {
				t.Fatalf("daiso query %s", r.URL.RawQuery)
			}
			return response(200, `<html><head><meta property="og:title" content="미니 청소기 · 다이소몰"><script type="application/ld+json">[{"@type":"Product","name":"다른 상품","sku":"9999999","offers":{"price":"1000","priceCurrency":"KRW"}},{"@type":"Product","name":"미니 청소기","sku":"1058641","offers":{"price":"3000","priceCurrency":"KRW"}}]</script></head></html>`, "text/html"), nil
		case r.URL.Path == "/realtime-web-search/search":
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		}
		t.Fatalf("unexpected request %s%s", r.URL.Host, r.URL.Path)
		return nil, nil
	})
	g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", Control: &admissionControl{}, Client: &http.Client{Transport: transport}})
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "미니 청소기", Country: "KR", Vertical: "LIVING", Seeds: []string{"미니 청소기"}})
	if err != nil {
		t.Fatal(err)
	}
	prices := map[researchdomain.Source]int64{}
	for _, o := range result.Observations {
		if o.Price.Kind != "OBSERVED" {
			t.Fatalf("price missing for %+v", o)
		}
		prices[o.ProductRef.Source] = *o.Price.AmountMinor
	}
	if prices[researchdomain.SourceLotteon] != 25900 || prices[researchdomain.SourceDaisomall] != 3000 || len(result.Observations) != 2 {
		t.Fatalf("living observations=%+v", result.Observations)
	}
	if len(webQueries) != 3 {
		t.Fatalf("a living Round asks 11st, Lotte ON and Daiso Mall: %v", webQueries)
	}

	webQueries = nil
	result, err = g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "미니 청소기", Country: "KR", Seeds: []string{"미니 청소기"}})
	// Every asked path answered, just with nothing: an empty Round, not a failure.
	if err != nil || len(result.Observations) != 0 {
		t.Fatalf("empty answers are not failures: err=%v observations=%d", err, len(result.Observations))
	}
	if len(webQueries) != 1 || !strings.Contains(webQueries[0], "site:11st.co.kr/products") {
		t.Fatalf("unknown vertical must ask only 11st: %v", webQueries)
	}
}

func TestOutboundOriginsArePinnedToProvidersAndRegistryHosts(t *testing.T) {
	for base, ok := range map[string]bool{"https://api.openwebninja.com": true, "https://zigzag.kr": true, "https://api.kurly.com": true, "https://www.daisomall.co.kr": true, "https://www.musinsa.com": false, "https://evil.example": false, "http://www.11st.co.kr": false} {
		if allowedBase(base) != ok {
			t.Fatalf("%s allowed=%v want %v", base, allowedBase(base), ok)
		}
	}
	for _, id := range []string{"KURLY_JSON", "ZIGZAG_HTML", "LOTTEON_HTML", "DAISOMALL_HTML", "ELEVENST_HTML"} {
		g, _ := New(Config{Enabled: true, Control: &admissionControl{}, Client: &http.Client{}})
		if !g.Configured(id) {
			t.Fatalf("%s needs no credential", id)
		}
	}
}

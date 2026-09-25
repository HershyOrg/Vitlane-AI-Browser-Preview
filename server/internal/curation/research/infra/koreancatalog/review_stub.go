package koreancatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

// These are sanitized review observations, not raw responses or a live
// catalog. They preserve the reviewed sample title, id, URL and, where
// available, price without inventing missing values.
//
//go:embed review_observations.json
var reviewObservationsJSON []byte

func reviewObservations() map[string][]researchdomain.ExternalProductObservation {
	var records map[string][]researchdomain.ExternalProductObservation
	if err := json.Unmarshal(reviewObservationsJSON, &records); err != nil {
		panic(err)
	}
	return records
}
func reviewObservedAt(id string) time.Time {
	var earliest time.Time
	for _, rows := range reviewObservations() {
		for _, o := range rows {
			if o.ProductRef.ProductID == id && (earliest.IsZero() || o.ObservedAt.Before(earliest)) {
				earliest = o.ObservedAt
			}
		}
	}
	return earliest
}

// NewReviewStub exercises the real parsers, API controls, local budgets and call
// ledger without external HTTP or provider credentials. Use a dedicated review DB.
func NewReviewStub(environment string, control researchapp.CatalogProviderControlRepository) (*Gateway, error) {
	if environment != "development" && environment != "test" {
		return nil, fmt.Errorf("Korean catalog review stub requires development or test")
	}
	client, err := sharedhttpclient.NewClient(reviewTransport{}, 35*time.Second)
	if err != nil {
		return nil, err
	}
	g, err := New(Config{Enabled: true, OWNKey: "review-stub", NaverClientID: "review-stub", NaverClientSecret: "review-stub", SerpKey: "review-stub", Control: control, Client: client})
	if err == nil {
		g.reviewStub = true
	}
	return g, err
}

type reviewTransport struct{}

func (reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	if r.Method != http.MethodGet {
		return nil, fmt.Errorf("review fixture request unsupported")
	}
	records := reviewObservations()
	q := strings.ToLower(r.URL.Query().Get("q") + " " + r.URL.Query().Get("query") + " " + r.URL.Query().Get("keyword"))
	// Deliberately small fixture matching, never a substitute for the live query model.
	pen := strings.Contains(q, "라미") || strings.Contains(q, "만년필") || strings.Contains(q, "lamy") || strings.Contains(q, "pen") || strings.Contains(q, "safari")
	buds := strings.Contains(q, "버즈") || strings.Contains(q, "이어폰") || strings.Contains(q, "buds") || strings.Contains(q, "ear") || strings.Contains(q, "galaxy")
	// The direct mall paths (PR 4a) each hold exactly one audited product.
	mall := map[string]bool{
		"KURLY_JSON":     strings.Contains(q, "우유") || strings.Contains(q, "milk"),
		"ZIGZAG_HTML":    strings.Contains(q, "바지") || strings.Contains(q, "팬츠") || strings.Contains(q, "pants"),
		"DAISOMALL_HTML": strings.Contains(q, "케이블") || strings.Contains(q, "cable"),
	}
	body, contentType, status := any(nil), "application/json", 200
	switch r.URL.Host + r.URL.Path {
	case "api.openwebninja.com/usage":
		body = map[string]any{"status": "OK", "data": map[string]any{"api_id": r.URL.Query().Get("api_id"), "plan": map[string]any{"is_free": true}, "quotas": []any{map[string]any{"name": "Requests", "limit": 100, "used": 0, "remaining": 100, "reset_at": time.Now().UTC().Add(30 * 24 * time.Hour)}}}}
	case "serpapi.com/account":
		body = map[string]any{"searches_per_month": 250, "this_month_usage": 0, "total_searches_left": 250, "plan_renewal_date": time.Now().UTC().Add(30 * 24 * time.Hour).Format("2006-01-02")}
	case "api.openwebninja.com/realtime-product-search/v2/search":
		products := []any{}
		if buds {
			o := records["OWN_PRODUCT"][0]
			products = append(products, map[string]any{"product_id": "review-buds", "product_title": o.Title})
		}
		body = map[string]any{"status": "OK", "data": map[string]any{"products": products}}
	case "api.openwebninja.com/realtime-product-search/v2/product-details":
		if r.URL.Query().Get("product_id") != "review-buds" {
			return nil, fmt.Errorf("review product unknown")
		}
		o := records["OWN_PRODUCT"][0]
		body = map[string]any{"status": "OK", "data": map[string]any{"product_title": o.Title, "offers": []any{map[string]any{"offer_page_url": o.ProductURL, "price": fmt.Sprintf("₩%d", *o.Price.AmountMinor)}}}}
	case "api.kurly.com/search/v4/sites/market/normal-search":
		items := []any{}
		if mall["KURLY_JSON"] {
			for _, o := range records["KURLY_JSON"] {
				item := map[string]any{"no": json.Number(o.ProductRef.ProductID), "name": o.Title, "isSoldOut": false, "listImageUrl": "", "salesPrice": nil, "discountedPrice": nil}
				if o.Price.Kind == "OBSERVED" {
					item["salesPrice"] = *o.Price.AmountMinor
				}
				items = append(items, item)
			}
		}
		sections := []any{}
		if len(items) > 0 {
			sections = append(sections, map[string]any{"view": "list", "data": map[string]any{"items": items}})
		}
		body = map[string]any{"success": true, "data": map[string]any{"listSections": sections, "meta": map[string]any{"pagination": map[string]any{"currentPage": 1, "totalPages": 1}}}}
	case "naverapihub.apigw.ntruss.com/search/v1/webkr", "api.openwebninja.com/realtime-web-search/search", "serpapi.com/search.json":
		id := "NAVER_WEBKR"
		if r.URL.Host == "api.openwebninja.com" {
			id = "OWN_WEB"
		}
		if r.URL.Host == "serpapi.com" {
			id = "SERP_GOOGLE"
		}
		links := []any{}
		if pen && strings.Contains(q, "site:11st.co.kr") {
			for _, o := range records[id] {
				links = append(links, map[string]any{"title": o.Title, "link": o.ProductURL, "url": o.ProductURL})
			}
		}
		// Each mall answers its own site: search with its audited product, and
		// nothing when the query is not that product.
		for _, key := range []string{"ZIGZAG_HTML", "DAISOMALL_HTML"} {
			registered, ok := researchdomain.KoreanMall(mallSource(key))
			if !ok || !mall[key] || !strings.Contains(q, strings.ToLower(registered.SiteFilter)) {
				continue
			}
			for _, o := range records[key] {
				links = append(links, map[string]any{"title": o.Title, "link": o.ProductURL, "url": o.ProductURL})
			}
		}
		if id == "NAVER_WEBKR" {
			body = map[string]any{"items": links}
		} else if id == "OWN_WEB" {
			body = map[string]any{"status": "OK", "data": map[string]any{"organic_results": links}}
		} else {
			body = map[string]any{"organic_results": links}
		}
	default:
		page := "https://" + r.URL.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			page += "?" + r.URL.RawQuery
		}
		if r.URL.Host != "www.11st.co.kr" && !reviewMallPage(records, page) {
			return nil, fmt.Errorf("review endpoint unsupported")
		}
		found := false
		for _, rows := range records {
			for _, o := range rows {
				if o.ProductURL != page && !(r.URL.Host == "www.11st.co.kr" && o.ProductRef.ProductID == strings.TrimPrefix(r.URL.Path, "/products/")) {
					continue
				}
				found = true
				contentType = "text/html"
				// Daiso Mall publishes the price only as JSON-LD; 11st and
				// Zigzag publish og and price metadata. The fixture mirrors
				// each page's real shape so the parsers stay honest.
				if o.ProductRef.Source == researchdomain.SourceDaisomall {
					offers := ""
					if o.Price.Kind == "OBSERVED" {
						offers = fmt.Sprintf(`,"offers":{"@type":"Offer","price":"%d","priceCurrency":"KRW"}`, *o.Price.AmountMinor)
					}
					body = `<meta property="og:title" content="` + html.EscapeString(o.Title) + `">` +
						`<script type="application/ld+json">{"@type":"Product","name":"` + html.EscapeString(o.Title) + `","sku":"` + o.ProductRef.ProductID + `"` + offers + `}</script>`
					continue
				}
				body = `<meta property="og:title" content="` + html.EscapeString(o.Title) + `">`
				if o.Price.Kind == "OBSERVED" {
					body = body.(string) + fmt.Sprintf(`<meta property="product:price:amount" content="%d"><meta property="product:price:currency" content="KRW">`, *o.Price.AmountMinor)
				}
			}
		}
		if !found {
			status = 404
			body = "review product unknown"
		}
	}
	var raw []byte
	if contentType == "text/html" {
		raw = []byte(body.(string))
	} else {
		raw, _ = json.Marshal(body)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
}

// mallSource maps a mall API id to the registry source the fixture answers for.
func mallSource(detailAPI string) researchdomain.Source {
	for _, m := range researchdomain.KoreanMalls() {
		if m.DetailAPI == detailAPI {
			return m.Source
		}
	}
	return ""
}

// reviewMallPage reports whether the fixture holds this exact product page.
func reviewMallPage(records map[string][]researchdomain.ExternalProductObservation, page string) bool {
	for _, rows := range records {
		for _, o := range rows {
			if o.ProductURL == page {
				return true
			}
		}
	}
	return false
}

package http

import (
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type amazonHTTPFake struct {
	catalogWorkspaceHTTPServiceFakeV2
	calls int
	input researchapp.MarkExternalPurchaseInput
}

func (f *amazonHTTPFake) ResolveAmazonVariant(context.Context, string, string, string, string, researchdomain.SourceVariantRef) (researchapp.CatalogProductObservation, error) {
	panic("unexpected provider call")
}
func (f *amazonHTTPFake) ResolveExternalLink(context.Context, string, string, string) (string, error) {
	return "https://www.amazon.com/dp/B012345678", nil
}
func (f *amazonHTTPFake) AmazonState(context.Context, string, string, string) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *amazonHTTPFake) MarkExternalPurchase(_ context.Context, in researchapp.MarkExternalPurchaseInput) (researchapp.PurchaseFeedback, error) {
	f.calls++
	f.input = in
	return researchapp.PurchaseFeedback{Version: 1, Records: []researchapp.ExternalPurchaseRecord{}}, nil
}
func TestAmazonHTTPRequiresOwnerAndExplicitDesiredState(t *testing.T) {
	fake := &amazonHTTPFake{}
	h := NewLiveCatalogReviewHandlerV2(fake)
	for _, action := range []string{"state", "external-link", "resolve", "purchase-check"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("amazonAction", action)
		w := httptest.NewRecorder()
		h.AmazonCandidate(w, r)
		if w.Code != 401 {
			t.Fatalf("anonymous %s status=%d", action, w.Code)
		}
	}
	for _, body := range []string{`{"variantRef":{"source":"AMAZON","marketplace":"US","asin":"B012345678"}}`, `{"variantRef":{"source":"AMAZON","marketplace":"US","asin":"B012345678"},"checked":true,"expectedVersion":0}`} {
		r := catalogWorkspaceHTTPRequestV2(body, "command-1")
		r.SetPathValue("candidateId", "candidate-1")
		r.SetPathValue("amazonAction", "purchase-check")
		w := httptest.NewRecorder()
		h.AmazonCandidate(w, r)
		if strings.Contains(body, "checked") {
			if w.Code != 200 || fake.calls != 1 || fake.input.UserID != "user-1" || fake.input.CandidateID != "candidate-1" {
				t.Fatalf("owner forwarding: %d %#v", w.Code, fake.input)
			}
		} else if w.Code != 400 || fake.calls != 0 {
			t.Fatalf("missing desired state: %d", w.Code)
		}
	}
}
func TestUnknownAmazonPriceNeverSerializesAsZeroOrPrivateVendor(t *testing.T) {
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B012345678"}
	result := mapLiveCatalogReviewResultV2(researchapp.LiveCatalogReviewResultV2{Search: researchapp.CatalogProductSearchResult{Products: []researchapp.CatalogProductObservation{{ProviderProductID: "candidate-1", SourceProductRef: &ref, VariantObservation: &researchdomain.VariantObservation{Price: researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_OBSERVED"}}}}}})
	raw, err := json.Marshal(result.Products[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, `"priceMinimumMinor":0`) || strings.Contains(text, "OPENWEBNINJA") || strings.Contains(text, "providerProductId") || !strings.Contains(text, `"candidateId":"candidate-1"`) {
		t.Fatal(text)
	}
}

// A Shopify product the server has not read from Shopify — the DB-only workspace
// skeleton, or a lookup that left it unresolved — has no price. Its zero must
// never reach the wire as "priceMinimumMinor":0; a read product keeps its range.
func TestUnreadShopifyPriceNeverSerializesAsZero(t *testing.T) {
	result := mapLiveCatalogReviewResultV2(researchapp.LiveCatalogReviewResultV2{Search: researchapp.CatalogProductSearchResult{Products: []researchapp.CatalogProductObservation{
		{ProviderProductID: "skeleton"},
		{ProviderProductID: "read", Title: "Camp chair", PriceRange: researchapp.CatalogPriceRange{
			Minimum: researchapp.CatalogMoney{AmountMinor: 2499, Currency: "USD"}, Maximum: researchapp.CatalogMoney{AmountMinor: 3999, Currency: "USD"}}},
		{ProviderProductID: "free-sample", Title: "Sample", PriceRange: researchapp.CatalogPriceRange{
			Minimum: researchapp.CatalogMoney{AmountMinor: 0, Currency: "USD"}, Maximum: researchapp.CatalogMoney{AmountMinor: 0, Currency: "USD"}}},
	}}})
	texts := make([]string, 0, 3)
	for _, product := range result.Products {
		raw, err := json.Marshal(product)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(raw))
	}
	if strings.Contains(texts[0], "priceMinimumMinor") || strings.Contains(texts[0], "priceMaximumMinor") {
		t.Fatalf("an unread product states no price: %s", texts[0])
	}
	if !strings.Contains(texts[1], `"priceMinimumMinor":2499`) || !strings.Contains(texts[1], `"priceMaximumMinor":3999`) || !strings.Contains(texts[1], `"currency":"USD"`) {
		t.Fatalf("a read product keeps its range: %s", texts[1])
	}
	// A price Shopify actually stated as zero, with its currency, is still a price.
	if !strings.Contains(texts[2], `"priceMinimumMinor":0`) || !strings.Contains(texts[2], `"currency":"USD"`) {
		t.Fatalf("an observed zero is kept: %s", texts[2])
	}
}

type savedAmazonUsage struct {
	researchapp.CatalogAPIUsageRepository
	value researchapp.CatalogAPIUsage
}

func (s savedAmazonUsage) ReadAmazonUsage(context.Context) (researchapp.CatalogAPIUsage, error) {
	return s.value, nil
}

type stubUsageGateway struct {
	researchapp.AmazonCatalogGateway
}

func (stubUsageGateway) RefreshAmazonUsage(context.Context) (researchapp.CatalogAPIUsage, error) {
	return researchapp.CatalogAPIUsage{Mode: "STUB"}, nil
}

func TestAmazonStubUsagePreservesLastLiveQuotaAndLabelsMode(t *testing.T) {
	at := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	repo := savedAmazonUsage{value: researchapp.CatalogAPIUsage{
		Source: researchdomain.SourceAmazon, Requests24h: 48,
		Quota: &researchapp.CatalogAPIQuota{Limit: 100, Remaining: 45, ObservedAt: at},
	}}
	h := AmazonUsageHandler{Mode: "STUB", Control: &amazonHTTPControl{enabled: true, version: 1}, Repo: repo, Gateway: stubUsageGateway{}}
	for _, query := range []string{"", "?refresh=true"} {
		w := httptest.NewRecorder()
		h.Usage(w, httptest.NewRequest("GET", "/usage"+query, nil))
		var got researchapp.CatalogAPIUsage
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || got.Mode != "STUB" || !got.Enabled || got.Requests24h != 48 || got.Quota.Remaining != 45 || !got.Quota.ObservedAt.Equal(at) {
			t.Fatalf("stub changed historical usage: %#v", got)
		}
	}
	h.Gateway = nil
	w := httptest.NewRecorder()
	h.Usage(w, httptest.NewRequest("GET", "/usage", nil))
	if !strings.Contains(w.Body.String(), `"mode":"DISABLED"`) {
		t.Fatal(w.Body.String())
	}
}

type amazonHTTPControl struct {
	enabled bool
	version int64
	writes  int
}

func (c *amazonHTTPControl) ReadAmazonControl(context.Context) (researchapp.AmazonSourceControl, error) {
	return researchapp.AmazonSourceControl{Enabled: c.enabled, Version: c.version}, nil
}
func (c *amazonHTTPControl) UpdateAmazonControl(_ context.Context, _ string, enabled bool, _ int64, _ time.Time) (researchapp.AmazonSourceControl, error) {
	c.enabled = enabled
	c.version++
	c.writes++
	return c.ReadAmazonControl(context.Background())
}
func TestAmazonControlRequiresExplicitStateAndNeverRefreshesQuota(t *testing.T) {
	c := &amazonHTTPControl{enabled: true, version: 1}
	h := AmazonUsageHandler{Mode: "STUB", Control: c, Gateway: stubUsageGateway{}}
	for _, body := range []string{`{}`, `{"enabled":false}`, `{"enabled":false,"expectedVersion":0}`, `{"enabled":false,"expectedVersion":1}`} {
		r := catalogWorkspaceHTTPRequestV2(body, "")
		w := httptest.NewRecorder()
		h.SetControl(w, r)
		if strings.Contains(body, `"expectedVersion":1`) {
			if w.Code != 200 || c.writes != 1 || c.enabled {
				t.Fatalf("set: %d %s", w.Code, w.Body.String())
			}
		} else if w.Code != 400 || c.writes != 0 {
			t.Fatalf("invalid: %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.SetControl(w, httptest.NewRequest("PUT", "/control", strings.NewReader(`{"enabled":true,"expectedVersion":2}`)))
	if w.Code != 401 || c.writes != 1 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	h.Gateway = nil
	w = httptest.NewRecorder()
	h.SetControl(w, catalogWorkspaceHTTPRequestV2(`{"enabled":true,"expectedVersion":2}`, ""))
	if w.Code != 409 || c.writes != 1 {
		t.Fatalf("unconfigured: %d", w.Code)
	}
}

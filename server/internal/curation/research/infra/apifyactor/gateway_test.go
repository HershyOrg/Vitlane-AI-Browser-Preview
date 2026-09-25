package apifyactor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type memoryControl struct {
	researchapp.CatalogProviderControlRepository
	reserved []string
	codes    []string
	statuses []int
	retries  []int
	deny     error
}

func (c *memoryControl) ReserveProviderCall(_ context.Context, id, operation string, _ time.Time) (string, error) {
	if c.deny != nil {
		return "", c.deny
	}
	c.reserved = append(c.reserved, id+":"+operation)
	return "call", nil
}

func (c *memoryControl) CompleteProviderCall(_ context.Context, _ string, code string, status int, retry int, _ time.Time) error {
	c.codes = append(c.codes, code)
	c.statuses = append(c.statuses, status)
	c.retries = append(c.retries, retry)
	return nil
}

type memoryLedger struct {
	begun    []researchapp.ActorRun
	finished []researchapp.ActorRun
	existing *researchapp.ActorRun
	spend    int64
}

func (l *memoryLedger) BeginActorRun(_ context.Context, run researchapp.ActorRun) (researchapp.ActorRun, bool, error) {
	if l.existing != nil {
		return *l.existing, false, nil
	}
	l.begun = append(l.begun, run)
	run.Status = researchapp.ActorRunRunning
	return run, true, nil
}

func (l *memoryLedger) FinishActorRun(_ context.Context, run researchapp.ActorRun) error {
	l.finished = append(l.finished, run)
	return nil
}

func (l *memoryLedger) ActorSpendMicros(context.Context, time.Time) (int64, error) {
	return l.spend, nil
}

func musinsa(t *testing.T) researchdomain.KRMall {
	t.Helper()
	mall, ok := researchdomain.KoreanMall(researchdomain.SourceMusinsa)
	if !ok || mall.Detail != researchdomain.DetailActor {
		t.Fatalf("Musinsa is not an Actor mall: %+v", mall)
	}
	return mall
}

func fixedNow() time.Time { return time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC) }

// A successful run maps only the mall's own product pages, drops sold-out and
// sponsored rows, settles the ledger with the provider's cost, and closes the
// admission call.
func TestActorRunAdmitsOwnProductsAndSettlesCost(t *testing.T) {
	const items = `[
	 {"product_id":"5198233","name":"슬림핏 스퀘어넥 티셔츠","price_krw":48000,"sale_price_krw":39000,"url":"https://www.musinsa.com/products/5198233","image_url":"https://image.musinsa.com/a.jpg"},
	 {"product_id":"5198234","name":"품절 티셔츠","price_krw":10000,"is_sold_out":true,"url":"https://www.musinsa.com/products/5198234"},
	 {"product_id":"1","name":"다른 몰","price_krw":1000,"url":"https://www.coupang.com/vp/products/1"},
	 {"product_id":"2","name":"상품 아님","price_krw":1000,"url":"https://www.musinsa.com/search?q=tee"}
	]`
	paths := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v2/acts/"):
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatal("run started without the token")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"mode":"search"`) || !strings.Contains(string(body), `"maxItems":5`) {
				t.Fatalf("actor input=%s", body)
			}
			return jsonResponse(201, `{"data":{"id":"run-1","status":"RUNNING"}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v2/actor-runs/run-1":
			return jsonResponse(200, `{"data":{"id":"run-1","status":"SUCCEEDED","defaultDatasetId":"ds-1","usageTotalUsd":0.02005}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v2/datasets/ds-1/items":
			return jsonResponse(200, items), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return nil, nil
	})}
	control := &memoryControl{}
	ledger := &memoryLedger{}
	g, err := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: control, Ledger: ledger,
		Now: fixedNow, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-1:MUSINSA", UserID: "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Products) != 1 || result.Items != 4 || result.Rejected != 3 || result.Reused {
		t.Fatalf("result=%+v", result)
	}
	product := result.Products[0]
	if product.ProductRef.Source != researchdomain.SourceMusinsa || product.ProductRef.ProductID != "5198233" ||
		product.ProductURL != "https://www.musinsa.com/products/5198233" || product.Price.Kind != "OBSERVED" ||
		*product.Price.AmountMinor != 39000 || product.Price.Currency != "KRW" ||
		product.Provenance.APIProvider != "Apify" || product.Provenance.DiscoveryChannel != "ACTOR_SEARCH" ||
		product.ImageURL == "" || product.Validate() != nil {
		t.Fatalf("observation=%+v", product)
	}
	if len(ledger.begun) != 1 || ledger.begun[0].RunKey != "attempt-1:MUSINSA" || ledger.begun[0].APIID != "APIFY_MUSINSA" {
		t.Fatalf("ledger begun=%+v", ledger.begun)
	}
	// The provider reported more than the published prices imply, so its own
	// number is what the ledger keeps.
	if len(ledger.finished) != 1 || ledger.finished[0].Status != researchapp.ActorRunSucceeded ||
		ledger.finished[0].ProviderRunID != "run-1" || ledger.finished[0].ItemCount != 4 ||
		ledger.finished[0].CostMicros != 20051 || result.CostMicros != 20051 {
		t.Fatalf("ledger finished=%+v cost=%d", ledger.finished, result.CostMicros)
	}
	if len(control.reserved) != 1 || control.reserved[0] != "APIFY_MUSINSA:SEARCH" || len(control.codes) != 1 || control.codes[0] != "SUCCESS" {
		t.Fatalf("admission reserved=%v codes=%v", control.reserved, control.codes)
	}
}

// The monthly cap is checked before anything is reserved or started.
func TestActorRunStopsAtTheMonthlyCap(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("a capped month still called %s", r.URL)
		return nil, nil
	})}
	control := &memoryControl{}
	ledger := &memoryLedger{spend: 5_000_000}
	g, _ := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: control, Ledger: ledger, Now: fixedNow})
	_, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-1:MUSINSA", UserID: "user-1"})
	if f, ok := fault.As(err); !ok || f.Reason != "CATALOG_ACTOR_BUDGET_EXHAUSTED" {
		t.Fatalf("err=%v", err)
	}
	if len(control.reserved) != 0 || len(ledger.begun) != 0 {
		t.Fatalf("a capped month reserved=%v begun=%v", control.reserved, ledger.begun)
	}
}

// A retry of the same attempt never buys the same search twice: a finished run
// is read again for free, and anything else is refused.
func TestActorRunIsNeverBoughtTwiceForTheSameAttempt(t *testing.T) {
	const items = `[{"product_id":"5198233","name":"티셔츠","price_krw":39000,"url":"https://www.musinsa.com/products/5198233"}]`
	starts := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			starts++
			return jsonResponse(201, `{"data":{"id":"run-2","status":"RUNNING"}}`), nil
		}
		if r.URL.Path == "/v2/actor-runs/run-1" {
			return jsonResponse(200, `{"data":{"id":"run-1","status":"SUCCEEDED","defaultDatasetId":"ds-1","usageTotalUsd":0.02}}`), nil
		}
		return jsonResponse(200, items), nil
	})}
	finished := fixedNow()
	done := researchapp.ActorRun{RunKey: "attempt-1:MUSINSA", Status: researchapp.ActorRunSucceeded, ProviderRunID: "run-1", FinishedAt: &finished}
	ledger := &memoryLedger{existing: &done}
	control := &memoryControl{}
	g, _ := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: control, Ledger: ledger,
		Now: fixedNow, Sleep: func(context.Context, time.Duration) error { return nil }})
	result, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-1:MUSINSA", UserID: "user-1"})
	if err != nil || !result.Reused || len(result.Products) != 1 || starts != 0 {
		t.Fatalf("reuse result=%+v err=%v starts=%d", result, err, starts)
	}

	running := researchapp.ActorRun{RunKey: "attempt-1:MUSINSA", Status: researchapp.ActorRunRunning, ProviderRunID: "run-9"}
	ledger.existing = &running
	_, err = g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-1:MUSINSA", UserID: "user-1"})
	if f, ok := fault.As(err); !ok || f.Reason != "CATALOG_ACTOR_RUN_ALREADY_SPENT" || starts != 0 {
		t.Fatalf("err=%v starts=%d", err, starts)
	}
}

// A cancelled Round aborts the run instead of leaving it to finish and bill.
func TestActorRunAbortsWhenTheRoundIsCancelled(t *testing.T) {
	aborted := false
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
			aborted = true
			return jsonResponse(200, `{"data":{"id":"run-1","status":"ABORTED"}}`), nil
		case r.Method == http.MethodPost:
			return jsonResponse(201, `{"data":{"id":"run-1","status":"RUNNING"}}`), nil
		}
		return jsonResponse(200, `{"data":{"id":"run-1","status":"RUNNING"}}`), nil
	})}
	control := &memoryControl{}
	ledger := &memoryLedger{}
	g, _ := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: control, Ledger: ledger, Now: fixedNow,
		Sleep: func(context.Context, time.Duration) error { cancel(); return context.Canceled }})
	_, err := g.SearchMallActor(ctx, researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-2:MUSINSA", UserID: "user-1"})
	if f, ok := fault.As(err); !ok || f.Reason != "CATALOG_REQUEST_CANCELLED" {
		t.Fatalf("err=%v", err)
	}
	if !aborted || len(ledger.finished) != 1 || ledger.finished[0].Status != researchapp.ActorRunAborted {
		t.Fatalf("aborted=%v ledger=%+v", aborted, ledger.finished)
	}
}

// Without a token the gateway is simply not configured, and a mall Vitlane has
// no Actor for is never run.
func TestActorGatewayRefusesUnknownMallsAndMissingToken(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unconfigured gateway called out")
		return nil, nil
	})}
	g, _ := New(Config{Token: "", MonthlyCapMicros: 5_000_000, Client: client, Control: &memoryControl{}, Ledger: &memoryLedger{}, Now: fixedNow})
	if g.Configured() {
		t.Fatal("a gateway without a token reported configured")
	}
	if _, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "티셔츠", RunKey: "a:MUSINSA", UserID: "user-1"}); err == nil {
		t.Fatal("missing token accepted")
	}
	kurly, _ := researchdomain.KoreanMall(researchdomain.SourceKurly)
	withToken, _ := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: &memoryControl{}, Ledger: &memoryLedger{}, Now: fixedNow})
	if _, err := withToken.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: kurly, Query: "우유", RunKey: "a:KURLY", UserID: "user-1"}); err == nil {
		t.Fatal("a mall with no Actor was run")
	}
}

// Per-item charges settle after a run ends, so a run read at completion often
// reports only its start event. The ledger must still record what the run
// costs, or the monthly cap would wave through runs the account pays for.
func TestActorRunRecordsThePublishedPriceWhenTheProviderHasNotSettled(t *testing.T) {
	const items = `[
	 {"product_id":"5198233","name":"티셔츠 하나","price_krw":39000,"url":"https://www.musinsa.com/products/5198233"},
	 {"product_id":"5198235","name":"티셔츠 둘","price_krw":29000,"url":"https://www.musinsa.com/products/5198235"}
	]`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost:
			return jsonResponse(201, `{"data":{"id":"run-3","status":"RUNNING"}}`), nil
		case r.URL.Path == "/v2/actor-runs/run-3":
			// 0.000051 USD: the start event only, exactly what a freshly
			// finished run reports.
			return jsonResponse(200, `{"data":{"id":"run-3","status":"SUCCEEDED","defaultDatasetId":"ds-3","usageTotalUsd":0.000051}}`), nil
		}
		return jsonResponse(200, items), nil
	})}
	ledger := &memoryLedger{}
	g, _ := New(Config{Token: "token", MonthlyCapMicros: 5_000_000, Client: client, Control: &memoryControl{}, Ledger: ledger,
		Now: fixedNow, Sleep: func(context.Context, time.Duration) error { return nil }})
	result, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{
		Mall: musinsa(t), Query: "반팔 티셔츠", RunKey: "attempt-3:MUSINSA", UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	// Two items at the measured $0.004 plus the start event.
	if result.CostMicros != 8050 || len(ledger.finished) != 1 || ledger.finished[0].CostMicros != 8050 {
		t.Fatalf("cost=%d ledger=%+v", result.CostMicros, ledger.finished)
	}
	if len(result.Products) != 2 {
		t.Fatalf("products=%d", len(result.Products))
	}
}

func TestActorThrottlePropagatesToResourceCooldown(t *testing.T) {
	for _, tc := range []struct {
		header  string
		seconds int
	}{
		{"120", 120}, {fixedNow().Add(45 * time.Second).Format(http.TimeFormat), 45}, {"", 60},
	} {
		control := &memoryControl{}
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			response := jsonResponse(429, "{}")
			response.Header.Set("Retry-After", tc.header)
			return response, nil
		})}
		ledger := &memoryLedger{}
		g, _ := New(Config{Token: "token", MonthlyCapMicros: 5000000, Client: client, Control: control, Ledger: ledger, Now: fixedNow})
		_, err := g.SearchMallActor(context.Background(), researchapp.ActorSearchRequest{Mall: musinsa(t), Query: "test", RunKey: "throttled", UserID: "user"})
		f, ok := fault.As(err)
		if !ok || f.Reason != "CATALOG_UPSTREAM_RATE_LIMITED" || f.RetryAfter != time.Duration(tc.seconds)*time.Second {
			t.Fatalf("header=%q fault=%v", tc.header, err)
		}
		if len(control.codes) != 1 || control.codes[0] != "CATALOG_UPSTREAM_RATE_LIMITED" || control.statuses[0] != 429 || control.retries[0] != tc.seconds {
			t.Fatalf("codes=%v statuses=%v retries=%v", control.codes, control.statuses, control.retries)
		}
	}
}

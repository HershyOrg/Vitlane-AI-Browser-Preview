package koreancatalog

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// admissionControl denies the first reservation of every API with the given
// fault and admits afterwards, recording how often each API asked.
type admissionControl struct {
	mu sync.Mutex
	researchapp.CatalogProviderControlRepository
	deny     map[string]*fault.Error
	attempts map[string]int
	codes    []string
}

func (c *admissionControl) ReserveProviderCall(_ context.Context, id, _ string, _ time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempts == nil {
		c.attempts = map[string]int{}
	}
	c.attempts[id]++
	if deny := c.deny[id]; deny != nil && c.attempts[id] == 1 {
		return "", deny
	}
	if deny := c.deny[id]; deny != nil && !deny.Retryable {
		return "", deny
	}
	return "call", nil
}
func (c *admissionControl) CompleteProviderCall(_ context.Context, _ string, code string, _, _ int, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, code)
	return nil
}
func (c *admissionControl) ReadProviderUsage(_ context.Context, id string) (researchapp.CatalogProviderUsage, error) {
	d, _ := researchapp.CatalogAPIDefinitionFor(id)
	return researchapp.CatalogProviderUsage{CatalogAPIDefinition: d, Control: researchapp.AmazonSourceControl{Enabled: true}, Quota: &researchapp.CatalogProviderQuota{Limit: 100, Remaining: 100, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}}, nil
}

func heldByAnotherRound() *fault.Error {
	f := fault.New(fault.RateLimited, "CATALOG_API_RATE_LIMITED", true)
	f.RetryAfter = 2 * time.Second
	return f
}

func TestAdmissionWaitsOnceWhenAnotherRoundHoldsTheSlot(t *testing.T) {
	control := &admissionControl{deny: map[string]*fault.Error{"OWN_PRODUCT": heldByAnotherRound()}}
	requests := 0
	g, _ := New(Config{Enabled: true, OWNKey: "k", Control: control, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return response(200, `{"status":"OK","data":{"products":[]}}`, "application/json"), nil
	})}})
	waited := time.Duration(0)
	g.wait = func(_ context.Context, d time.Duration) error { waited += d; return nil }
	var out any
	if _, err := g.get(context.Background(), "OWN_PRODUCT", "SEARCH", "https://api.openwebninja.com", "/realtime-product-search/v2/search", nil, &out); err != nil {
		t.Fatalf("second reservation should admit: %v", err)
	}
	if control.attempts["OWN_PRODUCT"] != 2 || waited != 2*time.Second || requests != 1 || len(control.codes) != 1 || control.codes[0] != "SUCCESS" {
		t.Fatalf("attempts=%v waited=%v requests=%d codes=%v", control.attempts, waited, requests, control.codes)
	}

	// A per-minute cap or provider cooldown is longer than the Round should wait for.
	long := heldByAnotherRound()
	long.RetryAfter = time.Minute
	control = &admissionControl{deny: map[string]*fault.Error{"OWN_PRODUCT": long}}
	g, _ = New(Config{Enabled: true, OWNKey: "k", Control: control, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("no request without admission"); return nil, nil })}})
	g.wait = func(context.Context, time.Duration) error { t.Fatal("must not wait a minute"); return nil }
	if _, err := g.get(context.Background(), "OWN_PRODUCT", "SEARCH", "https://api.openwebninja.com", "/realtime-product-search/v2/search", nil, &out); err == nil || !strings.Contains(err.Error(), "CATALOG_API_RATE_LIMITED") || control.attempts["OWN_PRODUCT"] != 1 {
		t.Fatalf("long hold: err=%v attempts=%v", err, control.attempts)
	}

	// Usage refresh never waits: it is a non-billable read the operator retries by hand.
	control = &admissionControl{deny: map[string]*fault.Error{"OWN_PRODUCT": heldByAnotherRound()}}
	g, _ = New(Config{Enabled: true, OWNKey: "k", Control: control, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("no request without admission"); return nil, nil })}})
	g.wait = func(context.Context, time.Duration) error { t.Fatal("usage must not wait"); return nil }
	if _, err := g.get(context.Background(), "OWN_PRODUCT", "USAGE", "https://api.openwebninja.com", "/usage", nil, &out); err == nil || control.attempts["OWN_PRODUCT"] != 1 {
		t.Fatalf("usage: err=%v attempts=%v", err, control.attempts)
	}
}

func TestAllSourcesFailingIsRetryableOnlyForTransientReasons(t *testing.T) {
	for _, test := range []struct {
		name      string
		deny      *fault.Error
		retryable bool
	}{
		{"held by another round", func() *fault.Error { f := heldByAnotherRound(); f.RetryAfter = time.Minute; return f }(), true},
		{"disabled by operator", fault.New(fault.ProviderUnavailable, "CATALOG_API_DISABLED", false), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			control := &admissionControl{deny: map[string]*fault.Error{"OWN_PRODUCT": test.deny, "NAVER_WEBKR": test.deny, "OWN_WEB": test.deny, "SERP_GOOGLE": test.deny}}
			g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", SerpKey: "p", Control: control, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("no request without admission"); return nil, nil })}})
			g.wait = func(context.Context, time.Duration) error { return nil }
			result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "라미 사파리", Country: "KR"})
			f, ok := fault.As(err)
			if !ok || f.Reason != "KOREAN_CATALOG_UNAVAILABLE" || f.Retryable != test.retryable {
				t.Fatalf("err=%v retryable=%v", err, test.retryable)
			}
			for _, c := range result.Coverage {
				if c.Source == "COUPANG" || c.Source == "ELEVENST" {
					if c.Status != "SKIPPED" || c.ReasonCode != test.deny.Reason {
						t.Fatalf("admission denial must read as SKIPPED with its reason: %+v", c)
					}
				}
			}
		})
	}
}

// blockedControl reports every API as held by another Round until readyAt, so
// the Round must defer at plan time without spending a single call.
type blockedControl struct {
	admissionControl
	readyAt time.Time
}

func (c *blockedControl) ReadProviderUsage(ctx context.Context, id string) (researchapp.CatalogProviderUsage, error) {
	usage, err := c.admissionControl.ReadProviderUsage(ctx, id)
	if err != nil {
		return usage, err
	}
	at := c.readyAt
	usage.Resources = &researchapp.ProviderResourceState{CanStart: false, CostClass: "INCLUDED", Reason: "CATALOG_API_RATE_LIMITED", ReadyAt: &at, ObservedAt: time.Now()}
	return usage, nil
}

func TestRoundDefersAtPlanTimeWhenTheMallsAreHeldByAnotherRound(t *testing.T) {
	control := &blockedControl{readyAt: time.Now().Add(20 * time.Second)}
	g, _ := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s", SerpKey: "p", Control: control, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("a deferred Round must not call any provider")
		return nil, nil
	})}})
	g.wait = func(context.Context, time.Duration) error { t.Fatal("deferral does not wait in place"); return nil }
	_, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "라미 사파리", Country: "KR", Vertical: "GENERAL"})
	f, ok := fault.As(err)
	if !ok || f.Code != fault.RateLimited || f.Reason != "CATALOG_ROUTES_DEFERRED" || !f.Retryable || f.RetryAfter < 15*time.Second || f.RetryAfter > 20*time.Second {
		t.Fatalf("deferral fault = %v", err)
	}
	if control.attempts["OWN_PRODUCT"] != 0 || len(control.codes) != 0 {
		t.Fatalf("no reservation may be attempted: attempts=%v codes=%v", control.attempts, control.codes)
	}
}

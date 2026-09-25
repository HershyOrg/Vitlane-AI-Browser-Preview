package shopifyucp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testShopifyJWT(t *testing.T, expiry time.Time, scopes string, suffix string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"exp": expiry.Unix(), "scopes": scopes})
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature-" + suffix
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTokenSourceCachesAndSingleflightsRefresh(t *testing.T) {
	var calls atomic.Int64
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	tokenValue := testShopifyJWT(t, now.Add(time.Hour), "checkout", "cache")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, _ := io.ReadAll(request.Body)
		if strings.Contains(string(body), "secret-value") == false {
			t.Fatalf("credential body missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"` + tokenValue + `"}`)), Header: make(http.Header)}, nil
	})}
	source, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", "client-id", "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return now }
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			token, tokenErr := source.Token(context.Background())
			if tokenErr != nil || token.AccessToken != tokenValue || token.Scope != "checkout" {
				t.Errorf("token=%#v err=%v", token, tokenErr)
			}
		}()
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("exchanges=%d", calls.Load())
	}
	if token, err := source.Token(context.Background()); err != nil || token.AccessToken != tokenValue || calls.Load() != 1 {
		t.Fatalf("cached token=%#v err=%v calls=%d", token, err, calls.Load())
	}
}

func TestInvalidateForcesOneRefresh(t *testing.T) {
	var calls atomic.Int64
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		value := calls.Add(1)
		tokenValue := testShopifyJWT(t, now.Add(time.Hour), "checkout", string(rune('0'+value)))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"` + tokenValue + `"}`)), Header: make(http.Header)}, nil
	})}
	source, _ := NewTokenSource(client, "https://api.shopify.com/auth/access_token", "client-id", "secret-value")
	source.now = func() time.Time { return now }
	first, _ := source.Token(context.Background())
	source.Invalidate(first.AccessToken)
	second, err := source.Token(context.Background())
	if err != nil || first.AccessToken == second.AccessToken || calls.Load() != 2 {
		t.Fatalf("first=%s second=%s calls=%d err=%v", first.AccessToken, second.AccessToken, calls.Load(), err)
	}
}

func TestTokenSourceRejectsMalformedOrExpiringJWT(t *testing.T) {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	for name, tokenValue := range map[string]string{
		"opaque":   "not-a-jwt",
		"expiring": testShopifyJWT(t, now.Add(5*time.Minute), "checkout", "expiring"),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"` + tokenValue + `"}`)), Header: make(http.Header)}, nil
			})}
			source, _ := NewTokenSource(client, "https://api.shopify.com/auth/access_token", "client-id", "secret-value")
			source.now = func() time.Time { return now }
			if _, err := source.Token(context.Background()); err == nil {
				t.Fatal("expected invalid Shopify token response")
			}
		})
	}
}

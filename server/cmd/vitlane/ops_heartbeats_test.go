package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// 7-3c: the dashboard's L1 card reads Healthchecks through a server-held
// read-only key. The proxy must never leak the key to the browser, must
// absorb dashboard refreshes with a cache, and must keep serving the last
// snapshot across one upstream blip.
func TestOpsHeartbeatsProxy(t *testing.T) {
	var fetches atomic.Int64
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") != "test-readonly-key" {
				t.Errorf("upstream called without the API key header")
			}
			if fail.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			fetches.Add(1)
			_, _ = w.Write([]byte(`{"checks": [
				{"name": "vitlane-ops-watch", "slug": "vitlane-ops-watch",
				 "status": "up", "last_ping": "2026-08-08T11:55:00Z",
				 "timeout": 300, "grace": 600},
				{"name": "vitlane-pitr-drill", "slug": "vitlane-pitr-drill",
				 "status": "new", "schedule": "@quarterly"}
			]}`))
		},
	))
	defer upstream.Close()

	client := newHealthchecksClient(
		"test-readonly-key", upstream.URL, http.DefaultTransport,
		sharedapp.SystemClock{},
	)
	handler := newOpsHeartbeatsHandler(client)

	decode := func(recorder *httptest.ResponseRecorder) heartbeatsResponse {
		t.Helper()
		var response heartbeatsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %q: %v", recorder.Body.String(), err)
		}
		return response
	}

	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/heartbeats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("first fetch failed: %d %s", recorder.Code, recorder.Body.String())
	}
	first := decode(recorder)
	if !first.Configured || len(first.Checks) != 2 {
		t.Fatalf("unexpected response: %#v", first)
	}
	if first.Checks[0].Status != "up" || first.Checks[0].TimeoutSeconds != 300 {
		t.Fatalf("check mapping lost fields: %#v", first.Checks[0])
	}
	if first.Checks[1].Schedule != "@quarterly" {
		t.Fatalf("cron-style check lost its schedule: %#v", first.Checks[1])
	}

	// A second dashboard refresh inside the cache window must not touch the
	// upstream again.
	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/heartbeats", nil))
	if recorder.Code != http.StatusOK || fetches.Load() != 1 {
		t.Fatalf("cache miss: fetches=%d code=%d", fetches.Load(), recorder.Code)
	}

	// One upstream failure after the cache expires must serve the previous
	// snapshot instead of blanking the card.
	fail.Store(true)
	client.mu.Lock()
	client.fetchedAt = time.Now().Add(-2 * time.Minute)
	client.mu.Unlock()
	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/heartbeats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("stale-cache fallback failed: %d", recorder.Code)
	}
	if stale := decode(recorder); len(stale.Checks) != 2 {
		t.Fatalf("fallback lost the snapshot: %#v", stale)
	}

	// A cold cache with a dead upstream is an explicit upstream error.
	client.mu.Lock()
	client.fetchedAt = time.Time{}
	client.cached = nil
	client.mu.Unlock()
	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/heartbeats", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("cold failure must be 502, got %d", recorder.Code)
	}

	// No key configured is a valid deployment state, not an error.
	unconfigured := newOpsHeartbeatsHandler(newHealthchecksClient(
		"", upstream.URL, http.DefaultTransport, sharedapp.SystemClock{},
	))
	recorder = httptest.NewRecorder()
	unconfigured(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/heartbeats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unconfigured must be 200, got %d", recorder.Code)
	}
	if response := decode(recorder); response.Configured || response.Checks == nil {
		t.Fatalf("unconfigured shape wrong: %#v", response)
	}
}

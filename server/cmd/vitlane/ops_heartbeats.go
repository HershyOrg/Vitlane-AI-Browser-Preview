package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

// healthchecksClient proxies the Healthchecks project state (the L1 layer of
// ADR-0040 §6) to the operator dashboard. The server holds a read-only API
// key, so the browser never sees ping URLs or the key itself, and a short
// cache keeps one dashboard from turning into upstream polling.
type healthchecksClient struct {
	apiKey    string
	apiURL    string
	transport *http.Client
	clock     sharedapp.SystemClock

	mu        sync.Mutex
	cached    []heartbeatCheck
	fetchedAt time.Time
}

const (
	heartbeatsCacheFresh   = time.Minute
	heartbeatsCacheGrace   = 15 * time.Minute
	heartbeatsFetchTimeout = 5 * time.Second
	heartbeatsMaxBodyBytes = 1 << 20
)

type heartbeatCheck struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Status         string `json:"status"`
	LastPing       string `json:"lastPing,omitempty"`
	TimeoutSeconds int64  `json:"timeoutSeconds,omitempty"`
	GraceSeconds   int64  `json:"graceSeconds,omitempty"`
	Schedule       string `json:"schedule,omitempty"`
}

type heartbeatsResponse struct {
	Configured bool             `json:"configured"`
	FetchedAt  string           `json:"fetchedAt,omitempty"`
	Checks     []heartbeatCheck `json:"checks"`
}

func newHealthchecksClient(
	apiKey string,
	apiURL string,
	transport http.RoundTripper,
	clock sharedapp.SystemClock,
) *healthchecksClient {
	// The shared factory owns every outbound client in this process; a nil
	// transport cannot happen from run(), and if it ever does the card
	// degrades to "not configured" instead of panicking.
	client, err := httpclient.NewClient(transport, heartbeatsFetchTimeout)
	if err != nil {
		client = nil
	}
	return &healthchecksClient{
		apiKey:    apiKey,
		apiURL:    apiURL,
		transport: client,
		clock:     clock,
	}
}

func (c *healthchecksClient) configured() bool {
	return c != nil && c.apiKey != "" && c.transport != nil
}

// checks returns the cached project state, refreshing it when older than one
// minute. A failed refresh keeps serving the previous snapshot for a bounded
// grace window so one upstream blip does not blank the dashboard's L1 card.
func (c *healthchecksClient) checks(
	ctx context.Context, now time.Time,
) ([]heartbeatCheck, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetchedAt.IsZero() && now.Sub(c.fetchedAt) < heartbeatsCacheFresh {
		return c.cached, c.fetchedAt, nil
	}
	fresh, err := c.fetch(ctx)
	if err != nil {
		if !c.fetchedAt.IsZero() && now.Sub(c.fetchedAt) < heartbeatsCacheGrace {
			return c.cached, c.fetchedAt, nil
		}
		return nil, time.Time{}, err
	}
	c.cached = fresh
	c.fetchedAt = now
	return c.cached, c.fetchedAt, nil
}

func (c *healthchecksClient) fetch(ctx context.Context) ([]heartbeatCheck, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Api-Key", c.apiKey)
	// Read-only in both senses: the key cannot mutate upstream state and the
	// call is ReadOnly for the effect classifier. The wrapped error may embed
	// the request URL but never the key, which travels in a header.
	response, err := httpclient.Do(ctx, c.transport, request, httpclient.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("healthchecks fetch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("healthchecks fetch: status %d", response.StatusCode)
	}
	body, err := httpclient.ReadBody(response.Body, heartbeatsMaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("healthchecks read: %w", err)
	}
	var payload struct {
		Checks []struct {
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Status   string `json:"status"`
			LastPing string `json:"last_ping"`
			Timeout  int64  `json:"timeout"`
			Grace    int64  `json:"grace"`
			Schedule string `json:"schedule"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("healthchecks decode: %w", err)
	}
	checks := make([]heartbeatCheck, 0, len(payload.Checks))
	for _, check := range payload.Checks {
		checks = append(checks, heartbeatCheck{
			Name:           check.Name,
			Slug:           check.Slug,
			Status:         check.Status,
			LastPing:       check.LastPing,
			TimeoutSeconds: check.Timeout,
			GraceSeconds:   check.Grace,
			Schedule:       check.Schedule,
		})
	}
	return checks, nil
}

// newOpsHeartbeatsHandler serves GET /api/v1/admin/ops/heartbeats behind
// operator authentication. An unconfigured key is a valid state, not an
// error: the dashboard renders the L1 card as "연결 안 됨" and everything
// else keeps working.
func newOpsHeartbeatsHandler(client *healthchecksClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !client.configured() {
			httpapi.WriteJSON(w, http.StatusOK, heartbeatsResponse{
				Configured: false, Checks: []heartbeatCheck{},
			})
			return
		}
		checks, fetchedAt, err := client.checks(
			r.Context(), client.clock.Now().UTC(),
		)
		if err != nil {
			httpapi.WriteError(
				w, http.StatusBadGateway, "HEARTBEATS_UPSTREAM_UNAVAILABLE",
				"Healthchecks 상태를 불러오지 못했습니다.",
			)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, heartbeatsResponse{
			Configured: true,
			FetchedAt:  fetchedAt.UTC().Format(time.RFC3339),
			Checks:     checks,
		})
	}
}

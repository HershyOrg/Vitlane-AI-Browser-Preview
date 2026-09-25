// Package koreancatalog reads public product observations through explicitly
// configured APIs. Provider tokens and search URLs never enter returned errors.
package koreancatalog

import (
	"context"
	"encoding/json"
	"errors"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Enabled                                           bool
	OWNKey, NaverClientID, NaverClientSecret, SerpKey string
	BrowserBaseURL, BrowserToken                      string
	Client                                            *http.Client
	Control                                           researchapp.CatalogProviderControlRepository
	// DetailLimit is how many OWN Product search hits get a detail call per
	// Round. Each detail is a paid call, so the operator raises it with the
	// plan (D1). Zero means the historical two.
	DetailLimit   int
	MaxConcurrent int
	SearchTimeout time.Duration
	// Actor buys a search result for the malls that answer neither Vitlane nor
	// web search. Without it those malls keep their OWN offers only.
	Actor ActorSearcher
}

// ActorSearcher runs one paid third-party Actor. It lives behind an interface
// so the Korean gateway never learns a provider's API, and so a deployment
// without an Actor token simply has none.
type ActorSearcher interface {
	SearchMallActor(context.Context, researchapp.ActorSearchRequest) (researchapp.ActorSearchResult, error)
	Configured() bool
}

func (g *Gateway) detailLimit() int {
	if g.config.DetailLimit <= 0 {
		return 4
	}
	return g.config.DetailLimit
}

type Gateway struct {
	config     Config
	client     *http.Client
	quotaMu    sync.Map
	reviewStub bool
	wait       func(context.Context, time.Duration) error
}

// maximumAdmissionWait bounds how long one Round waits for another Round to
// release an API slot. A per-minute cap or a provider cooldown is longer than
// this, so those still skip the source for this Round.
const maximumAdmissionWait = 5 * time.Second

func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func New(c Config) (*Gateway, error) {
	if c.Client == nil || c.Control == nil {
		return nil, fault.New(fault.InvalidInput, "KOREAN_CATALOG_CONFIG_INVALID", false)
	}
	if (strings.TrimSpace(c.BrowserBaseURL) == "") != (strings.TrimSpace(c.BrowserToken) == "") ||
		c.BrowserBaseURL != "" && !validBrowserBridgeBase(c.BrowserBaseURL) ||
		c.BrowserToken != "" && len([]byte(c.BrowserToken)) < 32 {
		return nil, fault.New(fault.InvalidInput, "KOREAN_CATALOG_BROWSER_CONFIG_INVALID", false)
	}
	if c.BrowserBaseURL != "" {
		parsed, _ := url.Parse(c.BrowserBaseURL)
		c.BrowserBaseURL = parsed.Scheme + "://" + parsed.Host
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Gateway{config: c, client: &client, wait: waitFor}, nil
}

// reserve admits one provider call. When another Round holds this API's slot
// the ledger answers with a short RetryAfter; waiting once keeps the source in
// this Round instead of silently dropping it. The first denial stays in the
// ledger as a non-billable record so operators can see the collision.
func (g *Gateway) reserve(ctx context.Context, id, operation string) (string, error) {
	call, err := g.config.Control.ReserveProviderCall(ctx, id, operation, time.Now().UTC())
	f, ok := fault.As(err)
	if !ok || f.Reason != "CATALOG_API_RATE_LIMITED" || operation == "USAGE" || f.RetryAfter <= 0 || f.RetryAfter > maximumAdmissionWait {
		return call, err
	}
	if waitErr := g.wait(ctx, f.RetryAfter); waitErr != nil {
		return "", safeFailure("CATALOG_REQUEST_CANCELLED")
	}
	return g.config.Control.ReserveProviderCall(ctx, id, operation, time.Now().UTC())
}
func (g *Gateway) Configured(id string) bool {
	if !g.config.Enabled {
		return false
	}
	if d, ok := researchapp.CatalogAPIDefinitionFor(id); ok && d.Keyless {
		return true
	}
	switch id {
	case browserDiscoveryAPIID:
		return g.config.BrowserBaseURL != "" && g.config.BrowserToken != ""
	case "OWN_PRODUCT", "OWN_WEB":
		return g.config.OWNKey != ""
	case "NAVER_WEBKR":
		return g.config.NaverClientID != "" && g.config.NaverClientSecret != ""
	case "SERP_GOOGLE":
		return g.config.SerpKey != ""
	case "APIFY_MUSINSA", "APIFY_29CM", "APIFY_GMARKET":
		return g.config.Actor != nil && g.config.Actor.Configured()
	}
	return false
}

func validBrowserBridgeBase(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

// allowedBase pins every outbound origin: the four provider APIs plus the
// detail hosts the mall registry declares. Nothing else is ever requested.
func allowedBase(base string) bool {
	switch base {
	case "https://api.openwebninja.com", "https://naverapihub.apigw.ntruss.com", "https://serpapi.com":
		return true
	}
	for _, mall := range researchdomain.KoreanMalls() {
		if mall.DetailHost != "" && base == mall.DetailHost {
			return true
		}
	}
	return false
}
func safeFailure(code string) error { return fault.New(fault.ProviderUnavailable, code, false) }

func (g *Gateway) get(ctx context.Context, id, operation, base, path string, query url.Values, out any) (raw []byte, err error) {
	if !g.Configured(id) {
		return nil, safeFailure("CATALOG_API_NOT_CONFIGURED")
	}
	if !allowedBase(base) {
		return nil, safeFailure("CATALOG_HOST_INVALID")
	}
	release, err := acquireRouteSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	call, err := g.reserve(ctx, id, operation)
	if err != nil {
		return nil, err
	}
	status, retry := 0, 0
	defer func() {
		code := "SUCCESS"
		if err != nil {
			code = "CATALOG_INTERNAL_ERROR"
			if f, ok := fault.As(err); ok {
				code = f.Reason
			}
		}
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if e := g.config.Control.CompleteProviderCall(finalCtx, call, code, status, retry, time.Now().UTC()); e != nil && err == nil {
			err = safeFailure("CATALOG_CALL_FINALIZATION_FAILED")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	q := url.Values{}
	for key, values := range query {
		q[key] = append([]string(nil), values...)
	}
	if id == "SERP_GOOGLE" {
		q.Set("api_key", g.config.SerpKey)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if e != nil {
		return nil, safeFailure("CATALOG_REQUEST_INVALID")
	}
	if strings.HasPrefix(id, "OWN_") {
		req.Header.Set("x-api-key", g.config.OWNKey)
	}
	if id == "NAVER_WEBKR" {
		req.Header.Set("X-NCP-APIGW-API-KEY-ID", g.config.NaverClientID)
		req.Header.Set("X-NCP-APIGW-API-KEY", g.config.NaverClientSecret)
	}
	req.Header.Set("Accept", "application/json, text/html;q=0.8")
	effect := sharedhttpclient.ReadOnly
	if operation != "USAGE" && (strings.HasPrefix(id, "OWN_") || id == "SERP_GOOGLE") {
		effect = sharedhttpclient.ExternalEffect
	}
	response, e := sharedhttpclient.Do(ctx, g.client, req, effect)
	if e != nil {
		if fault.CodeOf(e) == fault.ExternalEffectUnknown {
			return nil, safeFailure("CATALOG_RESULT_UNKNOWN")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, safeFailure("CATALOG_REQUEST_CANCELLED")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || fault.CodeOf(e) == fault.DeadlineExceeded {
			return nil, safeFailure("CATALOG_TIMEOUT")
		}
		return nil, safeFailure("CATALOG_NETWORK_FAILED")
	}
	defer response.Body.Close()
	status = response.StatusCode
	if seconds, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && seconds > 0 {
		retry = min(seconds, 86400)
	} else if at, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
		retry = max(0, min(int(time.Until(at).Seconds())+1, 86400))
	}
	if status == 429 {
		if retry == 0 {
			retry = 60
		}
		return nil, safeFailure("CATALOG_UPSTREAM_RATE_LIMITED")
	}
	if status == 401 || status == 403 {
		return nil, safeFailure("CATALOG_AUTH_REJECTED")
	}
	if status < 200 || status >= 300 {
		return nil, safeFailure("CATALOG_UPSTREAM_FAILED")
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(raw) > 2<<20 {
		return nil, safeFailure("CATALOG_BODY_INVALID")
	}
	if out == nil && strings.HasSuffix(id, "_HTML") && operation == "DETAIL" && !publicProductPage(raw) {
		// Malls answer discontinued or invalid product numbers with HTTP 200
		// and a script-only stub (11st, W Concept). The call completed and
		// found no product, so the ledger records the success-class outcome
		// instead of a failure.
		return nil, safeFailure(researchapp.CatalogOutcomeProductNotFound)
	}
	if out != nil {
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		// NAVER HUB's authenticated Webkr JSON is served as text/plain.
		if !strings.Contains(contentType, "json") && !(id == "NAVER_WEBKR" && strings.HasPrefix(contentType, "text/plain")) {
			return nil, safeFailure("CATALOG_CONTENT_TYPE_INVALID")
		}
		if err = validateEnvelope(id, operation, raw); err != nil {
			return nil, err
		}
		if e = json.Unmarshal(raw, out); e != nil {
			return nil, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
	}
	return raw, nil
}

func (g *Gateway) Usage(ctx context.Context, id string, refresh bool) (researchapp.CatalogProviderUsage, error) {
	if refresh {
		if err := g.refreshQuota(ctx, id); err != nil {
			return researchapp.CatalogProviderUsage{}, err
		}
	}
	usage, err := g.config.Control.ReadProviderUsage(ctx, id)
	usage.Configured = g.Configured(id)
	if usage.Resources != nil && !usage.Configured {
		usage.Resources.CanStart = false
		usage.Resources.Reason = "CATALOG_API_NOT_CONFIGURED"
		usage.Resources.ReadyAt = nil
	}
	if g.reviewStub {
		usage.APIProduct += " [STUB · 2026-09-11 audit]"
	}
	return usage, err
}
func (g *Gateway) ensureQuota(ctx context.Context, id string) error {
	entry, _ := g.quotaMu.LoadOrStore(id, &sync.Mutex{})
	lock := entry.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	usage, err := g.Usage(ctx, id, false)
	if err != nil {
		return err
	}
	if !usage.Control.Enabled {
		return safeFailure("CATALOG_API_DISABLED")
	}
	if !usage.Configured {
		return safeFailure("CATALOG_API_NOT_CONFIGURED")
	}
	if usage.Quota == nil || !usage.Quota.ResetAt.After(time.Now()) || time.Since(usage.Quota.ObservedAt) > 24*time.Hour {
		return g.refreshQuota(ctx, id)
	}
	return nil
}
func (g *Gateway) refreshQuota(ctx context.Context, id string) error {
	d, ok := researchapp.CatalogAPIDefinitionFor(id)
	if !ok {
		return safeFailure("CATALOG_API_INVALID")
	}
	if !d.NeedsQuota {
		return nil
	}
	baseline := time.Now().UTC()
	var quota researchapp.CatalogAPIQuota
	if strings.HasPrefix(id, "OWN_") {
		var body struct {
			Status string `json:"status"`
			Data   struct {
				APIID string `json:"api_id"`
				Plan  struct {
					Free bool `json:"is_free"`
				} `json:"plan"`
				Quotas []struct {
					Name                   string `json:"name"`
					Limit, Used, Remaining int64
					Reset                  time.Time `json:"reset_at"`
				} `json:"quotas"`
			} `json:"data"`
		}
		if _, err := g.get(ctx, id, "USAGE", "https://api.openwebninja.com", "/usage", url.Values{"api_id": {d.QuotaScope}}, &body); err != nil {
			return err
		}
		if body.Status != "OK" || body.Data.APIID != d.QuotaScope {
			return safeFailure("CATALOG_QUOTA_SCHEMA_MISMATCH")
		}
		for _, q := range body.Data.Quotas {
			if q.Name == "Requests" {
				quota = researchapp.CatalogAPIQuota{Limit: q.Limit, Used: q.Used, Remaining: q.Remaining, ResetAt: q.Reset, ObservedAt: time.Now().UTC(), IsFree: body.Data.Plan.Free}
			}
		}
	} else {
		var body struct {
			Limit     int64 `json:"searches_per_month"`
			Used      int64 `json:"this_month_usage"`
			Remaining int64 `json:"total_searches_left"`

			MonthEnd string `json:"plan_renewal_date"`
		}
		if _, err := g.get(ctx, id, "USAGE", "https://serpapi.com", "/account", nil, &body); err != nil {
			return err
		}
		reset, err := time.Parse("2006-01-02", body.MonthEnd)
		if err != nil {
			return safeFailure("CATALOG_QUOTA_RESET_UNCONFIRMED")
		}
		quota = researchapp.CatalogAPIQuota{Limit: body.Limit, Used: body.Used, Remaining: body.Remaining, ResetAt: reset, ObservedAt: time.Now().UTC()}
	}
	if quota.Limit <= 0 || quota.Used < 0 || quota.Remaining < 0 || quota.Remaining > quota.Limit || !quota.ResetAt.After(time.Now()) {
		return safeFailure("CATALOG_QUOTA_SCHEMA_MISMATCH")
	}
	return g.config.Control.SaveProviderQuota(ctx, id, quota, baseline)
}

// SearchExternalMalls executes the app route policy with bounded parallel calls.
// Each route owns its result; aggregation follows planned order, not completion
// timing, and the existing single CandidatePool finalize remains unchanged.
func (g *Gateway) SearchExternalMalls(ctx context.Context, req researchapp.KoreanSearchRequest) (researchapp.ExternalCatalogSearchResult, error) {
	result := researchapp.ExternalCatalogSearchResult{Observations: []researchdomain.ExternalProductObservation{}, Coverage: []researchapp.SourceCoverage{{Source: researchdomain.SourceShopify, Status: "UNSUPPORTED", ReasonCode: "SHOPIFY_KR_MARKET_UNSUPPORTED"}, {Source: researchdomain.SourceAmazon, Status: "UNSUPPORTED", ReasonCode: "AMAZON_MARKET_UNSUPPORTED"}}}
	if req.Country != "KR" {
		return result, safeFailure("KOREAN_CATALOG_MARKET_UNSUPPORTED")
	}
	result.NextProgress = researchapp.SourceProgressSet{}
	successes := 0
	pathErr := map[researchdomain.Source]error{}
	asked := map[researchdomain.Source]bool{}

	timeout := g.config.SearchTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	concurrency := g.config.MaxConcurrent
	if concurrency <= 0 {
		concurrency = 4
	}
	ctx = context.WithValue(ctx, routeSlotsKey{}, make(chan struct{}, concurrency))
	// Browser searches share one local Chrome process. Queue reviewed merchant
	// recipes inside the Round instead of turning ordinary overlap into a
	// source failure or launching several heavyweight contexts at once.
	ctx = context.WithValue(ctx, browserRouteSlotKey{}, make(chan struct{}, 1))
	routes, alternatives := g.planRoutes(ctx, req)
	// A Round that would start with most of its malls blocked by another
	// Round's slots or a per-minute cap waits for them instead of paying for a
	// narrow result. The wait is a deferral of the whole job, not a failure.
	if wait, deferred := researchapp.RouteDeferralWait(routes, time.Now().UTC()); deferred {
		for _, route := range routes {
			if !route.Resources.CanStart {
				g.recordRoute(ctx, req, route, "DEFERRED", route.Resources.Reason)
			}
		}
		pending := fault.New(fault.RateLimited, "CATALOG_ROUTES_DEFERRED", true)
		pending.RetryAfter = wait
		return result, pending
	}
	outcomes := make([]routeOutcome, len(routes))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for n := 0; n < min(concurrency, len(routes)); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				route := routes[i]
				if !route.Resources.CanStart && route.Resources.ReadyAt != nil && time.Until(*route.Resources.ReadyAt) <= maximumAdmissionWait {
					if g.wait(ctx, max(time.Duration(0), time.Until(*route.Resources.ReadyAt))) == nil {
						route.Resources = g.refreshRouteResources(ctx, route)
					}
				}
				if !route.Resources.CanStart {
					outcomes[i].err = safeFailure(route.Resources.Reason)
					g.recordRoute(ctx, req, route, "SKIPPED", route.Resources.Reason)
					continue
				}
				g.recordRoute(ctx, req, route, "STARTED", "")
				outcomes[i] = g.executeRoute(ctx, req, route)
				g.finishRoute(ctx, req, route, outcomes[i])
				// One alternate discovery path may recover a failed or empty HTML path.
				// This is not a loop to fill CandidateUpdateSize.
				if len(outcomes[i].products) == 0 && len(alternatives[route.Source]) > 0 && ctx.Err() == nil {
					fallback := alternatives[route.Source][0]
					if fallback.Resources.CanStart {
						g.recordRoute(ctx, req, fallback, "STARTED", "")
						more := g.executeRoute(ctx, req, fallback)
						for k, v := range more.progress {
							outcomes[i].progress[k] = v
						}
						outcomes[i].rejected += more.rejected
						if more.err == nil || len(more.products) > 0 {
							outcomes[i].products = more.products
							outcomes[i].err = more.err
						}
						g.finishRoute(ctx, req, fallback, more)
					}
				}
			}
		}()
	}
	for i := range routes {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for i, out := range outcomes {
		route := routes[i]
		source := researchdomain.Source(route.Source)
		if route.ID == "OWN_PRODUCT" {
			source = researchdomain.SourceCoupang
		}
		asked[source] = true
		pathErr[source] = out.err
		if out.err == nil {
			successes++
		}
		result.Observations = append(result.Observations, out.products...)
		result.RejectedCount += out.rejected
		for k, v := range out.progress {
			result.NextProgress[k] = v
		}
	}
	if g.reviewStub {
		for i := range result.Observations {
			o := &result.Observations[i]
			o.Provenance.APIProduct += " [STUB · 2026-09-11 audit]"
			o.ObservedAt = reviewObservedAt(o.ProductRef.ProductID)
		}
	}

	// One coverage row per platform the Round touched or observed. The two
	// historical rows stay first and always present; Coupang carries the
	// OWN Product path outcome and 11st the web-search path outcome.
	counts := map[researchdomain.Source]int{}
	for _, o := range result.Observations {
		counts[o.ProductRef.Source]++
	}
	order := []researchdomain.Source{researchdomain.SourceCoupang, researchdomain.SourceElevenStreet}
	for _, mall := range researchdomain.KoreanMalls() {
		if mall.Source != researchdomain.SourceCoupang && mall.Source != researchdomain.SourceElevenStreet && (asked[mall.Source] || counts[mall.Source] > 0) {
			order = append(order, mall.Source)
		}
	}
	for _, source := range order {
		coverage := researchapp.SourceCoverage{Source: source, Status: "EMPTY", CandidateCount: counts[source]}
		if coverage.CandidateCount > 0 {
			coverage.Status = "SUCCEEDED"
		}
		if err := pathErr[source]; err != nil {
			coverage.Status = "FAILED"
			if f, ok := fault.As(err); ok {
				coverage.ReasonCode = f.Reason
				// Admission conditions are not source failures: the API was off,
				// unconfigured, out of quota or held by another Round. The Round
				// went on without it and the ledger keeps the reason.
				if f.Reason == "CATALOG_API_DISABLED" || f.Reason == "CATALOG_API_NOT_CONFIGURED" || f.Reason == "CATALOG_QUOTA_EXHAUSTED" || f.Reason == "CATALOG_LOCAL_DAILY_LIMIT" || f.Reason == "CATALOG_API_RATE_LIMITED" || f.Reason == "CATALOG_QUOTA_UNCONFIRMED" {
					coverage.Status = "SKIPPED"
				}
			}
			if coverage.CandidateCount > 0 {
				coverage.Status = "PARTIAL"
			}
		}
		result.Coverage = append(result.Coverage, coverage)
	}
	if successes == 0 && len(result.Observations) == 0 {
		// Nothing answered. The Round may be reopened only when every path
		// failed for a momentary reason; a disabled, unconfigured or rejected
		// API does not get better by retrying. When every path was merely
		// held by another Round or a cap, the job is deferred for a short
		// while rather than counted as a failed attempt.
		transient, held := true, len(pathErr) > 0
		for _, err := range pathErr {
			if err == nil {
				continue
			}
			f, ok := fault.As(err)
			if !ok || !researchapp.CatalogAdmissionTransient(f.Reason) {
				transient = false
			}
			if !ok || f.Reason != "CATALOG_API_RATE_LIMITED" {
				held = false
			}
		}
		if held {
			pending := fault.New(fault.RateLimited, "KOREAN_CATALOG_UNAVAILABLE", true)
			pending.RetryAfter = 30 * time.Second
			return result, pending
		}
		return result, fault.New(fault.ProviderUnavailable, "KOREAN_CATALOG_UNAVAILABLE", transient)
	}
	return result, nil
}

// Validate the API envelope before recording completion: HTTP 200 may still
// represent an authentication error or an incompatible upstream schema.
func validateEnvelope(id, operation string, raw []byte) error {
	var body map[string]json.RawMessage
	bad := func() error { return safeFailure("CATALOG_SCHEMA_MISMATCH") }
	if json.Unmarshal(raw, &body) != nil || body == nil {
		return bad()
	}
	if strings.HasPrefix(id, "OWN_") {
		var status string
		if json.Unmarshal(body["status"], &status) != nil || status != "OK" {
			return bad()
		}
		var data map[string]json.RawMessage
		if json.Unmarshal(body["data"], &data) != nil || data == nil {
			return bad()
		}
		field := "products"
		if id == "OWN_WEB" {
			field = "organic_results"
		}
		if operation == "DETAIL" {
			field = "offers"
		}
		if operation == "USAGE" {
			field = "quotas"
		}
		var list []json.RawMessage
		if json.Unmarshal(data[field], &list) != nil || list == nil {
			return bad()
		}
		if operation == "USAGE" {
			var api string
			def, _ := researchapp.CatalogAPIDefinitionFor(id)
			if json.Unmarshal(data["api_id"], &api) != nil || api != def.QuotaScope {
				return safeFailure("CATALOG_QUOTA_SCHEMA_MISMATCH")
			}
			valid := false
			for _, entry := range list {
				var q struct {
					Name                   string
					Limit, Used, Remaining int64
					Reset                  time.Time `json:"reset_at"`
				}
				if json.Unmarshal(entry, &q) == nil && q.Name == "Requests" && q.Limit > 0 && q.Used >= 0 && q.Remaining >= 0 && q.Remaining <= q.Limit && q.Reset.After(time.Now()) {
					valid = true
				}
			}
			if !valid {
				return safeFailure("CATALOG_QUOTA_SCHEMA_MISMATCH")
			}
		}
	} else if id == "KURLY_JSON" {
		var success bool
		if json.Unmarshal(body["success"], &success) != nil || !success {
			return bad()
		}
		var data map[string]json.RawMessage
		if json.Unmarshal(body["data"], &data) != nil || data == nil {
			return bad()
		}
	} else if id == "NAVER_WEBKR" {
		if e := body["errorCode"]; len(e) > 0 {
			return bad()
		}
		var list []json.RawMessage
		if json.Unmarshal(body["items"], &list) != nil || list == nil {
			return bad()
		}
	} else if id == "SERP_GOOGLE" {
		if e := body["error"]; len(e) > 0 && string(e) != `""` {
			return bad()
		}
		if operation == "USAGE" {
			var date string
			if json.Unmarshal(body["plan_renewal_date"], &date) != nil {
				return safeFailure("CATALOG_QUOTA_RESET_UNCONFIRMED")
			}
			if at, e := time.Parse("2006-01-02", date); e != nil || !at.After(time.Now()) {
				return safeFailure("CATALOG_QUOTA_RESET_UNCONFIRMED")
			}
		} else {
			var list []json.RawMessage
			if json.Unmarshal(body["organic_results"], &list) != nil || list == nil {
				return bad()
			}
		}
	}
	return nil
}

func (g *Gateway) DescribeDiscovery() researchapp.DiscoveryDescriptor {
	return researchapp.KoreanDiscoveryDescriptor()
}

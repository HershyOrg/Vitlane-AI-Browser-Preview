package apifyactor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const apifyBase = "https://api.apify.com"

// maximumItems bounds one run. The Actors charge per returned item, so the
// Round's cost is bounded before it starts, not after it is billed.
const maximumItems = 5

type Config struct {
	Token            string
	MonthlyCapMicros int64
	Client           *http.Client
	Control          researchapp.CatalogProviderControlRepository
	Ledger           researchapp.ActorRunLedger
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

type Gateway struct{ config Config }

func New(config Config) (*Gateway, error) {
	if config.Client == nil || config.Control == nil || config.Ledger == nil {
		return nil, fmt.Errorf("apify actor gateway needs a client, a control ledger and a run ledger")
	}
	if config.MonthlyCapMicros <= 0 {
		return nil, fmt.Errorf("apify actor gateway needs a positive monthly cap")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Sleep == nil {
		config.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &Gateway{config: config}, nil
}

// Configured reports whether a token is present. Without one the registry's
// Actor malls are simply not asked.
func (g *Gateway) Configured() bool { return g != nil && strings.TrimSpace(g.config.Token) != "" }

func failure(code string) error { return fault.New(fault.ProviderUnavailable, code, false) }

// SearchMallActor buys one mall's search result. It records the run before
// starting it, never starts a second run for the same key, aborts the run if
// the Round is cancelled, and refuses to start once the month's cap is spent.
func (g *Gateway) SearchMallActor(ctx context.Context, request researchapp.ActorSearchRequest) (researchapp.ActorSearchResult, error) {
	result := researchapp.ActorSearchResult{Products: []researchdomain.ExternalProductObservation{}}
	spec, known := actorSpecs[request.Mall.Source]
	if !known || request.Mall.Detail != researchdomain.DetailActor || request.Mall.DetailAPI == "" {
		return result, failure("CATALOG_ACTOR_UNSUPPORTED")
	}
	if !g.Configured() {
		return result, failure("CATALOG_API_NOT_CONFIGURED")
	}
	query := strings.TrimSpace(request.Query)
	if query == "" || strings.TrimSpace(request.RunKey) == "" || strings.TrimSpace(request.UserID) == "" {
		return result, failure("CATALOG_ACTOR_REQUEST_INVALID")
	}
	limit := request.Limit
	if limit <= 0 || limit > maximumItems {
		limit = maximumItems
	}
	now := g.config.Now()
	spend, err := g.config.Ledger.ActorSpendMicros(ctx, researchapp.ActorMonthStart(now))
	if err != nil {
		return result, failure("CATALOG_ACTOR_LEDGER_UNAVAILABLE")
	}
	if spend >= g.config.MonthlyCapMicros {
		return result, failure("CATALOG_ACTOR_BUDGET_EXHAUSTED")
	}

	call, err := g.config.Control.ReserveProviderCall(ctx, request.Mall.DetailAPI, "SEARCH", now)
	if err != nil {
		return result, err
	}
	run := researchapp.ActorRun{
		RunKey: request.RunKey, UserID: request.UserID, APIID: request.Mall.DetailAPI,
		Source: string(request.Mall.Source), StartedAt: now,
		ReservedMicros: researchapp.CatalogActorReservationMicros(request.Mall.DetailAPI), BudgetLimitMicros: g.config.MonthlyCapMicros,
	}
	run, started, err := g.config.Ledger.BeginActorRun(ctx, run)
	if err != nil {
		if f, ok := fault.As(err); ok && f.Reason == "CATALOG_ACTOR_BUDGET_EXHAUSTED" {
			g.complete(ctx, call, f.Reason, 0)
			return result, err
		}
		g.complete(ctx, call, "CATALOG_ACTOR_LEDGER_UNAVAILABLE", 0)
		return result, failure("CATALOG_ACTOR_LEDGER_UNAVAILABLE")
	}
	if !started {
		// The same attempt already paid for this mall. A finished run's items
		// are read again for free; anything else is not bought twice.
		if run.Status == researchapp.ActorRunSucceeded && run.ProviderRunID != "" {
			items, readErr := g.datasetItems(ctx, run.ProviderRunID, limit)
			if readErr != nil {
				return result, g.completeFailure(ctx, call, "CATALOG_ACTOR_DATASET_UNAVAILABLE", readErr)
			}
			result = g.observations(request.Mall, spec, items)
			result.Reused = true
			g.complete(ctx, call, "", http.StatusOK)
			return result, nil
		}
		g.complete(ctx, call, "CATALOG_ACTOR_RUN_ALREADY_SPENT", 0)
		return result, failure("CATALOG_ACTOR_RUN_ALREADY_SPENT")
	}

	providerRun, err := g.start(ctx, spec, query, limit)
	if err != nil {
		g.settle(ctx, run, researchapp.ActorRunFailed, providerRun)
		return result, g.completeFailure(ctx, call, "CATALOG_ACTOR_START_FAILED", err)
	}
	if ledger, ok := g.config.Ledger.(interface {
		AttachActorRun(context.Context, string, string) error
	}); ok {
		if err := ledger.AttachActorRun(ctx, run.RunKey, providerRun.ID); err != nil {
			g.abort(ctx, providerRun.ID)
			g.complete(ctx, call, "CATALOG_ACTOR_LEDGER_UNAVAILABLE", 0)
			return result, failure("CATALOG_ACTOR_LEDGER_UNAVAILABLE")
		}
	}
	final, err := g.await(ctx, providerRun)
	if err != nil {
		// A cancelled Round must not keep paying: the run is aborted with a
		// context that outlives the cancellation.
		g.abort(ctx, providerRun.ID)
		g.settle(ctx, run, researchapp.ActorRunAborted, final)
		return result, g.completeFailure(ctx, call, "CATALOG_REQUEST_CANCELLED", err)
	}
	if final.Status != "SUCCEEDED" {
		g.settle(ctx, run, researchapp.ActorRunFailed, final)
		g.complete(ctx, call, "CATALOG_ACTOR_RUN_FAILED", 0)
		return result, failure("CATALOG_ACTOR_RUN_FAILED")
	}
	items, err := g.datasetItems(ctx, final.ID, limit)
	if err != nil {
		g.settle(ctx, run, researchapp.ActorRunSucceeded, final)
		return result, g.completeFailure(ctx, call, "CATALOG_ACTOR_DATASET_UNAVAILABLE", err)
	}
	result = g.observations(request.Mall, spec, items)
	final.ItemCount = len(items)
	final.CostMicros = spec.runCost(final.UsageTotalUsd, len(items))
	g.settle(ctx, run, researchapp.ActorRunSucceeded, final)
	result.CostMicros = final.CostMicros
	g.complete(ctx, call, "", http.StatusOK)
	return result, nil
}

type providerRun struct {
	ID               string
	Status           string
	DefaultDatasetID string
	UsageTotalUsd    any
	ItemCount        int
	CostMicros       int64
}

func (g *Gateway) start(ctx context.Context, spec actorSpec, query string, limit int) (providerRun, error) {
	body, err := json.Marshal(spec.input(query, limit))
	if err != nil {
		return providerRun{}, err
	}
	path := fmt.Sprintf("/v2/acts/%s/runs?timeout=%d&memory=%d", url.PathEscape(spec.actor), runTimeoutSecs, runMemoryMB)
	var decoded struct {
		Data struct {
			ID               string `json:"id"`
			Status           string `json:"status"`
			DefaultDatasetID string `json:"defaultDatasetId"`
		} `json:"data"`
	}
	if err := g.request(ctx, http.MethodPost, path, body, &decoded); err != nil {
		return providerRun{}, err
	}
	if decoded.Data.ID == "" {
		return providerRun{}, failure("CATALOG_ACTOR_START_FAILED")
	}
	return providerRun{ID: decoded.Data.ID, Status: decoded.Data.Status, DefaultDatasetID: decoded.Data.DefaultDatasetID}, nil
}

func (g *Gateway) await(ctx context.Context, run providerRun) (providerRun, error) {
	deadline := g.config.Now().Add(runWallClock)
	current := run
	for {
		if terminal(current.Status) {
			return current, nil
		}
		if g.config.Now().After(deadline) {
			return current, failure("CATALOG_ACTOR_RUN_TIMEOUT")
		}
		if err := g.config.Sleep(ctx, pollInterval); err != nil {
			return current, err
		}
		var decoded struct {
			Data struct {
				ID               string `json:"id"`
				Status           string `json:"status"`
				DefaultDatasetID string `json:"defaultDatasetId"`
				UsageTotalUsd    any    `json:"usageTotalUsd"`
			} `json:"data"`
		}
		if err := g.request(ctx, http.MethodGet, "/v2/actor-runs/"+url.PathEscape(current.ID), nil, &decoded); err != nil {
			return current, err
		}
		current = providerRun{ID: decoded.Data.ID, Status: decoded.Data.Status,
			DefaultDatasetID: decoded.Data.DefaultDatasetID, UsageTotalUsd: decoded.Data.UsageTotalUsd}
	}
}

func (g *Gateway) datasetItems(ctx context.Context, runID string, limit int) ([]map[string]any, error) {
	var run struct {
		Data struct {
			DefaultDatasetID string `json:"defaultDatasetId"`
		} `json:"data"`
	}
	if err := g.request(ctx, http.MethodGet, "/v2/actor-runs/"+url.PathEscape(runID), nil, &run); err != nil {
		return nil, err
	}
	if run.Data.DefaultDatasetID == "" {
		return nil, failure("CATALOG_ACTOR_DATASET_UNAVAILABLE")
	}
	var items []map[string]any
	if err := g.request(ctx, http.MethodGet, datasetPath(run.Data.DefaultDatasetID, limit), nil, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// abort stops a run whose Round is gone. The cancelled request's context would
// refuse to carry it, so the call is detached from that cancellation and given
// its own short deadline.
func (g *Gateway) abort(ctx context.Context, runID string) {
	if runID == "" {
		return
	}
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = g.request(abortCtx, http.MethodPost, "/v2/actor-runs/"+url.PathEscape(runID)+"/abort", []byte("{}"), nil)
}

func (g *Gateway) settle(ctx context.Context, run researchapp.ActorRun, status string, final providerRun) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	at := g.config.Now()
	run.Status = status
	run.ProviderRunID = final.ID
	run.ItemCount = final.ItemCount
	run.CostMicros = final.CostMicros
	if reported := usdMicros(final.UsageTotalUsd); reported > run.CostMicros {
		run.CostMicros = reported
	}
	if status != researchapp.ActorRunSucceeded || final.CostMicros == 0 {
		run.CostMicros = max(run.CostMicros, run.ReservedMicros)
	}
	run.FinishedAt = &at
	_ = g.config.Ledger.FinishActorRun(finishCtx, run)
}

func (g *Gateway) complete(ctx context.Context, call, code string, status int, retrySeconds ...int) {
	if call == "" {
		return
	}
	if code == "" {
		code = "SUCCESS"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	retry := 0
	if len(retrySeconds) > 0 {
		retry = retrySeconds[0]
	}
	_ = g.config.Control.CompleteProviderCall(finishCtx, call, code, status, retry, g.config.Now())
}

// Preserve upstream throttling across the Actor lifecycle so the same resource
// cooldown used for API admission also applies to Actor routes.
func (g *Gateway) completeFailure(ctx context.Context, call, fallback string, err error) error {
	if f, ok := fault.As(err); ok && f.Reason == "CATALOG_UPSTREAM_RATE_LIMITED" {
		retry := max(1, min(86400, int((f.RetryAfter+time.Second-1)/time.Second)))
		g.complete(ctx, call, f.Reason, http.StatusTooManyRequests, retry)
		return err
	}
	g.complete(ctx, call, fallback, 0)
	return failure(fallback)
}

func (g *Gateway) request(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, apifyBase+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+g.config.Token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// Starting or aborting a run changes state at the provider and costs
	// money, so those carry the external effect; reads are read-only.
	effect := sharedhttpclient.ReadOnly
	if method == http.MethodPost {
		effect = sharedhttpclient.ExternalEffect
	}
	response, err := sharedhttpclient.Do(ctx, g.config.Client, request, effect)
	if err != nil {
		return failure("CATALOG_NETWORK_FAILED")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return failure("CATALOG_NETWORK_FAILED")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		retry := 60 * time.Second
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			retry = time.Duration(min(seconds, 86400)) * time.Second
		} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && at.After(g.config.Now()) {
			retry = min(24*time.Hour, at.Sub(g.config.Now()))
		}
		f := fault.New(fault.ProviderUnavailable, "CATALOG_UPSTREAM_RATE_LIMITED", true)
		f.RetryAfter = retry
		return f
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return failure("CATALOG_UPSTREAM_FAILED")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return failure("CATALOG_SCHEMA_MISMATCH")
	}
	return nil
}

// observations turns dataset items into Round observations. An item is
// admitted only when its URL is the mall's own product page and it is neither
// sold out nor a paid placement; everything else is counted as rejected, the
// same way the public paths treat a non-product result.
func (g *Gateway) observations(mall researchdomain.KRMall, spec actorSpec, items []map[string]any) researchapp.ActorSearchResult {
	result := researchapp.ActorSearchResult{Products: []researchdomain.ExternalProductObservation{}, Items: len(items)}
	for _, raw := range items {
		item := spec.item(raw)
		if item.soldOut || item.sponsored {
			result.Rejected++
			continue
		}
		ref, err := researchdomain.SourceProductFromURL(item.url)
		if err != nil || ref.Source != mall.Source {
			result.Rejected++
			continue
		}
		canonical, err := ref.ExternalProductURL()
		if err != nil {
			result.Rejected++
			continue
		}
		observation := researchdomain.ExternalProductObservation{
			SchemaVersion: "vitlane.external-product-observation.v1",
			ProductRef:    ref,
			ProductURL:    canonical,
			Title:         shortTitle(item.title),
			ImageURL:      httpsImage(item.imageURL),
			Price:         wonPrice(item.price),
			PriceScope:    "PRODUCT",
			Seller:        researchdomain.ObservedSeller{Kind: "UNKNOWN"},
			Provenance: researchdomain.ProductProvenance{
				APIProvider: "Apify", APIProduct: spec.actor, DetailAPIProvider: "Apify",
				DiscoveryChannel: "ACTOR_SEARCH", Country: "KR", QueryLanguage: "ko",
			},
			ObservedAt: g.config.Now(),
		}
		if observation.Validate() != nil {
			result.Rejected++
			continue
		}
		result.Products = append(result.Products, observation)
	}
	return result
}

func shortTitle(raw string) string {
	value := strings.Join(strings.Fields(raw), " ")
	runes := []rune(value)
	if len(runes) > 300 {
		return strings.TrimSpace(string(runes[:300]))
	}
	return value
}

func httpsImage(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

// wonPrice reads the Actor's KRW amount. Anything that is not a plain whole
// number of won is unknown rather than guessed.
func wonPrice(raw string) researchdomain.VariantObservedPrice {
	unknown := researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 15 {
		return unknown
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 {
		return unknown
	}
	return researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "KRW"}
}

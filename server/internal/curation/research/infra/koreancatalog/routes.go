package koreancatalog

import (
	"context"
	"errors"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"log/slog"
	"time"
)

type routeSlotsKey struct{}
type browserRouteSlotKey struct{}

func acquireRouteSlot(ctx context.Context) (func(), error) {
	slots, ok := ctx.Value(routeSlotsKey{}).(chan struct{})
	if !ok {
		return func() {}, nil
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, safeFailure("CATALOG_REQUEST_CANCELLED")
	}
}

func acquireBrowserRouteSlot(ctx context.Context) (func(), error) {
	slot, ok := ctx.Value(browserRouteSlotKey{}).(chan struct{})
	if !ok {
		return func() {}, nil
	}
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, safeFailure("CATALOG_REQUEST_CANCELLED")
	}
}

type routeOutcome struct {
	products []researchdomain.ExternalProductObservation
	progress researchapp.SourceProgressSet
	rejected int
	err      error
}

func (g *Gateway) planRoutes(ctx context.Context, req researchapp.KoreanSearchRequest) ([]researchapp.ResearchRoute, map[string][]researchapp.ResearchRoute) {
	now := time.Now().UTC()
	states := researchapp.ResearchProviderResources{}
	state := func(id string) researchapp.ProviderResourceState {
		if s, ok := states[id]; ok {
			return s
		}
		s := researchapp.ProviderResourceState{CostClass: "FREE", Reason: "CATALOG_API_NOT_CONFIGURED"}
		if g.Configured(id) {
			d, _ := researchapp.CatalogAPIDefinitionFor(id)
			var err error
			if d.NeedsQuota {
				err = g.ensureQuota(ctx, id)
			}
			if err == nil {
				usage, e := g.Usage(ctx, id, false)
				err = e
				if err == nil {
					if usage.Resources != nil {
						s = *usage.Resources
					} else {
						s = researchapp.ProviderResourceState{CanStart: usage.Control.Enabled, CostClass: "FREE", ObservedAt: now}
						if !s.CanStart {
							s.Reason = "CATALOG_API_DISABLED"
						}
						if d.NeedsQuota {
							s.CostClass = "INCLUDED"
						}
						if d.APIProvider == "Apify" {
							s.CostClass = "METERED"
						}
					}
				}
			}
			if err != nil {
				s.CanStart = false
				s.Reason = "CATALOG_RESOURCE_UNAVAILABLE"
				if f, ok := fault.As(err); ok {
					s.Reason = f.Reason
				}
			}
		}
		states[id] = s
		return s
	}
	actorAllowed := g.config.Actor != nil && g.config.Actor.Configured() && req.AttemptKey != "" && req.UserID != ""
	for _, id := range researchapp.ResearchRouteAPIIDs(req.Vertical, actorAllowed) {
		state(id)
	}
	plan := researchapp.PlanResearchRoutes(req.AttemptKey+"|"+req.Query, req.Vertical, actorAllowed, states, now)
	return plan.Routes, plan.Alternatives
}

func (g *Gateway) executeRoute(ctx context.Context, req researchapp.KoreanSearchRequest, route researchapp.ResearchRoute) routeOutcome {
	out := routeOutcome{progress: researchapp.SourceProgressSet{}}
	budget := req.CollectionBudget.Validated()
	if ctx.Err() != nil {
		out.err = safeFailure("CATALOG_REQUEST_CANCELLED")
		return out
	}
	if route.ID == "OWN_PRODUCT" {
		p := researchapp.SearchProgressFor(req.Progress, "OWN_PRODUCT", req.Query, req.Seeds)
		out.products, out.err = g.searchProducts(ctx, p.Query, budget.ProductDetails)
		var partial *partialDiscoveryError
		if out.err == nil || errors.As(out.err, &partial) {
			out.progress["OWN_PRODUCT"] = researchapp.AdvanceSearchProgress(p, req.Seeds, "", false, false)
		}
		return out
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.Source(route.Source))
	id := route.APIIDs[0]
	key := id
	if id == browserDiscoveryAPIID {
		key = id + ":" + route.Source
	} else if mall.Detail == researchdomain.DetailPublicHTML && mall.Source != researchdomain.SourceElevenStreet {
		key = id + ":" + route.Source
	}
	p := researchapp.SearchProgressFor(req.Progress, key, req.Query, req.Seeds)
	if id == browserDiscoveryAPIID {
		release, err := acquireBrowserRouteSlot(ctx)
		if err != nil {
			out.err = err
			return out
		}
		defer release()
		browserLimit := max(1, min(20, budget.ProductDetails))
		browserOffset := max((p.Page-1)*browserLimit, req.ExistingBrowserSourceCounts[mall.Source])
		found, err := g.searchViaBrowser(ctx, mall, p.Query, browserLimit, browserOffset)
		for _, product := range found.Products {
			if !req.ExistingExternalProductKeys[product.ProductRef.IdentityKey()] {
				out.products = append(out.products, product)
			}
		}
		out.rejected = found.Rejected
		out.err = err
		if found.DiscoveryCompleted {
			out.progress[key] = researchapp.AdvanceSearchProgress(p, req.Seeds, "", found.Items == browserLimit, true)
		}
		return out
	}
	switch mall.Detail {
	case researchdomain.DetailPublicJSON:
		found, err := g.searchMallJSON(ctx, mall, p.Query, p.Page)
		out.products = found.Products
		out.rejected = found.Rejected
		out.err = err
		if err == nil {
			out.progress[key] = researchapp.AdvanceSearchProgress(p, req.Seeds, "", found.HasNext, true)
		}
	case researchdomain.DetailActor:
		release, err := acquireRouteSlot(ctx)
		if err != nil {
			out.err = err
			return out
		}
		defer release()
		found, err := g.config.Actor.SearchMallActor(ctx, researchapp.ActorSearchRequest{Mall: mall, Query: p.Query, Limit: budget.ActorItems, UserID: req.UserID, RunKey: req.AttemptKey + ":" + route.Source})
		out.products = found.Products
		out.rejected = found.Rejected
		out.err = err
		if err == nil {
			out.progress[key] = researchapp.AdvanceSearchProgress(p, req.Seeds, "", false, true)
		}
	case researchdomain.DetailPublicHTML:
		found, err := g.searchViaWebPage(ctx, mall, p.Query, id, p.Page, budget.HTMLDetails, p.Cursor, len(route.APIIDs) == 1)
		out.products = found.Products
		out.rejected = found.Rejected
		out.err = err
		if found.DiscoveryCompleted {
			out.progress[key] = researchapp.AdvanceSearchProgress(p, req.Seeds, found.NextCursor, found.HasNext, id == "NAVER_WEBKR")
		}
	}
	return out
}

type routeRecorder interface {
	RecordResearchRoute(context.Context, string, string, string, researchapp.ResearchRoute, string, string, time.Time) error
}

func (g *Gateway) recordRoute(ctx context.Context, req researchapp.KoreanSearchRequest, route researchapp.ResearchRoute, decision, reason string) {
	if req.AttemptKey == "" || req.UserID == "" {
		return
	}
	if recorder, ok := g.config.Control.(routeRecorder); ok {
		final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := recorder.RecordResearchRoute(final, req.UserID, req.AttemptKey, req.Vertical, route, decision, reason, time.Now().UTC()); err != nil {
			slog.WarnContext(final, "research route evidence could not be recorded", "event", "research.route_record_failed", "route_id", route.ID, "decision", decision)
		}
	}
}
func (g *Gateway) finishRoute(ctx context.Context, req researchapp.KoreanSearchRequest, route researchapp.ResearchRoute, out routeOutcome) {
	decision, reason := "SUCCEEDED", ""
	if len(out.products) == 0 {
		decision = "EMPTY"
	}
	if out.err != nil {
		decision = "FAILED"
		if len(out.products) > 0 {
			decision = "PARTIAL"
		}
		reason = "CATALOG_ROUTE_FAILED"
		if f, ok := fault.As(out.err); ok {
			reason = f.Reason
		}
	}
	g.recordRoute(ctx, req, route, decision, reason)
}

// Refresh after a bounded wait; actual call admission still reserves atomically.
func (g *Gateway) refreshRouteResources(ctx context.Context, route researchapp.ResearchRoute) researchapp.ProviderResourceState {
	states := []researchapp.ProviderResourceState{}
	for _, id := range route.APIIDs {
		usage, err := g.Usage(ctx, id, false)
		if err != nil {
			return researchapp.ProviderResourceState{Reason: "CATALOG_RESOURCE_UNAVAILABLE"}
		}
		if usage.Resources != nil {
			states = append(states, *usage.Resources)
		} else {
			states = append(states, researchapp.ProviderResourceState{CanStart: usage.Configured && usage.Control.Enabled, CostClass: route.Resources.CostClass, Reason: "CATALOG_API_DISABLED"})
		}
	}
	return researchapp.CombineRouteResources(time.Now().UTC(), states...)
}

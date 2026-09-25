package http

import (
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
	"strconv"
	"time"
)

type CatalogAPIHandler struct {
	BackgroundConfigured bool
	Gateway              researchapp.ExternalCatalogGateway
	// ShopifyConfigured reports whether a Shopify catalog gateway is wired. Shopify's
	// row is read from the ledger directly: it has no quota to refresh and no key of
	// its own to check beyond the gateway being there.
	ShopifyConfigured bool
	// ShopifyStub is true when the local review stub answers instead of Shopify. The
	// row says so, as the Korean rows do, so nobody reads stub traffic as Shopify's.
	ShopifyStub bool
	Control     researchapp.CatalogProviderControlRepository
	FX          *researchapp.ExchangeRateService
	Curations   *curationapp.Service
	Summary     researchapp.ResearchRoundSummaryReader
}

// RoundSummary reports the recent Round ledger. It reads durable tables only,
// so refreshing the operator page never spends a provider call.
func (h *CatalogAPIHandler) RoundSummary(w http.ResponseWriter, r *http.Request) {
	if h.Summary == nil {
		httpapi.WriteError(w, 503, "RESEARCH_ROUND_SUMMARY_UNAVAILABLE", "Round summary unavailable")
		return
	}
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 30 {
			httpapi.WriteError(w, 400, "RESEARCH_ROUND_SUMMARY_WINDOW_INVALID", "days must be between 1 and 30")
			return
		}
		days = value
	}
	now := time.Now().UTC()
	summary, err := h.Summary.ReadResearchRoundSummary(r.Context(), now.Add(-time.Duration(days)*24*time.Hour), now)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Round summary unavailable")
		return
	}
	summary.WindowDays = days
	if summary.Steps == nil {
		summary.Steps = []researchapp.ResearchStepDuration{}
	}
	if summary.Attempts == nil {
		summary.Attempts = []researchapp.ResearchAttemptCount{}
	}
	if summary.Reservations == nil {
		summary.Reservations = []researchapp.ManagedReservationCount{}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, summary)
}

// usage reads one API's row. Shopify's comes straight from the ledger; every
// other API's goes through its gateway, which may refresh a provider quota.
func (h *CatalogAPIHandler) usage(r *http.Request, id string, refresh bool) (researchapp.CatalogProviderUsage, error) {
	if id != researchapp.ShopifyCatalogAPIID && id != "TELEGRAM_JIRUM" {
		return h.Gateway.Usage(r.Context(), id, refresh)
	}
	usage, err := h.Control.ReadProviderUsage(r.Context(), id)
	usage.Configured = h.ShopifyConfigured
	if id == "TELEGRAM_JIRUM" {
		usage.Configured = h.BackgroundConfigured
	}
	if h.ShopifyStub && id == researchapp.ShopifyCatalogAPIID {
		usage.APIProduct += " [STUB]"
	}
	if usage.Resources != nil && !usage.Configured {
		usage.Resources.CanStart = false
		usage.Resources.Reason = "CATALOG_API_NOT_CONFIGURED"
		usage.Resources.ReadyAt = nil
	}
	return usage, err
}

func (h *CatalogAPIHandler) configured(id string) bool {
	if id == "TELEGRAM_JIRUM" {
		return h.BackgroundConfigured
	}
	if id == researchapp.ShopifyCatalogAPIID {
		return h.ShopifyConfigured
	}
	return h.Gateway.Configured(id)
}

func (h *CatalogAPIHandler) Usage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if id := r.PathValue("apiId"); id != "" {
		usage, err := h.usage(r, id, r.URL.Query().Get("refresh") == "true")
		if err != nil {
			httpapi.WriteFault(w, r, err, "API usage unavailable")
			return
		}
		httpapi.WriteJSON(w, 200, usage)
		return
	}
	rows := []researchapp.CatalogProviderUsage{}
	for _, definition := range researchapp.OperatorCatalogAPIs() {
		usage, err := h.usage(r, definition.ID, false)
		if err != nil {
			httpapi.WriteFault(w, r, err, "API usage unavailable")
			return
		}
		rows = append(rows, usage)
	}
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.catalog-api-list.v1", "apis": rows})
}
func (h *CatalogAPIHandler) SetControl(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion   string `json:"schemaVersion"`
		Enabled         *bool  `json:"enabled"`
		ExpectedVersion *int64 `json:"expectedVersion"`
	}
	if !httpapi.DecodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != "vitlane.catalog-api-control.v1" || body.Enabled == nil || body.ExpectedVersion == nil {
		httpapi.WriteError(w, 400, "CATALOG_CONTROL_INVALID", "Invalid API control")
		return
	}
	id := r.PathValue("apiId")
	if *body.Enabled && !h.configured(id) {
		httpapi.WriteError(w, 409, "CATALOG_API_NOT_CONFIGURED", "API configuration required")
		return
	}
	if _, err := h.Control.UpdateProviderControl(r.Context(), id, user, *body.Enabled, *body.ExpectedVersion, time.Now().UTC()); err != nil {
		httpapi.WriteFault(w, r, err, "API control unavailable")
		return
	}
	usage, err := h.usage(r, id, false)
	if err != nil {
		httpapi.WriteFault(w, r, err, "API usage unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, usage)
}
func (h *CatalogAPIHandler) ExchangeRate(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.Curations.ResearchSettings(r.Context(), user, r.PathValue("curationId")); err != nil {
		httpapi.WriteError(w, 404, "CURATION_NOT_FOUND", "Curation unavailable")
		return
	}
	view, err := h.FX.View(r.Context())
	if err != nil {
		httpapi.WriteFault(w, r, err, "Exchange rate unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, view)
}

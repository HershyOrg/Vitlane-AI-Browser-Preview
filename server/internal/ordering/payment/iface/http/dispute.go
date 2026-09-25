package http

import (
	"net/http"
	"strconv"
	"strings"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

// ListPayPalDisputes is the operator queue handler. The environment parameter
// is mandatory so Sandbox and Live facts can never be returned in one list.
// Suggested route: GET /api/v1/admin/payment/paypal/disputes.
func (h *Handler) ListPayPalDisputes(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	environment, ok := requirePayPalDisputeEnvironment(w, r)
	if !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, r, domain.ErrDisputeInvalid)
			return
		}
		limit = parsed
	}
	items, err := h.service.ListPayPalDisputeQueue(r.Context(), paymentapp.DisputeQueueFilter{
		Environment: environment,
		State:       r.URL.Query().Get("state"), Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.paypal-dispute-queue.v1",
		"disputes":      items,
	})
}

// GetPayPalDispute returns the case and append-only manual Resolution Center
// records. Suggested route: GET /api/v1/admin/payment/paypal/disputes/{caseId}.
func (h *Handler) GetPayPalDispute(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	environment, ok := requirePayPalDisputeEnvironment(w, r)
	if !ok {
		return
	}
	view, err := h.service.GetPayPalDisputeView(
		r.Context(), environment, r.PathValue("caseId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.paypal-dispute.v1",
		"dispute":       view,
	})
}

// RecordPayPalDisputeAction only records an action that the operator already
// performed in PayPal Resolution Center. It never calls a PayPal write API.
// Suggested route: POST /api/v1/admin/payment/paypal/disputes/{caseId}/actions.
func (h *Handler) RecordPayPalDisputeAction(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	environment, ok := requirePayPalDisputeEnvironment(w, r)
	if !ok {
		return
	}
	var input paymentapp.RecordPayPalDisputeActionInput
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.RecordPayPalDisputeAction(
		r.Context(), environment, r.PathValue("caseId"), actorUserID,
		r.Header.Get("Idempotency-Key"), input,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.paypal-dispute-action.v1",
		"result":        result,
	})
}

func requirePayPalDisputeEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) (string, bool) {
	environment := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("environment")))
	if environment != "SANDBOX" && environment != "LIVE" {
		writeError(w, r, domain.ErrDisputeInvalid)
		return "", false
	}
	return environment, true
}

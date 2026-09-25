package http

import (
	"encoding/json"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"net/http"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func (h *Handler) ReviewSurface(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	taskID := r.PathValue("taskId")
	decisions, err := h.service.ListTaskManualDecisions(r.Context(), taskID, operatorID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	requests, err := h.service.ListTaskCustomerRequests(r.Context(), taskID, operatorID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.procurement-manual-review.v1",
		"decisions":     decisions, "customerRequests": requests,
	})
}

func (h *Handler) RecordManualDecision(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input procurementapp.RecordManualDecisionInput
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, operatorID, "OPERATOR", procmsg.RequestManualDecision, "", "", r.PathValue("taskId"), "", input)
}

func (h *Handler) CreateCustomerRequest(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input procurementapp.CreateCustomerRequestInput
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, operatorID, "OPERATOR", procmsg.RequestCustomerQuestion, "", "", r.PathValue("taskId"), "", input)
}

type operatorResolveRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	Cancel          bool   `json:"cancel"`
	Reason          string `json:"reason"`
}

func (h *Handler) ResolveCustomerRequest(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input operatorResolveRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, operatorID, "OPERATOR", procmsg.RequestCloseCustomerQuestion, "", "", "", r.PathValue("requestId"), input)
}

func (h *Handler) BeginMerchantEffect(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	h.submitAction(w, r, actor, "OPERATOR", procmsg.RequestPurchase, "", "", r.PathValue("taskId"), "", nil)
}

func (h *Handler) ListCustomerRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	requests, err := h.service.ListOrderCustomerRequests(
		r.Context(), r.PathValue("agencyOrderId"), userID,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion":    "vitlane.procurement-customer-requests.v1",
		"customerRequests": requests,
	})
}

type customerResponseRequest struct {
	ExpectedVersion int64           `json:"expectedVersion"`
	Decline         bool            `json:"decline"`
	Response        json.RawMessage `json:"response"`
}

func (h *Handler) RespondCustomerRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input customerResponseRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, userID, "CUSTOMER", procmsg.RequestCustomerResponse, "", "", "", r.PathValue("requestId"), input)
}

package http

import (
	"errors"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

func (h *Handler) Budget(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var result curationdomain.BudgetLedger
	var err error
	if r.Method == http.MethodPatch {
		var input struct {
			curationdomain.BudgetCommand
			ExpectedVersion *int64 `json:"expectedVersion"`
		}
		if !httpapi.DecodeJSON(w, r, &input) {
			return
		}
		if input.ExpectedVersion == nil {
			httpapi.WriteError(w, 400, "BUDGET_INVALID", "Expected budget version is required")
			return
		}
		input.BudgetCommand.ExpectedVersion = *input.ExpectedVersion
		result, err = h.service.ChangeBudget(r.Context(), user, r.PathValue("curationId"), input.BudgetCommand)
	} else {
		result, err = h.service.Budget(r.Context(), user, r.PathValue("curationId"))
	}
	if errors.Is(err, curationdomain.ErrPlanNotFound) {
		httpapi.WriteError(w, 404, "CURATION_NOT_FOUND", "Curation unavailable")
		return
	}
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "Budget unavailable") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Budget unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, result)
}

// Compatibility endpoint: automatic allocation now belongs to a curation thread.
func (h *Handler) BudgetProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	httpapi.WriteError(w, http.StatusGone, "CURATION_BUDGET_PROPOSAL_RETIRED", "Use the curation Auto request flow")
}

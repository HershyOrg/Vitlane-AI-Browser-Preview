package http

import (
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

func (h *Handler) Criteria(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var value any
	var err error
	if r.Method == http.MethodPut {
		var input a.CriteriaCommand
		if !httpapi.DecodeJSON(w, r, &input) {
			return
		}
		value, err = h.service.ChangeTargetCriteria(r.Context(), user, r.PathValue("curationId"), r.PathValue("targetId"), input)
	} else {
		value, err = h.service.TargetCriteria(r.Context(), user, r.PathValue("curationId"), r.PathValue("targetId"))
	}
	if err != nil {
		if errors.Is(err, d.ErrTargetNotFound) {
			httpapi.WriteError(w, 404, "TARGET_NOT_FOUND", "Target unavailable")
			return
		}
		if httpapi.WriteFaultIfClassified(w, r, err, "Criteria unavailable") {
			return
		}
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "Criteria unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, value)
}

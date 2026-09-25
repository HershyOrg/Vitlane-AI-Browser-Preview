package http

import (
	"errors"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

func (h *Handler) ResearchSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var result curationdomain.ResearchSettings
	var err error
	if r.Method == http.MethodPatch {
		var input struct {
			SchemaVersion   string `json:"schemaVersion"`
			Country         string `json:"country"`
			ExpectedVersion *int64 `json:"expectedVersion"`
		}
		if !httpapi.DecodeJSON(w, r, &input) {
			return
		}
		if input.SchemaVersion != "vitlane.research-settings.v1" || input.ExpectedVersion == nil {
			httpapi.WriteError(w, 400, "RESEARCH_SETTINGS_INVALID", "Invalid research settings")
			return
		}
		result, err = h.service.ChangeResearchSettings(r.Context(), user, r.PathValue("curationId"), input.Country, *input.ExpectedVersion)
	} else {
		result, err = h.service.ResearchSettings(r.Context(), user, r.PathValue("curationId"))
	}
	if errors.Is(err, curationdomain.ErrPlanNotFound) {
		httpapi.WriteError(w, 404, "CURATION_NOT_FOUND", "Curation unavailable")
		return
	}
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "Research settings unavailable") {
			return
		}
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "Research settings unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, result)
}

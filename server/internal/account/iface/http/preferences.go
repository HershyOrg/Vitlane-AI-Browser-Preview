package http

import (
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

func (h *Handler) Preferences(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.preferences == nil {
		httpapi.WriteError(w, 503, "USER_PREFERENCES_UNAVAILABLE", "Preferences unavailable")
		return
	}
	var p accountdomain.UserPreferences
	var err error
	if r.Method == http.MethodPatch {
		var input struct {
			SchemaVersion   string `json:"schemaVersion"`
			ExpectedVersion *int64 `json:"expectedVersion"`
			accountdomain.PreferencesPatch
		}
		if !httpapi.DecodeJSON(w, r, &input) {
			return
		}
		if input.SchemaVersion != "vitlane.user-preferences.v1" || input.ExpectedVersion == nil {
			httpapi.WriteError(w, 400, "USER_PREFERENCES_INVALID", "Invalid preferences")
			return
		}
		p, err = h.preferences.Patch(r.Context(), user, input.PreferencesPatch, input.ExpectedVersion)
	} else {
		p, err = h.preferences.Read(r.Context(), user)
	}
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "Preferences unavailable") {
			return
		}
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "Preferences unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, map[string]any{
		"preferences": p,
		"effective":   p.Effective(accountapp.RequestPreferenceDefaults(r.Context())),
	})
}

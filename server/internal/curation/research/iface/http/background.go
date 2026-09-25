package http

import (
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
	"regexp"
)

type BackgroundHandler struct{ Service *a.BackgroundService }

var backgroundUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func (h *BackgroundHandler) View(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if !validBackgroundIDs(w, r, "curationId") {
		return
	}
	view, e := h.Service.Repository.BackgroundView(r.Context(), user, r.PathValue("curationId"))
	if e != nil {
		httpapi.WriteFault(w, r, e, "Background research unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, view)
}
func (h *BackgroundHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if !validBackgroundIDs(w, r, "curationId", "subscriptionId") {
		return
	}
	if e := h.Service.Repository.CancelSubscription(r.Context(), user, r.PathValue("curationId"), r.PathValue("subscriptionId")); e != nil {
		httpapi.WriteFault(w, r, e, "Subscription unavailable")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{"status": "RECORDED"})
}
func (h *BackgroundHandler) Hide(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if !validBackgroundIDs(w, r, "curationId", "findingId") {
		return
	}
	if e := h.Service.Repository.HideFinding(r.Context(), user, r.PathValue("curationId"), r.PathValue("findingId")); e != nil {
		httpapi.WriteFault(w, r, e, "Finding unavailable")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{"status": "RECORDED"})
}
func (h *BackgroundHandler) Import(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if !validBackgroundIDs(w, r, "curationId", "findingId") {
		return
	}
	candidate, e := h.Service.ImportFinding(r.Context(), user, r.PathValue("curationId"), r.PathValue("findingId"))
	if e != nil {
		httpapi.WriteFault(w, r, e, "Finding could not be added")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{"candidateId": candidate})
}
func (h *BackgroundHandler) Notices(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var in struct {
		SchemaVersion string `json:"schemaVersion"`
		ClientID      string `json:"clientId"`
		CurationID    string `json:"curationId"`
		Visible       bool   `json:"visible"`
	}
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	if in.SchemaVersion != "vitlane.curation-notice-sync.v1" || !backgroundUUID.MatchString(in.ClientID) || (in.CurationID != "" && !backgroundUUID.MatchString(in.CurationID)) {
		httpapi.WriteError(w, 400, "NOTICE_SYNC_INVALID", "Invalid notice sync")
		return
	}
	ids, e := h.Service.Repository.NoticeSync(r.Context(), user, in.ClientID, in.CurationID, in.Visible)
	if e != nil {
		httpapi.WriteFault(w, r, e, "Notice sync unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.curation-notices.v1", "curationIds": ids})
}

func validBackgroundIDs(w http.ResponseWriter, r *http.Request, names ...string) bool {
	for _, name := range names {
		if !backgroundUUID.MatchString(r.PathValue(name)) {
			httpapi.WriteError(w, 400, "BACKGROUND_ID_INVALID", "Invalid background research identifier")
			return false
		}
	}
	return true
}

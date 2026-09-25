package http

import (
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
	"strings"
)

type ThreadHandler struct{ service *a.ThreadService }

func NewThreadHandler(s *a.ThreadService) *ThreadHandler { return &ThreadHandler{s} }
func (h *ThreadHandler) GetMode(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	out, err := h.service.Mode(r.Context(), user, r.PathValue("curationId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, out)
}
func (h *ThreadHandler) SetMode(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var in d.ControlMode
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	out, err := h.service.ChangeMode(r.Context(), user, r.PathValue("curationId"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, out)
}
func (h *ThreadHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	mode, out, err := h.service.List(r.Context(), user, r.PathValue("curationId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": d.ThreadSchema, "controlMode": mode, "threads": out})
}
func (h *ThreadHandler) Submit(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	principal, ok := shared.WebPrincipalFrom(r.Context())
	if !ok {
		return
	}
	var in a.SubmitThreadInput
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" || r.Header.Get("Idempotency-Key") != in.ID {
		httpapi.WriteError(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "요청 식별자를 확인해 주세요.")
		return
	}
	out, err := h.service.Submit(r.Context(), user, principal.AuthSessionID, r.PathValue("curationId"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 202, out)
}
func (h *ThreadHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	out, err := h.service.Cancel(r.Context(), user, r.PathValue("curationId"), r.PathValue("threadId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, out)
}
func (h *ThreadHandler) Answer(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var in d.ThreadAnswer
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	out, err := h.service.Answer(r.Context(), user, r.PathValue("curationId"), r.PathValue("threadId"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 202, out)
}
func (h *ThreadHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if httpapi.WriteFaultIfClassified(w, r, err, "요청을 처리하지 못했습니다.") {
		return
	}
	httpapi.WriteError(w, http.StatusConflict, err.Error(), "현재 큐레이션 상태를 확인해 주세요.")
}

func (h *ThreadHandler) CancelAction(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	out, err := h.service.CancelAction(r.Context(), user, r.PathValue("curationId"), r.PathValue("threadId"), r.PathValue("actionId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, out)
}

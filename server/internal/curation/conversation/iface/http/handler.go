package http

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	a "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
	"strings"
)

type Handler struct{ Service *a.Service }

func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	principal, ok := shared.WebPrincipalFrom(r.Context())
	if !ok || principal.UserID != user {
		httpapi.WriteError(w, 401, "AUTHENTICATION_REQUIRED", "로그인이 필요합니다.")
		return
	}
	var in a.RequestInput
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	if in.ClientRequestID == "" || in.ClientRequestID != strings.TrimSpace(r.Header.Get("Idempotency-Key")) {
		httpapi.WriteError(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "요청 식별자가 필요합니다.")
		return
	}
	in.UserID = user
	in.AuthSessionID = principal.AuthSessionID
	in.CurationID = r.PathValue("curationId")
	result, err := h.Service.Execute(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, result)
}
func (h *Handler) Respond(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	principal, ok := shared.WebPrincipalFrom(r.Context())
	if !ok || principal.UserID != user {
		httpapi.WriteError(w, 401, "AUTHENTICATION_REQUIRED", "로그인이 필요합니다.")
		return
	}
	var in a.ResponseInput
	if !httpapi.DecodeJSON(w, r, &in) {
		return
	}
	if in.ClientRequestID == "" || in.ClientRequestID != strings.TrimSpace(r.Header.Get("Idempotency-Key")) {
		httpapi.WriteError(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "요청 식별자가 필요합니다.")
		return
	}
	in.UserID = user
	in.AuthSessionID = principal.AuthSessionID
	in.CurationID = r.PathValue("curationId")
	in.MessageID = r.PathValue("messageId")
	if err := h.Service.Respond(r.Context(), in); err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "RECORDED"})
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "요청을 처리하지 못했습니다.")
		return
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Message {
		case "CURATION_ACTION_IN_PROGRESS", "TOO_MANY_ACTIVE_ACTIONS", "CONVERSATION_IDEMPOTENCY_CONFLICT", "CONVERSATION_VERSION_CONFLICT", "VERSION_CONFLICT":
			httpapi.WriteError(w, 409, pg.Message, "요청 상태가 바뀌었습니다. 다시 확인해 주세요.")
			return
		}
	}
	switch err.Error() {
	case "VERSION_CONFLICT", "CURATION_ACTION_IN_PROGRESS", "RESEARCH_ACTION_IN_PROGRESS", "AUTO_RESEARCH_IN_PROGRESS":
		httpapi.WriteError(w, 409, err.Error(), "요청 상태가 바뀌었습니다. 다시 확인해 주세요.")
		return
	}
	httpapi.WriteError(w, 422, "CONVERSATION_REQUEST_UNAVAILABLE", "현재 요청을 실행할 수 없습니다.")
}

package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	autoapp "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	autodomain "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Executor interface {
	Execute(context.Context, autoapp.Input) (autoapp.Result, error)
}

type Handler struct {
	service Executor
}

func NewHandler(service Executor) *Handler {
	return &Handler{service: service}
}

type request struct {
	Request                 string `json:"request"`
	ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
	ClientRequestID         string `json:"clientRequestId"`
}

func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok || principal.UserID != userID {
		httpapi.WriteError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "로그인이 필요합니다.")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Auto 조사 요청에는 Idempotency-Key가 필요합니다.")
		return
	}
	var payload request
	if !httpapi.DecodeJSON(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.ClientRequestID) != idempotencyKey {
		httpapi.WriteError(w, http.StatusBadRequest, autodomain.ErrInvalid.Error(), "Auto 조사 요청 값을 확인해 주세요.")
		return
	}
	result, err := h.service.Execute(r.Context(), autoapp.Input{
		UserID: userID, AuthSessionID: principal.AuthSessionID,
		CurationID: r.PathValue("curationId"), Request: payload.Request,
		ExpectedCurationVersion: payload.ExpectedCurationVersion,
		ClientRequestID:         payload.ClientRequestID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, result)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "Auto 조사 요청을 처리하지 못했습니다.")
		return
	}
	status := http.StatusUnprocessableEntity
	code := autoapp.ErrorCode(err)
	message := "현재 Curation 상태와 요청을 확인해 주세요."
	switch {
	case errors.Is(err, autodomain.ErrInvalid):
		status, message = http.StatusBadRequest, "Auto 조사 요청 값을 확인해 주세요."
	case autoapp.IsConflict(err), errors.Is(err, curationdomain.ErrVersionConflict):
		status, message = http.StatusConflict, "Curation 상태가 변경되었습니다. 새 상태에서 다시 요청해 주세요."
	case errors.Is(err, curationdomain.ErrCurationNotFound),
		errors.Is(err, curationdomain.ErrPlanNotFound):
		status, message = http.StatusNotFound, "Curation을 찾을 수 없습니다."
	}
	httpapi.WriteError(w, status, code, message)
}

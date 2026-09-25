package http

import (
	"errors"
	"net/http"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livedomain "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct{ service *liveapp.Service }

func NewHandler(service *liveapp.Service) *Handler { return &Handler{service: service} }

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.State(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable,
			"LIVE_CONTROL_UNAVAILABLE", "Live 제어 상태를 불러오지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.live-control.v1", "liveControl": state,
	})
}

type killRequest struct {
	Scope           string `json:"scope"`
	Confirmation    string `json:"confirmation"`
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

func (h *Handler) Kill(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input killRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	state, err := h.service.Kill(r.Context(), liveapp.ChangeRequest{
		Scope: input.Scope, Confirmation: input.Confirmation, Reason: input.Reason,
		ExpectedVersion: input.ExpectedVersion, Actor: actor,
	})
	if err != nil {
		switch {
		case errors.Is(err, livedomain.ErrInvalid):
			httpapi.WriteError(w, http.StatusUnprocessableEntity,
				"LIVE_CONTROL_CONFIRMATION_INVALID", "확인 문구, 사유 또는 범위가 올바르지 않습니다.")
		case errors.Is(err, livedomain.ErrVersionConflict):
			httpapi.WriteError(w, http.StatusConflict,
				"LIVE_CONTROL_VERSION_CONFLICT", "다른 운영 변경이 먼저 반영되었습니다. 상태를 새로고침해 주세요.")
		default:
			httpapi.WriteError(w, http.StatusServiceUnavailable,
				"LIVE_CONTROL_MUTATION_FAILED", "Live 종료 명령을 반영하지 못했습니다.")
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.live-control.v1", "liveControl": state,
	})
}

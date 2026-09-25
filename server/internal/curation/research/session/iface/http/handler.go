package http

import (
	"encoding/json"
	"errors"
	"net/http"

	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service *shoppingsessionapp.Service
}

func NewHandler(service *shoppingsessionapp.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	session, err := h.service.Get(r.Context(), userID, r.PathValue("sessionId"))
	if errors.Is(err, shoppingsessiondomain.ErrSessionNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "쇼핑 세션을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "세션을 불러오지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "세션을 불러오지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id": session.ID, "planTargetId": session.PlanTargetID, "userId": session.UserID,
			"targetSnapshot":         json.RawMessage(session.TargetSnapshot),
			"researchScopeSnapshot":  json.RawMessage(session.ResearchScopeSnapshot),
			"status":                 session.Status,
			"currentResearchRoundId": session.CurrentResearchRoundID,
			"version":                session.Version,
			"createdAt":              session.CreatedAt, "updatedAt": session.UpdatedAt,
		},
	})
}

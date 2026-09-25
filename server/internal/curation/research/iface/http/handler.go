package http

import (
	"context"
	"encoding/json"
	"errors"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	"net/http"
	"strings"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	threads  *curationapp.ThreadService
	service  *researchapp.Service
	research researchStarter
}

type researchStarter interface {
	StartResearch(
		context.Context,
		researchapp.StartResearchInput,
	) (researchapp.StartResearchResult, error)
}

func (h *Handler) SetThreads(threads *curationapp.ThreadService) { h.threads = threads }

func NewHandler(service *researchapp.Service) *Handler {
	return &Handler{service: service, research: service}
}

func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetPlanResearch(
		r.Context(), userID, r.PathValue("planId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	groups := make([]map[string]any, 0, len(result.Groups))
	for _, group := range result.Groups {
		session := group.Session
		groups = append(groups, map[string]any{
			"session": map[string]any{
				"id": session.ID, "planTargetId": session.PlanTargetID,
				"userId":                 session.UserID,
				"targetSnapshot":         json.RawMessage(session.TargetSnapshot),
				"researchScopeSnapshot":  json.RawMessage(session.ResearchScopeSnapshot),
				"status":                 session.Status,
				"currentResearchRoundId": session.CurrentResearchRoundID,
				"version":                session.Version, "createdAt": session.CreatedAt,
				"updatedAt": session.UpdatedAt,
			},
			"round": group.Round, "rounds": group.Rounds,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"planId": result.PlanID, "groups": groups,
	})
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.Cancel(r.Context(), userID, r.PathValue("sessionId")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListLikedVariantsV2(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	values, err := h.service.ListLikedVariantsV2(r.Context(), userID, 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	products, err := h.service.ListLikedProducts(r.Context(), userID, 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"candidates": values, "products": products})
}

// ListPurchaseChecks is the account-scope list of self-reported purchases
// (ADR-0075). It is a projection of the user's own records, not order history.
func (h *Handler) ListPurchaseChecks(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	values, err := h.service.ListPurchaseChecks(r.Context(), userID, 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"schemaVersion": "vitlane.account-purchase-checks.v1", "records": values})
}

type researchAgainRequest struct {
	SchemaVersion           string `json:"schemaVersion,omitempty"`
	ExpectedCriteriaVersion *int64 `json:"expectedCriteriaVersion,omitempty"`
	CurationID              string `json:"curationId"`
	TargetID                string `json:"targetId"`
	CurationActionID        string `json:"curationActionId"`
	Feedback                string `json:"feedback"`
	ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
	ExpectedSessionVersion  int64  `json:"expectedSessionVersion"`
}

type startResearchRequest struct {
	CurationID              string   `json:"curationId"`
	CurationActionID        string   `json:"curationActionId"`
	ExpectedCurationVersion int64    `json:"expectedCurationVersion"`
	SessionIDs              []string `json:"sessionIds"`
}

func (h *Handler) StartResearch(w http.ResponseWriter, r *http.Request) {
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED",
			"로그인이 필요합니다.",
		)
		return
	}
	requestID, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request startResearchRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.CurationID) == "" ||
		strings.TrimSpace(request.CurationActionID) == "" ||
		request.ExpectedCurationVersion < 1 ||
		len(request.SessionIDs) == 0 {
		httpapi.WriteError(
			w, http.StatusBadRequest, "INVALID_REQUEST",
			"조사를 맡길 항목을 확인해 주세요.",
		)
		return
	}
	result, err := h.research.StartResearch(
		r.Context(),
		researchapp.StartResearchInput{
			UserID: principal.UserID, AuthSessionID: principal.AuthSessionID,
			PlanID: r.PathValue("planId"), CurationID: request.CurationID,
			CurationActionID:        request.CurationActionID,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
			SessionIDs:              append([]string(nil), request.SessionIDs...),
			IdempotencyKey:          requestID,
		},
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) ResearchAgain(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok || principal.UserID != userID {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED",
			"로그인이 필요합니다.",
		)
		return
	}
	requestID, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request researchAgainRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if request.SchemaVersion != "" && request.SchemaVersion != "vitlane.research-again.v2" {
		httpapi.WriteError(w, 400, "RESEARCH_COMMAND_INVALID", "Unknown research command schema")
		return
	}
	if request.SchemaVersion == "vitlane.research-again.v2" && request.ExpectedCriteriaVersion == nil {
		httpapi.WriteError(w, 400, "RESEARCH_COMMAND_INVALID", "Expected criteria version is required")
		return
	}
	if h.threads != nil {
		if request.CurationActionID != requestID {
			httpapi.WriteError(w, 400, "RESEARCH_COMMAND_INVALID", "Action ID must match the request key")
			return
		}
		result, err := h.threads.Submit(r.Context(), userID, principal.AuthSessionID, request.CurationID, curationapp.SubmitThreadInput{ID: requestID, Kind: "RESEARCH_AGAIN", Request: request.Feedback, TargetID: request.TargetID, ExpectedCurationVersion: request.ExpectedCurationVersion, SessionID: r.PathValue("sessionId"), ExpectedSessionVersion: &request.ExpectedSessionVersion, ExpectedCriteriaVersion: request.ExpectedCriteriaVersion})
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusAccepted, result)
		return
	}
	result, err := h.service.ResearchAgain(r.Context(), researchapp.ResearchAgainInput{ExpectedCriteriaVersion: request.ExpectedCriteriaVersion,
		UserID: userID, AuthSessionID: principal.AuthSessionID,
		CurationID:              request.CurationID,
		TargetID:                request.TargetID,
		SessionID:               r.PathValue("sessionId"),
		CurationActionID:        request.CurationActionID,
		Feedback:                request.Feedback,
		ExpectedCurationVersion: request.ExpectedCurationVersion,
		ExpectedSessionVersion:  request.ExpectedSessionVersion,
		ClientRequestID:         requestID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpapi.WriteError(
			w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED",
			"상태 변경 요청에는 Idempotency-Key가 필요합니다.",
		)
		return "", false
	}
	return key, true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "외부 상품 조회를 완료하지 못했습니다.")
		return
	}
	status := http.StatusUnprocessableEntity
	code := reasonCode(err)
	message := "현재 상태와 요청 값을 확인해 주세요."
	switch {
	case errors.Is(err, curationdomain.ErrPlanNotFound),
		errors.Is(err, shoppingsessiondomain.ErrSessionNotFound),
		errors.Is(err, researchdomain.ErrRoundNotFound),
		errors.Is(err, researchdomain.ErrFeedbackNotFound),
		errors.Is(err, researchdomain.ErrCandidateInvalid),
		errors.Is(err, researchdomain.ErrConfigurationNotFound):
		status, message = http.StatusNotFound, "Plan, Session 또는 조사 작업을 찾을 수 없습니다."
	case errors.Is(err, researchdomain.ErrRoundClosed),
		errors.Is(err, shoppingsessiondomain.ErrSessionNotReady),
		errors.Is(err, shoppingsessiondomain.ErrSessionNotResearching),
		errors.Is(err, shoppingsessiondomain.ErrResearchRoundMismatch):
		status, message = http.StatusConflict, "조사 상태가 변경되었습니다."
	case errors.Is(err, shoppingsessiondomain.ErrSessionVersionConflict),
		errors.Is(err, researchdomain.ErrIdempotencyConflict),
		errors.Is(err, researchdomain.ErrInvalidResearchCommand):
		status, message = http.StatusConflict, "다른 변경이 먼저 저장되었습니다. 최신 상태를 다시 확인해 주세요."
	case errors.Is(err, curationdomain.ErrPlanNotConfirmed):
		status, message = http.StatusConflict, "확정된 Plan만 조사할 수 있습니다."
	case errors.Is(err, curationdomain.ErrExternalAgentRetired):
		status, message = http.StatusConflict,
			"외부 Agent는 전환 준비 중입니다. ManagedAgent로 다시 시도해 주세요."
	case errors.Is(err, curationdomain.ErrVersionConflict),
		errors.Is(err, curationdomain.ErrIdempotencyKeyReused),
		errors.Is(err, curationdomain.ErrCurationActionUnavailable),
		errors.Is(err, curationdomain.ErrCurationActionInProgress),
		errors.Is(err, researchdomain.ErrResearchActionInProgress),
		errors.Is(err, curationdomain.ErrCurationArchived):
		status, message = http.StatusConflict, "큐레이션 상태가 변경되었거나 다른 행동이 진행 중입니다."
	case errors.Is(err, researchdomain.ErrTooManyActiveActions):
		status, message = http.StatusConflict,
			"동시에 진행할 수 있는 요청 수를 넘었습니다. 진행 중인 요청이 끝나거나 취소된 뒤 다시 시도해 주세요."
	case code == "INTERNAL_ERROR":
		status, message = http.StatusInternalServerError, "조사 요청을 처리하지 못했습니다."
	}
	httpapi.WriteError(w, status, code, message)
}

func reasonCode(err error) string {
	known := []error{
		curationdomain.ErrPlanNotFound, curationdomain.ErrPlanNotConfirmed,
		curationdomain.ErrExternalAgentRetired,
		curationdomain.ErrVersionConflict,
		curationdomain.ErrIdempotencyKeyReused,
		curationdomain.ErrCurationActionUnavailable,
		curationdomain.ErrCurationActionInProgress,
		curationdomain.ErrCurationArchived,
		shoppingsessiondomain.ErrSessionNotFound, shoppingsessiondomain.ErrSessionNotReady,
		shoppingsessiondomain.ErrSessionNotResearching, shoppingsessiondomain.ErrResearchRoundMismatch,
		researchdomain.ErrRoundNotFound, researchdomain.ErrRoundClosed,
		researchdomain.ErrFeedbackNotFound, researchdomain.ErrFeedbackInvalid,
		researchdomain.ErrCandidateInvalid,
		researchdomain.ErrIdempotencyConflict,
		researchdomain.ErrLikedVariantInvalid,
		researchdomain.ErrConfigurationInvalid,
		researchdomain.ErrConfigurationNotFound,
		shoppingsessiondomain.ErrSessionVersionConflict,
		shoppingsessiondomain.ErrCandidateNotPurchasable,
		researchdomain.ErrInvalidResearchCommand,
		researchdomain.ErrResearchActionInProgress,
		researchdomain.ErrTooManyActiveActions,
	}
	for _, candidate := range known {
		if errors.Is(err, candidate) {
			return candidate.Error()
		}
	}
	return "INTERNAL_ERROR"
}

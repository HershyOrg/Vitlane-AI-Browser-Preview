package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service *curationapp.Service
	logger  *slog.Logger
}

func NewHandler(
	service *curationapp.Service,
	logger *slog.Logger,
) *Handler {
	return &Handler{service: service, logger: logger}
}

type moneyRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (m moneyRequest) input() curationapp.MoneyInput {
	return curationapp.MoneyInput{Amount: m.Amount, Currency: m.Currency}
}

type createPlanRequest struct {
	ControlMode    string                               `json:"controlMode,omitempty"`
	OriginalIntent string                               `json:"originalIntent"`
	PlanningMode   string                               `json:"planningMode"`
	TotalBudget    moneyRequest                         `json:"totalBudget"`
	BudgetRequest  *curationdomain.InitialBudgetRequest `json:"budget"`
	ExecutionMode  string                               `json:"executionMode"`
	Location       struct {
		Country string `json:"country"`
		City    string `json:"city"`
	} `json:"location"`
	Category     string        `json:"category"`
	AllowedItems []string      `json:"allowedItems"`
	BlockedItems []string      `json:"blockedItems"`
	MinPrice     *moneyRequest `json:"minPrice"`
	MaxPrice     *moneyRequest `json:"maxPrice"`
	ReferenceURL string        `json:"referenceUrl"`
	URLMode      string        `json:"urlMode"`
	// AgentMode and ModelKey pick the execution owner for this plan. Omitting
	// them keeps the ADR-0024 external-agent behaviour.
	AgentMode string `json:"agentMode"`
	ModelKey  string `json:"modelKey"`
}

func (h *Handler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePlanningWebPrincipal(w, r)
	if !ok {
		return
	}
	userID := principal.UserID
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		httpapi.WriteError(
			w, http.StatusBadRequest, curationdomain.ErrIdempotencyKeyRequired.Error(),
			"Plan 생성 요청에는 Idempotency-Key가 필요합니다.",
		)
		return
	}
	var request createPlanRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.CreatePlan(r.Context(), curationapp.CreatePlanInput{
		UserID: userID, AuthSessionID: principal.AuthSessionID,
		OriginalIntent: request.OriginalIntent, ControlMode: request.ControlMode,
		PlanningMode: request.PlanningMode, ExecutionMode: request.ExecutionMode,
		TotalBudget:   request.TotalBudget.input(),
		BudgetRequest: request.BudgetRequest,
		Country:       request.Location.Country, City: request.Location.City,
		Category: request.Category, AllowedItems: request.AllowedItems,
		BlockedItems: request.BlockedItems,
		MinPrice:     optionalMoneyInput(request.MinPrice),
		MaxPrice:     optionalMoneyInput(request.MaxPrice),
		ReferenceURL: request.ReferenceURL, URLMode: request.URLMode,
		AgentMode: request.AgentMode, ModelKey: request.ModelKey,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		h.writePlanningError(w, r, userID, "", err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, planResponse(result))
}

// ListCurations serves the sidebar: one page of titles, newest first. before
// continues toward older Curations and after asks only for Curations created
// since the newest one the caller already shows (ADR-0079).
func (h *Handler) ListCurations(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	query, err := parseCurationListQuery(r)
	if err == nil {
		var page curationapp.CurationListPage
		page, err = h.service.ListCurations(r.Context(), userID, query)
		if err == nil {
			writeCurationListPage(w, page)
			return
		}
	}
	if errors.Is(err, curationapp.ErrCurationListQueryInvalid) {
		httpapi.WriteError(
			w, http.StatusBadRequest, curationapp.ErrCurationListQueryInvalid.Error(),
			"큐레이션 목록 조건이 올바르지 않습니다.",
		)
		return
	}
	if httpapi.WriteFaultIfClassified(w, r, err, "큐레이션 목록을 불러오지 못했습니다.") {
		return
	}
	httpapi.WriteError(
		w, http.StatusInternalServerError, "INTERNAL_ERROR",
		"큐레이션 목록을 불러오지 못했습니다.",
	)
}

func writeCurationListPage(w http.ResponseWriter, page curationapp.CurationListPage) {
	body := struct {
		SchemaVersion string                         `json:"schemaVersion"`
		Curations     []curationapp.CurationListItem `json:"curations"`
		NextCursor    string                         `json:"nextCursor,omitempty"`
		LatestCursor  string                         `json:"latestCursor,omitempty"`
	}{SchemaVersion: "vitlane.curation-list.v2", Curations: page.Items}
	for _, pair := range []struct {
		cursor *curationapp.CurationListCursor
		target *string
	}{{page.NextCursor, &body.NextCursor}, {page.LatestCursor, &body.LatestCursor}} {
		if pair.cursor == nil {
			continue
		}
		encoded, err := encodeCurationListCursor(*pair.cursor)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "큐레이션 목록을 불러오지 못했습니다.")
			return
		}
		*pair.target = encoded
	}
	httpapi.WriteJSON(w, http.StatusOK, body)
}

// parseCurationListQuery accepts only before or after. The old view, sort,
// limit and cursor parameters are refused rather than ignored, so a caller
// written for the removed saved views learns the contract changed.
func parseCurationListQuery(r *http.Request) (curationapp.CurationListQuery, error) {
	var query curationapp.CurationListQuery
	for name, values := range r.URL.Query() {
		var target **curationapp.CurationListCursor
		switch name {
		case "before":
			target = &query.Before
		case "after":
			target = &query.After
		default:
			return curationapp.CurationListQuery{}, curationapp.ErrCurationListQueryInvalid
		}
		if len(values) != 1 {
			return curationapp.CurationListQuery{}, curationapp.ErrCurationListQueryInvalid
		}
		cursor, err := decodeCurationListCursor(values[0])
		if err != nil {
			return curationapp.CurationListQuery{}, curationapp.ErrCurationListQueryInvalid
		}
		*target = &cursor
	}
	return query, nil
}

func encodeCurationListCursor(cursor curationapp.CurationListCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCurationListCursor(encoded string) (curationapp.CurationListCursor, error) {
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return curationapp.CurationListCursor{}, err
	}
	var cursor curationapp.CurationListCursor
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return curationapp.CurationListCursor{}, err
	}
	if !curationapp.ValidCurationListCursor(cursor) {
		return curationapp.CurationListCursor{}, curationapp.ErrCurationListQueryInvalid
	}
	return cursor, nil
}

func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	result, err := h.service.Get(r.Context(), userID, r.PathValue("planId"))
	if err != nil {
		h.writePlanningError(w, r, userID, r.PathValue("planId"), err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, planResponse(result))
}

type createExpansionRequest struct {
	CurationID              string                            `json:"curationId"`
	CurationActionID        string                            `json:"curationActionId"`
	Type                    curationdomain.CurationActionType `json:"type"`
	Instruction             string                            `json:"instruction"`
	ExpectedCurationVersion int64                             `json:"expectedCurationVersion"`
}

func (h *Handler) CreateExpansion(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePlanningWebPrincipal(w, r)
	if !ok {
		return
	}
	userID := principal.UserID
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		httpapi.WriteError(
			w, http.StatusBadRequest, curationdomain.ErrIdempotencyKeyRequired.Error(),
			"항목 추가 요청에는 Idempotency-Key가 필요합니다.",
		)
		return
	}
	var request createExpansionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if request.CurationActionID != idempotencyKey {
		h.writePlanningError(
			w, r, userID, r.PathValue("planId"),
			curationdomain.ErrCurationActionInvalid,
		)
		return
	}
	result, err := h.service.ExecuteExpansionAction(
		r.Context(),
		curationapp.ExecuteExpansionActionInput{
			ActionID: request.CurationActionID,
			UserID:   userID, AuthSessionID: principal.AuthSessionID,
			CurationID:              request.CurationID,
			PlanID:                  r.PathValue("planId"),
			Type:                    request.Type,
			Instruction:             request.Instruction,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
		},
	)
	if err != nil {
		h.writePlanningError(w, r, userID, r.PathValue("planId"), err)
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, expansionResponse(result))
}

func (h *Handler) GetExpansion(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	run, err := h.service.GetExpansion(
		r.Context(), userID, r.PathValue("planId"), r.PathValue("runId"),
	)
	if err != nil {
		h.writePlanningError(w, r, userID, r.PathValue("planId"), err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *Handler) GetCurrentExpansion(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetCurrentExpansion(
		r.Context(), userID, r.PathValue("planId"),
	)
	if err != nil {
		h.writePlanningError(w, r, userID, r.PathValue("planId"), err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, expansionResponse(result))
}

func (h *Handler) writePlanningError(
	w http.ResponseWriter,
	r *http.Request,
	userID, planID string,
	err error,
) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "계획 요청을 처리하지 못했습니다.")
		return
	}
	code := curationapp.ReasonCode(err)
	status := http.StatusUnprocessableEntity
	message := "입력과 현재 상태를 확인해 주세요."
	switch {
	case errors.Is(err, curationdomain.ErrPlanNotFound),
		errors.Is(err, curationdomain.ErrTargetNotFound),
		errors.Is(err, curationdomain.ErrPlanningTaskNotFound),
		errors.Is(err, curationdomain.ErrCurationRunNotFound):
		status, message = http.StatusNotFound, "쇼핑 계획 또는 Planning task를 찾을 수 없습니다."
	case errors.Is(err, curationdomain.ErrVersionConflict),
		errors.Is(err, curationdomain.ErrPlanningContextStale),
		errors.Is(err, curationdomain.ErrPlanningTaskClosed),
		errors.Is(err, curationdomain.ErrCurationRunClosed),
		errors.Is(err, curationdomain.ErrExpansionUnavailable),
		errors.Is(err, curationdomain.ErrTargetRemovalBlocked),
		errors.Is(err, curationdomain.ErrExpansionInProgress),
		errors.Is(err, curationdomain.ErrCurationActionInProgress):
		status, message = http.StatusConflict, "계획이 변경되었거나 이미 처리되었습니다. 최신 내용을 다시 불러와 주세요."
	case errors.Is(err, curationdomain.ErrTooManyActiveActions):
		status, message = http.StatusConflict,
			"동시에 진행할 수 있는 요청 수를 넘었습니다. 진행 중인 요청이 끝나거나 취소된 뒤 다시 시도해 주세요."
	case errors.Is(err, curationdomain.ErrIdempotencyKeyRequired),
		errors.Is(err, curationdomain.ErrProposalIDInvalid),
		errors.Is(err, curationdomain.ErrProposalSchemaInvalid),
		errors.Is(err, curationdomain.ErrExpansionInstruction),
		errors.Is(err, curationdomain.ErrCurationActionInvalid),
		errors.Is(err, curationdomain.ErrCurationActionTypeInvalid),
		errors.Is(err, curationdomain.ErrCurationActionSubjectInvalid),
		errors.Is(err, curationdomain.ErrCurationActionBodyInvalid),
		errors.Is(err, curationdomain.ErrCurationActionVersionInvalid):
		status, message = http.StatusBadRequest, "요청 식별자 또는 스키마를 확인해 주세요."
	case errors.Is(err, curationdomain.ErrIdempotencyKeyReused):
		status, message = http.StatusConflict, "같은 식별자를 다른 요청에 사용할 수 없습니다."
	case errors.Is(err, curationdomain.ErrExternalAgentRetired):
		status, message = http.StatusConflict,
			"외부 Agent는 전환 준비 중입니다. ManagedAgent로 다시 시도해 주세요."
	case code == "INTERNAL_ERROR":
		status, message = http.StatusInternalServerError, "요청을 처리하지 못했습니다."
	}
	h.logger.WarnContext(r.Context(), "plan rejected",
		"event", "plan.rejected", "result", "rejected", "reason_code", code,
		"request_id", sharedapp.RequestID(r.Context()),
		"user_id", userID, "plan_id", planID)
	httpapi.WriteError(w, status, code, message)
}

func optionalMoneyInput(value *moneyRequest) *curationapp.MoneyInput {
	if value == nil {
		return nil
	}
	input := value.input()
	return &input
}

type sessionResponseBody struct {
	ID                     shoppingsessiondomain.ShoppingSessionID `json:"id"`
	PlanTargetID           shoppingsessiondomain.PlanTargetID      `json:"planTargetId"`
	UserID                 shoppingsessiondomain.UserID            `json:"userId"`
	TargetSnapshot         json.RawMessage                         `json:"targetSnapshot"`
	ResearchScopeSnapshot  json.RawMessage                         `json:"researchScopeSnapshot"`
	Status                 shoppingsessiondomain.SessionStatus     `json:"status"`
	CurrentResearchRoundID *string                                 `json:"currentResearchRoundId,omitempty"`
	Version                int64                                   `json:"version"`
	CreatedAt              time.Time                               `json:"createdAt"`
	UpdatedAt              time.Time                               `json:"updatedAt"`
}

func sessionResponse(session shoppingsessiondomain.ShoppingSession) sessionResponseBody {
	return sessionResponseBody{
		ID: session.ID, PlanTargetID: session.PlanTargetID, UserID: session.UserID,
		TargetSnapshot:         session.TargetSnapshot,
		ResearchScopeSnapshot:  session.ResearchScopeSnapshot,
		Status:                 session.Status,
		CurrentResearchRoundID: session.CurrentResearchRoundID,
		Version:                session.Version,
		CreatedAt:              session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

func planResponse(result curationapp.PlanResult) map[string]any {
	sessions := make([]sessionResponseBody, len(result.Sessions))
	for index, session := range result.Sessions {
		sessions[index] = sessionResponse(session)
	}
	response := map[string]any{
		"plan": result.Plan, "curation": result.Curation,
		"targets": result.Targets, "sessions": sessions,
		"availableActions": result.AvailableActions,
		"journey":          result.Journey,
	}
	if result.PlanningTask != nil {
		response["planningTask"] = result.PlanningTask
	}
	if result.IntelligenceJob != nil {
		response["intelligenceJob"] = result.IntelligenceJob
	}
	return response
}

func expansionResponse(result curationapp.ExpansionResult) map[string]any {
	response := map[string]any{
		"run":          result.Run,
		"planningTask": result.PlanningTask,
		"replay":       result.Replay,
	}
	if result.IntelligenceJob != nil {
		response["intelligenceJob"] = result.IntelligenceJob
	}
	return response
}

func requirePlanningWebPrincipal(
	w http.ResponseWriter,
	r *http.Request,
) (sharedapp.WebPrincipal, bool) {
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
		)
		return sharedapp.WebPrincipal{}, false
	}
	return principal, true
}

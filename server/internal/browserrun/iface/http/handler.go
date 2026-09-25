package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	browserapp "github.com/vitlane/vitlane/server/internal/browserrun/app"
	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service *browserapp.Service
}

func NewHandler(service *browserapp.Service) *Handler {
	return &Handler{service: service}
}

type createRequest struct{}

type commandRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type preparationApprovalRequest struct {
	ExpectedVersion         int64                           `json:"expectedVersion"`
	IdempotencyKey          string                          `json:"idempotencyKey"`
	PlanRevision            int64                           `json:"planRevision"`
	QuoteDigest             string                          `json:"quoteDigest"`
	AllowedPreparationSteps []browserdomain.PreparationStep `json:"allowedPreparationSteps"`
	PriceCeilingMinor       int64                           `json:"priceCeilingMinor"`
	PriceCurrency           string                          `json:"priceCurrency"`
}

type observationRequest struct {
	ExpectedVersion       int64                          `json:"expectedVersion"`
	ObservationRevision   int64                          `json:"observationRevision"`
	Kind                  browserdomain.ObservationKind  `json:"kind"`
	Origin                string                         `json:"origin"`
	PageIdentityDigest    string                         `json:"pageIdentityDigest"`
	SessionStateHint      browserdomain.SessionStateHint `json:"sessionStateHint,omitempty"`
	Sanitized             bool                           `json:"sanitized"`
	ContainsSensitiveData bool                           `json:"containsSensitiveData"`
}

type handoffRequest struct {
	ExpectedVersion int64                       `json:"expectedVersion"`
	Reason          browserdomain.HandoffReason `json:"reason"`
}

type resultVerificationRequest struct {
	ExpectedVersion     int64                              `json:"expectedVersion"`
	Outcome             browserdomain.ResultOutcome        `json:"outcome"`
	EvidenceSource      browserdomain.ResultEvidenceSource `json:"evidenceSource"`
	EvidenceDigest      string                             `json:"evidenceDigest,omitempty"`
	ObservationRevision int64                              `json:"observationRevision,omitempty"`
}

type response struct {
	SchemaVersion string            `json:"schemaVersion"`
	Run           browserdomain.Run `json:"run"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request createRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.Create(r.Context(), browserapp.CreateInput{
		UserID: userID, CurationID: r.PathValue("curationId"),
		CandidateID: r.PathValue("candidateId"), IdempotencyKey: key,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusCreated, result)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	run, err := h.service.Get(r.Context(), userID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, response{SchemaVersion: browserapp.SchemaVersion, Run: run})
}

func (h *Handler) ApproveNavigation(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, h.service.ApproveNavigation)
}

func (h *Handler) TakeOver(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, h.service.TakeOver)
}

func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, h.service.Pause)
}

func (h *Handler) RequestResume(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, h.service.RequestResume)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, h.service.Cancel)
}

func (h *Handler) command(
	w http.ResponseWriter,
	r *http.Request,
	execute func(context.Context, browserapp.CommandInput) (browserapp.Result, error),
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request commandRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := execute(r.Context(), browserapp.CommandInput{
		UserID: userID, RunID: r.PathValue("runId"),
		ExpectedVersion: request.ExpectedVersion, IdempotencyKey: key,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusOK, result)
}

func (h *Handler) ApprovePreparation(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request preparationApprovalRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || request.IdempotencyKey != key {
		httpapi.WriteError(w, http.StatusBadRequest, "BROWSER_RUN_IDEMPOTENCY_MISMATCH", "승인 요청을 확인해 주세요.")
		return
	}
	result, err := h.service.ApprovePreparation(r.Context(), browserapp.PreparationApprovalInput{
		CommandInput: browserapp.CommandInput{
			UserID: userID, RunID: r.PathValue("runId"),
			ExpectedVersion: request.ExpectedVersion, IdempotencyKey: key,
		},
		PlanRevision: request.PlanRevision, QuoteDigest: request.QuoteDigest,
		AllowedPreparationSteps: append([]browserdomain.PreparationStep(nil), request.AllowedPreparationSteps...),
		PriceCeilingMinor:       request.PriceCeilingMinor, PriceCurrency: request.PriceCurrency,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusOK, result)
}

func (h *Handler) RecordObservation(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request observationRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.RecordObservation(r.Context(), browserapp.ObservationInput{
		CommandInput: browserapp.CommandInput{
			UserID: userID, RunID: r.PathValue("runId"),
			ExpectedVersion: request.ExpectedVersion, IdempotencyKey: key,
		},
		ObservationRevision: request.ObservationRevision, Kind: request.Kind,
		Origin: request.Origin, PageIdentityDigest: request.PageIdentityDigest,
		SessionStateHint: request.SessionStateHint,
		Sanitized:        request.Sanitized, ContainsSensitiveData: request.ContainsSensitiveData,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusOK, result)
}

func (h *Handler) RequireHandoff(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request handoffRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.RequireHandoff(r.Context(), browserapp.HandoffInput{
		CommandInput: browserapp.CommandInput{
			UserID: userID, RunID: r.PathValue("runId"),
			ExpectedVersion: request.ExpectedVersion, IdempotencyKey: key,
		},
		Reason: request.Reason,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusOK, result)
}

func (h *Handler) VerifyResult(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request resultVerificationRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.VerifyResult(r.Context(), browserapp.ResultVerificationInput{
		CommandInput: browserapp.CommandInput{
			UserID: userID, RunID: r.PathValue("runId"),
			ExpectedVersion: request.ExpectedVersion, IdempotencyKey: key,
		},
		Outcome: request.Outcome, EvidenceSource: request.EvidenceSource,
		EvidenceDigest:      request.EvidenceDigest,
		ObservationRevision: request.ObservationRevision,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeResult(w, http.StatusOK, result)
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key가 필요합니다.")
		return "", false
	}
	return key, true
}

func writeResult(w http.ResponseWriter, status int, result browserapp.Result) {
	w.Header().Set("Cache-Control", "no-store")
	if result.Replay {
		w.Header().Set("X-Idempotent-Replay", "true")
	}
	httpapi.WriteJSON(w, status, response{SchemaVersion: browserapp.SchemaVersion, Run: result.Run})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "브라우저 작업을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, browserdomain.ErrRunNotFound), errors.Is(err, browserdomain.ErrCandidateNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "BROWSER_RUN_NOT_FOUND", "브라우저 작업을 찾지 못했습니다.")
	case errors.Is(err, browserdomain.ErrInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, "BROWSER_RUN_INVALID", "브라우저 작업 요청을 확인해 주세요.")
	case errors.Is(err, browserdomain.ErrInvalidTransition),
		errors.Is(err, browserdomain.ErrVersionConflict),
		errors.Is(err, browserdomain.ErrIdempotencyConflict):
		httpapi.WriteError(w, http.StatusConflict, "BROWSER_RUN_CONFLICT", "브라우저 작업 상태가 변경되었습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "브라우저 작업을 처리하지 못했습니다.")
	}
}

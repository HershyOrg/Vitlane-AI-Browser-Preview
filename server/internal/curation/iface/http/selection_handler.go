package http

import (
	"errors"
	"net/http"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type SelectionHandler struct {
	service *curationapp.SelectionService
}

func NewSelectionHandler(service *curationapp.SelectionService) *SelectionHandler {
	return &SelectionHandler{service: service}
}

func (h *SelectionHandler) GetCurationCart(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	view, err := h.service.GetCurationCart(
		r.Context(),
		userID,
		r.PathValue("curationId"),
	)
	if err != nil {
		writeSelectionError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, view)
}

type createSelectionRequest struct {
	ClientCommandID          string `json:"clientCommandId"`
	CandidateConfigurationID string `json:"candidateConfigurationId"`
	ExpectedCurationVersion  int64  `json:"expectedCurationVersion"`
	Quantity                 int64  `json:"quantity"`
}

func (h *SelectionHandler) CreateSelection(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request createSelectionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.CreateSelection(
		r.Context(),
		curationapp.CreateSelectionInput{
			UserID:                  userID,
			CurationID:              r.PathValue("curationId"),
			ConfigurationID:         request.CandidateConfigurationID,
			Quantity:                request.Quantity,
			ClientCommandID:         request.ClientCommandID,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
		},
	)
	if err != nil {
		writeSelectionError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

type updateSelectionRequest struct {
	ClientCommandID          string `json:"clientCommandId"`
	ExpectedVersion          int64  `json:"expectedVersion"`
	ExpectedCurationVersion  int64  `json:"expectedCurationVersion"`
	CandidateConfigurationID string `json:"candidateConfigurationId"`
	Quantity                 int64  `json:"quantity"`
}

func (h *SelectionHandler) UpdateSelection(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request updateSelectionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.UpdateSelection(
		r.Context(),
		curationapp.UpdateSelectionInput{
			UserID:                  userID,
			CurationID:              r.PathValue("curationId"),
			SelectionID:             r.PathValue("selectionId"),
			ExpectedVersion:         request.ExpectedVersion,
			Quantity:                request.Quantity,
			ConfigurationID:         request.CandidateConfigurationID,
			ClientCommandID:         request.ClientCommandID,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
		},
	)
	if err != nil {
		writeSelectionError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

type removeSelectionRequest struct {
	ClientCommandID         string `json:"clientCommandId"`
	ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
	ExpectedVersion         int64  `json:"expectedVersion"`
}

func (h *SelectionHandler) RemoveSelection(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request removeSelectionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.RemoveSelection(
		r.Context(),
		curationapp.RemoveSelectionInput{
			UserID:                  userID,
			CurationID:              r.PathValue("curationId"),
			SelectionID:             r.PathValue("selectionId"),
			ExpectedVersion:         request.ExpectedVersion,
			ClientCommandID:         request.ClientCommandID,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
		},
	)
	if err != nil {
		writeSelectionError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func writeSelectionError(w http.ResponseWriter, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, nil, err, "선택을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, curationdomain.ErrCurationNotFound),
		errors.Is(err, curationdomain.ErrSelectionNotFound):
		httpapi.WriteError(
			w,
			http.StatusNotFound,
			err.Error(),
			"큐레이션 또는 선택을 찾을 수 없습니다.",
		)
	case errors.Is(err, curationdomain.ErrSelectionVersionConflict),
		errors.Is(err, curationdomain.ErrSelectionCommandConflict),
		errors.Is(err, curationdomain.ErrVersionConflict),
		errors.Is(err, curationdomain.ErrIdempotencyKeyReused),
		errors.Is(err, curationdomain.ErrCurationActionUnavailable),
		errors.Is(err, curationdomain.ErrCurationActionInProgress),
		errors.Is(err, curationdomain.ErrCurationArchived):
		httpapi.WriteError(
			w,
			http.StatusConflict,
			err.Error(),
			"선택이 변경되었거나 요청 식별자가 충돌했습니다.",
		)
	case errors.Is(err, curationdomain.ErrSelectionInvalid),
		errors.Is(err, researchdomain.ErrCandidateInvalid),
		errors.Is(err, researchdomain.ErrConfigurationInvalid),
		errors.Is(err, researchdomain.ErrConfigurationNotFound):
		httpapi.WriteError(
			w,
			http.StatusUnprocessableEntity,
			err.Error(),
			"선택할 상품, 옵션과 수량을 확인해 주세요.",
		)
	default:
		httpapi.WriteError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"선택을 처리하지 못했습니다.",
		)
	}
}

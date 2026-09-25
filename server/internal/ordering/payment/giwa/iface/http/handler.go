package http

import (
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"net/http"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service *settlementapp.Service
}

func NewHandler(service *settlementapp.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Config(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"settlement": h.service.Config()})
}

type authorizeAgencyOrderRequest struct {
	WalletID         string `json:"walletId"`
	OwnershipProofID string `json:"ownershipProofId"`
}

func (h *Handler) AuthorizeAgencyOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request authorizeAgencyOrderRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	record, err := h.service.AuthorizeAgencyOrder(
		r.Context(), userID, r.PathValue("agencyOrderId"),
		request.WalletID, request.OwnershipProofID,
	)
	if errors.Is(err, procmsg.ErrInstructionPending) {
		httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"schemaVersion": "vitlane.payment-instruction-confirmation.v1", "outcome": "WAITING", "reasonCode": procmsg.ErrInstructionPending.Error()})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, record)
}

type submitTransactionRequest struct {
	TxHash string `json:"txHash"`
}

type submitWalletTransactionRequest struct {
	Purpose string `json:"purpose"`
	TxHash  string `json:"txHash"`
}

func (h *Handler) SubmitAgencyOrderWalletTransaction(w http.ResponseWriter, r *http.Request) {
	h.submitWalletTransaction(w, r, r.PathValue("agencyOrderId"))
}

func (h *Handler) submitWalletTransaction(w http.ResponseWriter, r *http.Request, agencyOrderID string) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request submitWalletTransactionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if err := h.service.RecordWalletTransaction(
		r.Context(), userID, agencyOrderID, request.Purpose, request.TxHash,
	); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SubmitAgencyOrderPayTransaction(w http.ResponseWriter, r *http.Request) {
	h.submitPayTransaction(w, r, r.PathValue("agencyOrderId"))
}

func (h *Handler) submitPayTransaction(w http.ResponseWriter, r *http.Request, agencyOrderID string) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request submitTransactionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if err := h.service.RecordPayTransaction(
		r.Context(), userID, agencyOrderID, request.TxHash,
	); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateAgencyOrderRefundIntent(w http.ResponseWriter, r *http.Request) {
	h.createRefundIntent(w, r, r.PathValue("agencyOrderId"))
}

func (h *Handler) createRefundIntent(w http.ResponseWriter, r *http.Request, agencyOrderID string) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	intent, err := h.service.CreateRefundIntent(r.Context(), userID, agencyOrderID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"refundIntent": intent})
}

func (h *Handler) SubmitAgencyOrderRefundTransaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request submitTransactionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if err := h.service.RecordRefundTransaction(
		r.Context(), userID, r.PathValue("agencyOrderId"), request.TxHash,
	); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetAgencyOrderPayment(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	agencyOrderID := r.PathValue("agencyOrderId")
	payment, err := h.service.GetPayment(r.Context(), userID, agencyOrderID)
	if err != nil {
		writeError(w, err)
		return
	}
	authorization, err := h.service.GetAuthorization(r.Context(), userID, agencyOrderID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"payment":       payment,
		"authorization": authorization,
	})
}

func writeError(w http.ResponseWriter, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, nil, err, "테스트 정산을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, settlementdomain.ErrPaymentNotFound):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "Settlement를 찾을 수 없습니다.")
	case errors.Is(err, settlementdomain.ErrAuthorizationInstructionStale):
		httpapi.WriteError(
			w,
			http.StatusConflict,
			err.Error(),
			"결제 설정이 변경되었습니다. 최신 결제 조건을 다시 확인해 주세요.",
		)
	case errors.Is(err, settlementdomain.ErrAuthorizationInvalid),
		errors.Is(err, settlementdomain.ErrWalletOwnershipRequired),
		errors.Is(err, settlementdomain.ErrAuthorizationExpired),
		errors.Is(err, settlementdomain.ErrPaymentStateInvalid),
		errors.Is(err, settlementdomain.ErrTransactionInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "테스트 정산 요청을 확인해 주세요.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "테스트 정산을 처리하지 못했습니다.")
	}
}

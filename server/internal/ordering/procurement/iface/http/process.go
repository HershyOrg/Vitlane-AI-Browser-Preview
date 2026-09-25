package http

import (
	"net/http"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func (h *Handler) submitAction(w http.ResponseWriter, r *http.Request, actor, role string, kind procmsg.RequestKind, order, mo, task, reference string, input any) {
	h.stageAndSubmit(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: kind, AgencyOrderID: order, MerchantOrderID: mo, TaskID: task, ReferenceID: reference, ActorID: actor, ActorRole: role}, input)
}
func (h *Handler) submitCancellation(w http.ResponseWriter, r *http.Request, actor, mo, kind string) {
	h.stageAndSubmit(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestCancel, CancelKind: kind, AgencyOrderID: r.PathValue("agencyOrderId"), MerchantOrderID: mo, ActorID: actor, ActorRole: "CUSTOMER"}, nil)
}
func (h *Handler) stageAndSubmit(w http.ResponseWriter, r *http.Request, request procmsg.ActionRequest, input any) {
	if h.processor == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "ORDER_PROCESS_UNAVAILABLE", "주문 처리를 사용할 수 없습니다.")
		return
	}
	request, err := h.service.StageAction(r.Context(), request, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.processor.Submit(r.Context(), request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusAccepted, result)
}

func (h *Handler) RevealResult(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.processor == nil {
		httpapi.WriteError(w, 503, "ORDER_PROCESS_UNAVAILABLE", "주문 처리를 사용할 수 없습니다.")
		return
	}
	subject, err := h.service.PurchaseSubject(r.Context(), r.PathValue("taskId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	receipt, err := h.processor.Receipt(r.Context(), subject.MerchantOrder.AgencyOrderID, r.PathValue("requestId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if receipt.Outcome != "COMPLETED" {
		httpapi.WriteJSON(w, 409, receipt)
		return
	}
	value, err := h.service.RevealResult(r.Context(), r.PathValue("taskId"), actor, r.PathValue("requestId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, value)
}

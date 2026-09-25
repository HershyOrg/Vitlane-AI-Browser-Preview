// Package http는 Logistics 운영자 work surface다(계약 v7 §9): shipment 등록,
// tracking evidence 입력, 수령 일괄 확인(+예외 분기). 자동 carrier 연동은
// Phase 8 비범위이므로 모든 입력이 운영자 evidence다.
package http

import (
	"errors"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"net/http"
	"time"

	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	processor procmsg.RequestSubmitter
	service   *logisticsapp.Service
}

func NewHandler(service *logisticsapp.Service) *Handler {
	return &Handler{service: service}
}

type createShipmentRequest struct {
	MerchantOrderID string `json:"merchantOrderId"`
	Carrier         string `json:"carrier"`
	TrackingRef     string `json:"trackingRef"`
	// 비우면 해당 merchant order의 미배정 기대 unit 전부가 배정된다.
	ExpectedUnitIDs []string `json:"expectedUnitIds"`
}

func (h *Handler) CreateShipment(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request createShipmentRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestCreateShipment, MerchantOrderID: request.MerchantOrderID, ReferenceID: "", ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

type shipmentEventRequest struct {
	Status     string    `json:"status"`
	Note       string    `json:"note"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (h *Handler) RecordEvent(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request shipmentEventRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestShipmentEvent, MerchantOrderID: "", ReferenceID: r.PathValue("shipmentId"), ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

type deliveredRequest struct {
	// expectedUnitId → MISSING | WRONG_ACTUAL. 나머지 배정 unit은 일괄
	// DELIVERED_EXPECTED로 확인된다.
	Exceptions map[string]string `json:"exceptions"`
}

func (h *Handler) ConfirmDelivered(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request deliveredRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestConfirmDelivery, MerchantOrderID: "", ReferenceID: r.PathValue("shipmentId"), ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

func (h *Handler) ListOrderShipments(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	views, err := h.service.ListOrderShipments(r.Context(), r.PathValue("agencyOrderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.logistics-shipments.v1", "shipments": views,
	})
}

func (h *Handler) ListExceptionUnits(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	units, err := h.service.ListExceptionUnits(r.Context(), 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.logistics-exceptions.v1", "units": units,
	})
}

type resolutionRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

// ResolveException은 배송 예외의 write-once 판정이다(§9.1). REFUND는 Payment의
// MERCHANT_FAULT GROSS 환불로 이어지고, DELIVERED_OK는 오탐 정정이다.
func (h *Handler) ResolveException(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request resolutionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestResolveDelivery, MerchantOrderID: "", ReferenceID: r.PathValue("expectedUnitId"), ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

type returnRequest struct {
	ExpectedUnitID      string `json:"expectedUnitId"`
	State               string `json:"state"`
	MerchantDisposition string `json:"merchantDisposition"`
	Note                string `json:"note"`
}

func (h *Handler) CreateReturn(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request returnRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestCreateReturn, MerchantOrderID: "", ReferenceID: request.ExpectedUnitID, ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

func (h *Handler) ListReturns(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	items, err := h.service.ListReturns(r.Context(), r.URL.Query().Get("all") != "true", 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.logistics-returns.v1", "returns": items,
	})
}

func (h *Handler) UpdateReturn(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request returnRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestUpdateReturn, MerchantOrderID: "", ReferenceID: r.PathValue("returnId"), ActorID: operatorID, ActorRole: "OPERATOR"}, request)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "배송 요청을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, procmsg.ErrRequestInvalid):
		httpapi.WriteError(w, 400, err.Error(), "요청 값과 Idempotency-Key를 확인해 주세요.")
	case errors.Is(err, processdomain.ErrRequestConflict):
		httpapi.WriteError(w, 409, err.Error(), "같은 요청 키의 입력을 확인해 주세요.")
	case errors.Is(err, procmsg.ErrRequestNotFound):
		httpapi.WriteError(w, 404, err.Error(), "저장된 주문 요청을 찾을 수 없습니다.")
	case errors.Is(err, domain.ErrShipmentNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "LOGISTICS_SHIPMENT_NOT_FOUND", "배송 항목을 찾지 못했습니다.")
	case errors.Is(err, domain.ErrShipmentInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "LOGISTICS_SHIPMENT_INVALID", "배송 입력이 올바르지 않습니다.")
	case errors.Is(err, domain.ErrUnitsNotAllocable):
		httpapi.WriteError(w, http.StatusConflict, "LOGISTICS_UNITS_NOT_ALLOCABLE",
			"배정 가능한 물품 단위가 없습니다(LIVE 주문 완료 후, 미배정 단위만 가능).")
	case errors.Is(err, domain.ErrTransitionInvalid):
		httpapi.WriteError(w, http.StatusConflict, "LOGISTICS_TRANSITION_INVALID", "현재 배송 상태에서 허용되지 않는 변경입니다.")
	case errors.Is(err, domain.ErrEvidenceDuplicate):
		httpapi.WriteError(w, http.StatusConflict, "LOGISTICS_EVIDENCE_DUPLICATE", "같은 운송장이 이미 등록되어 있습니다.")
	case errors.Is(err, domain.ErrUnitNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "LOGISTICS_UNIT_NOT_FOUND", "배송 단위를 찾지 못했습니다.")
	case errors.Is(err, domain.ErrResolutionInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "LOGISTICS_RESOLUTION_INVALID",
			"판정할 수 없는 상태입니다(MISSING/WRONG_ACTUAL/LOST 단위만, 판정은 한 번).")
	case errors.Is(err, domain.ErrReturnNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "LOGISTICS_RETURN_NOT_FOUND", "회수 항목을 찾지 못했습니다.")
	case errors.Is(err, domain.ErrReturnInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "LOGISTICS_RETURN_INVALID",
			"허용되지 않는 회수 상태 변경입니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "LOGISTICS_INTERNAL", "요청을 처리하지 못했습니다.")
	}
}

func (h *Handler) EnableProcessor(p procmsg.RequestSubmitter) { h.processor = p }
func (h *Handler) submitAction(w http.ResponseWriter, r *http.Request, p procmsg.ActionRequest, input any) {
	if h.processor == nil {
		httpapi.WriteError(w, 503, "ORDER_PROCESS_UNAVAILABLE", "주문 처리를 사용할 수 없습니다.")
		return
	}
	p, err := h.service.StageAction(r.Context(), p, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.processor.Submit(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 202, result)
}

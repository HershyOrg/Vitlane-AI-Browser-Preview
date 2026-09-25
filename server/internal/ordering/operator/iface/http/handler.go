// Package http는 운영자 work surface의 목록 창구다(ADR-0055 §5). 명령
// endpoint는 각 owner 제품에 그대로 있다 — 예외는 소진 Effect 개입
// (RETRY·안전한 비금전 ABANDON)으로, owner가 ordering/process 자신이라 여기 함께 얹는다
// (ADR-0056 §3).
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	operatorapp "github.com/vitlane/vitlane/server/internal/ordering/operator/app"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type ProcessIntervention interface {
	RetryEffect(context.Context, string, string, string) (procmsg.RequestReceipt, error)
	Receipt(context.Context, string, string) (procmsg.RequestReceipt, error)
	Timeline(context.Context, string) (processapp.OrderTimeline, error)
}

type Handler struct {
	service      *operatorapp.Service
	intervention ProcessIntervention
}

func NewHandler(service *operatorapp.Service) *Handler {
	return &Handler{service: service}
}

// EnableProcessIntervention은 소진 Effect 개입 명령을 연다. 자금 보상
// Effect는 RETRY만 허용한다.
func (h *Handler) EnableProcessIntervention(intervention ProcessIntervention) {
	h.intervention = intervention
}

// RetryProcessEffect keeps the immutable effect/provider identity.
func (h *Handler) RetryProcessEffect(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.intervention == nil {
		httpapi.WriteError(w, 503, "ORDER_PROCESS_UNAVAILABLE", "주문 처리를 사용할 수 없습니다.")
		return
	}
	result, err := h.intervention.RetryEffect(r.Context(), r.PathValue("effectId"), actor, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeInterventionError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 202, result)
}
func (h *Handler) ProcessRequestReceipt(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	if h.intervention == nil {
		httpapi.WriteError(w, 503, "ORDER_PROCESS_UNAVAILABLE", "주문 처리를 사용할 수 없습니다.")
		return
	}
	result, err := h.intervention.Receipt(r.Context(), r.PathValue("agencyOrderId"), r.PathValue("requestId"))
	if err != nil {
		writeInterventionError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, result)
}

func (h *Handler) GetOrderTimeline(w http.ResponseWriter, r *http.Request) {
	operatorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.intervention == nil {
		httpapi.WriteError(w, http.StatusNotFound,
			"ORDER_PROCESS_INTERVENTION_DISABLED", "process 개입 창구가 비활성입니다.")
		return
	}
	agencyOrderID := r.PathValue("agencyOrderId")
	if err := h.service.AuditOrderDetailView(r.Context(), agencyOrderID, operatorUserID); err != nil {
		writeLookupError(w, r, err)
		return
	}
	timeline, err := h.intervention.Timeline(r.Context(), agencyOrderID)
	if err != nil {
		writeInterventionError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.ordering-operator-order-timeline.v3",
		"timeline":      timeline,
	})
}

func writeInterventionError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "process 개입을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, procmsg.ErrRequestNotFound):
		httpapi.WriteError(w, 404, err.Error(), "저장된 주문 요청을 찾을 수 없습니다.")
	case errors.Is(err, procmsg.ErrRequestInvalid):
		httpapi.WriteError(w, 400, err.Error(), "요청 값과 Idempotency-Key를 확인해 주세요.")
	case errors.Is(err, processapp.ErrInterventionNotFound):
		httpapi.WriteError(w, http.StatusNotFound,
			"ORDER_PROCESS_INTERVENTION_NOT_FOUND", "대상 Effect를 찾을 수 없습니다.")
	case errors.Is(err, processapp.ErrTimelineNotFound):
		httpapi.WriteError(w, http.StatusNotFound,
			"ORDER_PROCESS_TIMELINE_NOT_FOUND", "이 주문의 process 이력이 없습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError,
			"ORDER_PROCESS_INTERVENTION_FAILED", "process 개입을 처리하지 못했습니다.")
	}
}

// ListWorkItemCounts는 kind별 전역 카운트다(ADR-0057 2차 P2 — nav 뱃지).
func (h *Handler) ListWorkItemCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := h.service.Counts(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError,
			"ORDERING_OPERATOR_WORK_COUNTS_UNAVAILABLE", "운영 작업 카운트를 불러오지 못했습니다.")
		return
	}
	livePayPalOrderCount, err := h.service.CountLivePayPalOrders(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError,
			"ORDERING_OPERATOR_LIVE_PAYPAL_COUNT_UNAVAILABLE", "Live PayPal 주문 카운트를 불러오지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion":        "vitlane.ordering-operator-work-item-counts.v2",
		"counts":               counts,
		"livePayPalOrderCount": livePayPalOrderCount,
	})
}

func (h *Handler) ListWorkItems(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	view := operatorapp.WorkViewOpen
	switch strings.ToUpper(r.URL.Query().Get("view")) {
	case "", string(operatorapp.WorkViewOpen):
	case string(operatorapp.WorkViewResolved):
		view = operatorapp.WorkViewResolved
	default:
		httpapi.WriteError(w, http.StatusBadRequest,
			"ORDERING_OPERATOR_WORK_VIEW_INVALID", "view는 OPEN 또는 RESOLVED입니다.")
		return
	}
	surface, err := h.service.List(r.Context(), view, limit)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError,
			"ORDERING_OPERATOR_WORK_ITEMS_UNAVAILABLE", "운영 작업 목록을 불러오지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.ordering-operator-work-items.v1",
		"items":         surface.Items,
		"counts":        surface.Counts,
	})
}

// LookupOrder는 운영자 exact lookup이다. 원문 identifier가 access log나
// browser history에 남지 않도록 POST body로만 받는다.
func (h *Handler) LookupOrder(w http.ResponseWriter, r *http.Request) {
	operatorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input operatorapp.OrderLookupInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest,
			"ORDERING_OPERATOR_LOOKUP_INVALID", "조회 조건이 올바르지 않습니다.")
		return
	}
	match, err := h.service.LookupOrder(r.Context(), input, operatorUserID)
	if err != nil {
		writeLookupError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.ordering-operator-order-lookup.v1",
		"match":         match,
	})
}

// GetOrderInvestigation은 canonical AgencyOrder ID 기준의 PII-safe 조사 사영이다.
func (h *Handler) GetOrderInvestigation(w http.ResponseWriter, r *http.Request) {
	operatorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	investigation, err := h.service.InvestigateOrder(
		r.Context(), r.PathValue("agencyOrderId"), operatorUserID,
	)
	if err != nil {
		writeLookupError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.ordering-operator-order-investigation.v1",
		"investigation": investigation,
	})
}

func writeLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "주문 조회를 완료하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, operatorapp.ErrOrderLookupInvalid):
		httpapi.WriteError(w, http.StatusBadRequest,
			"ORDERING_OPERATOR_LOOKUP_INVALID", "조회 조건이 올바르지 않습니다.")
	case errors.Is(err, operatorapp.ErrOrderLookupNotFound):
		httpapi.WriteError(w, http.StatusNotFound,
			"ORDERING_OPERATOR_LOOKUP_NOT_FOUND", "일치하는 주문을 찾지 못했습니다.")
	case errors.Is(err, operatorapp.ErrOrderLookupAmbiguous):
		httpapi.WriteError(w, http.StatusConflict,
			"ORDERING_OPERATOR_LOOKUP_AMBIGUOUS", "둘 이상의 주문이 일치합니다. 식별자 종류나 한정자를 추가하세요.")
	case errors.Is(err, operatorapp.ErrOrderLookupDisabled):
		httpapi.WriteError(w, http.StatusNotFound,
			"ORDERING_OPERATOR_LOOKUP_DISABLED", "주문 조회 창구가 비활성입니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError,
			"ORDERING_OPERATOR_LOOKUP_UNAVAILABLE", "주문 조회를 완료하지 못했습니다.")
	}
}

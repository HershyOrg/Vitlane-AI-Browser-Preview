package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	processor            procmsg.RequestSubmitter
	service              *agencyapp.Service
	lifecycle            *agencyapp.LifecycleService
	trustedProxies       []netip.Prefix
	paypalSandboxEnabled bool
	paypalLiveEnabled    bool
}

func NewHandler(service *agencyapp.Service, trustedProxies []netip.Prefix) *Handler {
	return &Handler{service: service, trustedProxies: append([]netip.Prefix(nil), trustedProxies...)}
}

func (h *Handler) EnableLifecycle(service *agencyapp.LifecycleService) {
	h.lifecycle = service
}

// EnablePayPalSandbox는 PAYPAL_SANDBOX 결제수단 발행을 연다(설정 게이트).
func (h *Handler) EnablePayPalSandbox() {
	h.paypalSandboxEnabled = true
	h.service.EnablePayPalSandboxIssue()
}

// EnablePayPalLive는 새 AgencyOrder에 PAYPAL/LIVE execution profile을 발행한다.
// 시작 설정의 독립 issue gate가 열린 경우에만 wiring에서 호출한다.
func (h *Handler) EnablePayPalLive() {
	h.paypalLiveEnabled = true
	h.service.EnablePayPalLiveIssue()
}

type createOrderSheetRequest struct {
	ExpectedCartVersion int64  `json:"expectedCartVersion"`
	IdempotencyKey      string `json:"idempotencyKey"`
}

func (h *Handler) CreateOrderSheet(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request createOrderSheetRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	session, err := h.service.CreateOrderSheet(r.Context(), agencyapp.CreateOrderSheetInput{
		UserID: userID, CurationID: r.PathValue("curationId"),
		ExpectedCartVersion: request.ExpectedCartVersion, IdempotencyKey: request.IdempotencyKey,
		BuyerIP: h.buyerIP(r),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writeOrderSheet(w, r, http.StatusCreated, userID, session)
}

func (h *Handler) GetOrderSheet(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	session, err := h.service.GetSession(r.Context(), userID, r.PathValue("orderSheetId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writeOrderSheet(w, r, http.StatusOK, userID, session)
}

type shippingAddressRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	RecipientName   string `json:"recipientName"`
	AddressLine1    string `json:"addressLine1"`
	AddressLine2    string `json:"addressLine2"`
	City            string `json:"city"`
	Region          string `json:"region"`
	PostalCode      string `json:"postalCode"`
	Country         string `json:"country"`
	Phone           string `json:"phone"`
}

func (h *Handler) SetShippingAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request shippingAddressRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	session, err := h.service.SetShippingAddress(r.Context(), agencyapp.SetShippingAddressInput{
		UserID: userID, SessionID: r.PathValue("orderSheetId"),
		ExpectedVersion: request.ExpectedVersion, BuyerIP: h.buyerIP(r),
		Address: agencyapp.ShippingAddress{
			RecipientName: request.RecipientName, AddressLine1: request.AddressLine1,
			AddressLine2: request.AddressLine2, City: request.City, Region: request.Region,
			PostalCode: request.PostalCode, Country: request.Country, Phone: request.Phone,
		},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writeOrderSheet(w, r, http.StatusOK, userID, session)
}

type orderSheetView struct {
	agencydomain.OrderSheetSession
	ShippingAddressInput  agencyapp.ShippingAddress `json:"shippingAddressInput"`
	ShippingAddressSource string                    `json:"shippingAddressSource"`
}

func (h *Handler) writeOrderSheet(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	userID string,
	session agencydomain.OrderSheetSession,
) {
	if session.MerchantCheckouts == nil {
		session.MerchantCheckouts = make([]agencydomain.MerchantCheckout, 0)
	}
	draft, err := h.service.ShippingDraft(r.Context(), userID, session)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	httpapi.WriteJSON(w, status, map[string]any{
		"schemaVersion": "vitlane.order-sheet.v1",
		"orderSheet": orderSheetView{
			OrderSheetSession: session, ShippingAddressInput: draft.Address,
			ShippingAddressSource: draft.Source,
		},
	})
}

type preflightRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	PaymentMethod   string `json:"paymentMethod"`
}

type deliverySelectionRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
	Selections      []struct {
		ShopDomain string `json:"shopDomain"`
		GroupID    string `json:"groupId"`
		OptionID   string `json:"optionId"`
	} `json:"selections"`
}

func (h *Handler) SelectDelivery(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request deliverySelectionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	selections := make([]agencyapp.MerchantDeliverySelection, 0, len(request.Selections))
	for _, selection := range request.Selections {
		selections = append(selections, agencyapp.MerchantDeliverySelection{
			ShopDomain: selection.ShopDomain, GroupID: selection.GroupID, OptionID: selection.OptionID,
		})
	}
	session, err := h.service.SelectDelivery(r.Context(), agencyapp.SelectDeliveryInput{
		UserID: userID, SessionID: r.PathValue("orderSheetId"),
		ExpectedVersion: request.ExpectedVersion, Selections: selections, BuyerIP: h.buyerIP(r),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writeOrderSheet(w, r, http.StatusOK, userID, session)
}

// preflightBudget은 병렬화 뒤에도 가장 느린 Shop의 create/update/get/get 왕복을
// 감당해야 하는 preflight 전용 예산이다. 전역 interactive 요청 예산(기본 10s)과
// 서버 WriteTimeout(15s)은 provider 왕복을 중간에 끊어 외부 효과를
// EXTERNAL_EFFECT_UNKNOWN으로 남기므로 이 라우트만 분리한다.
const preflightBudget = 30 * time.Second

func extendProviderBudget(
	w http.ResponseWriter, r *http.Request, budget time.Duration,
) (context.Context, context.CancelFunc) {
	// 클라이언트가 기다리다 끊어도 이미 전송된 provider 호출은 완주해 세션에
	// 결과를 남기는 편이 안전하므로 요청 취소에서 분리한다. 응답 write
	// deadline도 함께 늘려 성공 응답이 WriteTimeout에 잘리지 않게 한다.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget + 5*time.Second))
	return context.WithTimeout(context.WithoutCancel(r.Context()), budget)
}

func (h *Handler) Preflight(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request preflightRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	var rail agencydomain.PaymentRail
	switch request.PaymentMethod {
	case "", "TVITUSD":
		rail = agencydomain.PaymentRailTVITUSD
	case "PAYPAL_SANDBOX":
		if !h.paypalSandboxEnabled {
			httpapi.WriteError(w, http.StatusUnprocessableEntity, "PAYPAL_RAIL_UNAVAILABLE",
				"PayPal 결제가 아직 활성화되지 않았습니다. tVITUSD를 선택해 주세요.")
			return
		}
		rail = agencydomain.PaymentRailPayPalSandbox
	case "PAYPAL_LIVE":
		if !h.paypalLiveEnabled {
			httpapi.WriteError(w, http.StatusUnprocessableEntity, "PAYPAL_RAIL_UNAVAILABLE",
				"PayPal 결제가 아직 활성화되지 않았습니다. tVITUSD를 선택해 주세요.")
			return
		}
		allowed, _, err := h.service.LiveIssueStatus(r.Context())
		if err != nil || !allowed {
			httpapi.WriteError(w, http.StatusServiceUnavailable, "PAYPAL_LIVE_KILLED",
				"PayPal Live 신규 주문이 현재 일시 중단되었습니다.")
			return
		}
		rail = agencydomain.PaymentRailPayPalLive
	default:
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PAYMENT_METHOD_INVALID",
			"지원하지 않는 결제수단입니다.")
		return
	}
	preflightContext, cancelPreflight := extendProviderBudget(w, r, preflightBudget)
	defer cancelPreflight()
	session, err := h.service.Preflight(preflightContext, agencyapp.PreflightInput{
		UserID: userID, SessionID: r.PathValue("orderSheetId"),
		ExpectedVersion: request.ExpectedVersion, PaymentRail: rail, BuyerIP: h.buyerIP(r),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writeOrderSheet(w, r, http.StatusOK, userID, session)
}

type issueRequest struct {
	ExpectedVersion            int64                            `json:"expectedVersion"`
	ExpectedCapabilityRevision int64                            `json:"expectedCapabilityRevision"`
	DisplayedSnapshotHash      string                           `json:"displayedSnapshotHash"`
	IdempotencyKey             string                           `json:"idempotencyKey"`
	ProcurementApproval        agencydomain.ProcurementApproval `json:"procurementApproval"`
}

func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request issueRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	order, instruction, err := h.service.Issue(r.Context(), agencyapp.IssueOrderInput{
		UserID: userID, SessionID: r.PathValue("orderSheetId"), ExpectedVersion: request.ExpectedVersion,
		ExpectedCapabilityRevision: request.ExpectedCapabilityRevision,
		DisplayedSnapshotHash:      request.DisplayedSnapshotHash, IdempotencyKey: request.IdempotencyKey,
		ProcurementApproval: request.ProcurementApproval,
		BuyerIP:             h.buyerIP(r),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"schemaVersion": "vitlane.agency-order.v1", "agencyOrder": order, "paymentInstruction": instruction,
	})
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle != nil {
		projection, err := h.lifecycle.Get(r.Context(), userID, r.PathValue("agencyOrderId"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"schemaVersion": "vitlane.agency-order-projection.v1", "agencyOrder": projection,
		})
		return
	}
	order, instruction, err := h.service.GetOrder(r.Context(), userID, r.PathValue("agencyOrderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.agency-order.v1", "agencyOrder": order, "paymentInstruction": instruction,
	})
}

func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "AGENCY_ORDER_LIFECYCLE_UNAVAILABLE", "AgencyOrder 조회가 아직 준비되지 않았습니다.")
		return
	}
	query, err := parseListQuery(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, agencyapp.ErrListQueryInvalid.Error(), "AgencyOrder 필터 또는 정렬 조건이 올바르지 않습니다.")
		return
	}
	page, err := h.lifecycle.List(r.Context(), userID, query)
	if err != nil {
		writeError(w, r, err)
		return
	}
	nextCursor := ""
	if page.NextCursor != nil {
		nextCursor, err = encodeListCursor(*page.NextCursor)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.agency-order-list.v1", "agencyOrders": page.Items,
		"countsByView": page.Counts, "nextCursor": nextCursor,
	})
}

func parseListQuery(r *http.Request) (agencyapp.ListQuery, error) {
	values := r.URL.Query()
	query := agencyapp.ListQuery{
		View: agencyapp.ListView(values.Get("view")),
		Sort: agencyapp.ListSort(values.Get("sort")),
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return agencyapp.ListQuery{}, agencyapp.ErrListQueryInvalid
		}
		query.Limit = limit
	}
	if rawCursor := values.Get("cursor"); rawCursor != "" {
		cursor, err := decodeListCursor(rawCursor)
		if err != nil {
			return agencyapp.ListQuery{}, agencyapp.ErrListQueryInvalid
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func encodeListCursor(cursor agencyapp.ListCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeListCursor(encoded string) (agencyapp.ListCursor, error) {
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return agencyapp.ListCursor{}, err
	}
	var cursor agencyapp.ListCursor
	if err := json.Unmarshal(value, &cursor); err != nil {
		return agencyapp.ListCursor{}, err
	}
	if cursor.AgencyOrderID == "" || cursor.IssuedAt.IsZero() || cursor.View == "" || cursor.Sort == "" {
		return agencyapp.ListCursor{}, agencyapp.ErrListQueryInvalid
	}
	return cursor, nil
}

func (h *Handler) RevealOrderShipping(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		writeLifecycleUnavailable(w)
		return
	}
	address, err := h.lifecycle.RevealOwnerShipping(
		r.Context(), userID, r.PathValue("agencyOrderId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"address": address})
}

func (h *Handler) GetReceipt(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		writeLifecycleUnavailable(w)
		return
	}
	receipt, err := h.lifecycle.GetReceipt(r.Context(), userID, r.PathValue("agencyOrderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"receipt": receipt})
}

// 종전 고지함 창구(EnableNotices·ListNotices·MarkNoticeRead·
// PublishOperatorNotice)는 ADR-0059로 Support 대화에 흡수되어 제거됐다.
// SYSTEM 지연 rule 고지는 projection.notices로 계속 내려간다(§2.6 잔여).

func writeLifecycleUnavailable(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusServiceUnavailable, "AGENCY_ORDER_LIFECYCLE_UNAVAILABLE", "AgencyOrder 처리가 아직 준비되지 않았습니다.")
}

func (h *Handler) buyerIP(r *http.Request) net.IP {
	address, ok := httpapi.ResolveClientAddress(r, h.trustedProxies)
	if !ok {
		return nil
	}
	return net.IP(address.AsSlice())
}

type refundRequestBody struct {
	MerchantOrderID string `json:"merchantOrderId"`
	// Change-of-mind is absent from the post-effect reason vocabulary.
	ReasonCode      string `json:"reasonCode"`
	PublicRationale string `json:"publicRationale"`
}

// RequestRefund: POST /api/v1/agencyOrder/{agencyOrderId}/refund-requests —
// one whole-MerchantOrder refund request.
func (h *Handler) RequestRefund(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "AGENCY_ORDER_DISABLED",
			"주문 처리 기능이 비활성화되어 있습니다.")
		return
	}
	var request refundRequestBody
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestRefund, AgencyOrderID: r.PathValue("agencyOrderId"), MerchantOrderID: request.MerchantOrderID, ActorID: userID, ActorRole: "CUSTOMER"}, agencyapp.RefundInput{ReasonCode: request.ReasonCode, PublicRationale: request.PublicRationale})
}

// ListRefundQueue: GET /api/v1/admin/agencyOrder/refund-requests — 운영자 심사 큐.
func (h *Handler) ListRefundQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	if h.lifecycle == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "AGENCY_ORDER_DISABLED",
			"주문 처리 기능이 비활성화되어 있습니다.")
		return
	}
	requests, err := h.lifecycle.ListRefundQueue(r.Context(), 100)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion":  "vitlane.agency-order-refund-queue.v2",
		"refundRequests": requests,
	})
}

type refundDecisionBody struct {
	Approve         *bool  `json:"approve"`
	PublicRationale string `json:"publicRationale"`
	InternalNote    string `json:"internalNote"`
}

// DecideRefundRequest: POST /api/v1/admin/agencyOrder/refund-requests/{requestId}/decisions
// 한 번의 결정이 요청의 모든 항목을 종결한다. 승인 항목은 Payment가 실행한다.
func (h *Handler) DecideRefundRequest(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "AGENCY_ORDER_DISABLED",
			"주문 처리 기능이 비활성화되어 있습니다.")
		return
	}
	var body refundDecisionBody
	if !httpapi.DecodeJSON(w, r, &body) {
		return
	}
	if body.Approve == nil {
		writeError(w, r, agencydomain.ErrRefundRequestInvalid)
		return
	}
	h.submitAction(w, r, procmsg.ActionRequest{ID: r.Header.Get("Idempotency-Key"), Kind: procmsg.RequestRefundDecision, ReferenceID: r.PathValue("requestId"), ActorID: actorUserID, ActorRole: "OPERATOR"}, agencyapp.RefundDecision{Approve: *body.Approve, PublicRationale: body.PublicRationale, InternalNote: body.InternalNote})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var consumed *agencydomain.OrderSheetAlreadyConsumedError
	if errors.As(err, &consumed) {
		httpapi.WriteJSON(w, http.StatusConflict, httpapi.ErrorResponse{Error: httpapi.APIError{
			Code: "ORDER_SHEET_ALREADY_CONSUMED", Message: "이미 생성된 AgencyOrder 결제를 계속해 주세요.",
			ReasonCode: "ORDER_SHEET_ALREADY_CONSUMED", Retryable: false,
			RequestID: sharedapp.RequestID(r.Context()), AgencyOrderID: consumed.AgencyOrderID,
		}})
		return
	}
	var lineFailure *agencydomain.OrderPreparationLineError
	if errors.As(err, &lineFailure) {
		httpapi.WriteJSON(w, http.StatusConflict, httpapi.ErrorResponse{Error: httpapi.APIError{
			Code: "CONFLICT", Message: "선택한 상품을 현재 주문할 수 없습니다.",
			ReasonCode: lineFailure.ReasonCode, Retryable: lineFailure.Retryable,
			RequestID: sharedapp.RequestID(r.Context()), ItemTitle: lineFailure.ItemTitle,
		}})
		return
	}
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "주문서를 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, procmsg.ErrRequestNotFound):
		httpapi.WriteError(w, 404, err.Error(), "저장된 주문 요청을 찾을 수 없습니다.")
	case errors.Is(err, procmsg.ErrRequestInvalid):
		httpapi.WriteError(w, 400, err.Error(), "요청 값과 Idempotency-Key를 확인해 주세요.")
	case errors.Is(err, processdomain.ErrRequestConflict):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "주문 작업 상태가 변경되었습니다. 다시 확인해 주세요.")
	case errors.Is(err, agencyapp.ErrListQueryInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, err.Error(), "AgencyOrder 필터 또는 정렬 조건이 올바르지 않습니다.")
	case errors.Is(err, agencyapp.ErrPayPalIssueDisabled),
		errors.Is(err, agencyapp.ErrLiveIssueDisabled):
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PAYPAL_RAIL_UNAVAILABLE",
			"이 PayPal 주문 환경의 신규 주문 발행이 현재 중단되었습니다.")
	case errors.Is(err, agencyapp.ErrLiveControlVersionConflict):
		httpapi.WriteError(w, http.StatusConflict, "LIVE_CONTROL_VERSION_CONFLICT",
			"Live 제어 상태가 변경되었습니다. 결제수단을 다시 확인해 주세요.")
	case errors.Is(err, agencydomain.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "AGENCY_ORDER_NOT_FOUND", "주문서 또는 주문을 찾을 수 없습니다.")
	case errors.Is(err, agencydomain.ErrRefundRequestInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "AGENCY_ORDER_REFUND_REQUEST_INVALID",
			"환불 요청 항목을 확인해 주세요. 이미 요청·환불된 상품이거나 PayPal 결제 주문이 아닐 수 있습니다.")
	case errors.Is(err, agencydomain.ErrShippingInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "미국 배송에 필요한 주소 항목을 확인해 주세요.")
	case errors.Is(err, agencydomain.ErrShippingRequired):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "저장된 배송정보를 찾을 수 없습니다.")
	case errors.Is(err, agencydomain.ErrNotReady), errors.Is(err, agencydomain.ErrStateInvalid), errors.Is(err, agencydomain.ErrInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "현재 상태에서는 주문을 만들 수 없습니다.")
	case errors.Is(err, agencydomain.ErrVersionConflict):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "다른 화면에서 주문서가 변경되었습니다. 새로고침해 주세요.")
	case errors.Is(err, agencydomain.ErrExecutionNotFound), errors.Is(err, agencydomain.ErrReceiptNotFound):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "AgencyOrder 처리 기록을 찾을 수 없습니다.")
	case errors.Is(err, agencydomain.ErrAssignmentRequired), errors.Is(err, agencydomain.ErrPIIAccessDenied):
		httpapi.WriteError(w, http.StatusForbidden, err.Error(), "담당 운영자 할당과 감사된 배송정보 열람이 필요합니다.")
	case errors.Is(err, agencydomain.ErrAssignmentConflict), errors.Is(err, agencydomain.ErrProcessStateInvalid):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "AgencyOrder 상태가 변경되었습니다. 목록을 새로고침해 주세요.")
	case errors.Is(err, agencydomain.ErrExecutionMixedTerminal):
		httpapi.WriteError(w, http.StatusConflict, err.Error(),
			"이미 처리 완료된 Shop이 있는 주문은 전체 환불로 실패 처리할 수 없습니다. 부분 환불이 도입되기 전까지는 남은 Shop도 완료 처리해 주세요.")
	case errors.Is(err, agencydomain.ErrExecutionInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, err.Error(), "요청 값과 Idempotency-Key를 확인해 주세요.")
	default:
		slog.ErrorContext(r.Context(), "agency order request failed",
			"event", "agency_order.request_failed",
			"request_id", sharedapp.RequestID(r.Context()),
			"error", err,
		)
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "주문서를 처리하지 못했습니다.")
	}
}

func (h *Handler) EnableProcessor(p procmsg.RequestSubmitter) { h.processor = p }

func (h *Handler) submitAction(w http.ResponseWriter, r *http.Request, request procmsg.ActionRequest, input any) {
	if h.processor == nil || h.lifecycle == nil {
		writeLifecycleUnavailable(w)
		return
	}
	request, err := h.lifecycle.StageAction(r.Context(), request, input)
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

// RequestReceipt always checks the authenticated Order owner before lookup.
func (h *Handler) RequestReceipt(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.processor == nil || h.lifecycle == nil {
		writeLifecycleUnavailable(w)
		return
	}
	order := r.PathValue("agencyOrderId")
	if _, err := h.lifecycle.Get(r.Context(), actor, order); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.processor.Receipt(r.Context(), order, r.PathValue("requestId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, result)
}

func (h *Handler) ListProcessRequests(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.lifecycle == nil {
		writeLifecycleUnavailable(w)
		return
	}
	order := r.PathValue("agencyOrderId")
	if _, err := h.lifecycle.Get(r.Context(), actor, order); err != nil {
		writeError(w, r, err)
		return
	}
	reader, ok := h.processor.(interface {
		Requests(context.Context, string) ([]procmsg.RequestReceipt, error)
	})
	if !ok {
		writeLifecycleUnavailable(w)
		return
	}
	receipts, err := reader.Requests(r.Context(), order)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.order-process-requests.v1", "requests": receipts})
}

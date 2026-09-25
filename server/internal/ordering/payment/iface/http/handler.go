// Package http는 Payment의 사용자 결제 route와 PayPal webhook ingress를 소유한다.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service  *paymentapp.Service
	adoption payPalResourceAdoptionService
}

func NewHandler(service *paymentapp.Service) *Handler {
	return &Handler{service: service, adoption: service}
}

type checkoutResponse struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Payment       domain.CustomerPayment `json:"payment"`
	Attempt       domain.PayPalAttempt   `json:"attempt"`
	ApprovalURL   string                 `json:"approvalUrl,omitempty"`
	ReturnNonce   string                 `json:"returnNonce,omitempty"`
}

func writeCheckout(w http.ResponseWriter, status int, view paymentapp.CheckoutView) {
	httpapi.WriteJSON(w, status, checkoutResponse{
		SchemaVersion: "vitlane.payment-paypal-checkout.v1",
		Payment:       view.Payment, Attempt: view.Attempt,
		ApprovalURL: view.ApprovalURL, ReturnNonce: view.ReturnNonce,
	})
}

// StartCheckout: POST /api/v1/agencyOrder/{agencyOrderId}/paypal/checkout
func (h *Handler) StartCheckout(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	view, err := h.service.StartCheckout(r.Context(), userID, r.PathValue("agencyOrderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCheckout(w, http.StatusOK, view)
}

type resumeRequest struct {
	ReturnNonce string `json:"returnNonce"`
	Cancelled   bool   `json:"cancelled"`
}

// Resume: POST /api/v1/agencyOrder/{agencyOrderId}/paypal/resume — PayPal
// redirect 복귀 뒤 wake-up. redirect만으로 성공을 표시하지 않는다.
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request resumeRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	view, err := h.service.Resume(r.Context(), paymentapp.ResumeInput{
		UserID: userID, AgencyOrderID: r.PathValue("agencyOrderId"),
		ReturnNonce: request.ReturnNonce, Cancelled: request.Cancelled,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCheckout(w, http.StatusOK, view)
}

// GetView: GET /api/v1/agencyOrder/{agencyOrderId}/paypal — polling 조회.
func (h *Handler) GetView(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	view, found, err := h.service.GetView(r.Context(), userID, r.PathValue("agencyOrderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !found {
		httpapi.WriteError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND",
			"진행 중인 PayPal 결제가 없습니다.")
		return
	}
	writeCheckout(w, http.StatusOK, view)
}

// webhookEnvelope는 서명 검증 뒤에만 사용하는 safe field 추출용 구조다.
// raw body 원문이 검증의 권위이며 저장하지 않는다.
type webhookEnvelope struct {
	ID           string `json:"id"`
	EventType    string `json:"event_type"`
	ResourceType string `json:"resource_type"`
	CreateTime   string `json:"create_time"`
	Resource     struct {
		ID                    string `json:"id"`
		DisputeID             string `json:"dispute_id"`
		Status                string `json:"status"`
		Reason                string `json:"reason"`
		DisputeLifecycleStage string `json:"dispute_life_cycle_stage"`
		SellerResponseDueDate string `json:"seller_response_due_date"`
		UpdateTime            string `json:"update_time"`
		CreateTime            string `json:"create_time"`
		DisputeOutcome        struct {
			OutcomeCode string `json:"outcome_code"`
		} `json:"dispute_outcome"`
		DisputedTransactions []struct {
			SellerTransactionID string `json:"seller_transaction_id"`
			SellerTransaction   struct {
				ID string `json:"id"`
			} `json:"seller_transaction"`
		} `json:"disputed_transactions"`
		SupplementaryData struct {
			RelatedIDs struct {
				OrderID   string `json:"order_id"`
				CaptureID string `json:"capture_id"`
			} `json:"related_ids"`
		} `json:"supplementary_data"`
	} `json:"resource"`
}

// Webhook: POST /api/webhooks/paypal/sandbox — 인증 없는 공개 ingress.
// 서명 검증 실패는 상태 변경 없이 거절하고, 검증자 일시 장애는 5xx로 반환해
// provider retry를 허용한다.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	h.webhook("SANDBOX", w, r)
}

// LiveWebhook is wired before activation so PayPal dashboard readback can use
// a stable URL. With no registered Live provider it fails closed and performs
// no external or local effect.
func (h *Handler) LiveWebhook(w http.ResponseWriter, r *http.Request) {
	h.webhook("LIVE", w, r)
}

func (h *Handler) webhook(environment string, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read failure", http.StatusBadRequest)
		return
	}
	var envelope webhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil ||
		strings.TrimSpace(envelope.ID) == "" {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	disputedCaptureID, err := envelope.disputedCaptureID()
	if err != nil {
		http.Error(w, "invalid dispute transaction binding", http.StatusBadRequest)
		return
	}
	err = h.service.HandleWebhook(r.Context(), paymentapp.WebhookInput{
		ProviderEnvironment:   environment,
		AuthAlgo:              r.Header.Get("Paypal-Auth-Algo"),
		CertURL:               r.Header.Get("Paypal-Cert-Url"),
		TransmissionID:        r.Header.Get("Paypal-Transmission-Id"),
		TransmissionSig:       r.Header.Get("Paypal-Transmission-Sig"),
		TransmissionTime:      r.Header.Get("Paypal-Transmission-Time"),
		EventID:               envelope.ID,
		EventType:             envelope.EventType,
		ResourceKind:          envelope.ResourceType,
		ResourceID:            envelope.Resource.ID,
		RelatedOrderID:        envelope.Resource.SupplementaryData.RelatedIDs.OrderID,
		DisputeID:             firstValue(envelope.Resource.DisputeID, envelope.Resource.ID),
		DisputedCaptureID:     disputedCaptureID,
		DisputeStatus:         envelope.Resource.Status,
		DisputeOutcome:        envelope.Resource.DisputeOutcome.OutcomeCode,
		DisputeReason:         envelope.Resource.Reason,
		DisputeLifecycleStage: envelope.Resource.DisputeLifecycleStage,
		SellerResponseDueAt:   parsePayPalTime(envelope.Resource.SellerResponseDueDate),
		ResourceOccurredAt: parsePayPalTime(firstValue(
			envelope.Resource.UpdateTime, envelope.Resource.CreateTime, envelope.CreateTime,
		)),
		RawBody: body,
	})
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, domain.ErrInvalid):
		http.Error(w, "invalid signature", http.StatusUnauthorized)
	case errors.Is(err, domain.ErrDisputeInvalid),
		errors.Is(err, domain.ErrDisputeNotFound),
		errors.Is(err, domain.ErrInstructionMismatch):
		http.Error(w, "dispute binding rejected", http.StatusUnprocessableEntity)
	default:
		http.Error(w, "verification unavailable", http.StatusServiceUnavailable)
	}
}

func (envelope webhookEnvelope) disputedCaptureID() (string, error) {
	values := map[string]struct{}{}
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			values[value] = struct{}{}
		}
	}
	add(envelope.Resource.SupplementaryData.RelatedIDs.CaptureID)
	for _, transaction := range envelope.Resource.DisputedTransactions {
		add(transaction.SellerTransactionID)
		add(transaction.SellerTransaction.ID)
	}
	if len(values) > 1 {
		return "", domain.ErrDisputeInvalid
	}
	for value := range values {
		return value, nil
	}
	return "", nil
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func parsePayPalTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "결제를 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND",
			"결제 대상 주문을 찾을 수 없습니다.")
	case errors.Is(err, domain.ErrRailUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PAYPAL_RAIL_UNAVAILABLE",
			"PayPal 결제가 아직 활성화되지 않았습니다.")
	case errors.Is(err, domain.ErrInstructionExpired):
		httpapi.WriteError(w, http.StatusConflict, "PAYMENT_INSTRUCTION_EXPIRED",
			"주문 유효기간이 지났습니다. 주문서를 다시 발행해 주세요.")
	case errors.Is(err, domain.ErrInstructionMismatch):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PAYMENT_INSTRUCTION_MISMATCH",
			"이 주문은 PayPal Sandbox 결제 대상이 아닙니다.")
	case errors.Is(err, domain.ErrBindingMismatch):
		httpapi.WriteError(w, http.StatusConflict, "PAYPAL_ACCOUNT_BINDING_MISMATCH",
			"PayPal 수취 계정 검증에 실패해 결제를 중단했습니다.")
	case errors.Is(err, domain.ErrCaptureBlocked):
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PAYPAL_CAPTURE_DISABLED",
			"PayPal 결제 승인은 확인됐지만 수납 기능이 아직 활성화되지 않았습니다.")
	case errors.Is(err, domain.ErrDisputeNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "PAYPAL_DISPUTE_NOT_FOUND",
			"PayPal 분쟁 건을 찾을 수 없습니다.")
	case errors.Is(err, domain.ErrDisputeInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PAYPAL_DISPUTE_INVALID",
			"PayPal 분쟁 처리 기록을 확인해 주세요.")
	case errors.Is(err, domain.ErrRefundBlockedByDispute):
		httpapi.WriteError(w, http.StatusConflict, "PAYPAL_REFUND_BLOCKED_BY_DISPUTE",
			"PayPal 분쟁 상태가 확정될 때까지 별도 환불을 보낼 수 없습니다.")
	case errors.Is(err, domain.ErrConflict):
		httpapi.WriteError(w, http.StatusConflict, "PAYMENT_CONFLICT",
			"결제 상태가 변경되었습니다. 새로고침해 주세요.")
	case errors.Is(err, domain.ErrInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, "PAYMENT_INVALID",
			"결제 요청이 올바르지 않습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "PAYMENT_INTERNAL",
			"결제 처리 중 오류가 발생했습니다.")
	}
}

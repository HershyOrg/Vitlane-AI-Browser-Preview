// Package http는 Procurement 운영자 work surface다(계약 v7 §14 PROCUREMENT_READY
// 큐). 모든 결과·reveal은 claim/lease 재검사와 append-only 감사를 요구한다.
package http

import (
	"errors"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"net/http"
	"strconv"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	processor procmsg.RequestSubmitter
	service   *procurementapp.Service
}

func NewHandler(service *procurementapp.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ListQueue(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListQueue(r.Context(), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.procurement-queue.v1", "items": items,
	})
}

func (h *Handler) ClaimTask(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	h.submitAction(w, r, actor, "OPERATOR", procmsg.RequestClaimTask, "", "", r.PathValue("taskId"), "", nil)
}

type resultRequest struct {
	Done        bool   `json:"done"`
	FailureCode string `json:"failureCode"`
	// Sandbox TEST와 LIVE는 같은 operator-authored field를 사용한다. Kind가
	// 경제적 의미를 고정하며 runtime execution mode와 다르면 거절된다.
	// Hash, observedAt, actor는 저장 transaction에서 서버가 생성한다.
	EvidenceKind      domain.PlacementEvidenceKind `json:"evidenceKind"`
	AmountMode        domain.PlacementAmountMode   `json:"amountMode"`
	ExternalOrderRef  string                       `json:"externalOrderRef"`
	ReceiptSafeRef    string                       `json:"receiptSafeRef"`
	ActualAmountMinor int64                        `json:"actualAmountMinor"`
	EvidenceSource    domain.EvidenceSource        `json:"evidenceSource"`
}

func (h *Handler) RecordResult(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input resultRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	kind := procmsg.RequestFailureEvidence
	if input.Done {
		kind = procmsg.RequestPlacementEvidence
	}
	h.submitAction(w, r, actor, "OPERATOR", kind, "", "", r.PathValue("taskId"), "", input)
}

type revealRequest struct {
	ReasonCode    string `json:"reasonCode"`
	ReasonDetail  string `json:"reasonDetail"`
	CorrelationID string `json:"correlationId"`
}

func (h *Handler) RevealShipping(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input revealRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, actor, "OPERATOR", procmsg.RequestRevealShipping, "", "", r.PathValue("taskId"), "", input)
}

// RevealContinueURL은 운용 참조 정보 인계다(ADR-0052 §4.4). 만료·회수 시 410으로
// 답한다. 실제 구매는 이 URL을 재사용하거나 자동 fallback하지 않고 운영자가
// 주문 sheet를 보고 판매처에서 수동 수행한다.
func (h *Handler) RevealContinueURL(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input revealRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitAction(w, r, actor, "OPERATOR", procmsg.RequestRevealCheckout, "", "", r.PathValue("taskId"), "", input)
}

// CancelOrder는 결제 후·첫 merchant effect 전 고객 자유 취소의 intent 접수다
// (ADR-0056 §6 — 비동기). 자격의 권위 검증과 실행은 process Effect의
// executor transaction이 수행하고, 경합 패배는 REJECTED로 고객 notice에
// 반영된다. 환불 basis는 실행 시 disclosure policy로 확정된다.
func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		MerchantOrderID string `json:"merchantOrderId"`
	}
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitCancellation(w, r, actor, input.MerchantOrderID, "PRE_EFFECT")
}

// CancelDelayRule은 30일 지연 rule 취소의 intent 접수다(FTC — 항상 GROSS,
// 비동기 실행은 process Effect).
func (h *Handler) CancelDelayRule(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		MerchantOrderID string `json:"merchantOrderId"`
	}
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	h.submitCancellation(w, r, actor, input.MerchantOrderID, "DELAY_RULE")
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "조달 요청을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, procmsg.ErrRequestNotFound):
		httpapi.WriteError(w, 404, err.Error(), "저장된 주문 요청을 찾을 수 없습니다.")
	case errors.Is(err, procmsg.ErrRequestInvalid):
		httpapi.WriteError(w, 400, err.Error(), "요청 값과 Idempotency-Key를 확인해 주세요.")
	case errors.Is(err, processdomain.ErrRequestConflict):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "주문 실행 요청을 확인해 주세요.")
	case errors.Is(err, domain.ErrTaskNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "PROCUREMENT_TASK_NOT_FOUND", "처리 항목을 찾지 못했습니다.")
	case errors.Is(err, domain.ErrRequestNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "PROCUREMENT_CUSTOMER_REQUEST_NOT_FOUND", "고객 요청을 찾지 못했습니다.")
	case errors.Is(err, domain.ErrOrderNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "AGENCY_ORDER_NOT_FOUND", "주문을 찾을 수 없습니다.")
	case errors.Is(err, domain.ErrCancelNotEligible):
		httpapi.WriteError(w, http.StatusConflict, "CANCEL_NOT_ELIGIBLE",
			"이미 구매 처리가 시작되어 자유 취소할 수 없습니다. 필요한 경우 환불 요청을 이용해 주세요.")
	case errors.Is(err, domain.ErrEvidenceInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_EVIDENCE_INVALID",
			"실행 증거가 주문 모드 또는 승인 조건과 다릅니다(주문·영수증 참조, 정확한 금액, 출처·해시·관찰 시각 필요).")
	case errors.Is(err, domain.ErrAssignmentConflict):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_ASSIGNMENT_CONFLICT", "다른 담당자가 처리 중입니다.")
	case errors.Is(err, domain.ErrAssignmentRequired):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_ASSIGNMENT_REQUIRED", "먼저 이 항목을 담당해야 합니다.")
	case errors.Is(err, domain.ErrLeaseExpired):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_LEASE_EXPIRED", "담당 lease가 만료되었습니다. 다시 담당해 주세요.")
	case errors.Is(err, domain.ErrTaskStateInvalid), errors.Is(err, domain.ErrResultInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_RESULT_INVALID", "현재 상태에서 허용되지 않는 요청입니다.")
	case errors.Is(err, domain.ErrDecisionInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_DECISION_INVALID", "판단 근거와 고객 공개 사유를 확인해 주세요.")
	case errors.Is(err, domain.ErrRequestInvalid), errors.Is(err, domain.ErrRequestResponseInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_CUSTOMER_REQUEST_INVALID", "질문 또는 답변 형식이 올바르지 않습니다.")
	case errors.Is(err, domain.ErrRequestStateConflict):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_CUSTOMER_REQUEST_STATE_CONFLICT", "이 요청은 이미 다른 응답으로 처리되었습니다. 새로고침해 주세요.")
	case errors.Is(err, domain.ErrNoResponseTooEarly):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_NO_RESPONSE_TOO_EARLY", "응답 기한 전에는 무응답 실패로 닫을 수 없습니다.")
	case errors.Is(err, domain.ErrManualDecisionRequired):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_MANUAL_DECISION_REQUIRED", "구매 시작 전에 최신 조건에 대한 운영자 판단이 필요합니다.")
	case errors.Is(err, domain.ErrAuthorizationMissing):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_AUTHORIZATION_MISSING", "이 주문에는 고객의 구매대행 승인 스냅샷이 없어 구매를 시작할 수 없습니다.")
	case errors.Is(err, domain.ErrOpenCustomerRequest):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_CUSTOMER_REQUEST_OPEN", "고객 응답을 기다리는 요청이 있습니다.")
	case errors.Is(err, domain.ErrEffectAlreadyStarted):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_EFFECT_ALREADY_STARTED", "구매 실행이 이미 시작되었습니다.")
	case errors.Is(err, domain.ErrManualReviewUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PROCUREMENT_MANUAL_REVIEW_UNAVAILABLE", "수동 구매 심사 기능을 사용할 수 없습니다.")
	case errors.Is(err, domain.ErrOrderInException):
		httpapi.WriteError(w, http.StatusConflict, "ORDER_IN_EXCEPTION_RESOLUTION",
			"이 주문은 예외 처리(환불·판정) 국면입니다 — 예외 처리 탭에서 진행해 주세요.")
	case errors.Is(err, domain.ErrLogisticsNotReady):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_LOGISTICS_NOT_READY",
			"배송 기대 등록이 아직 완료되지 않았습니다. 잠시 후 다시 시도해 주세요.")
	case errors.Is(err, domain.ErrFundingNotReady):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_FUNDING_NOT_READY",
			"Merchant Order 자금 활성화가 확정되지 않아 판매처 구매를 시작할 수 없습니다.")
	case errors.Is(err, domain.ErrLiveModeClosed):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_LIVE_MODE_CLOSED", "LIVE 실행은 아직 활성화되지 않았습니다(Step 6).")
	case errors.Is(err, domain.ErrPIIAccessDenied):
		httpapi.WriteError(w, http.StatusForbidden, "PROCUREMENT_PII_ACCESS_DENIED", "열람 조건을 충족하지 못했습니다.")
	case errors.Is(err, domain.ErrContinueURLGone):
		httpapi.WriteError(w, http.StatusGone, "PROCUREMENT_CONTINUE_URL_UNAVAILABLE", "선택적 checkout 참고 링크를 사용할 수 없습니다. 주문 sheet의 Shop·상품·variant·옵션·수량으로 판매처에서 수동 처리해 주세요.")
	case errors.Is(err, domain.ErrRecoveryNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "PROCUREMENT_RECOVERY_NOT_FOUND", "회수 기록 대상을 찾지 못했습니다. 주문 번호를 확인해 주세요.")
	case errors.Is(err, domain.ErrRecoveryInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, "PROCUREMENT_RECOVERY_INVALID", "회수 기입 값이 올바르지 않습니다. 주문·MO 번호와 원인·금액을 확인해 주세요.")
	case errors.Is(err, domain.ErrRecoveryVersionConflict):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_RECOVERY_VERSION_CONFLICT", "다른 곳에서 먼저 갱신되었습니다. 목록을 새로고침한 뒤 다시 시도해 주세요.")
	case errors.Is(err, domain.ErrRecoveryReferenceConflict):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_RECOVERY_REFERENCE_CONFLICT", "입력한 번호가 서로 다른 주문과 MO에 동시에 연결됩니다. 운영 확인이 필요합니다.")
	case errors.Is(err, domain.ErrRecoveryManualOnly):
		httpapi.WriteError(w, http.StatusConflict, "PROCUREMENT_RECOVERY_MANUAL_ONLY", "자동 생성된 회수 기록은 삭제할 수 없습니다 — 대신 포기로 닫아 주세요.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "PROCUREMENT_INTERNAL", "요청을 처리하지 못했습니다.")
	}
}

func (h *Handler) EnableProcessor(processor procmsg.RequestSubmitter) { h.processor = processor }

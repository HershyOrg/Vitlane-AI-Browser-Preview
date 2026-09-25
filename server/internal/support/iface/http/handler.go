// Package http는 Support 대화(ADR-0059)의 HTTP 창구다. 고객 라우트는 세션,
// 운영자 목록·스레드는 RequireOperator, 운영자 발신은 freshOperatorRoute가
// 감싼다(라우팅은 cmd/vitlane/routes.go).
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

const (
	messagesSchemaVersion      = "vitlane.support-messages.v1"
	summarySchemaVersion       = "vitlane.support-summary.v1"
	conversationsSchemaVersion = "vitlane.support-conversations.v2"
	threadSchemaVersion        = "vitlane.support-thread.v2"
	countsSchemaVersion        = "vitlane.support-counts.v2"
	handlingSchemaVersion      = "vitlane.support-no-reply-resolution.v1"
)

type Handler struct {
	service *supportapp.Service
}

func NewHandler(service *supportapp.Service) *Handler {
	return &Handler{service: service}
}

// ListMessages: GET /api/v1/support/messages — 내 대화 최신순.
func (h *Handler) ListMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	messages, err := h.service.ListMessages(
		r.Context(), userID, limit, r.URL.Query().Get("before"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": messagesSchemaVersion, "messages": messages,
	})
}

type sendMessageRequest struct {
	Body string `json:"body"`
	// 선택적 주문 첨부 — 운영자와 대칭이다. 자기 소유 주문만 서버가 허용한다.
	AgencyOrderID string `json:"agencyOrderId"`
}

// SendMessage: POST /api/v1/support/messages — 고객 발신. 링크는 구조화
// 참조로만 성립한다(본문 파싱 없음, ADR-0059 §5).
func (h *Handler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	request, ok := decodeMessagePayload(w, r, true)
	if !ok {
		return
	}
	message, replay, err := h.service.SendCustomerMessageWithImages(
		r.Context(), userID, request.Body, request.AgencyOrderID,
		r.Header.Get("Idempotency-Key"), request.Images,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpapi.WriteJSON(w, status, map[string]any{"message": message, "replay": replay})
}

// MarkRead: POST /api/v1/support/messages/read — 읽음 워터마크(전체 일괄).
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.MarkAllRead(r.Context(), userID); err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"read": true})
}

// Summary: GET /api/v1/support/summary — 안 읽음 뱃지 폴링 전용 카운트.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	unread, err := h.service.CountUnread(r.Context(), userID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": summarySchemaVersion, "unread": unread,
	})
}

// ListConversations: GET /api/v1/admin/support/conversations?view=AWAITING|ALL.
func (h *Handler) ListConversations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	conversations, err := h.service.ListConversations(
		r.Context(), r.URL.Query().Get("view"), limit,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": conversationsSchemaVersion, "conversations": conversations,
	})
}

// OperatorThread: GET /api/v1/admin/support/conversations/{userId}/messages.
func (h *Handler) OperatorThread(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	thread, err := h.service.OperatorThread(
		r.Context(), r.PathValue("userId"), limit, r.URL.Query().Get("before"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion":             threadSchemaVersion,
		"userId":                    thread.UserID,
		"customer":                  thread.Customer,
		"messages":                  thread.Messages,
		"awaitingReply":             thread.AwaitingReply,
		"awaitingCustomerMessageId": thread.AwaitingCustomerMessageID,
		"actionRequired":            thread.ActionRequired,
	})
}

// HandleWithoutReply: PUT
// /api/v1/admin/support/conversations/{userId}/messages/{messageId}/no-reply-resolution
// — 고객에게 메시지를 보내지 않고 마지막 고객 메시지를 운영 완료로 처리한다.
func (h *Handler) HandleWithoutReply(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	replay, err := h.service.HandleWithoutReply(
		r.Context(), r.PathValue("userId"), r.PathValue("messageId"), operatorID,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": handlingSchemaVersion,
		"handled":       true,
		"replay":        replay,
	})
}

// ReplyOperator: POST /api/v1/admin/support/conversations/{userId}/messages —
// 운영자 답변(선택적 주문 첨부, 소유자 검증은 app이 소유).
func (h *Handler) ReplyOperator(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	request, ok := decodeMessagePayload(w, r, true)
	if !ok {
		return
	}
	message, replay, err := h.service.ReplyOperatorWithImages(
		r.Context(), r.PathValue("userId"), operatorID,
		request.Body, request.AgencyOrderID, r.Header.Get("Idempotency-Key"),
		request.Images,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpapi.WriteJSON(w, status, map[string]any{"message": message, "replay": replay})
}

// SendOrderMessage: POST /api/v1/admin/support/orders/{agencyOrderId}/messages —
// 종전 "고객 안내 보내기"의 대체. 주문 소유 고객의 대화로 첨부 발신한다.
func (h *Handler) SendOrderMessage(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	request, ok := decodeMessagePayload(w, r, false)
	if !ok {
		return
	}
	message, replay, err := h.service.SendOrderMessageWithImages(
		r.Context(), r.PathValue("agencyOrderId"), operatorID,
		request.Body, r.Header.Get("Idempotency-Key"), request.Images,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpapi.WriteJSON(w, status, map[string]any{"message": message, "replay": replay})
}

// Counts: GET /api/v1/admin/support/counts — 답변 대기 대화 전역 카운트.
func (h *Handler) Counts(w http.ResponseWriter, r *http.Request) {
	awaiting, err := h.service.CountAwaiting(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	actionRequired, err := h.service.CountActionRequired(r.Context())
	if errors.Is(err, supportdomain.ErrQueryInvalid) {
		actionRequired = 0
	} else if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": countsSchemaVersion,
		"counts":        map[string]int{"awaiting": awaiting, "actionRequired": actionRequired},
	})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	// 인프라 분류(fault taxonomy)는 제품 오류 코드로 가리지 않고 그대로 낸다.
	if _, classified := fault.As(err); classified {
		httpapi.WriteFault(w, r, err, "요청을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, supportdomain.ErrMessageInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "메시지 내용을 확인해 주세요.")
	case errors.Is(err, supportdomain.ErrQueryInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "조회 조건을 확인해 주세요.")
	case errors.Is(err, supportdomain.ErrOrderRefInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "첨부한 주문을 확인해 주세요.")
	case errors.Is(err, supportdomain.ErrUserNotFound):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "대상 사용자를 찾을 수 없습니다.")
	case errors.Is(err, supportdomain.ErrConversationNotAwaiting):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "이미 처리됐거나 더 이상 답변 대기 중인 메시지가 아닙니다.")
	case errors.Is(err, supportdomain.ErrIdempotencyKeyMissing):
		httpapi.WriteError(w, http.StatusBadRequest, err.Error(), "요청 식별자가 필요합니다.")
	case errors.Is(err, supportdomain.ErrIdempotencyKeyReused):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "같은 요청 식별자를 다른 내용에 사용할 수 없습니다.")
	case errors.Is(err, supportdomain.ErrTooManyMessages):
		w.Header().Set("Retry-After", "3600")
		httpapi.WriteError(w, http.StatusTooManyRequests, err.Error(), "메시지를 너무 자주 보냈습니다. 잠시 후 다시 시도해 주세요.")
	case errors.Is(err, supportdomain.ErrImageAttachmentInvalid),
		errors.Is(err, supportdomain.ErrBusinessCardInvalid),
		errors.Is(err, supportdomain.ErrActionCardInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "첨부 이미지 또는 업무 카드를 확인해 주세요.")
	case errors.Is(err, supportdomain.ErrImageAttachmentNotFound):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "첨부 이미지를 찾을 수 없습니다.")
	case errors.Is(err, supportdomain.ErrImageCipherUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, err.Error(), "첨부 이미지를 현재 처리할 수 없습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "요청을 처리하지 못했습니다.")
	}
}

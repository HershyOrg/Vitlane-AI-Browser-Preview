// Package domain은 Support 대화(ADR-0059)의 메시지 불변식을 소유한다.
// 사용자당 단일 연속 대화이며 대화 identity는 user_id다. 메시지는 사람↔사람
// 소통 기록일 뿐 어떤 도메인 명령의 소스도 아니다(ADR-0026의 규칙 유지).
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrMessageInvalid          = errors.New("SUPPORT_MESSAGE_INVALID")
	ErrQueryInvalid            = errors.New("SUPPORT_QUERY_INVALID")
	ErrOrderRefInvalid         = errors.New("SUPPORT_ORDER_REF_INVALID")
	ErrUserNotFound            = errors.New("SUPPORT_USER_NOT_FOUND")
	ErrConversationNotAwaiting = errors.New("SUPPORT_CONVERSATION_NOT_AWAITING")
	ErrActionCardInvalid       = errors.New("SUPPORT_ACTION_CARD_INVALID")
	ErrBusinessCardInvalid     = errors.New("SUPPORT_BUSINESS_CARD_INVALID")
	ErrImageAttachmentInvalid  = errors.New("SUPPORT_IMAGE_ATTACHMENT_INVALID")
	ErrImageCipherUnavailable  = errors.New("SUPPORT_IMAGE_CIPHER_UNAVAILABLE")
	ErrImageAttachmentNotFound = errors.New("SUPPORT_IMAGE_ATTACHMENT_NOT_FOUND")
	ErrTooManyMessages         = errors.New("SUPPORT_TOO_MANY_MESSAGES")
	ErrIdempotencyKeyMissing   = errors.New("IDEMPOTENCY_KEY_REQUIRED")
	ErrIdempotencyKeyReused    = errors.New("IDEMPOTENCY_KEY_REUSED")
)

const (
	AuthorCustomer = "CUSTOMER"
	AuthorOperator = "OPERATOR"
	AuthorSystem   = "SYSTEM"

	ContentKindText         = "TEXT"
	ContentKindBusinessCard = "BUSINESS_CARD"

	// Idempotency namespaces are assigned by trusted constructors, never by
	// HTTP callers. A client-controlled text key therefore cannot reserve the
	// deterministic key of an owner-fact BusinessCard projection.
	IdempotencyNamespaceExternal     = "EXTERNAL"
	IdempotencyNamespaceBusinessCard = "BUSINESS_CARD"
)

// Message는 지원 대화 메시지 하나다. AgencyOrderID는 주문 링크의 유일한
// 근거인 구조화 참조다 — 고객·운영자가 대칭으로 첨부하되 소유권 검증은 app이
// 강제하고, 본문 텍스트는 어떤 author든 파싱·링크화하지 않는다(ADR-0059 §5).
type Message struct {
	ID                   string                    `json:"id"`
	UserID               string                    `json:"-"`
	Author               string                    `json:"author"`
	Body                 string                    `json:"body"`
	AgencyOrderID        string                    `json:"agencyOrderId,omitempty"`
	CreatedByUserID      string                    `json:"-"`
	IdempotencyKey       string                    `json:"-"`
	IdempotencyNamespace string                    `json:"-"`
	CreatedAt            time.Time                 `json:"createdAt"`
	ReadAt               *time.Time                `json:"readAt,omitempty"`
	ContentKind          string                    `json:"contentKind"`
	BusinessCard         *BusinessCard             `json:"businessCard,omitempty"`
	Attachments          []ImageAttachmentMetadata `json:"attachments,omitempty"`
	// RequestHash is the canonical idempotency payload hash. It intentionally
	// excludes generated IDs and timestamps, and includes canonical image
	// content hashes so the same key cannot silently replace evidence.
	RequestHash string `json:"-"`
}

// ConversationHead는 화면용 마지막 메시지와 답변 판정용 최신 ordinary TEXT를
// 분리해 보존한다. 고객 메시지를 처리 완료로 표시해도 메시지 원문이나 author를
// 바꾸지 않는다(ADR-0062).
type ConversationHead struct {
	LastMessage           Message
	LastOrdinaryMessageID string
	LastOrdinaryAuthor    string
	HandledWithoutReply   bool
	ActionRequired        bool
}

func (h ConversationHead) AwaitingReply() bool {
	author := h.LastOrdinaryAuthor
	if author == "" && (h.LastMessage.ContentKind == "" || h.LastMessage.ContentKind == ContentKindText) {
		author = h.LastMessage.Author
	}
	return author == AuthorCustomer && !h.HandledWithoutReply
}

// AwaitingCustomerMessageID identifies the exact ordinary customer message
// that an operator may mark handled. Business cards can be newer than this
// message, so clients must never infer it from the visible array position.
func (h ConversationHead) AwaitingCustomerMessageID() string {
	if !h.AwaitingReply() {
		return ""
	}
	if h.LastOrdinaryMessageID != "" {
		return h.LastOrdinaryMessageID
	}
	return h.LastMessage.ID
}

// NoReplyResolution은 특정 고객 메시지를 답변 없이 처리한 append-only 감사
// 기록이다. 새 고객 메시지에는 적용되지 않는다.
type NoReplyResolution struct {
	CustomerMessageID string
	HandledByUserID   string
	HandledAt         time.Time
}

func NewNoReplyResolution(
	customerMessageID, handledByUserID string,
	now time.Time,
) (NoReplyResolution, error) {
	customerMessageID = strings.TrimSpace(customerMessageID)
	handledByUserID = strings.TrimSpace(handledByUserID)
	if customerMessageID == "" || handledByUserID == "" || now.IsZero() {
		return NoReplyResolution{}, ErrConversationNotAwaiting
	}
	return NoReplyResolution{
		CustomerMessageID: customerMessageID,
		HandledByUserID:   handledByUserID,
		HandledAt:         now,
	}, nil
}

// NewCustomerMessage는 고객 발신이다. agencyOrderID 첨부 시 "자기 주문"
// 소유권 검증은 app이 게이트웨이로 마친 뒤 호출한다.
func NewCustomerMessage(
	id, userID, body, agencyOrderID, idempotencyKey string,
	now time.Time,
) (Message, error) {
	return newMessage(
		id, userID, AuthorCustomer, body, agencyOrderID, "", idempotencyKey, now,
	)
}

// NewOperatorMessage는 운영자 답변·주문 안내다. agencyOrderID의 소유자 검증은
// app이 게이트웨이로 마친 뒤 호출한다.
func NewOperatorMessage(
	id, userID, operatorUserID, body, agencyOrderID, idempotencyKey string,
	now time.Time,
) (Message, error) {
	if strings.TrimSpace(operatorUserID) == "" {
		return Message{}, ErrMessageInvalid
	}
	return newMessage(
		id, userID, AuthorOperator, body,
		agencyOrderID, operatorUserID, idempotencyKey, now,
	)
}

func newMessage(
	id, userID, author, body, agencyOrderID, createdBy, idempotencyKey string,
	now time.Time,
) (Message, error) {
	body = strings.TrimSpace(body)
	length := utf8.RuneCountInString(body)
	if length < 1 || length > 2000 || strings.TrimSpace(userID) == "" {
		return Message{}, ErrMessageInvalid
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 120 {
		return Message{}, ErrIdempotencyKeyMissing
	}
	message := Message{
		ID: id, UserID: strings.TrimSpace(userID), Author: author, Body: body,
		AgencyOrderID:        strings.TrimSpace(agencyOrderID),
		CreatedByUserID:      strings.TrimSpace(createdBy),
		IdempotencyKey:       idempotencyKey,
		IdempotencyNamespace: IdempotencyNamespaceExternal,
		CreatedAt:            now,
		ContentKind:          ContentKindText,
	}
	message.RequestHash = CanonicalRequestHash(message, nil)
	return message, nil
}

// CanonicalRequestHash binds an idempotent communication to its public
// content and canonical image hashes. Generated identity and observation time
// are deliberately excluded so a safe retry reaches the original row.
func CanonicalRequestHash(message Message, attachmentHashes []string) string {
	type cardHash struct {
		Type           string          `json:"type,omitempty"`
		ReferenceType  string          `json:"referenceType,omitempty"`
		ReferenceID    string          `json:"referenceId,omitempty"`
		ActionRequired bool            `json:"actionRequired,omitempty"`
		ResolvesCardID string          `json:"resolvesCardId,omitempty"`
		PublicPayload  json.RawMessage `json:"publicPayload,omitempty"`
	}
	type requestHash struct {
		UserID           string   `json:"userId"`
		Author           string   `json:"author"`
		Body             string   `json:"body"`
		AgencyOrderID    string   `json:"agencyOrderId,omitempty"`
		CreatedByUserID  string   `json:"createdByUserId,omitempty"`
		ContentKind      string   `json:"contentKind"`
		Card             cardHash `json:"card,omitempty"`
		AttachmentHashes []string `json:"attachmentHashes,omitempty"`
	}
	payload := requestHash{
		// Namespace scopes lookup and uniqueness, but is deliberately not a
		// request-hash field. Migration 000089 can move an existing card from the
		// legacy global namespace without invalidating its persisted replay hash.
		UserID: message.UserID, Author: message.Author, Body: message.Body,
		AgencyOrderID:    message.AgencyOrderID,
		CreatedByUserID:  message.CreatedByUserID,
		ContentKind:      message.ContentKind,
		AttachmentHashes: append([]string(nil), attachmentHashes...),
	}
	if message.BusinessCard != nil {
		payload.Card = cardHash{
			Type:           message.BusinessCard.Type,
			ReferenceType:  message.BusinessCard.Reference.Type,
			ReferenceID:    message.BusinessCard.Reference.ID,
			ActionRequired: message.BusinessCard.ActionRequired,
			ResolvesCardID: message.BusinessCard.ResolvesCardID,
			PublicPayload:  message.BusinessCard.PublicPayload,
		}
	}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

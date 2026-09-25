package domain

import (
	"encoding/json"
	"strings"
	"time"
)

const MaxBusinessCardPayloadBytes = 32 * 1024

type ConversationView string

const (
	ConversationViewAwaitingReply  ConversationView = "AWAITING_REPLY"
	ConversationViewActionRequired ConversationView = "ACTION_REQUIRED"
	ConversationViewAll            ConversationView = "ALL"
)

func ParseConversationView(value string) (ConversationView, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "AWAITING", "AWAITING_REPLY":
		return ConversationViewAwaitingReply, nil
	case "ACTION_REQUIRED":
		return ConversationViewActionRequired, nil
	case "ALL":
		return ConversationViewAll, nil
	default:
		return "", ErrQueryInvalid
	}
}

const (
	CardProcurementRequest  = "PROCUREMENT_REQUEST"
	CardProcurementResponse = "PROCUREMENT_RESPONSE"
	CardProcurementDecision = "PROCUREMENT_DECISION"
	CardRefundRequest       = "REFUND_REQUEST"
	CardRefundDecision      = "REFUND_DECISION"
	CardRefundStatus        = "REFUND_STATUS"
	CardPayPalDispute       = "PAYPAL_DISPUTE"
	CardPayPalDisputeStatus = "PAYPAL_DISPUTE_STATUS"
	CardDeliveryDelay       = "DELIVERY_DELAY"
	CardDeliveryResolution  = "DELIVERY_RESOLUTION"
	CardOrderCancellation   = "ORDER_CANCELLATION"
	// CardOrderCancellationDeclined는 취소 요청이 거절됐음의 고지다(ADR-0070
	// D3) — 종전에는 REJECTED 취소가 고객에게 무언이었다.
	CardOrderCancellationDeclined = "ORDER_CANCELLATION_DECLINED"
)

const (
	ReferenceProcurement   = "PROCUREMENT"
	ReferenceRefund        = "REFUND"
	ReferencePayPalDispute = "PAYPAL_DISPUTE"
	ReferenceDelivery      = "DELIVERY"
)

type BusinessReference struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// BusinessCard is a communication projection of an owning product fact. It
// never authorizes or executes that fact; the typed reference lets the UI
// navigate back to the owner instead of parsing prose.
type BusinessCard struct {
	Type           string            `json:"type"`
	Reference      BusinessReference `json:"reference"`
	ActionRequired bool              `json:"actionRequired"`
	ResolvesCardID string            `json:"resolvesCardId,omitempty"`
	PublicPayload  json.RawMessage   `json:"publicPayload"`
}

func NewBusinessCard(
	cardType, referenceType, referenceID string,
	actionRequired bool,
	resolvesCardID string,
	publicPayload json.RawMessage,
) (BusinessCard, error) {
	cardType = strings.TrimSpace(cardType)
	referenceType = strings.TrimSpace(referenceType)
	referenceID = strings.TrimSpace(referenceID)
	resolvesCardID = strings.TrimSpace(resolvesCardID)
	if !validCardType(cardType) || !validReferenceType(referenceType) ||
		referenceTypeForCard(cardType) != referenceType ||
		referenceID == "" || len(referenceID) > 160 {
		return BusinessCard{}, ErrBusinessCardInvalid
	}
	if actionRequired && resolvesCardID != "" {
		return BusinessCard{}, ErrBusinessCardInvalid
	}
	if len(publicPayload) == 0 {
		publicPayload = json.RawMessage(`{}`)
	}
	if len(publicPayload) > MaxBusinessCardPayloadBytes || !json.Valid(publicPayload) {
		return BusinessCard{}, ErrBusinessCardInvalid
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(publicPayload, &object); err != nil || object == nil {
		return BusinessCard{}, ErrBusinessCardInvalid
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return BusinessCard{}, ErrBusinessCardInvalid
	}
	return BusinessCard{
		Type:           cardType,
		Reference:      BusinessReference{Type: referenceType, ID: referenceID},
		ActionRequired: actionRequired,
		ResolvesCardID: resolvesCardID,
		PublicPayload:  append(json.RawMessage(nil), canonical...),
	}, nil
}

func NewCustomerBusinessCardMessage(
	id, userID, body, agencyOrderID, idempotencyKey string,
	card BusinessCard,
	now time.Time,
) (Message, error) {
	return newBusinessCardMessage(
		id, userID, AuthorCustomer, "", body, agencyOrderID,
		idempotencyKey, card, now,
	)
}

func NewOperatorBusinessCardMessage(
	id, userID, operatorUserID, body, agencyOrderID, idempotencyKey string,
	card BusinessCard,
	now time.Time,
) (Message, error) {
	if strings.TrimSpace(operatorUserID) == "" {
		return Message{}, ErrMessageInvalid
	}
	return newBusinessCardMessage(
		id, userID, AuthorOperator, operatorUserID, body, agencyOrderID,
		idempotencyKey, card, now,
	)
}

func NewSystemBusinessCardMessage(
	id, userID, body, agencyOrderID, idempotencyKey string,
	card BusinessCard,
	now time.Time,
) (Message, error) {
	return newBusinessCardMessage(
		id, userID, AuthorSystem, "", body, agencyOrderID,
		idempotencyKey, card, now,
	)
}

func newBusinessCardMessage(
	id, userID, author, createdBy, body, agencyOrderID, idempotencyKey string,
	card BusinessCard,
	now time.Time,
) (Message, error) {
	validated, err := NewBusinessCard(
		card.Type, card.Reference.Type, card.Reference.ID,
		card.ActionRequired, card.ResolvesCardID, card.PublicPayload,
	)
	if err != nil {
		return Message{}, err
	}
	message, err := newMessage(
		id, userID, author, body, agencyOrderID, createdBy, idempotencyKey, now,
	)
	if err != nil {
		return Message{}, err
	}
	message.ContentKind = ContentKindBusinessCard
	message.IdempotencyNamespace = IdempotencyNamespaceBusinessCard
	message.BusinessCard = &validated
	message.RequestHash = CanonicalRequestHash(message, nil)
	return message, nil
}

func validCardType(value string) bool {
	switch value {
	case CardProcurementRequest, CardProcurementResponse,
		CardProcurementDecision, CardRefundRequest, CardRefundDecision,
		CardRefundStatus, CardPayPalDispute, CardPayPalDisputeStatus,
		CardDeliveryDelay, CardDeliveryResolution, CardOrderCancellation,
		CardOrderCancellationDeclined:
		return true
	default:
		return false
	}
}

func validReferenceType(value string) bool {
	switch value {
	case ReferenceProcurement, ReferenceRefund, ReferencePayPalDispute,
		ReferenceDelivery:
		return true
	default:
		return false
	}
}

func referenceTypeForCard(cardType string) string {
	switch cardType {
	case CardProcurementRequest, CardProcurementResponse, CardProcurementDecision,
		CardOrderCancellation, CardOrderCancellationDeclined:
		return ReferenceProcurement
	case CardRefundRequest, CardRefundDecision, CardRefundStatus:
		return ReferenceRefund
	case CardPayPalDispute, CardPayPalDisputeStatus:
		return ReferencePayPalDispute
	case CardDeliveryDelay, CardDeliveryResolution:
		return ReferenceDelivery
	default:
		return ""
	}
}

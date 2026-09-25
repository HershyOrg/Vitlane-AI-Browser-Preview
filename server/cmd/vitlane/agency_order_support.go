package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

// agencyOrderSupportConversation is a communication projection only. The
// Ordering transaction commits first; typed Support cards cannot authorize or
// execute the referenced refund or delayed-order cancellation.
type agencyOrderSupportConversation struct {
	support *supportapp.Service
}

func (p *agencyOrderSupportConversation) PublishDelayRuleNotice(
	ctx context.Context,
	userID, agencyOrderID, _ string,
	observedAt time.Time,
) error {
	payload, _ := json.Marshal(map[string]any{
		"agencyOrderId": agencyOrderID,
		"state":         "ELIGIBLE_FOR_DELAY_CANCELLATION",
		"delayedDays":   30,
		"observedAt":    observedAt,
		"actionPath":    "/agencyOrder/" + agencyOrderID,
	})
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: userID, Author: supportdomain.AuthorSystem,
		Body: "Delivery delay / 배송 지연", AgencyOrderID: agencyOrderID,
		IdempotencyKey: "support:delivery-delay:" + agencyOrderID,
		Type:           supportdomain.CardDeliveryDelay,
		ReferenceType:  supportdomain.ReferenceDelivery,
		ReferenceID:    agencyOrderID + ":30-day", PublicPayload: payload,
	})
	return err
}

func (p *agencyOrderSupportConversation) PublishRefundRequest(
	ctx context.Context,
	request agencydomain.RefundRequest,
) error {
	payload := refundRequestSupportPayload(request)
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: request.UserID, Author: supportdomain.AuthorCustomer,
		ActorUserID: request.UserID, Body: request.PublicRationale,
		AgencyOrderID:  request.AgencyOrderID,
		IdempotencyKey: "support:refund:request:" + request.ID,
		Type:           supportdomain.CardRefundRequest,
		ReferenceType:  supportdomain.ReferenceRefund,
		ReferenceID:    request.ID, PublicPayload: payload,
	})
	return err
}

func refundRequestSupportPayload(request agencydomain.RefundRequest) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		// A refund request row is mutable: review later changes State. This card
		// is the immutable request event, so retries always reconstruct the
		// state at creation instead of hashing the row's current state.
		"requestId": request.ID, "state": agencydomain.RefundRequestRequested,
		"reasonCode":           request.ReasonCode,
		"publicRationale":      request.PublicRationale,
		"merchantOrderId":      request.MerchantOrderID,
		"allocationId":         request.AllocationID,
		"requestedGrossAmount": request.RequestedGrossAmount,
		"createdAt":            request.CreatedAt,
		"actionPath":           "/agencyOrder/" + request.AgencyOrderID,
	})
	return payload
}

func (p *agencyOrderSupportConversation) PublishRefundDecision(
	ctx context.Context,
	request agencydomain.RefundRequest,
	operatorUserID string,
) error {
	payload, _ := json.Marshal(map[string]any{
		"requestId": request.ID, "state": request.State,
		"merchantOrderId":         request.MerchantOrderID,
		"allocationId":            request.AllocationID,
		"requestedGrossAmount":    request.RequestedGrossAmount,
		"decision":                request.Decision,
		"decisionPublicRationale": request.DecisionPublicRationale,
		"decidedAt":               request.DecidedAt, "updatedAt": request.UpdatedAt,
		"actionPath": "/agencyOrder/" + request.AgencyOrderID,
	})
	body := "Refund review completed / 환불 심사 완료"
	if strings.TrimSpace(request.DecisionPublicRationale) != "" {
		body = request.DecisionPublicRationale
	}
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: request.UserID, Author: supportdomain.AuthorOperator,
		ActorUserID: strings.TrimSpace(operatorUserID), Body: body,
		AgencyOrderID:  request.AgencyOrderID,
		IdempotencyKey: "support:refund:decision:" + request.ID,
		Type:           supportdomain.CardRefundDecision,
		ReferenceType:  supportdomain.ReferenceRefund,
		ReferenceID:    request.ID, PublicPayload: payload,
	})
	return err
}

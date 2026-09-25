package main

import (
	"context"
	"encoding/json"
	"strings"

	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

// procurementSupportConversation is an app-to-app projection adapter. The
// Procurement fact commits first; a retry replays that fact and idempotently
// fills the same single Support conversation card if publication was briefly
// unavailable.
type procurementSupportConversation struct {
	support  *supportapp.Service
	ordering *agencyorderapp.LifecycleService
}

func (p *procurementSupportConversation) PublishProcurementDecision(
	ctx context.Context,
	record procurementdomain.DecisionRecord,
	operatorUserID string,
) error {
	owner, err := p.ordering.ResolveOrderOwner(ctx, record.AgencyOrderID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"decisionRecordId":  record.ID,
		"merchantOrderId":   record.MerchantOrderID,
		"decision":          record.Decision,
		"publicRationale":   record.PublicRationale,
		"observedCondition": record.ObservedCondition,
		"evidenceSource":    record.EvidenceSource,
		"observedAt":        record.ObservedAt,
	})
	_, _, err = p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: owner, Author: supportdomain.AuthorOperator,
		ActorUserID: strings.TrimSpace(operatorUserID), Body: record.PublicRationale,
		AgencyOrderID:  record.AgencyOrderID,
		IdempotencyKey: "support:procurement:decision:" + record.ID,
		Type:           supportdomain.CardProcurementDecision,
		ReferenceType:  supportdomain.ReferenceProcurement,
		ReferenceID:    record.ID, PublicPayload: payload,
	})
	return err
}

func (p *procurementSupportConversation) PublishProcurementRequest(
	ctx context.Context,
	request procurementdomain.CustomerRequest,
	operatorUserID string,
) error {
	payload := procurementRequestSupportPayload(request)
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: request.UserID, Author: supportdomain.AuthorOperator,
		ActorUserID: strings.TrimSpace(operatorUserID), Body: request.PublicContext,
		AgencyOrderID:  request.AgencyOrderID,
		IdempotencyKey: "support:procurement:request:" + request.ID,
		Type:           supportdomain.CardProcurementRequest,
		ReferenceType:  supportdomain.ReferenceProcurement,
		ReferenceID:    request.ID, ActionRequired: true, PublicPayload: payload,
	})
	return err
}

func procurementRequestSupportPayload(
	request procurementdomain.CustomerRequest,
) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"requestId": request.ID, "kind": request.Kind,
		"prompt": request.Prompt, "publicContext": request.PublicContext,
		"responseType":    request.ResponseType,
		"responseOptions": request.ResponseOptions,
		// The owner row is mutable after a customer response. The request card
		// represents the immutable creation event, so a retry after resolution
		// must still hash the original PENDING/v1 snapshot.
		"state": procurementdomain.RequestPending, "dueAt": request.DueAt,
		"version": int64(1),
	})
	return payload
}

func (p *procurementSupportConversation) PublishProcurementRequestResolution(
	ctx context.Context,
	request procurementdomain.CustomerRequest,
	actorUserID string,
) error {
	author := supportdomain.AuthorOperator
	if actorUserID == request.UserID &&
		(request.State == procurementdomain.RequestAnswered ||
			request.State == procurementdomain.RequestDeclined) {
		author = supportdomain.AuthorCustomer
	}
	payload, _ := json.Marshal(map[string]any{
		"requestId": request.ID, "state": request.State,
		"response": request.Response, "resolutionReason": request.ResolutionReason,
		"resolvedAt": request.ResolvedAt, "version": request.Version,
	})
	_, _, err := p.support.PublishBusinessCardResolution(ctx, supportapp.BusinessCardInput{
		TargetUserID: request.UserID, Author: author,
		ActorUserID:    strings.TrimSpace(actorUserID),
		Body:           "Customer response recorded / 고객 응답 기록",
		AgencyOrderID:  request.AgencyOrderID,
		IdempotencyKey: "support:procurement:resolution:" + request.ID,
		Type:           supportdomain.CardProcurementResponse,
		ReferenceType:  supportdomain.ReferenceProcurement,
		ReferenceID:    request.ID, PublicPayload: payload,
	})
	return err
}

func (p *procurementSupportConversation) PublishOrderCancellation(
	ctx context.Context,
	item procurementapp.CancellationSupportProjection,
) error {
	payload, _ := json.Marshal(map[string]any{
		"agencyOrderId":   item.AgencyOrderID,
		"merchantOrderId": item.MerchantOrderID,
		"kind":            item.Kind,
		"refundBasis":     item.RefundBasis,
		"state":           "CANCELLED",
		"createdAt":       item.CreatedAt,
		"actionPath":      "/agencyOrder/" + item.AgencyOrderID,
	})
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: item.UserID, Author: supportdomain.AuthorSystem,
		Body: "Order cancelled / 주문 취소 완료", AgencyOrderID: item.AgencyOrderID,
		IdempotencyKey: "support:order:cancellation:" + item.MerchantOrderID,
		Type:           supportdomain.CardOrderCancellation,
		ReferenceType:  supportdomain.ReferenceProcurement,
		ReferenceID:    item.MerchantOrderID,
		PublicPayload:  payload,
	})
	return err
}

// PublishOrderCancellationDeclined는 취소 거절 고지다(ADR-0070 D3). 거절 사유
// code는 리듀서 intent의 code(EFFECT_IN_PROGRESS·DELIVERED·MERCHANT_ORDER_UNKNOWN
// 등)이며 고객 화면이 어휘로 렌더한다.
func (p *procurementSupportConversation) PublishOrderCancellationDeclined(
	ctx context.Context,
	userID, agencyOrderID, merchantOrderID, idempotencyKey string,
	detail map[string]string,
) error {
	payload, _ := json.Marshal(map[string]any{
		"agencyOrderId":   agencyOrderID,
		"merchantOrderId": merchantOrderID,
		"kind":            detail["kind"],
		"code":            detail["code"],
		"state":           "DECLINED",
		"actionPath":      "/agencyOrder/" + agencyOrderID,
	})
	_, _, err := p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: userID, Author: supportdomain.AuthorSystem,
		Body: "Cancellation declined / 취소 요청 거절", AgencyOrderID: agencyOrderID,
		IdempotencyKey: idempotencyKey,
		Type:           supportdomain.CardOrderCancellationDeclined,
		ReferenceType:  supportdomain.ReferenceProcurement,
		ReferenceID:    merchantOrderID,
		PublicPayload:  payload,
	})
	return err
}

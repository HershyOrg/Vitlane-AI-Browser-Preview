package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

// paymentSupportConversation projects committed Payment facts into the one
// customer Messages conversation. It never calls PayPal or changes a money
// or dispute state.
type paymentSupportConversation struct {
	support  *supportapp.Service
	ordering *agencyorderapp.LifecycleService
}

func (p *paymentSupportConversation) PublishMOCompensation(
	ctx context.Context,
	item paymentapp.MOCompensationNotification,
) error {
	compensation := item.Compensation
	owner, err := p.ordering.ResolveOrderOwner(ctx, compensation.AgencyOrderID)
	if err != nil {
		return err
	}
	body := "Refund completed / 환불 완료"
	if compensation.Action == paymentdomain.MOCompensationVoid {
		body = "Cancellation completed / 취소 완료"
	}
	payload, _ := json.Marshal(map[string]any{
		"compensationId":      compensation.ID,
		"merchantOrderId":     item.MerchantOrderID,
		"state":               compensation.State,
		"action":              compensation.Action,
		"cause":               compensation.Cause,
		"amountMinor":         compensation.AmountMinor,
		"currency":            compensation.Currency,
		"paymentRail":         compensation.Rail,
		"providerEnvironment": compensation.ProviderEnvironment,
		"providerReference":   strings.TrimSpace(compensation.ProviderResourceID),
		"completedAt":         compensation.CompletedAt,
		"actionPath":          "/agencyOrder/" + compensation.AgencyOrderID,
	})
	_, _, err = p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: owner, Author: supportdomain.AuthorSystem,
		Body: body, AgencyOrderID: compensation.AgencyOrderID,
		IdempotencyKey: "support:mo-compensation:status:" + compensation.ID,
		Type:           supportdomain.CardRefundStatus,
		ReferenceType:  supportdomain.ReferenceRefund,
		ReferenceID:    compensation.ID, PublicPayload: payload,
	})
	return err
}

func (p *paymentSupportConversation) PublishPayPalDispute(
	ctx context.Context,
	item paymentdomain.PayPalDisputeCase,
) error {
	owner, err := p.ordering.ResolveOrderOwner(ctx, item.AgencyOrderID)
	if err != nil {
		return err
	}
	payload := disputePublicPayload(item)
	key := fmt.Sprintf("support:paypal-dispute:webhook:%s:%d", item.ID, item.Version)
	if item.State == paymentdomain.DisputeResolved {
		return p.publishDisputeResolution(ctx, owner, item, "", key,
			"PayPal dispute resolved / PayPal 분쟁 종결", payload)
	}
	cardType := supportdomain.CardPayPalDisputeStatus
	actionRequired := false
	if item.Version == 1 {
		cardType = supportdomain.CardPayPalDispute
		actionRequired = true
	}
	_, _, err = p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: owner, Author: supportdomain.AuthorSystem,
		Body:          "PayPal dispute update / PayPal 분쟁 업데이트",
		AgencyOrderID: item.AgencyOrderID, IdempotencyKey: key,
		Type: cardType, ReferenceType: supportdomain.ReferencePayPalDispute,
		ReferenceID: item.ID, ActionRequired: actionRequired, PublicPayload: payload,
	})
	return err
}

func (p *paymentSupportConversation) PublishPayPalDisputeAction(
	ctx context.Context,
	item paymentdomain.PayPalDisputeCase,
	action paymentdomain.PayPalDisputeManualAction,
) error {
	owner, err := p.ordering.ResolveOrderOwner(ctx, item.AgencyOrderID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"caseId": item.ID, "environment": item.Environment,
		"state": item.State, "providerStatus": item.ProviderStatus,
		"outcome": item.Outcome, "actionKind": action.ActionKind,
		"externalReference": action.ExternalReference,
		"publicRationale":   action.PublicRationale,
		"observedAt":        action.ObservedAt,
		"actionPath":        "/agencyOrder/" + item.AgencyOrderID,
	})
	key := "support:paypal-dispute:action:" + action.ID
	if item.State == paymentdomain.DisputeResolved {
		return p.publishDisputeResolution(ctx, owner, item, action.ActorUserID,
			key, action.PublicRationale, payload)
	}
	_, _, err = p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: owner, Author: supportdomain.AuthorOperator,
		ActorUserID: action.ActorUserID, Body: action.PublicRationale,
		AgencyOrderID: item.AgencyOrderID, IdempotencyKey: key,
		Type:          supportdomain.CardPayPalDisputeStatus,
		ReferenceType: supportdomain.ReferencePayPalDispute,
		ReferenceID:   item.ID, PublicPayload: payload,
	})
	return err
}

func (p *paymentSupportConversation) publishDisputeResolution(
	ctx context.Context,
	owner string,
	item paymentdomain.PayPalDisputeCase,
	actorUserID, idempotencyKey, body string,
	payload json.RawMessage,
) error {
	author := supportdomain.AuthorSystem
	if strings.TrimSpace(actorUserID) != "" {
		author = supportdomain.AuthorOperator
	}
	input := supportapp.BusinessCardInput{
		TargetUserID: owner, Author: author, ActorUserID: actorUserID,
		Body: body, AgencyOrderID: item.AgencyOrderID,
		IdempotencyKey: idempotencyKey,
		Type:           supportdomain.CardPayPalDisputeStatus,
		ReferenceType:  supportdomain.ReferencePayPalDispute,
		ReferenceID:    item.ID, PublicPayload: payload,
	}
	_, _, err := p.support.PublishBusinessCardResolution(ctx, input)
	if !errors.Is(err, supportdomain.ErrActionCardInvalid) {
		return err
	}
	// A resolved event may be the first observation. There is then no open card
	// to resolve, so append the terminal fact as a normal status card.
	_, _, err = p.support.PublishBusinessCard(ctx, input)
	return err
}

func disputePublicPayload(item paymentdomain.PayPalDisputeCase) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"caseId": item.ID, "environment": item.Environment,
		"state": item.State, "providerStatus": item.ProviderStatus,
		"outcome": item.Outcome, "reason": item.Reason,
		"lifecycleStage":      item.LifecycleStage,
		"sellerResponseDueAt": item.SellerResponseDueAt,
		"lastObservedAt":      item.LastObservedAt,
		"actionPath":          "/agencyOrder/" + item.AgencyOrderID,
	})
	return payload
}

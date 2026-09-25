package main

import (
	"context"
	"errors"
	"strconv"

	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	logisticsdomain "github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
)

// supportCardHandler는 SUPPORT executor다(ADR-0070 §4.5). 리듀서가 owner
// 이벤트에서 발행한 support.publish_card.v1 커맨드를 받아, 참조 id로 commit된
// owner 사실을 읽고 기존 어댑터의 카드 payload로 Support 대화에 붙인다.
// 카드 key(SupportKey)는 종전 발신 키와 같아 컷오버 전후로 중복이 없다.
//
// 사실이 없으면 REJECTED(FACT_NOT_FOUND)로 종결한다 — 재시도해도 생기지 않는
// 참조다(운영자 timeline에 남는다). 그 밖의 오류(Support 저장 실패 등)는
// backoff 재시도로 흐른다.
type supportCardHandler struct {
	inbox       procmsg.EffectInbox
	clock       sharedapp.Clock
	ordering    *agencyorderapp.LifecycleService
	procurement *procurementapp.Service
	logistics   *logisticsapp.Service
	payment     *paymentapp.Service
	agency      *agencyOrderSupportConversation
	procure     *procurementSupportConversation
	delivery    *logisticsSupportConversation
	money       *paymentSupportConversation
}

func newSupportCardHandler(
	support *supportapp.Service,
	ordering *agencyorderapp.LifecycleService,
	procurement *procurementapp.Service,
	logistics *logisticsapp.Service,
	payment *paymentapp.Service,
) supportCardHandler {
	return supportCardHandler{
		ordering: ordering, procurement: procurement, logistics: logistics, payment: payment,
		agency:   &agencyOrderSupportConversation{support: support},
		procure:  &procurementSupportConversation{support: support, ordering: ordering},
		delivery: &logisticsSupportConversation{support: support, ordering: ordering},
		money:    &paymentSupportConversation{support: support, ordering: ordering},
	}
}

func (h supportCardHandler) Accept(ctx context.Context, d procmsg.Delivery) error {
	if d.Effect.Target != string(procmsg.TargetSupport) || d.Effect.Type != procmsg.EffectPublishSupportCard {
		return procmsg.ErrEffectInvalid
	}
	return h.inbox.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
		payload, err := procmsg.ParsePayload[procmsg.PublishSupportCardPayload](e.Payload)
		if err != nil || payload.SupportKey == "" {
			return procmsg.ErrEffectInvalid
		}
		err = h.publish(tx, e.AgencyOrderID, payload)
		if err != nil {
			return err
		}
		return h.inbox.Report(tx, e, "SUCCEEDED", "", nil, "published", h.clock.Now())
	})
}

var errCardOwnerUnavailable = errors.New("support card owner product unavailable")

func isFactNotFound(err error) bool {
	return errors.Is(err, agencydomain.ErrNotFound) ||
		errors.Is(err, procurementdomain.ErrRequestNotFound) ||
		errors.Is(err, procurementdomain.ErrOrderNotFound) ||
		errors.Is(err, logisticsdomain.ErrResolutionNotFound) ||
		errors.Is(err, paymentdomain.ErrCompensationNotAvailable) ||
		errors.Is(err, paymentdomain.ErrDisputeNotFound)
}

func (h supportCardHandler) publish(
	ctx context.Context,
	agencyOrderID string,
	payload procmsg.PublishSupportCardPayload,
) error {
	switch payload.Card {
	case procmsg.SupportCardOrderCancellation:
		if h.procurement == nil {
			return errCardOwnerUnavailable
		}
		item, found, err := h.procurement.GetCancellationProjection(ctx, payload.MerchantOrderID)
		if err != nil {
			return err
		}
		if !found {
			return procurementdomain.ErrOrderNotFound
		}
		return h.procure.PublishOrderCancellation(ctx, item)
	case procmsg.SupportCardOrderCancellationDeclined:
		owner, err := h.ordering.ResolveOrderOwner(ctx, agencyOrderID)
		if err != nil {
			return err
		}
		return h.procure.PublishOrderCancellationDeclined(
			ctx, owner, agencyOrderID, payload.MerchantOrderID, payload.SupportKey, payload.Detail,
		)
	case procmsg.SupportCardRefundRequest:
		request, err := h.ordering.GetRefundRequestByID(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		return h.agency.PublishRefundRequest(ctx, request)
	case procmsg.SupportCardRefundDecision:
		request, err := h.ordering.GetRefundRequestByID(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		if request.State != agencydomain.RefundRequestResolved {
			return agencydomain.ErrNotFound
		}
		return h.agency.PublishRefundDecision(ctx, request, request.DecidedBy)
	case procmsg.SupportCardRefundStatus:
		if h.payment == nil {
			return errCardOwnerUnavailable
		}
		item, err := h.payment.GetMOCompensationNotification(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		if item.Compensation.State != paymentdomain.MOCompensationSucceeded {
			return paymentdomain.ErrCompensationNotAvailable
		}
		return h.money.PublishMOCompensation(ctx, item)
	case procmsg.SupportCardProcurementDecision:
		if h.procurement == nil {
			return errCardOwnerUnavailable
		}
		record, err := h.procurement.GetDecisionRecord(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		return h.procure.PublishProcurementDecision(ctx, record, record.DecidedByUserID)
	case procmsg.SupportCardProcurementRequest:
		if h.procurement == nil {
			return errCardOwnerUnavailable
		}
		request, err := h.procurement.GetCustomerRequest(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		return h.procure.PublishProcurementRequest(ctx, request, request.RequestedByUserID)
	case procmsg.SupportCardProcurementResponse:
		if h.procurement == nil {
			return errCardOwnerUnavailable
		}
		request, err := h.procurement.GetCustomerRequest(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		if request.State == procurementdomain.RequestPending {
			return procurementdomain.ErrRequestNotFound
		}
		return h.procure.PublishProcurementRequestResolution(ctx, request, request.ResolvedByUserID)
	case procmsg.SupportCardDeliveryResolution:
		if h.logistics == nil {
			return errCardOwnerUnavailable
		}
		item, err := h.logistics.GetDeliveryResolution(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		return h.delivery.PublishDeliveryResolution(ctx, item.Resolution, item.OperatorUserID)
	case procmsg.SupportCardDeliveryDelay:
		notice, err := h.ordering.GetDelayRuleNotice(ctx, agencyOrderID, payload.ReferenceID)
		if err != nil {
			return err
		}
		return h.agency.PublishDelayRuleNotice(
			ctx, notice.UserID, agencyOrderID, notice.IdempotencyKey, notice.CreatedAt,
		)
	case procmsg.SupportCardPayPalDispute:
		if h.payment == nil {
			return errCardOwnerUnavailable
		}
		item, err := h.payment.GetPayPalDisputeCaseByID(ctx, payload.ReferenceID)
		if err != nil {
			return err
		}
		if payload.ActionID != "" {
			action, err := h.payment.GetPayPalDisputeActionByID(ctx, payload.ActionID)
			if err != nil {
				return err
			}
			return h.money.PublishPayPalDisputeAction(ctx, item, action)
		}
		// 카드 key는 관찰된 webhook version에 묶인다 — 나중 webhook이 행 version을
		// 올렸어도 이 커맨드의 카드는 자기 관찰 세대의 key로 붙는다.
		if version, err := strconv.ParseInt(payload.Detail["version"], 10, 64); err == nil && version > 0 {
			item.Version = version
		}
		return h.money.PublishPayPalDispute(ctx, item)
	default:
		return errCardOwnerUnavailable
	}
}

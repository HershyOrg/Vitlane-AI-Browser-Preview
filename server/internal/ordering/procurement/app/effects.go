package app

import (
	"context"
	"errors"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

type EffectConsumer struct {
	service *Service
	inbox   procmsg.EffectInbox
}

func NewEffectConsumer(service *Service, inbox procmsg.EffectInbox) *EffectConsumer {
	return &EffectConsumer{service: service, inbox: inbox}
}

func (c *EffectConsumer) Accept(ctx context.Context, delivery procmsg.Delivery) error {
	if delivery.Effect.Target != string(procmsg.TargetProcurement) {
		return procmsg.ErrEffectInvalid
	}
	report := func(tx context.Context, e procmsg.ProcessEffect, outcome, code string) (procmsg.Disposition, error) {
		if err := c.inbox.Report(tx, e, outcome, code, nil, outcome+":"+code, c.service.clock.Now()); err != nil {
			return procmsg.KeepClaim, err
		}
		if outcome == "WAITING" {
			return procmsg.RetryDelivery, nil
		}
		return procmsg.Consumed, nil
	}
	err := c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		outcome, code, err := c.apply(tx, e)
		if err != nil {
			return procmsg.KeepClaim, err
		}
		return report(tx, e, outcome, code)
	})
	if err == nil {
		return nil
	}
	// Roll back the refused attempt before publishing a typed refusal. A nested
	// Owner method may have changed local rows before detecting a business guard.
	var outcome, code string
	switch {
	case errors.Is(err, domain.ErrAssignmentRequired), errors.Is(err, domain.ErrAssignmentConflict), errors.Is(err, domain.ErrLeaseExpired):
		outcome, code = "WAITING", "ASSIGNMENT_REQUIRED"
	case errors.Is(err, domain.ErrLiveModeClosed):
		outcome, code = "WAITING", "MERCHANT_GATE_CLOSED"
	case errors.Is(err, domain.ErrEvidenceInvalid), errors.Is(err, domain.ErrPIIAccessDenied), errors.Is(err, domain.ErrContinueURLGone), errors.Is(err, domain.ErrDecisionInvalid), errors.Is(err, domain.ErrRequestInvalid), errors.Is(err, domain.ErrRequestResponseInvalid), errors.Is(err, domain.ErrRequestStateConflict), errors.Is(err, domain.ErrResultInvalid), errors.Is(err, domain.ErrNoResponseTooEarly),
		errors.Is(err, domain.ErrManualDecisionRequired), errors.Is(err, domain.ErrAuthorizationMissing), errors.Is(err, domain.ErrOpenCustomerRequest), errors.Is(err, domain.ErrTaskStateInvalid), errors.Is(err, domain.ErrOrderInException), errors.Is(err, domain.ErrEffectAlreadyStarted):
		outcome, code = "REJECTED", err.Error()
	default:
		return err
	}
	if outcome == "WAITING" && delivery.Effect.Type == procmsg.EffectApplyOwnerAction {
		outcome, code = "REJECTED", err.Error()
	}
	return c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		return report(tx, e, outcome, code)
	})
}

func (c *EffectConsumer) apply(tx context.Context, e procmsg.ProcessEffect) (string, string, error) {
	s := c.service
	if e.Type == procmsg.EffectApplyOwnerAction {
		return c.applyAction(tx, e)
	}
	if e.Type == procmsg.EffectPlanMerchantOrders {
		p, err := procmsg.ParsePayload[procmsg.PlanFromFundingPayload](e.Payload)
		if err != nil {
			return "", "", procmsg.ErrEffectInvalid
		}
		err = s.PlanFromFunding(tx, e.AgencyOrderID, p)
		if errors.Is(err, domain.ErrPlanNotEligible) {
			return "REJECTED", "PLAN_NOT_ELIGIBLE", nil
		}
		return "SUCCEEDED", "", err
	}
	if e.Type == procmsg.EffectCancelPrePurchase || e.Type == procmsg.EffectCancelByDelayRule {
		p, err := procmsg.ParsePayload[procmsg.CancellationContext](e.Payload)
		if err != nil || p.MerchantOrderID != e.MerchantOrderID || p.AgencyOrderID != e.AgencyOrderID || p.ID != e.RequestID || p.ReservationID == "" {
			return "", "", procmsg.ErrEffectInvalid
		}
		_, err = s.ExecuteCancellation(tx, e.AgencyOrderID, e.MerchantOrderID, p.ActorID, p.CancelKind, CancellationAuthority{UserID: p.UserID, IssuedAt: p.IssuedAt, FundingState: p.FundingState, UndeliveredUnits: p.UndeliveredUnits})
		if errors.Is(err, domain.ErrCancelNotEligible) {
			return "REJECTED", "CANCEL_NOT_AVAILABLE", nil
		}
		return "SUCCEEDED", "", err
	}
	p, err := procmsg.ParsePayload[procmsg.PurchaseContext](e.Payload)
	if err != nil || p.MerchantOrderID != e.MerchantOrderID || p.RequestID != e.RequestID {
		return "", "", procmsg.ErrEffectInvalid
	}
	subject, err := s.LockPurchaseSubject(tx, p.TaskID)
	if err != nil {
		return "", "", err
	}
	if subject.MerchantOrder.ID != e.MerchantOrderID || subject.MerchantOrder.AgencyOrderID != e.AgencyOrderID || subject.MerchantOrder.AllocationID != p.AllocationID {
		return "", "", procmsg.ErrEffectInvalid
	}
	authority := PurchasePreparation{AuthorizationKind: p.AuthorizationKind, AuthorizationHash: p.AuthorizationHash, ExecutionProfileHash: p.ExecutionProfileHash, ExecutionMode: p.ExecutionMode, FundingPositionID: p.FundingPositionID, FundingState: p.FundingState}
	switch e.Type {
	case procmsg.EffectReservePurchase:
		_, _, err = s.PreparePurchase(tx, p.TaskID, p.ActorID, e.FlowID, authority)
	case procmsg.EffectGrantMerchantPurchase:
		// An expired operator lease does not release purchase permission. The
		// current active assignee takes over the same reserved MO and funding ID.
		actor := subject.Task.AssignedOperatorUserID
		if actor == "" {
			return "WAITING", "ASSIGNMENT_REQUIRED", nil
		}
		var result QueueItem
		result, _, err = s.AuthorizeMerchant(tx, p.TaskID, actor, e.FlowID, authority)
		if err == nil && result.MerchantOrder.State != domain.OrderPlacementPending && result.MerchantOrder.State != domain.OrderPlaced {
			return "REJECTED", "MERCHANT_PERMISSION_CLOSED", nil
		}
	case procmsg.EffectReleasePurchase:
		repo, ok := s.repository.(interface {
			ReleasePurchase(context.Context, string, string, string, time.Time) error
		})
		if !ok {
			return "", "", domain.ErrManualReviewUnavailable
		}
		err = repo.ReleasePurchase(tx, e.AgencyOrderID, e.MerchantOrderID, e.FlowID, s.clock.Now())
	default:
		return "", "", procmsg.ErrEffectInvalid
	}
	return "SUCCEEDED", "", err
}

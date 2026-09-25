package app

import (
	"context"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

type EffectConsumer struct {
	service *Service
	inbox   procmsg.EffectInbox
}

func NewEffectConsumer(service *Service, inbox procmsg.EffectInbox) *EffectConsumer {
	return &EffectConsumer{service, inbox}
}

type manifestRepository interface {
	RegisterExpectedUnitManifest(context.Context, string, procmsg.RegisterExpectedUnitsPayload, time.Time) error
}

var ErrCancellationNotEligible = errors.New("CANCELLATION_NOT_ELIGIBLE")

type cancellationReservationRepository interface {
	ReserveCancellation(context.Context, procmsg.ProcessEffect, procmsg.CancellationContext, time.Time) (procmsg.CancellationReservation, error)
	ResolveCancellation(context.Context, procmsg.ProcessEffect, procmsg.CancellationContext, bool, time.Time) error
}

func (c *EffectConsumer) Accept(ctx context.Context, delivery procmsg.Delivery) error {
	if delivery.Effect.Target != string(procmsg.TargetLogistics) {
		return procmsg.ErrEffectInvalid
	}
	err := c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		outcome, code := "SUCCEEDED", ""
		var result any
		switch e.Type {
		case procmsg.EffectApplyOwnerAction:
			if err := c.applyAction(tx, e); err != nil {
				return procmsg.KeepClaim, err
			}
		case procmsg.EffectRegisterExpectedUnits:
			p, err := procmsg.ParsePayload[procmsg.RegisterExpectedUnitsPayload](e.Payload)
			if err != nil || p.MerchantOrderID != e.MerchantOrderID {
				return procmsg.KeepClaim, procmsg.ErrEffectInvalid
			}
			repo, ok := c.service.repository.(manifestRepository)
			if !ok {
				return procmsg.KeepClaim, ErrPurchaseRegistrationMissing
			}
			if err := repo.RegisterExpectedUnitManifest(tx, e.AgencyOrderID, p, c.service.clock.Now()); err != nil {
				return procmsg.KeepClaim, err
			}
		case procmsg.EffectConfirmPurchaseRegistration:
			p, err := procmsg.ParsePayload[procmsg.PurchaseContext](e.Payload)
			if err != nil || p.MerchantOrderID != e.MerchantOrderID || p.RequestID != e.RequestID {
				return procmsg.KeepClaim, procmsg.ErrEffectInvalid
			}
			err = c.service.RequirePurchaseRegistration(tx, e.AgencyOrderID, e.MerchantOrderID, p.UnitIDs)
			if errors.Is(err, ErrPurchaseRegistrationMissing) {
				outcome, code = "WAITING", "LOGISTICS_REGISTRATION_REQUIRED"
			} else if err != nil {
				return procmsg.KeepClaim, err
			}
		case procmsg.EffectReserveCancellation, procmsg.EffectReleaseCancellation, procmsg.EffectApplyLogisticsCancellation:
			p, err := procmsg.ParsePayload[procmsg.CancellationContext](e.Payload)
			if err != nil || p.MerchantOrderID != e.MerchantOrderID || p.ID != e.RequestID {
				return procmsg.KeepClaim, procmsg.ErrEffectInvalid
			}
			repo, ok := c.service.repository.(cancellationReservationRepository)
			if !ok {
				return procmsg.KeepClaim, ErrPurchaseRegistrationMissing
			}
			if e.Type == procmsg.EffectReserveCancellation {
				result, err = repo.ReserveCancellation(tx, e, p, c.service.clock.Now())
				if errors.Is(err, ErrCancellationNotEligible) {
					outcome, code, err = "REJECTED", "DELIVERED", nil
				}
				if errors.Is(err, ErrPurchaseRegistrationMissing) {
					outcome, code, err = "WAITING", "LOGISTICS_REGISTRATION_REQUIRED", nil
				}
			} else {
				err = repo.ResolveCancellation(tx, e, p, e.Type == procmsg.EffectApplyLogisticsCancellation, c.service.clock.Now())
			}
			if err != nil {
				return procmsg.KeepClaim, err
			}
		default:
			return procmsg.KeepClaim, procmsg.ErrEffectInvalid
		}
		if err := c.inbox.Report(tx, e, outcome, code, result, outcome+":"+code, c.service.clock.Now()); err != nil {
			return procmsg.KeepClaim, err
		}
		if outcome == "WAITING" {
			return procmsg.RetryDelivery, nil
		}
		return procmsg.Consumed, nil
	})
	if err != nil && delivery.Effect.Type == procmsg.EffectApplyOwnerAction {
		switch {
		case errors.Is(err, domain.ErrShipmentNotFound), errors.Is(err, domain.ErrShipmentInvalid), errors.Is(err, domain.ErrUnitsNotAllocable), errors.Is(err, domain.ErrTransitionInvalid), errors.Is(err, domain.ErrEvidenceDuplicate), errors.Is(err, domain.ErrUnitNotFound), errors.Is(err, domain.ErrResolutionInvalid), errors.Is(err, domain.ErrReturnInvalid):
			return c.inbox.Consume(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) error {
				return c.inbox.Report(tx, e, "REJECTED", err.Error(), nil, "rejected:"+err.Error(), c.service.clock.Now())
			})
		}
	}
	return err
}

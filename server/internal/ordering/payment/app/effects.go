package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

// EffectConsumer owns Payment's execution protocol. Process only supplies an
// immutable authorization. Provider operations, sender fencing, reconciliation
// and result verification remain private to Payment.
type EffectConsumer struct {
	service *Service
	inbox   procmsg.EffectInbox
}

func NewEffectConsumer(service *Service, inbox procmsg.EffectInbox) *EffectConsumer {
	return &EffectConsumer{service: service, inbox: inbox}
}

func (c *EffectConsumer) Accept(ctx context.Context, delivery procmsg.Delivery) error {
	if delivery.Effect.Target != string(procmsg.TargetPayment) {
		return procmsg.ErrEffectInvalid
	}
	switch delivery.Effect.Type {
	case procmsg.EffectConfirmInstruction:
		return c.inbox.Consume(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) error {
			value, err := procmsg.ParsePayload[procmsg.InstructionConfirmation](e.Payload)
			if err != nil || value.Claim.AgencyOrderID != e.AgencyOrderID {
				return procmsg.ErrEffectInvalid
			}
			gate, ok := c.service.instructions.(interface {
				RecordInstructionConfirmation(context.Context, procmsg.InstructionConfirmation) error
			})
			if !ok {
				return procmsg.ErrEffectInvalid
			}
			if err := gate.RecordInstructionConfirmation(tx, value); err != nil {
				return err
			}
			_, err = c.report(tx, e, "SUCCEEDED", "", nil)
			return err
		})
	case procmsg.EffectEnsureMOFunding:
		return c.ensureFunding(ctx, delivery)
	case procmsg.EffectCompensateMO:
		return c.compensate(ctx, delivery)
	default:
		return procmsg.ErrEffectInvalid
	}
}

func (c *EffectConsumer) report(tx context.Context, e procmsg.ProcessEffect, outcome, code string, result any) (procmsg.Disposition, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return procmsg.KeepClaim, err
	}
	err = c.inbox.Report(tx, e, outcome, code, result, outcome+":"+code+":"+procmsg.PayloadHash(raw), c.service.clock.Now())
	if outcome == "SUCCEEDED" || outcome == "REJECTED" {
		return procmsg.Consumed, err
	}
	return procmsg.RetryDelivery, err
}

func (c *EffectConsumer) ensureFunding(ctx context.Context, delivery procmsg.Delivery) error {
	var authorization json.RawMessage
	err := c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		p, err := procmsg.ParsePayload[procmsg.PurchaseContext](e.Payload)
		if err != nil || p.MerchantOrderID != e.MerchantOrderID || p.RequestID != e.RequestID {
			return procmsg.KeepClaim, procmsg.ErrEffectInvalid
		}
		facts, err := c.service.FundingFactsForEffect(tx, e.AgencyOrderID, e.MerchantOrderID, p.AllocationID)
		if err != nil {
			return procmsg.KeepClaim, err
		}
		if facts.PositionID != p.FundingPositionID {
			return procmsg.KeepClaim, domain.ErrInstructionMismatch
		}
		if facts.HasCompensation {
			return c.report(tx, e, "REJECTED", "COMPENSATION_ALREADY_REQUESTED", nil)
		}
		if facts.State == "ACTIVE" {
			return c.report(tx, e, "SUCCEEDED", "", procmsg.FundingResult{PositionID: facts.PositionID, State: facts.State})
		}
		if facts.State == "FAILED" || facts.State == "RELEASED" {
			return c.report(tx, e, "REJECTED", "FUNDING_NOT_AVAILABLE", procmsg.FundingResult{PositionID: facts.PositionID, State: facts.State})
		}
		authorization, err = c.service.prepareAuthorization(tx, e.MerchantOrderID)
		return procmsg.KeepClaim, err
	})
	if err != nil || authorization == nil {
		return err
	}
	observation, err := c.service.executeAuthorization(procmsg.EffectContext(ctx, delivery.Effect), authorization)
	if err != nil {
		return err
	}
	var funding json.RawMessage
	resolveAuthorization := func(tx context.Context, e procmsg.ProcessEffect, mayContinue bool) (procmsg.Disposition, error) {
		state, err := c.service.resolveAuthorization(tx, authorization, observation)
		if err != nil {
			return procmsg.KeepClaim, err
		}
		switch state {
		case "READY":
			if !mayContinue {
				return c.report(tx, e, "WAITING", "AUTHORIZATION_CONFIRMED", nil)
			}
			funding, err = c.service.prepareFunding(tx, e.MerchantOrderID)
			return procmsg.KeepClaim, err
		case "FAILED":
			return c.report(tx, e, "REJECTED", "AUTHORIZATION_REJECTED", nil)
		case "UNKNOWN":
			return c.report(tx, e, "EFFECT_UNKNOWN", "AUTHORIZATION_RESULT_REQUIRED", nil)
		default:
			return c.report(tx, e, "WAITING", state, nil)
		}
	}
	err = c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		return resolveAuthorization(tx, e, true)
	})
	if errors.Is(err, procmsg.ErrStaleDelivery) {
		return c.inbox.Observe(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) error {
			_, err := resolveAuthorization(tx, e, false)
			return err
		})
	}
	if err != nil || funding == nil {
		return err
	}
	observedFunding, err := c.service.executeFunding(procmsg.EffectContext(ctx, delivery.Effect), funding)
	if err != nil {
		return err
	}
	resolveFunding := func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		result, err := c.service.resolveFunding(tx, funding, observedFunding)
		if err != nil {
			return procmsg.KeepClaim, err
		}
		fact := procmsg.FundingResult{PositionID: result.Position.ID, State: string(result.Position.State)}
		switch result.Position.State {
		case domain.MOFundingActive:
			return c.report(tx, e, "SUCCEEDED", "", fact)
		case domain.MOFundingFailed:
			return c.report(tx, e, "REJECTED", "FUNDING_ACTIVATION_FAILED", fact)
		case domain.MOFundingActivationUnknown:
			return c.report(tx, e, "EFFECT_UNKNOWN", "FUNDING_RESULT_REQUIRED", fact)
		default:
			var observed fundingObservation
			if err := json.Unmarshal(observedFunding, &observed); err != nil {
				return procmsg.KeepClaim, err
			}
			if observed.Reason == "MONEY_GATE_CLOSED" {
				return c.report(tx, e, "WAITING", observed.Reason, fact)
			}
			return c.report(tx, e, "WAITING", "FUNDING_RESULT_REQUIRED", fact)
		}
	}
	err = c.inbox.WithDelivery(ctx, delivery, resolveFunding)
	if errors.Is(err, procmsg.ErrStaleDelivery) {
		return c.inbox.Observe(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) error { _, err := resolveFunding(tx, e); return err })
	}
	return err
}

func (c *EffectConsumer) compensate(ctx context.Context, delivery procmsg.Delivery) error {
	var prepared json.RawMessage
	err := c.inbox.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		p, err := procmsg.ParsePayload[procmsg.ExecuteMOCompensationPayload](e.Payload)
		if err != nil || p.MerchantOrderID != e.MerchantOrderID {
			return procmsg.KeepClaim, procmsg.ErrEffectInvalid
		}
		if _, err = c.service.FundingFactsForEffect(tx, e.AgencyOrderID, e.MerchantOrderID, p.AllocationID); err != nil {
			return procmsg.KeepClaim, err
		}
		prepared, err = c.service.prepareCompensation(tx, MOCompensationRequest{AgencyOrderID: e.AgencyOrderID, MerchantOrderID: e.MerchantOrderID, AllocationID: p.AllocationID, Cause: domain.MOCompensationCause(p.Cause), IdempotencyKey: e.IdempotencyKey})
		return procmsg.KeepClaim, err
	})
	if err != nil || prepared == nil {
		return err
	}
	observed, err := c.service.executeCompensation(procmsg.EffectContext(ctx, delivery.Effect), prepared)
	if err != nil {
		return err
	}
	resolve := func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		result, err := c.service.resolveCompensation(tx, prepared, observed)
		if err != nil {
			return procmsg.KeepClaim, err
		}
		// A failed compensation attempt does not erase the obligation to refund.
		switch result.State {
		case domain.MOCompensationSucceeded:
			return c.report(tx, e, "SUCCEEDED", "", nil)
		case domain.MOCompensationFailed:
			return c.report(tx, e, "ATTENTION_REQUIRED", "COMPENSATION_ATTEMPT_FAILED", nil)
		case domain.MOCompensationOutcomeUnknown:
			return c.report(tx, e, "EFFECT_UNKNOWN", "COMPENSATION_RESULT_REQUIRED", nil)
		default:
			return c.report(tx, e, "WAITING", "COMPENSATION_RESULT_REQUIRED", nil)
		}
	}
	err = c.inbox.WithDelivery(ctx, delivery, resolve)
	if errors.Is(err, procmsg.ErrStaleDelivery) {
		return c.inbox.Observe(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) error { _, err := resolve(tx, e); return err })
	}
	return err
}

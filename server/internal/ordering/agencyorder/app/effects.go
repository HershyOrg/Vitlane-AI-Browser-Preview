package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

type RefundInput struct {
	ReasonCode      string `json:"reasonCode"`
	PublicRationale string `json:"publicRationale"`
}

func (s *LifecycleService) EnableProcessInputs(inputs procmsg.ActionInputs) { s.inputs = inputs }
func (s *LifecycleService) StageAction(ctx context.Context, r procmsg.ActionRequest, input any) (procmsg.ActionRequest, error) {
	if s.inputs == nil {
		return r, procmsg.ErrRequestInvalid
	}
	switch r.Kind {
	case procmsg.RequestRefund:
		owner, err := s.ResolveOrderOwner(ctx, r.AgencyOrderID)
		if err != nil {
			return r, err
		}
		if owner != r.ActorID {
			return r, domain.ErrNotFound
		}
	case procmsg.RequestRefundDecision:
		original, err := s.GetRefundRequestByID(ctx, r.ReferenceID)
		if err != nil {
			return r, err
		}
		r.AgencyOrderID, r.MerchantOrderID, r.AllocationID = original.AgencyOrderID, original.MerchantOrderID, original.AllocationID
	default:
		return r, procmsg.ErrRequestInvalid
	}
	return s.inputs.Stage(ctx, r, input)
}

type EffectConsumer struct {
	service *LifecycleService
	inbox   procmsg.EffectInbox
}

func NewEffectConsumer(s *LifecycleService, inbox procmsg.EffectInbox) *EffectConsumer {
	return &EffectConsumer{s, inbox}
}
func (c *EffectConsumer) Accept(ctx context.Context, d procmsg.Delivery) error {
	if d.Effect.Target != string(procmsg.TargetAgencyOrder) {
		return procmsg.ErrEffectInvalid
	}
	if d.Effect.Type == procmsg.EffectConsumeInstruction {
		return c.inbox.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
			claim, err := procmsg.ParsePayload[procmsg.InstructionClaim](e.Payload)
			if err != nil || claim.Validate() != nil || claim.AgencyOrderID != e.AgencyOrderID {
				return procmsg.ErrEffectInvalid
			}
			owner, ok := c.service.repository.(interface {
				ConsumeInstructionClaim(context.Context, procmsg.InstructionClaim) (bool, error)
			})
			if !ok {
				return procmsg.ErrEffectInvalid
			}
			confirmed, err := owner.ConsumeInstructionClaim(tx, claim)
			if err != nil {
				return err
			}
			value := procmsg.InstructionConfirmation{Claim: claim, Confirmed: confirmed}
			if !confirmed {
				value.Reason = "PAYMENT_INSTRUCTION_NOT_CONSUMABLE"
			}
			return c.inbox.Report(tx, e, "SUCCEEDED", "", value, "confirmed", c.service.clock.Now())
		})
	}
	report := func(tx context.Context, e procmsg.ProcessEffect, outcome, code string) error {
		return c.inbox.Report(tx, e, outcome, code, nil, outcome+":"+code, c.service.clock.Now())
	}
	err := c.inbox.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
		if err := c.apply(tx, e); err != nil {
			return err
		}
		return report(tx, e, "SUCCEEDED", "")
	})
	if errors.Is(err, domain.ErrRefundRequestInvalid) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrStateInvalid) {
		return c.inbox.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error { return report(tx, e, "REJECTED", err.Error()) })
	}
	return err
}
func (c *EffectConsumer) apply(ctx context.Context, e procmsg.ProcessEffect) error {
	s := c.service
	switch e.Type {
	case procmsg.EffectIssueReceipt:
		return s.IssueReceipt(ctx, e.AgencyOrderID)
	case procmsg.EffectSendNotice:
		p, err := procmsg.ParsePayload[procmsg.SendNoticePayload](e.Payload)
		if err != nil || p.Kind != procmsg.NoticeKindDelayRule {
			return procmsg.ErrEffectInvalid
		}
		return s.SendDelayRuleNotice(ctx, e.AgencyOrderID, p.IdempotencyKey)
	case procmsg.EffectApplyOwnerAction:
		p, err := procmsg.ParsePayload[procmsg.OwnerActionContext](e.Payload)
		if err != nil || p.ID != e.RequestID || p.AgencyOrderID != e.AgencyOrderID || p.MerchantOrderID != e.MerchantOrderID || s.inputs == nil {
			return procmsg.ErrEffectInvalid
		}
		raw, err := s.inputs.Load(ctx, p.ActionRequest)
		if err != nil {
			return err
		}
		switch p.Kind {
		case procmsg.RequestRefund:
			var in RefundInput
			if json.Unmarshal(raw, &in) != nil {
				return procmsg.ErrEffectInvalid
			}
			_, err = s.RequestRefund(ctx, p.ActorID, p.AgencyOrderID, p.MerchantOrderID, in.ReasonCode, in.PublicRationale, RefundAuthority{AllocationID: p.AllocationID, MerchantState: p.MerchantState, FundingState: p.FundingState, HasCompensation: p.HasCompensation})
		case procmsg.RequestRefundDecision:
			var in RefundDecision
			if json.Unmarshal(raw, &in) != nil {
				return procmsg.ErrEffectInvalid
			}
			original, loadErr := s.GetRefundRequestByID(ctx, p.ReferenceID)
			if loadErr != nil {
				return loadErr
			}
			if original.AgencyOrderID != e.AgencyOrderID || original.MerchantOrderID != e.MerchantOrderID {
				return procmsg.ErrEffectInvalid
			}
			_, err = s.DecideRefund(ctx, p.ReferenceID, p.ActorID, in)
		default:
			return procmsg.ErrEffectInvalid
		}
		return err
	default:
		return procmsg.ErrEffectInvalid
	}
}

package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

func (s *Service) EnableProcessInputs(inputs procmsg.ActionInputs) { s.inputs = inputs }

// StageAction resolves the exact Owner resource and stores private input before
// the authenticated request is submitted to Process. It does not execute it.
func (s *Service) StageAction(ctx context.Context, r procmsg.ActionRequest, input any) (procmsg.ActionRequest, error) {
	if s.inputs == nil {
		return r, domain.ErrManualReviewUnavailable
	}
	if r.ReferenceID != "" && (r.Kind == procmsg.RequestCustomerResponse || r.Kind == procmsg.RequestCloseCustomerQuestion) {
		question, err := s.GetCustomerRequest(ctx, r.ReferenceID)
		if err != nil {
			return r, err
		}
		if r.ActorRole == "CUSTOMER" && question.UserID != r.ActorID {
			return r, domain.ErrOrderNotFound
		}
		r.AgencyOrderID, r.MerchantOrderID = question.AgencyOrderID, question.MerchantOrderID
	}
	var subject QueueItem
	var err error
	if r.TaskID != "" {
		subject, err = s.PurchaseSubject(ctx, r.TaskID)
	} else {
		subject, err = s.PurchaseSubjectByMO(ctx, r.MerchantOrderID)
	}
	if err != nil {
		return r, err
	}
	if r.AgencyOrderID != "" && r.AgencyOrderID != subject.MerchantOrder.AgencyOrderID {
		return r, domain.ErrOrderNotFound
	}
	if r.MerchantOrderID != "" && r.MerchantOrderID != subject.MerchantOrder.ID {
		return r, domain.ErrOrderNotFound
	}
	r.AgencyOrderID, r.MerchantOrderID, r.AllocationID, r.TaskID = subject.MerchantOrder.AgencyOrderID, subject.MerchantOrder.ID, subject.MerchantOrder.AllocationID, subject.Task.ID
	if decision, ok := input.(RecordManualDecisionInput); ok {
		r.Decision = string(decision.Decision)
	}
	return s.inputs.Stage(ctx, r, input)
}

type ProcessCustomerResponse struct {
	ExpectedVersion int64           `json:"expectedVersion"`
	Decline         bool            `json:"decline"`
	Response        json.RawMessage `json:"response"`
}
type ProcessCloseQuestion struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	Cancel          bool   `json:"cancel"`
	Reason          string `json:"reason"`
}
type ProcessPlacementInput struct {
	Done              bool                         `json:"done"`
	FailureCode       string                       `json:"failureCode"`
	EvidenceKind      domain.PlacementEvidenceKind `json:"evidenceKind"`
	AmountMode        domain.PlacementAmountMode   `json:"amountMode"`
	ExternalOrderRef  string                       `json:"externalOrderRef"`
	ReceiptSafeRef    string                       `json:"receiptSafeRef"`
	ActualAmountMinor int64                        `json:"actualAmountMinor"`
	EvidenceSource    domain.EvidenceSource        `json:"evidenceSource"`
}
type ProcessRevealInput struct {
	ReasonCode    string `json:"reasonCode"`
	ReasonDetail  string `json:"reasonDetail"`
	CorrelationID string `json:"correlationId"`
}

func (c *EffectConsumer) applyAction(ctx context.Context, e procmsg.ProcessEffect) (string, string, error) {
	s := c.service
	r, err := procmsg.ParsePayload[procmsg.ActionRequest](e.Payload)
	if err != nil || r.ID != e.RequestID || r.MerchantOrderID != e.MerchantOrderID || r.AgencyOrderID != e.AgencyOrderID || s.inputs == nil {
		return "", "", procmsg.ErrEffectInvalid
	}
	raw, err := s.inputs.Load(ctx, r)
	if err != nil {
		return "", "", err
	}
	key := "process-action:" + r.InputRef
	switch r.Kind {
	case procmsg.RequestClaimTask:
		_, _, err = s.Claim(ctx, r.TaskID, r.ActorID, key)
	case procmsg.RequestManualDecision:
		var p RecordManualDecisionInput
		if err = json.Unmarshal(raw, &p); err == nil {
			if string(p.Decision) != r.Decision {
				return "", "", procmsg.ErrEffectInvalid
			}
			_, _, err = s.RecordManualDecision(ctx, r.TaskID, r.ActorID, key, p)
		}
	case procmsg.RequestCustomerQuestion:
		var p CreateCustomerRequestInput
		if err = json.Unmarshal(raw, &p); err == nil {
			_, _, err = s.CreateCustomerRequest(ctx, r.TaskID, r.ActorID, key, p)
		}
	case procmsg.RequestCustomerResponse:
		var p ProcessCustomerResponse
		if err = json.Unmarshal(raw, &p); err == nil {
			_, _, err = s.RespondToCustomerRequest(ctx, r.ReferenceID, r.ActorID, key, p.ExpectedVersion, p.Response, p.Decline)
		}
	case procmsg.RequestCloseCustomerQuestion:
		var p ProcessCloseQuestion
		if err = json.Unmarshal(raw, &p); err == nil {
			_, _, err = s.ResolveCustomerRequestWithoutResponse(ctx, r.ReferenceID, r.ActorID, key, p.ExpectedVersion, p.Cancel, p.Reason)
		}
	case procmsg.RequestPlacementEvidence, procmsg.RequestFailureEvidence:
		var p ProcessPlacementInput
		if err = json.Unmarshal(raw, &p); err == nil {
			if p.Done != (r.Kind == procmsg.RequestPlacementEvidence) {
				return "", "", procmsg.ErrEffectInvalid
			}
			_, _, err = s.RecordResult(ctx, r.TaskID, r.ActorID, key, p.Done, p.FailureCode, domain.PlacementEvidence{Kind: p.EvidenceKind, AmountMode: p.AmountMode, ExternalOrderRef: p.ExternalOrderRef, ReceiptSafeRef: p.ReceiptSafeRef, ActualAmountMinor: p.ActualAmountMinor, Currency: "USD", EvidenceSource: p.EvidenceSource, ClaimsExternalLive: p.EvidenceKind == domain.PlacementEvidenceLiveEffect})
		}
	case procmsg.RequestRevealShipping, procmsg.RequestRevealCheckout:
		var p ProcessRevealInput
		if err = json.Unmarshal(raw, &p); err == nil {
			if domain.ValidatePIIReveal(p.ReasonCode, p.ReasonDetail) != nil || p.CorrelationID == "" {
				return "", "", domain.ErrPIIAccessDenied
			}
			if r.Kind == procmsg.RequestRevealShipping {
				_, _, err = s.repository.AuthorizeShippingReveal(ctx, r.TaskID, r.ActorID, p.ReasonCode, p.ReasonDetail, p.CorrelationID, key, s.clock.Now())
			} else {
				_, _, err = s.repository.AuthorizeContinueURLReveal(ctx, r.TaskID, r.ActorID, p.ReasonCode, p.ReasonDetail, p.CorrelationID, key, s.clock.Now())
			}
		}
	default:
		return "", "", procmsg.ErrEffectInvalid
	}
	if errors.Is(err, domain.ErrPIIAccessDenied) && (r.Kind == procmsg.RequestRevealShipping || r.Kind == procmsg.RequestRevealCheckout) {
		return "REJECTED", err.Error(), nil
	}
	return "SUCCEEDED", "", err
}

// RevealResult reads a completed grant's private value. Assignment, lease,
// reference expiry and the original audited reason are rechecked at read time.
func (s *Service) RevealResult(ctx context.Context, task, actor, key string) (map[string]any, error) {
	lookup, ok := s.inputs.(interface {
		Lookup(context.Context, string, string, string) (procmsg.ActionRequest, error)
	})
	if !ok {
		return nil, domain.ErrPIIAccessDenied
	}
	subject, err := s.PurchaseSubject(ctx, task)
	if err != nil {
		return nil, err
	}
	r, err := lookup.Lookup(ctx, subject.MerchantOrder.AgencyOrderID, key, actor)
	if err != nil || r.TaskID != task {
		return nil, domain.ErrPIIAccessDenied
	}
	raw, err := s.inputs.Load(ctx, r)
	if err != nil {
		return nil, err
	}
	var p ProcessRevealInput
	if json.Unmarshal(raw, &p) != nil {
		return nil, domain.ErrPIIAccessDenied
	}
	switch r.Kind {
	case procmsg.RequestRevealShipping:
		value, _, err := s.RevealShipping(ctx, task, actor, p.ReasonCode, p.ReasonDetail, p.CorrelationID, "process-action:"+r.InputRef)
		return map[string]any{"shippingAddress": value}, err
	case procmsg.RequestRevealCheckout:
		value, hash, _, err := s.RevealContinueURL(ctx, task, actor, p.ReasonCode, p.ReasonDetail, p.CorrelationID, "process-action:"+r.InputRef)
		return map[string]any{"continueUrl": value, "continueUrlHash": hash}, err
	default:
		return nil, domain.ErrPIIAccessDenied
	}
}

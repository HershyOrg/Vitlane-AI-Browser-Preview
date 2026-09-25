package app

import (
	"context"
	"encoding/json"
	"errors"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

type compensationPrepared struct {
	Request   MOCompensationRequest   `json:"request"`
	Execution MOCompensationExecution `json:"execution"`
}
type compensationObservation struct {
	Refund     *paypal.Refund             `json:"refund,omitempty"`
	ResourceID string                     `json:"resourceId,omitempty"`
	State      domain.MOCompensationState `json:"state"`
	Reason     string                     `json:"reason,omitempty"`
}
type compensationEffectRepository interface {
	LoadCompensationForEffect(context.Context, MOCompensationExecution) (MOCompensationExecution, error)
}

func (s *Service) prepareCompensation(ctx context.Context, request MOCompensationRequest) (json.RawMessage, error) {
	if err := procmsg.RequireExecution(ctx, request.MerchantOrderID, procmsg.EffectCompensateMO); err != nil {
		return nil, err
	}
	if request.AgencyOrderID == "" || request.MerchantOrderID == "" || request.AllocationID == "" || request.IdempotencyKey == "" || !domain.ValidMOCompensationCause(request.Cause) {
		return nil, domain.ErrInvalid
	}
	repo, ok := s.repository.(moCompensationRepository)
	if !ok {
		return nil, domain.ErrCompensationNotAvailable
	}
	execution, _, err := repo.PrepareMOCompensation(ctx, request, s.ids.NewID(), s.ids.NewID(), s.clock.Now())
	if err != nil {
		return nil, err
	}
	if binder, ok := s.repository.(effectExecutionBinder); ok {
		if err := binder.BindProcessEffect(ctx, "COMPENSATION", execution.Compensation.ID); err != nil {
			return nil, err
		}
	}
	return json.Marshal(compensationPrepared{request, execution})
}
func (s *Service) executeCompensation(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if sharedapp.InTransaction(ctx) {
		return nil, procmsg.ErrExecutionRequired
	}
	var p compensationPrepared
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if err := procmsg.RequireExecution(ctx, p.Request.MerchantOrderID, procmsg.EffectCompensateMO); err != nil {
		return nil, err
	}
	repo, ok := s.repository.(moCompensationRepository)
	if !ok {
		return nil, domain.ErrCompensationNotAvailable
	}
	reader, ok := s.repository.(compensationEffectRepository)
	if !ok {
		return nil, domain.ErrCompensationNotAvailable
	}
	e, err := reader.LoadCompensationForEffect(ctx, p.Execution)
	if err != nil {
		return nil, err
	}
	encode := func(o compensationObservation) (json.RawMessage, error) { return json.Marshal(o) }
	if e.Compensation.State == domain.MOCompensationSucceeded {
		return encode(compensationObservation{State: e.Compensation.State, ResourceID: e.Compensation.ProviderResourceID})
	}
	if e.Compensation.Action == domain.MOCompensationTVitRefund {
		return encode(compensationObservation{State: domain.MOCompensationOutcomeUnknown, Reason: "CHAIN_FINALITY_REQUIRED"})
	}
	if e.Compensation.Action == domain.MOCompensationVoid && !e.ProviderVoidRequired {
		return encode(compensationObservation{State: domain.MOCompensationSucceeded})
	}
	registration, err := s.providerFor(e.Compensation.ProviderEnvironment)
	if err != nil {
		return nil, err
	}
	if e.Compensation.Action == domain.MOCompensationVoid {
		provider, ok := registration.Client.(authorizationProviderClient)
		if !ok {
			return nil, domain.ErrCaptureBlocked
		}
		observe := func() (compensationObservation, bool) {
			queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
			defer cancel()
			auth, err := provider.GetAuthorization(queryCtx, e.ProviderAuthorizationID)
			if err == nil && auth.ID == e.ProviderAuthorizationID && (auth.Status == "VOIDED" || auth.Status == "EXPIRED" || auth.Status == "DENIED") {
				return compensationObservation{State: domain.MOCompensationSucceeded, ResourceID: auth.ID, Reason: "AUTHORIZATION_" + auth.Status}, true
			}
			return compensationObservation{State: domain.MOCompensationOutcomeUnknown, Reason: "AUTHORIZATION_RELEASE_NOT_CONFIRMED"}, false
		}
		if e.OperationState == domain.OperationSent || e.OperationState == domain.OperationUnknown {
			if o, done := observe(); done {
				return encode(o)
			}
		}
		now := s.clock.Now()
		if err = repo.MarkMOCompensationSent(ctx, e, now, now.Add(s.config.RefundRetryWindow)); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				o, _ := observe()
				return encode(o)
			}
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, s.config.CaptureTimeout)
		_ = provider.VoidAuthorization(callCtx, e.ProviderAuthorizationID, e.OperationIdempotencyKey)
		cancel()
		o, _ := observe()
		return encode(o)
	}
	observe := func(id string) (json.RawMessage, error) {
		queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
		defer cancel()
		refund, err := registration.Client.GetRefund(queryCtx, id)
		if err != nil {
			return encode(compensationObservation{ResourceID: id, State: domain.MOCompensationOutcomeUnknown, Reason: "REFUND_GET_FAILED"})
		}
		return encode(compensationObservation{Refund: &refund, ResourceID: id, State: domain.MOCompensationOutcomeUnknown})
	}
	if e.OperationResourceID != "" {
		return observe(e.OperationResourceID)
	}
	locker, ok := s.repository.(moCompensationEffectLocker)
	if !ok {
		return nil, domain.ErrCompensationNotAvailable
	}
	// Receipt/dispute and sender permission commit together, before network I/O.
	// Later signed facts are still recorded; they cannot undo an already sent POST.
	err = locker.WithMOCompensationEffectLock(ctx, e, func(tx context.Context) error {
		now := s.clock.Now()
		return repo.MarkMOCompensationSent(tx, e, now, now.Add(s.config.RefundRetryWindow))
	})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return encode(compensationObservation{State: domain.MOCompensationOutcomeUnknown, Reason: "REFUND_SENDER_BUSY_OR_DEADLINE"})
		}
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.config.CaptureTimeout)
	refund, callErr := registration.Client.RefundCapture(callCtx, e.ProviderCaptureID, e.OperationIdempotencyKey, e.Compensation.AmountMinor)
	cancel()
	if refund.ID != "" {
		if _, err := repo.RecordMOCompensationOutcome(ctx, e, domain.MOCompensationOutcomeUnknown, domain.OperationUnknown, refund.ID, "REFUND_ID_CHECKPOINT", s.clock.Now()); err != nil {
			return nil, err
		}
		return observe(refund.ID)
	}
	if callErr != nil || refund.ID == "" {
		return encode(compensationObservation{State: domain.MOCompensationOutcomeUnknown, ResourceID: refund.ID, Reason: "REFUND_OUTCOME_UNKNOWN"})
	}
	return observe(refund.ID)
}
func (s *Service) resolveCompensation(ctx context.Context, raw, observed json.RawMessage) (domain.MOCompensation, error) {
	var p compensationPrepared
	var o compensationObservation
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.MOCompensation{}, err
	}
	if err := json.Unmarshal(observed, &o); err != nil {
		return domain.MOCompensation{}, err
	}
	if err := procmsg.RequireExecution(ctx, p.Request.MerchantOrderID, procmsg.EffectCompensateMO); err != nil {
		return domain.MOCompensation{}, err
	}
	repo, ok := s.repository.(moCompensationRepository)
	if !ok {
		return domain.MOCompensation{}, domain.ErrCompensationNotAvailable
	}
	reader, ok := s.repository.(compensationEffectRepository)
	if !ok {
		return domain.MOCompensation{}, domain.ErrCompensationNotAvailable
	}
	e, err := reader.LoadCompensationForEffect(ctx, p.Execution)
	if err != nil {
		return domain.MOCompensation{}, err
	}
	if e.Compensation.State == domain.MOCompensationSucceeded {
		return e.Compensation, nil
	}
	if e.Compensation.Action == domain.MOCompensationTVitRefund {
		return e.Compensation, nil
	}
	if o.Refund != nil {
		f := o.Refund
		exact := f.ID == o.ResourceID && f.AmountMinor == e.Compensation.AmountMinor && f.Currency == "USD" && f.ParentCaptureID == e.ProviderCaptureID && f.InvoiceID == e.OperationIdempotencyKey
		switch {
		case !exact:
			o.State = domain.MOCompensationOutcomeUnknown
			o.Reason = "REFUND_RESPONSE_MISMATCH"
		case f.Status == "COMPLETED":
			o.State = domain.MOCompensationSucceeded
		case f.Status == "FAILED" || f.Status == "CANCELLED":
			o.State = domain.MOCompensationFailed
			o.Reason = "REFUND_" + f.Status
		default:
			o.State = domain.MOCompensationOutcomeUnknown
			o.Reason = "REFUND_NOT_COMPLETED"
		}
	}
	operationState := domain.OperationUnknown
	if o.State == domain.MOCompensationSucceeded {
		operationState = domain.OperationSucceeded
	} else if o.State == domain.MOCompensationFailed {
		operationState = domain.OperationFailed
	}
	return repo.RecordMOCompensationOutcome(ctx, e, o.State, operationState, o.ResourceID, o.Reason, s.clock.Now())
}

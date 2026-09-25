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

type fundingObservation struct {
	Capture    *paypal.Capture       `json:"capture,omitempty"`
	ResourceID string                `json:"resourceId,omitempty"`
	State      domain.MOFundingState `json:"state"`
	Reason     string                `json:"reason,omitempty"`
}

// prepareFunding is transaction-bound. It freezes the Owner execution
// key/amount before Payment commits its own provider execution identity.
func (s *Service) prepareFunding(ctx context.Context, merchantOrderID string) (json.RawMessage, error) {
	if err := procmsg.RequireExecution(ctx, merchantOrderID, procmsg.EffectEnsureMOFunding); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(moFundingRepository)
	if !ok {
		return nil, domain.ErrFundingNotAvailable
	}
	operation := s.newOperation(domain.OperationPayPalMOCapture, "MO_FUNDING_POSITION", merchantOrderID, "paypal:mo-capture:"+merchantOrderID+":v1", 0)
	activation, _, err := repository.PrepareMOFundingActivation(ctx, merchantOrderID, operation, s.clock.Now())
	if err != nil {
		return nil, err
	}
	if binder, ok := s.repository.(effectExecutionBinder); ok {
		if err := binder.BindProcessEffect(ctx, "FUNDING", activation.PositionID); err != nil {
			return nil, err
		}
	}
	return json.Marshal(activation)
}

// executeFunding does network I/O without a caller-owned DB transaction.
// All retries use the Owner's stored key; static/runtime kill gates affect new
// sends only, so existing money observations remain recoverable.
func (s *Service) executeFunding(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if sharedapp.InTransaction(ctx) {
		return nil, procmsg.ErrExecutionRequired
	}
	var prepared MOFundingActivation
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return nil, domain.ErrFundingNotAvailable
	}
	if err := procmsg.RequireExecution(ctx, prepared.MerchantOrderID, procmsg.EffectEnsureMOFunding); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(moFundingRepository)
	if !ok {
		return nil, domain.ErrFundingNotAvailable
	}
	current, _, err := repository.GetMOFundingActivation(ctx, prepared.MerchantOrderID)
	if err != nil {
		return nil, err
	}
	if current.PositionID != prepared.PositionID || current.OperationID != prepared.OperationID || current.OperationIdempotencyKey != prepared.OperationIdempotencyKey || current.AllocationID != prepared.AllocationID || current.AmountMinor != prepared.AmountMinor {
		return nil, domain.ErrInstructionMismatch
	}
	current.FinalCapture = prepared.FinalCapture
	encode := func(o fundingObservation) (json.RawMessage, error) { return json.Marshal(o) }
	if current.State == domain.MOFundingActive || current.State == domain.MOFundingFailed || current.Rail == "GIWA" {
		return encode(fundingObservation{State: current.State})
	}
	registration, err := s.providerFor(current.ProviderEnvironment)
	if err != nil {
		return nil, err
	}
	provider, ok := registration.Client.(authorizationProviderClient)
	if !ok {
		return nil, domain.ErrCaptureBlocked
	}
	observe := func(id string) (json.RawMessage, error) {
		queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
		defer cancel()
		capture, err := registration.Client.GetCapture(queryCtx, id)
		if err != nil {
			return encode(fundingObservation{ResourceID: id, State: domain.MOFundingActivationUnknown, Reason: "CAPTURE_GET_FAILED"})
		}
		return encode(fundingObservation{Capture: &capture, ResourceID: id, State: domain.MOFundingActivationPending})
	}
	if current.ProviderCaptureID != "" {
		return observe(current.ProviderCaptureID)
	}
	if current.OperationState == domain.OperationSent || current.OperationState == domain.OperationUnknown {
		queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
		order, queryErr := provider.GetAuthorizedOrder(queryCtx, current.PayPalOrderID)
		cancel()
		if queryErr == nil {
			for _, capture := range order.Captures {
				if capture.InvoiceID == current.InvoiceID {
					return observe(capture.ID)
				}
			}
		}
		if current.OperationState == domain.OperationSent {
			return encode(fundingObservation{State: domain.MOFundingActivationUnknown, Reason: "CAPTURE_NOT_YET_OBSERVED"})
		}
	}
	if !registration.CaptureEnabled {
		return encode(fundingObservation{State: domain.MOFundingActivationPending, Reason: "MONEY_GATE_CLOSED"})
	}
	if err := s.requireLiveMoney(ctx, current.ProviderEnvironment); err != nil {
		return encode(fundingObservation{State: domain.MOFundingActivationPending, Reason: "MONEY_GATE_CLOSED"})
	}
	now := s.clock.Now()
	if err = repository.MarkMOFundingActivationSent(ctx, current.PositionID, current.OperationID, now, now.Add(s.config.CaptureRetryWindow)); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return encode(fundingObservation{State: domain.MOFundingActivationUnknown, Reason: "FUNDING_SENDER_BUSY_OR_DEADLINE"})
		}
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.config.CaptureTimeout)
	capture, callErr := provider.CaptureAuthorization(callCtx, paypal.CaptureAuthorizationInput{AuthorizationID: current.ProviderAuthorizationID, RequestID: current.OperationIdempotencyKey, InvoiceID: current.InvoiceID, AmountMinor: current.AmountMinor, Currency: current.Currency, FinalCapture: current.FinalCapture})
	cancel()
	if capture.ID != "" {
		current.ProviderCaptureID = capture.ID
		if _, err := repository.RecordMOFundingActivationOutcome(ctx, current, domain.OperationUnknown, domain.MOFundingActivationUnknown, "CAPTURE_ID_CHECKPOINT", s.clock.Now()); err != nil {
			return nil, err
		}
		return observe(capture.ID)
	}
	if callErr != nil {
		// A failed transport cannot prove that money did not move.
		return encode(fundingObservation{ResourceID: capture.ID, State: domain.MOFundingActivationUnknown, Reason: "CAPTURE_OUTCOME_UNKNOWN"})
	}
	return observe(capture.ID)
}

// resolveFunding records verified facts in Payment’s transaction.
// Payment commits these facts, its Event outbox and delivery ACK together.
func (s *Service) resolveFunding(ctx context.Context, raw, observed json.RawMessage) (MOFundingActivationResult, error) {
	var prepared MOFundingActivation
	var observation fundingObservation
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return MOFundingActivationResult{}, err
	}
	if err := json.Unmarshal(observed, &observation); err != nil {
		return MOFundingActivationResult{}, err
	}
	if err := procmsg.RequireExecution(ctx, prepared.MerchantOrderID, procmsg.EffectEnsureMOFunding); err != nil {
		return MOFundingActivationResult{}, err
	}
	repository, ok := s.repository.(moFundingRepository)
	if !ok {
		return MOFundingActivationResult{}, domain.ErrFundingNotAvailable
	}
	current, receipt, err := repository.GetMOFundingActivation(ctx, prepared.MerchantOrderID)
	if err != nil {
		return MOFundingActivationResult{}, err
	}
	if current.PositionID != prepared.PositionID || current.OperationID != prepared.OperationID || current.AmountMinor != prepared.AmountMinor {
		return MOFundingActivationResult{}, domain.ErrInstructionMismatch
	}
	current.FinalCapture = prepared.FinalCapture
	if current.State == domain.MOFundingActive || current.State == domain.MOFundingFailed {
		return MOFundingActivationResult{Position: fundingPositionFromActivation(current), Receipt: receipt, Replay: true}, nil
	}
	if observation.Capture != nil {
		result, err := s.adoptMOFundingCapture(ctx, repository, current, *observation.Capture, true)
		// A verified rejection or mismatch is a durable business outcome, not a
		// rollback of the Owner fact. The Process routes its resulting state.
		if result.Position.ID != "" {
			return result, nil
		}
		return result, err
	}
	if observation.Reason == "MONEY_GATE_CLOSED" {
		return MOFundingActivationResult{Position: fundingPositionFromActivation(current)}, nil
	}
	current.ProviderCaptureID = observation.ResourceID
	state := domain.OperationUnknown
	if observation.State == domain.MOFundingActivationPending {
		state = domain.OperationSent
	}
	position, err := repository.RecordMOFundingActivationOutcome(ctx, current, state, observation.State, observation.Reason, s.clock.Now())
	return MOFundingActivationResult{Position: position}, err
}

type FundingFactsForEffect struct {
	HasCompensation bool
	PositionID      string
	State           string
}
type fundingEffectRepository interface {
	BindFundingForEffect(context.Context, string, string, string) (FundingFactsForEffect, error)
}

func (s *Service) FundingFactsForEffect(ctx context.Context, orderID, moID, allocationID string) (FundingFactsForEffect, error) {
	repo, ok := s.repository.(fundingEffectRepository)
	if !ok {
		return FundingFactsForEffect{}, domain.ErrFundingNotAvailable
	}
	return repo.BindFundingForEffect(ctx, orderID, moID, allocationID)
}

type effectExecutionBinder interface {
	BindProcessEffect(context.Context, string, string) error
}

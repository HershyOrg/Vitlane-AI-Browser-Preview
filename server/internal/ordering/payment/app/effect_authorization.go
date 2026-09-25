package app

import (
	"context"
	"encoding/json"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

type authorizationPreparation struct {
	MerchantOrderID string
	Plan            MOReauthorizationPlan
}
type authorizationObservation struct {
	State         string
	Authorization paypal.Authorization
	Reason        string
}
type authorizationEffectRepository interface {
	moReauthorizationRepository
	LoadMOReauthorization(context.Context, MOReauthorizationPlan, time.Time) (MOReauthorizationPlan, error)
}

func (s *Service) prepareAuthorization(ctx context.Context, moID string) (json.RawMessage, error) {
	if err := procmsg.RequireExecution(ctx, moID, procmsg.EffectEnsureMOFunding); err != nil {
		return nil, err
	}
	repo, ok := s.repository.(authorizationEffectRepository)
	if !ok {
		return nil, domain.ErrAuthorizationBlocked
	}
	p, err := repo.PrepareMOReauthorization(ctx, moID, s.ids.NewID(), s.clock.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(authorizationPreparation{moID, p})
}
func (s *Service) executeAuthorization(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if sharedapp.InTransaction(ctx) {
		return nil, procmsg.ErrExecutionRequired
	}
	var prepared authorizationPreparation
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return nil, err
	}
	if err := procmsg.RequireExecution(ctx, prepared.MerchantOrderID, procmsg.EffectEnsureMOFunding); err != nil {
		return nil, err
	}
	emit := func(o authorizationObservation) (json.RawMessage, error) { return json.Marshal(o) }
	p := prepared.Plan
	if p.Expired {
		return emit(authorizationObservation{State: "FAILED"})
	}
	if !p.Required {
		return emit(authorizationObservation{State: "READY"})
	}
	repo, ok := s.repository.(authorizationEffectRepository)
	if !ok {
		return nil, domain.ErrAuthorizationBlocked
	}
	p, err := repo.LoadMOReauthorization(ctx, p, s.clock.Now())
	if err != nil {
		return nil, err
	}
	if p.OperationState == domain.OperationSucceeded {
		return emit(authorizationObservation{State: "READY"})
	}
	if p.OperationState == domain.OperationFailed {
		return emit(authorizationObservation{State: "FAILED"})
	}
	reg, err := s.providerFor(p.ProviderEnvironment)
	if err != nil {
		return nil, err
	}
	provider, ok := reg.Client.(authorizationProviderClient)
	if !ok {
		return nil, domain.ErrAuthorizationBlocked
	}
	observe := func(id string) (json.RawMessage, error) {
		query, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
		defer cancel()
		a, err := provider.GetAuthorization(query, id)
		if err != nil {
			return emit(authorizationObservation{State: "UNKNOWN", Reason: "REAUTHORIZATION_GET_FAILED"})
		}
		exact := a.ID == id && a.AmountMinor == p.RemainingCapturableMinor && a.Currency == p.Currency && a.ParentOrderID == p.PayPalOrderID && a.PayeeMerchant == p.PayeeMerchantID
		if exact && a.Status == "CREATED" {
			return emit(authorizationObservation{State: "READY", Authorization: a})
		}
		if exact && (a.Status == "DENIED" || a.Status == "VOIDED" || a.Status == "EXPIRED") {
			return emit(authorizationObservation{State: "FAILED", Authorization: a, Reason: "REAUTHORIZATION_" + a.Status})
		}
		return emit(authorizationObservation{State: "UNKNOWN", Reason: "REAUTHORIZATION_NONCONFORMING"})
	}
	if p.OperationResourceID != "" {
		return observe(p.OperationResourceID)
	}
	if p.OperationState == domain.OperationSent {
		return emit(authorizationObservation{State: "UNKNOWN", Reason: "AUTHORIZATION_SENDER_BUSY"})
	}
	if !reg.CaptureEnabled {
		return emit(authorizationObservation{State: "MONEY_GATE_CLOSED"})
	}
	if err := s.requireLiveMoney(ctx, p.ProviderEnvironment); err != nil {
		return emit(authorizationObservation{State: "MONEY_GATE_CLOSED"})
	}
	now := s.clock.Now()
	if err := repo.MarkMOReauthorizationSent(ctx, p, now, now.Add(s.config.CaptureRetryWindow)); err != nil {
		return emit(authorizationObservation{State: "UNKNOWN", Reason: "AUTHORIZATION_SENDER_BUSY_OR_DEADLINE"})
	}
	call, cancel := context.WithTimeout(ctx, s.config.CaptureTimeout)
	a, callErr := provider.ReauthorizeAuthorization(call, paypal.ReauthorizeAuthorizationInput{AuthorizationID: p.PreviousProviderAuthorizationID, RequestID: p.OperationIdempotencyKey, AmountMinor: p.RemainingCapturableMinor, Currency: p.Currency, OrderID: p.PayPalOrderID, PayeeMerchant: p.PayeeMerchantID})
	cancel()
	// Checkpoint the provider identity immediately. It is evidence for a later GET,
	// never permission to advance the Process or manufacture a success fact.
	if a.ID != "" {
		if err := repo.RecordMOReauthorizationOutcome(ctx, p, domain.OperationUnknown, a.ID, a.AmountMinor, a.Currency, time.Time{}, "REAUTHORIZATION_GET_PENDING", s.clock.Now()); err != nil {
			return nil, err
		}
	}
	if callErr != nil {
		return emit(authorizationObservation{State: "UNKNOWN", Reason: "REAUTHORIZATION_OUTCOME_UNKNOWN"})
	}
	return observe(a.ID)
}
func (s *Service) resolveAuthorization(ctx context.Context, raw, observed json.RawMessage) (string, error) {
	var prepared authorizationPreparation
	var o authorizationObservation
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return "", err
	}
	if err := json.Unmarshal(observed, &o); err != nil {
		return "", err
	}
	if err := procmsg.RequireExecution(ctx, prepared.MerchantOrderID, procmsg.EffectEnsureMOFunding); err != nil {
		return "", err
	}
	p := prepared.Plan
	if !p.Required || p.Expired {
		return o.State, nil
	}
	repo, ok := s.repository.(authorizationEffectRepository)
	if !ok {
		return "", domain.ErrAuthorizationBlocked
	}
	current, err := repo.LoadMOReauthorization(ctx, p, s.clock.Now())
	if err != nil {
		return "", err
	}
	if current.OperationState == domain.OperationSucceeded {
		return "READY", nil
	}
	if current.OperationState == domain.OperationFailed {
		return "FAILED", nil
	}
	if o.State == "MONEY_GATE_CLOSED" {
		return o.State, nil
	}
	state := domain.OperationUnknown
	at := time.Time{}
	if o.State == "READY" || o.State == "FAILED" {
		a := o.Authorization
		if a.ID == "" || a.ID != current.OperationResourceID || a.AmountMinor != p.RemainingCapturableMinor || a.Currency != p.Currency || a.ParentOrderID != p.PayPalOrderID || a.PayeeMerchant != p.PayeeMerchantID {
			return "", domain.ErrInstructionMismatch
		}
		if o.State == "READY" && a.Status == "CREATED" {
			state = domain.OperationSucceeded
			at = s.clock.Now()
			if parsed, e := time.Parse(time.RFC3339Nano, a.CreateTime); e == nil {
				at = parsed.UTC()
			}
		} else if o.State == "FAILED" && (a.Status == "DENIED" || a.Status == "VOIDED" || a.Status == "EXPIRED") {
			state = domain.OperationFailed
		} else {
			return "", domain.ErrInstructionMismatch
		}
	}
	err = repo.RecordMOReauthorizationOutcome(ctx, p, state, current.OperationResourceID, o.Authorization.AmountMinor, o.Authorization.Currency, at, o.Reason, s.clock.Now())
	return o.State, err
}

package app

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

func ownerEffectTestScope(ctx context.Context, mo, action string) context.Context {
	return procmsg.WithExecutionScope(ctx, procmsg.ExecutionScope{AgencyOrderID: "order-1", MerchantOrderID: mo, FlowID: "purchase-flow", EffectID: "funding-effect", ClaimVersion: 1, Action: action})
}
func (s *Service) runFundingEffectContract(ctx context.Context, mo, key string) (MOFundingActivationResult, error) {
	if _, ok := s.repository.(authorizationEffectRepository); ok {
		authCtx := ownerEffectTestScope(ctx, mo, procmsg.EffectEnsureMOFunding)
		raw, e := s.prepareAuthorization(authCtx, mo)
		if e != nil {
			return MOFundingActivationResult{}, e
		}
		observed, e := s.executeAuthorization(authCtx, raw)
		if e != nil {
			return MOFundingActivationResult{}, e
		}
		state, e := s.resolveAuthorization(authCtx, raw, observed)
		if e != nil {
			return MOFundingActivationResult{}, e
		}
		if state != "READY" {
			repo := s.repository.(moFundingRepository)
			a, receipt, e := repo.GetMOFundingActivation(ctx, mo)
			if e != nil {
				return MOFundingActivationResult{}, e
			}
			return MOFundingActivationResult{Position: fundingPositionFromActivation(a), Receipt: receipt}, nil
		}
	}
	ctx = ownerEffectTestScope(ctx, mo, procmsg.EffectEnsureMOFunding)
	raw, e := s.prepareFunding(ctx, mo)
	if e != nil {
		return MOFundingActivationResult{}, e
	}
	observation, e := s.executeFunding(ctx, raw)
	if e != nil {
		return MOFundingActivationResult{}, e
	}
	return s.resolveFunding(ctx, raw, observation)
}
func (s *Service) runCompensationEffectContract(ctx context.Context, r MOCompensationRequest) (MOCompensationResult, error) {
	ctx = ownerEffectTestScope(ctx, r.MerchantOrderID, procmsg.EffectCompensateMO)
	raw, e := s.prepareCompensation(ctx, r)
	if e != nil {
		return MOCompensationResult{}, e
	}
	observed, e := s.executeCompensation(ctx, raw)
	if e != nil {
		return MOCompensationResult{}, e
	}
	c, e := s.resolveCompensation(ctx, raw, observed)
	return MOCompensationResult{Compensation: c}, e
}
func (r *fakeMOReauthorizationFundingRepository) LoadMOReauthorization(_ context.Context, _ MOReauthorizationPlan, _ time.Time) (MOReauthorizationPlan, error) {
	return r.plan, nil
}
func (r *fakeMOCompensationRepository) LoadCompensationForEffect(_ context.Context, _ MOCompensationExecution) (MOCompensationExecution, error) {
	return r.execution, nil
}
func (r *fakeMOCompensationRepository) WithMOCompensationEffectLock(ctx context.Context, _ MOCompensationExecution, fn func(context.Context) error) error {
	return fn(ctx)
}

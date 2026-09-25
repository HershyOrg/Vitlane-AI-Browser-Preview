package app

import (
	"context"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type BudgetRepository interface {
	ReadBudget(context.Context, string, string, bool) (curationdomain.BudgetLedger, error)
	ApplyBudget(context.Context, string, string, curationdomain.BudgetCommand) (curationdomain.BudgetLedger, error)
}

type BudgetEstimateTarget struct {
	Title    string
	Quantity int
}
type BudgetEstimator interface {
	Estimate(context.Context, string, string, string, string, []BudgetEstimateTarget) ([]string, error)
}
type BudgetProposalInput struct {
	SchemaVersion   string `json:"schemaVersion"`
	RequestID       string `json:"requestId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Currency        string `json:"currency"`
	TotalAmount     string `json:"totalAmount"`
}
type BudgetProposal struct {
	SchemaVersion   string                        `json:"schemaVersion"`
	ExpectedVersion int64                         `json:"expectedVersion"`
	Currency        string                        `json:"currency"`
	TotalAmount     string                        `json:"totalAmount"`
	Allocations     []curationdomain.TargetBudget `json:"allocations"`
}

func (s *Service) EnableBudgetEstimator(estimator BudgetEstimator) { s.budgetEstimator = estimator }

// SuggestBudget creates an unsaved allocation. The managed provider owns cost
// admission; neither a model response nor closing the dialog commits a budget.
func (s *Service) SuggestBudget(ctx context.Context, user, id string, input BudgetProposalInput) (BudgetProposal, error) {
	result := BudgetProposal{SchemaVersion: curationdomain.BudgetSchema, ExpectedVersion: input.ExpectedVersion, Currency: input.Currency, TotalAmount: input.TotalAmount, Allocations: []curationdomain.TargetBudget{}}
	total, err := curationdomain.BudgetMinor(input.TotalAmount, input.Currency)
	if err != nil || input.SchemaVersion != curationdomain.BudgetSchema || !uuidPattern.MatchString(input.RequestID) {
		return result, fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
	}
	b, err := s.Budget(ctx, user, id)
	if err != nil {
		return result, err
	}
	if b.Version != input.ExpectedVersion {
		return result, fault.New(fault.Conflict, "BUDGET_VERSION_CONFLICT", false)
	}
	r, ok := s.repository.(CurationStateRepository)
	if !ok || s.budgetEstimator == nil {
		return result, fault.New(fault.ProviderUnavailable, "BUDGET_ESTIMATOR_UNAVAILABLE", true)
	}
	c, err := r.GetCuration(ctx, user, id, false)
	if err != nil {
		return result, err
	}
	record, err := s.repository.Get(ctx, user, string(c.ShoppingPlanID), false)
	if err != nil {
		return result, err
	}
	items := []BudgetEstimateTarget{}
	for _, a := range b.Allocations {
		for _, t := range record.Targets {
			if string(t.ID) == a.TargetID {
				items = append(items, BudgetEstimateTarget{Title: t.Title, Quantity: a.Quantity})
			}
		}
	}
	if len(items) != len(b.Allocations) {
		return result, fault.New(fault.Conflict, "BUDGET_VERSION_CONFLICT", false)
	}
	if len(items) == 0 {
		if total != 0 {
			return result, fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
		}
		return result, nil
	}
	estimates, err := s.budgetEstimator.Estimate(ctx, user, record.Plan.ModelKey, input.RequestID, input.Currency, items)
	if err != nil {
		return result, err
	}
	if len(estimates) != len(items) {
		return result, fault.New(fault.ProviderRejected, "BUDGET_ESTIMATE_INVALID", false)
	}
	weights := make([]int64, len(items))
	for i, amount := range estimates {
		weights[i], err = curationdomain.BudgetMinor(amount, input.Currency)
		if err != nil || weights[i] <= 0 {
			return result, fault.New(fault.ProviderRejected, "BUDGET_ESTIMATE_INVALID", false)
		}
	}
	values, err := curationdomain.AllocateBudget(total, weights)
	if err != nil {
		return result, err
	}
	for i, a := range b.Allocations {
		amount := curationdomain.BudgetAmount(values[i], input.Currency)
		a.Amount = &amount
		a.MinimumUnitAmount = nil
		result.Allocations = append(result.Allocations, a)
	}
	current, err := s.Budget(ctx, user, id)
	if err != nil {
		return result, err
	}
	if current.Version != b.Version {
		return result, fault.New(fault.Conflict, "BUDGET_VERSION_CONFLICT", false)
	}
	return result, nil
}

func (s *Service) Budget(ctx context.Context, user, id string) (curationdomain.BudgetLedger, error) {
	r, ok := s.repository.(BudgetRepository)
	if !ok {
		return curationdomain.BudgetLedger{}, fault.New(fault.InternalFailure, "BUDGET_UNAVAILABLE", false)
	}
	return r.ReadBudget(ctx, user, id, false)
}

func (s *Service) BudgetForPlan(ctx context.Context, user, id string) (curationdomain.BudgetLedger, error) {
	r, ok := s.repository.(BudgetRepository)
	if !ok {
		return curationdomain.BudgetLedger{}, fault.New(fault.InternalFailure, "BUDGET_UNAVAILABLE", false)
	}
	return r.ReadBudget(ctx, user, id, true)
}

func (s *Service) ChangeBudget(ctx context.Context, user, id string, c curationdomain.BudgetCommand) (curationdomain.BudgetLedger, error) {
	if !uuidPattern.MatchString(c.CommandID) {
		return curationdomain.BudgetLedger{}, fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
	}
	r, ok := s.repository.(BudgetRepository)
	if !ok {
		return curationdomain.BudgetLedger{}, fault.New(fault.InternalFailure, "BUDGET_UNAVAILABLE", false)
	}
	// A Target whose Round is open keeps its budget until the Round closes.
	if c.TargetID != "" {
		if err := s.guardTargetResearch(ctx, user, c.TargetID); err != nil {
			return curationdomain.BudgetLedger{}, err
		}
	}
	var result curationdomain.BudgetLedger
	err := s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		var err error
		result, err = r.ApplyBudget(tx, user, id, c)
		return err
	})
	return result, err
}

// Target membership and its complete budget allocation share the caller's transaction.
func (s *Service) insertPlannedTargets(ctx context.Context, targets []curationdomain.PlanTarget, resolved *curationdomain.InitialBudgetRequest, inputs []TargetInput) error {
	if repo, ok := s.repository.(interface {
		InsertBudgetedTargets(context.Context, []curationdomain.PlanTarget, *curationdomain.InitialBudgetRequest, []int) error
	}); ok {
		quantities := make([]int, len(inputs))
		for i, input := range inputs {
			quantities[i] = input.Quantity
			if quantities[i] == 0 {
				quantities[i] = 1
			}
		}
		if err := repo.InsertBudgetedTargets(ctx, targets, resolved, quantities); err != nil {
			return err
		}
		if r, ok := s.repository.(CriteriaRepository); ok {
			return r.InsertInitialCriteria(ctx, targets, inputs)
		}
		return nil
	}
	if resolved != nil {
		return fault.New(fault.InternalFailure, "BUDGET_REPOSITORY_UNAVAILABLE", false)
	}
	return s.repository.InsertTargets(ctx, targets)
}

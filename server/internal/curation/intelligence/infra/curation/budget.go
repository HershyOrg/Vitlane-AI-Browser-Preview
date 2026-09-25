package curation

import (
	"context"
	"encoding/json"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

type BudgetEstimator struct{ Provider intelligenceapp.Provider }

func (a BudgetEstimator) Estimate(ctx context.Context, user, model, request, currency string, targets []curationapp.BudgetEstimateTarget) ([]string, error) {
	if a.Provider == nil {
		return nil, fault.New(fault.ProviderUnavailable, "BUDGET_ESTIMATOR_UNAVAILABLE", true)
	}
	items := make([]intelligenceapp.BudgetEstimateTarget, len(targets))
	for i, t := range targets {
		items[i] = intelligenceapp.BudgetEstimateTarget{Title: t.Title, Quantity: t.Quantity}
	}
	if err := a.Provider.Available(ctx, user); err != nil {
		return nil, err
	}
	response, err := a.Provider.Complete(ctx, intelligenceapp.CompletionRequest{UserID: user, ModelKey: model, RequestKey: "curation-budget:" + request, JobID: request, AttemptID: request, SystemPrompt: intelligenceapp.BudgetEstimatesSystemPrompt, UserPrompt: intelligenceapp.BudgetEstimatesPrompt(currency, items), SchemaName: intelligenceapp.BudgetEstimatesSchemaName, Schema: intelligenceapp.BudgetEstimatesSchema(len(items)), Deadline: time.Now().Add(45 * time.Second)})
	if err != nil {
		return nil, err
	}
	var payload intelligenceapp.BudgetEstimatesPayload
	if json.Unmarshal([]byte(response.Content), &payload) != nil || intelligenceapp.ValidateBudgetEstimates(payload, len(items), currency) != nil {
		return nil, fault.New(fault.ProviderRejected, "BUDGET_ESTIMATE_INVALID", false)
	}
	amounts := make([]string, len(items))
	for i, a := range payload.Estimates {
		amounts[i] = a.Amount
	}
	return amounts, nil
}

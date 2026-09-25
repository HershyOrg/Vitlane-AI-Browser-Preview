package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadBudget(ctx context.Context, user, id string, byPlan bool) (curationdomain.BudgetLedger, error) {
	var b curationdomain.BudgetLedger
	b.SchemaVersion = curationdomain.BudgetSchema
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT b.enabled,b.currency,b.version,b.research_version,b.allocations FROM curation_budgets b JOIN curations c ON c.id=b.curation_id WHERE c.user_id=$1 AND (($3 AND c.shopping_plan_id=$2) OR (NOT $3 AND c.id=$2)) AND c.archived_at IS NULL`, user, id, byPlan).Scan(&b.Enabled, &b.Currency, &b.Version, &b.ResearchVersion, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return b, curationdomain.ErrPlanNotFound
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(raw, &b.Allocations); err != nil {
		return b, err
	}
	ids := make([]string, len(b.Allocations))
	for i, a := range b.Allocations {
		ids[i] = a.TargetID
	}
	err = b.Validate(ids)
	return b, err
}

func (r *Repository) lockBudgetOwner(ctx context.Context, user, id string) error {
	var stored string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id FROM curations WHERE user_id=$1 AND id=$2 AND archived_at IS NULL FOR UPDATE`, user, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.ErrPlanNotFound
	}
	return err
}

func (r *Repository) ApplyBudget(ctx context.Context, user, id string, c curationdomain.BudgetCommand) (curationdomain.BudgetLedger, error) {
	if err := r.lockBudgetOwner(ctx, user, id); err != nil {
		return curationdomain.BudgetLedger{}, err
	}
	hash, err := shareddomain.CanonicalJSONHash(struct {
		CurationID string
		Command    curationdomain.BudgetCommand
	}{id, c})
	if err != nil {
		return curationdomain.BudgetLedger{}, err
	}
	var previousHash string
	var raw []byte
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT request_hash,response FROM curation_budget_commands WHERE user_id=$1 AND command_id=$2`, user, c.CommandID).Scan(&previousHash, &raw)
	if err == nil {
		var result curationdomain.BudgetLedger
		if previousHash != hash {
			return result, fault.New(fault.Conflict, "BUDGET_IDEMPOTENCY_CONFLICT", false)
		}
		err = json.Unmarshal(raw, &result)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return curationdomain.BudgetLedger{}, err
	}
	if err := r.GuardThreadMutation(ctx, user, id); err != nil {
		return curationdomain.BudgetLedger{}, err
	}
	var materialized bool
	if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT initial_materialized FROM curation_budgets WHERE curation_id=$1`, id).Scan(&materialized); err != nil {
		return curationdomain.BudgetLedger{}, err
	}
	if !materialized {
		return curationdomain.BudgetLedger{}, fault.New(fault.Conflict, "BUDGET_PLANNING_IN_PROGRESS", false)
	}
	b, err := r.ReadBudget(ctx, user, id, false)
	if err != nil {
		return b, err
	}
	next, err := b.Apply(c)
	if err != nil {
		return b, err
	}
	if err = r.saveBudget(ctx, user, id, next, c.Kind); err != nil {
		return b, err
	}
	raw, err = json.Marshal(next)
	if err != nil {
		return b, err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_budget_commands(user_id,command_id,curation_id,request_hash,response) VALUES($1,$2,$3,$4,$5::jsonb)`, user, c.CommandID, id, hash, string(raw))
	if err == nil {
		before, _ := json.Marshal(b)
		after, _ := json.Marshal(next)
		err = r.recordManualThread(ctx, user, id, c.CommandID, "BUDGET", c.TargetID, []curationdomain.ActionEffect{{Kind: "BUDGET_CHANGED", TargetID: c.TargetID, Before: before, After: after}}, curationdomain.CurationAction{Budget: &c})
	}
	return next, err
}

func (r *Repository) saveBudget(ctx context.Context, user, id string, b curationdomain.BudgetLedger, reason string) error {
	// The Curation row lock serializes membership changes and all budget writes.
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id FROM plan_targets WHERE curation_id=$1 AND user_id=$2 AND removed_at IS NULL ORDER BY order_index`, id, user)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var target string
		if err = rows.Scan(&target); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = b.Validate(ids); err != nil {
		return err
	}
	raw, err := json.Marshal(b.Allocations)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE curation_budgets SET enabled=$2,currency=$3,version=$4,research_version=$5,allocations=$6::jsonb WHERE curation_id=$1`, id, b.Enabled, b.Currency, b.Version, b.ResearchVersion, string(raw))
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_budget_history(curation_id,version,reason,snapshot) VALUES($1,$2,$3,$4::jsonb)`, id, b.Version, reason, string(snapshot))
	return err
}

func (r *Repository) initialBudgetRequest(ctx context.Context, id string) (*curationdomain.InitialBudgetRequest, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT initial_request FROM curation_budgets WHERE curation_id=$1`, id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var value curationdomain.InitialBudgetRequest
	err = json.Unmarshal(raw, &value)
	return &value, err
}

func (r *Repository) addTargetBudgets(ctx context.Context, targets []curationdomain.PlanTarget, resolved *curationdomain.InitialBudgetRequest, quantities []int) error {
	if len(quantities) != 0 && len(quantities) != len(targets) {
		return fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
	}
	if len(targets) == 0 {
		return nil
	}
	user, id := string(targets[0].UserID), string(targets[0].CurationID)
	if err := r.lockBudgetOwner(ctx, user, id); err != nil {
		return err
	}
	b, err := r.ReadBudget(ctx, user, id, false)
	if err != nil {
		return err
	}
	request, err := r.initialBudgetRequest(ctx, id)
	if err != nil {
		return err
	}
	var materialized bool
	if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT initial_materialized FROM curation_budgets WHERE curation_id=$1`, id).Scan(&materialized); err != nil {
		return err
	}
	if resolved != nil {
		if materialized || request == nil || !request.AllowsInference() {
			return fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
		}
		if err := resolved.Validate(); err != nil {
			return err
		}
		request = resolved
	}

	if !materialized && request != nil {
		b.Enabled = request.TotalAmount != nil
		b.Currency = request.Currency
	}
	priorTotal := b.TotalAmount
	priorVersion, priorResearchVersion := b.Version, b.ResearchVersion
	for i, target := range targets {
		a := curationdomain.TargetBudget{TargetID: string(target.ID), Quantity: 1}
		if len(quantities) > 0 {
			a.Quantity = quantities[i]
		}
		if b.Enabled {
			amount := target.AllocatedBudget.Amount
			if string(target.AllocatedBudget.Currency) != b.Currency {
				return fault.New(fault.Conflict, "BUDGET_VERSION_CONFLICT", false)
			}
			a.Amount = &amount
		}
		b.Allocations = append(b.Allocations, a)
	}
	if materialized && b.Enabled && priorTotal != nil {
		b, err = b.Apply(curationdomain.BudgetCommand{SchemaVersion: curationdomain.BudgetSchema, ExpectedVersion: b.Version, Kind: "SET_TOTAL", Currency: b.Currency, TotalAmount: priorTotal, AllocationMode: "PROPORTIONAL"})
		if err != nil {
			return err
		}
	}
	if !materialized && request != nil && request.TotalAmount != nil {
		ids := []string{}
		for _, a := range b.Allocations {
			ids = append(ids, a.TargetID)
		}
		if err = b.Validate(ids); err != nil {
			return err
		}
		wanted, e := curationdomain.BudgetMinor(*request.TotalAmount, request.Currency)
		if e != nil {
			return e
		}
		actual, e := curationdomain.BudgetMinor(*b.TotalAmount, b.Currency)
		if e != nil || actual != wanted {
			return fault.New(fault.InvalidInput, "BUDGET_ALLOCATION_SUM_MISMATCH", false)
		}
	}
	b.Version = priorVersion + 1
	b.ResearchVersion = priorResearchVersion + 1
	if err = r.saveBudget(ctx, user, id, b, "TARGETS_ADDED"); err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE curation_budgets SET initial_materialized=true WHERE curation_id=$1`, id)
	return err
}

func (r *Repository) removeTargetBudget(ctx context.Context, target curationdomain.PlanTarget) error {
	user, id := string(target.UserID), string(target.CurationID)
	if err := r.lockBudgetOwner(ctx, user, id); err != nil {
		return err
	}
	b, err := r.ReadBudget(ctx, user, id, false)
	if err != nil {
		return err
	}
	next := []curationdomain.TargetBudget{}
	for _, a := range b.Allocations {
		if a.TargetID != string(target.ID) {
			next = append(next, a)
		}
	}
	b.Allocations = next
	b.Version++
	b.ResearchVersion++
	return r.saveBudget(ctx, user, id, b, "TARGET_REMOVED")
}

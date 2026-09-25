package domain

import (
	"testing"
	"time"
)

func TestInitialBudgetInferenceRequiresAutoMode(t *testing.T) {
	amount := "100000"
	resolved := &InitialBudgetRequest{SchemaVersion: BudgetSchema, InputMode: "EXPLICIT", Currency: "KRW", TotalAmount: &amount, AllocationMode: "AUTO"}
	for _, mode := range []string{"", "EXPLICIT", "AUTO"} {
		for _, enabled := range []bool{false, true} {
			original := &InitialBudgetRequest{SchemaVersion: BudgetSchema, InputMode: mode, Currency: "USD", AllocationMode: "AUTO"}
			if enabled {
				value := "50.00"
				original.TotalAmount = &value
			}
			plan := PlanSnapshot{BudgetRequest: original}
			_, err := plan.ResolveInitialBudget(resolved)
			allowed := mode == "AUTO"
			if (err == nil) != allowed {
				t.Fatalf("mode=%s enabled=%v err=%v", mode, enabled, err)
			}
			if plan.BudgetRequest.Currency != "USD" {
				t.Fatal("immutable request mutated")
			}
		}
	}
	invalid := InitialBudgetRequest{SchemaVersion: BudgetSchema, InputMode: "AUTO", Currency: "KRW", TotalAmount: &amount, AllocationMode: "AUTO"}
	if invalid.Validate() != nil {
		t.Fatal("AUTO must accept a manual baseline")
	}
}

func TestInferredBudgetMaterializesAgainstOriginalContextHash(t *testing.T) {
	input := fixtureInput(t, PlanningModeAuto)
	input.BudgetRequest = &InitialBudgetRequest{SchemaVersion: BudgetSchema, InputMode: "AUTO", Currency: "USD", AllocationMode: "AUTO"}
	plan, _, task, err := NewPlanSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := plan.ContextHash()
	amount := "100000"
	resolved := &InitialBudgetRequest{SchemaVersion: BudgetSchema, InputMode: "EXPLICIT", Currency: "KRW", TotalAmount: &amount, AllocationMode: "AUTO"}
	validationPlan, err := plan.ResolveInitialBudget(resolved)
	if err != nil {
		t.Fatal(err)
	}
	targets := []PlanTarget{fixtureTarget(t, validationPlan, "pen", "60000", 0), fixtureTarget(t, validationPlan, "ink", "40000", 1)}
	created, err := MaterializeInitialTargets(plan, task, targets, "initial-run", task.CreatedAt.Add(time.Minute), resolved)
	if err != nil || len(created) != 2 {
		t.Fatalf("materialized=%+v err=%v", created, err)
	}
	after, _ := plan.ContextHash()
	if before != after || plan.BudgetRequest.TotalAmount != nil {
		t.Fatal("resolved output changed original request")
	}
}

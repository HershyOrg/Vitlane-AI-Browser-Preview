package domain

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func TestNewSinglePlanCreatesInitialPlanningTaskWithoutTarget(t *testing.T) {
	plan, targets, task, err := NewPlanSnapshot(fixtureInput(t, PlanningModeSingle))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || len(targets) != 0 {
		t.Fatalf("single plan shape task=%#v targets=%d", task, len(targets))
	}
	if plan.ResearchScope.Category != "camping" ||
		task.ContextHash == "" {
		t.Fatalf("single settings/task were not captured: %#v %#v", plan, task)
	}
}

func TestNewSinglePlanDerivesCategoryWhenSettingsAreUntouched(t *testing.T) {
	input := fixtureInput(t, PlanningModeSingle)
	input.OriginalIntent = "노이즈 캔슬링 헤드폰 하나만 찾아줘"
	input.ResearchScope.Category = ""

	plan, targets, task, err := NewPlanSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || len(targets) != 0 {
		t.Fatalf("single plan shape task=%#v targets=%d", task, len(targets))
	}
	if plan.ResearchScope.Category != input.OriginalIntent {
		t.Fatalf("single category was not derived: %#v", plan.ResearchScope)
	}
}

func TestNewAutoPlanCreatesOnlyImmutableSnapshot(t *testing.T) {
	plan, targets, task, err := NewPlanSnapshot(fixtureInput(t, PlanningModeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 || task == nil {
		t.Fatalf("AUTO intent created work: plan=%#v targets=%#v task=%#v", plan, targets, task)
	}
	if plan.CreatedAt.IsZero() {
		t.Fatal("immutable Plan snapshot lost createdAt")
	}
}

func TestPlanSnapshotJSONContainsOnlyImmutableSnapshotFields(t *testing.T) {
	plan, _, _, err := NewPlanSnapshot(fixtureInput(t, PlanningModeAuto))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{
		// ADR-0032: who executes this plan's Agent work is part of the
		// immutable settings snapshot, fixed at submission.
		"agentMode",
		"createdAt",
		"executionMode",
		"id",
		"locationContext",
		"originalIntent",
		"planningMode",
		"researchScope",
		"totalBudget",
		"userId",
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("PlanSnapshot JSON keys=%v want=%v", keys, want)
	}
}

func TestMaterializeInitialTargetsConfirmsOneOrManyTargets(t *testing.T) {
	plan, task := fixtureAutoPlanWithTask(t)
	targets := []PlanTarget{
		fixtureTarget(t, plan, "chair", "60", 0),
		fixtureTarget(t, plan, "lamp", "40", 1),
	}
	now := task.CreatedAt.Add(10 * time.Minute)
	created, err := MaterializeInitialTargets(
		plan, task, targets, "run-initial", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != PlanningTaskStatusCompleted || len(created) != 2 {
		t.Fatalf("targets were not materialized: %#v %#v", task, created)
	}
	for _, target := range created {
		if target.ConfirmedAt == nil || target.TargetHash == "" ||
			target.CreatedByCurationRunID == nil ||
			*target.CreatedByCurationRunID != "run-initial" {
			t.Fatalf("target was not confirmed/provenanced: %#v", target)
		}
	}
}

func TestMaterializeInitialTargetsRejectsStaleContext(t *testing.T) {
	plan, task := fixtureAutoPlanWithTask(t)
	task.ContextHash = "stale"
	_, err := MaterializeInitialTargets(
		plan,
		task,
		[]PlanTarget{fixtureTarget(t, plan, "chair", "100", 0)},
		"run-initial",
		task.CreatedAt.Add(time.Minute),
	)
	if !errors.Is(err, ErrPlanningContextStale) {
		t.Fatalf("expected ErrPlanningContextStale, got %v", err)
	}
}

func TestTargetSetCannotExceedPlanBudget(t *testing.T) {
	plan, task := fixtureAutoPlanWithTask(t)
	_, err := MaterializeInitialTargets(
		plan,
		task,
		[]PlanTarget{
			fixtureTarget(t, plan, "chair", "60", 0),
			fixtureTarget(t, plan, "lamp", "50", 1),
		},
		"run-initial",
		task.CreatedAt.Add(time.Minute),
	)
	if !errors.Is(err, ErrTargetBudgetInvalid) {
		t.Fatalf("expected ErrTargetBudgetInvalid, got %v", err)
	}
}

func TestAppendConfirmedTargetsKeepsPlanAndExistingTargetsImmutable(t *testing.T) {
	plan, task := fixtureAutoPlanWithTask(t)
	now := task.CreatedAt.Add(time.Minute)
	current, err := MaterializeInitialTargets(
		plan,
		task,
		[]PlanTarget{
			fixtureTarget(t, plan, "chair", "60", 0),
			fixtureTarget(t, plan, "lamp", "40", 1),
		},
		"run-initial",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	originalPlan := plan
	originalTarget := current[0]
	next, created, err := AppendConfirmedTargets(
		plan,
		current,
		[]PlanTarget{fixtureTarget(t, plan, "monitor", "20", 0)},
		"run-expansion",
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, originalPlan) ||
		len(next) != 3 ||
		len(created) != 1 ||
		!reflect.DeepEqual(next[0], originalTarget) {
		t.Fatalf("expansion mutated immutable source: plan=%#v next=%#v", plan, next)
	}
	if created[0].CreatedByCurationRunID == nil ||
		*created[0].CreatedByCurationRunID != "run-expansion" ||
		created[0].ConfirmedAt == nil ||
		created[0].TargetHash == "" {
		t.Fatalf("new expansion target was not confirmed: %#v", created[0])
	}
}

func TestSingleModeAllowsExplicitExpansion(t *testing.T) {
	input := fixtureInput(t, PlanningModeSingle)
	plan, _, task, err := NewPlanSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	current, err := MaterializeInitialTargets(
		plan,
		task,
		[]PlanTarget{fixtureTarget(t, plan, "headphones", "30", 0)},
		"run-single-initial",
		input.Now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	next, created, err := AppendConfirmedTargets(
		plan,
		current,
		[]PlanTarget{
			fixtureTarget(t, plan, "keyboard", "40", 0),
			fixtureTarget(t, plan, "mouse", "30", 1),
		},
		"run-single-expansion",
		input.Now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("SINGLE constrains initial decomposition only: %v", err)
	}
	if len(next) != 3 || len(created) != 2 || next[0].ID != current[0].ID {
		t.Fatalf("unexpected SINGLE expansion: next=%#v created=%#v", next, created)
	}
}

func TestResearchScopeRejectsInvalidRange(t *testing.T) {
	location, _ := shareddomain.NewLocationContext("US", "")
	minimum, _ := shareddomain.NewMoney("20", "USD")
	maximum, _ := shareddomain.NewMoney("10", "USD")
	_, err := NewResearchScope(
		"chair", location, nil, nil, &minimum, &maximum, "", URLModeNone,
	)
	if !errors.Is(err, ErrPriceRangeInvalid) {
		t.Fatalf("expected ErrPriceRangeInvalid, got %v", err)
	}
}

func fixtureInput(t *testing.T, mode PlanningMode) NewPlanSnapshotInput {
	t.Helper()
	money, _ := shareddomain.NewMoney("100", "USD")
	location, _ := shareddomain.NewLocationContext("US", "Seattle")
	scope, _ := NewResearchScope(
		"camping", location, nil, nil, nil, nil, "", URLModeNone,
	)
	now := time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)
	return NewPlanSnapshotInput{
		PlanID: "plan-1", TargetID: "target-1", TaskID: "task-1",
		UserID: "user-1", OriginalIntent: "portable camping kit",
		PlanningMode: mode, ExecutionMode: ExecutionModeExperiment,
		TotalBudget: money, LocationContext: location, ResearchScope: scope,
		Now: now, TaskExpiresAt: now.Add(time.Hour),
	}
}

func fixtureAutoPlanWithTask(t *testing.T) (PlanSnapshot, *PlanningTask) {
	t.Helper()
	input := fixtureInput(t, PlanningModeAuto)
	plan, targets, task, err := NewPlanSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 || task == nil {
		t.Fatalf("AUTO intent created work: targets=%#v task=%#v", targets, task)
	}
	return plan, task
}

func fixtureTarget(
	t *testing.T,
	plan PlanSnapshot,
	category, amount string,
	index int,
) PlanTarget {
	t.Helper()
	budget, _ := shareddomain.NewMoney(amount, string(plan.TotalBudget.Currency))
	scope, _ := NewResearchScope(
		category, plan.LocationContext, nil, nil, nil, nil, "", URLModeNone,
	)
	now := time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)
	return PlanTarget{
		ID: PlanTargetID("target-" + category), PlanID: plan.ID,
		Title: category, NormalizedIntent: "buy " + category, Category: category,
		AllocatedBudget: budget, ResearchScope: scope, OrderIndex: index,
		TargetHashSchema: PlanTargetHashSchemaV1,
		Version:          1, CreatedAt: now, UpdatedAt: now,
	}
}

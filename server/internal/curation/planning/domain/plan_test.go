package domain

import (
	"errors"
	"testing"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func TestNewShoppingPlanPreservesImmutableRequestSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC)
	budget, err := shareddomain.NewMoney("1200", "USD")
	if err != nil {
		t.Fatal(err)
	}
	location, err := shareddomain.NewLocationContext("US", "Seattle")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := NewResearchScope(
		"laptop", location, []string{"new"}, []string{"used"},
		nil, nil, "", URLModeNone,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewShoppingPlan(NewShoppingPlanInput{
		PlanID: "plan-1", UserID: "user-1",
		OriginalIntent: "  개발용 노트북  ",
		PlanningMode:   PlanningModeAuto,
		ExecutionMode:  ExecutionModeLive,
		TotalBudget:    budget, LocationContext: location,
		ResearchScope: scope, AgentMode: AgentModeManaged,
		ModelKey: "gpt-5", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.OriginalIntent != "개발용 노트북" || plan.CreatedAt != now {
		t.Fatalf("plan=%+v", plan)
	}
	firstHash, err := plan.ContextHash()
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := plan.ContextHash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == "" || firstHash != secondHash {
		t.Fatalf("context hashes=%q %q", firstHash, secondHash)
	}
}

func TestNewShoppingPlanRejectsUnsupportedLiveCountry(t *testing.T) {
	budget, err := shareddomain.NewMoney("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	location, err := shareddomain.NewLocationContext("JP", "Seoul")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := NewResearchScope(
		"chair", location, nil, nil, nil, nil, "", URLModeNone,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewShoppingPlan(NewShoppingPlanInput{
		PlanID: "plan-1", UserID: "user-1", OriginalIntent: "chair",
		PlanningMode:  PlanningModeSingle,
		ExecutionMode: ExecutionModeLive,
		TotalBudget:   budget, LocationContext: location,
		ResearchScope: scope, AgentMode: AgentModeExternal,
		Now: time.Now(),
	})
	if !errors.Is(err, ErrLiveCountryUnsupported) {
		t.Fatalf("error=%v want=%v", err, ErrLiveCountryUnsupported)
	}
}

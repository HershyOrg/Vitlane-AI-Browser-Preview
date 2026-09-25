package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type curationCommandRollbackTransactor struct {
	repository *memoryCurationRepository
}

func (t curationCommandRollbackTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	record := t.repository.record
	record.Targets = append(
		[]curationdomain.PlanTarget(nil),
		t.repository.record.Targets...,
	)
	var task *curationdomain.PlanningTask
	if t.repository.task != nil {
		value := *t.repository.task
		task = &value
	}
	actions := make(map[string]curationdomain.CurationAction)
	for key, action := range t.repository.actions {
		actions[key] = action
	}
	runs := make(map[string]curationdomain.CurationRun)
	for key, run := range t.repository.runs {
		runs[key] = run
	}
	err := fn(context.WithValue(ctx, planningTransactionKey{}, true))
	if err != nil {
		t.repository.record = record
		t.repository.task = task
		t.repository.actions = actions
		t.repository.runs = runs
	}
	return err
}

func TestFirstPlanningActionCreatesInitialWorkAndReplays(t *testing.T) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	plan := curationdomain.PlanSnapshot{
		ID: "plan-1", UserID: "user-1",
		OriginalIntent: "휴대용 조명을 골라줘",
		PlanningMode:   curationdomain.PlanningModeAuto,
		CreatedAt:      now,
	}
	initialContextHash, err := plan.ContextHash()
	if err != nil {
		t.Fatal(err)
	}
	base := &memoryPlanningRepository{record: PlanRecord{
		Plan: plan,
		Curation: curationdomain.Curation{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1",
			Phase: curationdomain.CurationPhasePlanning, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		},
		Targets: []curationdomain.PlanTarget{},
	}}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	service := NewService(
		repository,
		&memorySessions{},
		curationCommandRollbackTransactor{repository: repository},
		fixedClock{now: now},
		&sequenceIDs{values: []string{"task-1", "run-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	input := ExecuteExpansionActionInput{
		ActionID: "11111111-1111-4111-8111-111111111111",
		UserID:   "user-1", AuthSessionID: "auth-session-1",
		CurationID: "curation-1", PlanID: "plan-1",
		Type:                    curationdomain.CurationActionPlanningAddTargets,
		Instruction:             "휴대용 조명을 추가해줘",
		ExpectedCurationVersion: 1,
	}

	first, err := service.ExecuteExpansionAction(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.ExecuteExpansionAction(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	action := base.actions[input.ActionID]
	if action.Type != curationdomain.CurationActionPlanningAddTargets ||
		action.SourceRefType != curationdomain.CurationActionSourceCurationRunRequest ||
		action.SourceRefID != input.ActionID ||
		first.Run.Kind != curationdomain.CurationRunInitial ||
		first.PlanningTask.ContextVersion != base.record.Curation.Version ||
		first.PlanningTask.ContextHash != initialContextHash ||
		!replay.Replay {
		t.Fatalf("action=%#v first=%#v replay=%#v", action, first, replay)
	}

	distinct := input
	distinct.ActionID = "22222222-2222-4222-8222-222222222222"
	distinct.Instruction = "업무용 조명으로 다시 구성해줘"
	if _, err := service.ExecuteExpansionAction(
		context.Background(),
		distinct,
	); !errors.Is(err, curationdomain.ErrExpansionInProgress) {
		t.Fatalf("distinct action while INITIAL task is active: %v", err)
	}
	if len(base.actions) != 1 ||
		len(repository.runs) != 1 ||
		base.task == nil ||
		base.task.ID != first.PlanningTask.ID {
		t.Fatalf(
			"conflict leaked state: actions=%#v runs=%#v task=%#v",
			base.actions, repository.runs, base.task,
		)
	}
}

func TestExecuteExpansionActionRejectsCurationPlanMismatch(t *testing.T) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	base := &memoryPlanningRepository{record: PlanRecord{
		Plan: curationdomain.PlanSnapshot{
			ID: "plan-1", UserID: "user-1",
		},
		Curation: curationdomain.Curation{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1",
			Phase: curationdomain.CurationPhasePlanning, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		},
	}}
	service := NewService(
		&memoryCurationRepository{
			memoryPlanningRepository: base,
			runs:                     map[string]curationdomain.CurationRun{},
		},
		&memorySessions{},
		passthroughTransactor{},
		fixedClock{now: now},
		&sequenceIDs{values: []string{"task-1", "run-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	_, err := service.ExecuteExpansionAction(
		context.Background(),
		ExecuteExpansionActionInput{
			ActionID: "11111111-1111-4111-8111-111111111111",
			UserID:   "user-1", CurationID: "curation-1",
			PlanID:      "another-plan",
			Type:        curationdomain.CurationActionPlanningAddTargets,
			Instruction: "조명 추가", ExpectedCurationVersion: 1,
		},
	)
	if err == nil {
		t.Fatal("mismatched Curation/Plan was accepted")
	}
}

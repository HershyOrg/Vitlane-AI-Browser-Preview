package domain

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestNewTargetRemoveCurationActionAllowsOnlyOwnedPatchEnvelope(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 11, 12, 0, time.UTC)
	targetID := "target-1"
	sum := sha256.Sum256([]byte("target remove"))
	action, err := NewTargetRemoveCurationAction(NewCurationActionInput{
		ID:                     "action-1",
		CurationID:             "curation-1",
		ActorUserID:            "user-1",
		PhaseAtRequest:         CurationActionPhasePlanning,
		CurrentCurationVersion: 3,
		Request: CurationActionRequest{
			Type:                    CurationActionTargetRemove,
			SubjectType:             CurationActionSubjectTarget,
			SubjectID:               &targetID,
			ExpectedCurationVersion: 3,
		},
		SourceRefType: CurationActionSourcePlanTarget,
		SourceRefID:   targetID,
		RequestHash:   sum[:],
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != CurationActionTargetRemove ||
		action.EffectKind != CurationActionEffectNone ||
		action.SubjectID == nil ||
		*action.SubjectID != targetID ||
		action.SourceRefType != CurationActionSourcePlanTarget ||
		action.SourceRefID != targetID {
		t.Fatalf("action=%#v", action)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("persisted target removal failed validation: %v", err)
	}

	action.SourceRefID = "target-2"
	if err := action.Validate(); !errors.Is(err, ErrCurationActionInvalid) {
		t.Fatalf("mismatched source lineage error=%v", err)
	}

	_, err = NewTargetRemoveCurationAction(NewCurationActionInput{
		Request: CurationActionRequest{
			Type: CurationActionPlanningAddTargets,
		},
	})
	if !errors.Is(err, ErrCurationActionTypeInvalid) {
		t.Fatalf("wrong action type error=%v", err)
	}
}

func TestCurationRemoveTargetBumpsOnlyCurationAndTargetVersions(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	curation := Curation{
		ID:             "curation-1",
		ShoppingPlanID: "plan-1",
		UserID:         "user-1",
		Phase:          CurationPhaseCurating,
		Version:        4,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}
	target := PlanTarget{
		ID:         "target-1",
		CurationID: curation.ID,
		UserID:     curation.UserID,
		PlanID:     curation.ShoppingPlanID,
		Version:    2,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	}
	removedAt := createdAt.Add(time.Minute)

	if err := curation.RemoveTarget(
		&target,
		"user-1",
		4,
		removedAt,
	); err != nil {
		t.Fatal(err)
	}
	if curation.Version != 5 ||
		!curation.UpdatedAt.Equal(removedAt) ||
		target.Version != 3 ||
		target.RemovedAt == nil ||
		!target.RemovedAt.Equal(removedAt) ||
		target.RemovedByUserID == nil ||
		*target.RemovedByUserID != "user-1" {
		t.Fatalf("curation=%#v target=%#v", curation, target)
	}
}

func TestCurationRemoveTargetRejectsStaleAndForeignLineage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	baseCuration := Curation{
		ID:             "curation-1",
		ShoppingPlanID: "plan-1",
		UserID:         "user-1",
		Phase:          CurationPhasePlanning,
		Version:        3,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	baseTarget := PlanTarget{
		ID:         "target-1",
		CurationID: baseCuration.ID,
		UserID:     baseCuration.UserID,
		PlanID:     baseCuration.ShoppingPlanID,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	tests := []struct {
		name            string
		expectedVersion int64
		actor           UserID
		mutate          func(*PlanTarget)
		want            error
	}{
		{
			name:            "stale curation",
			expectedVersion: 2,
			actor:           "user-1",
			mutate:          func(*PlanTarget) {},
			want:            ErrVersionConflict,
		},
		{
			name:            "foreign actor",
			expectedVersion: 3,
			actor:           "user-2",
			mutate:          func(*PlanTarget) {},
			want:            ErrTargetNotFound,
		},
		{
			name:            "foreign curation",
			expectedVersion: 3,
			actor:           "user-1",
			mutate: func(target *PlanTarget) {
				target.CurationID = "curation-2"
			},
			want: ErrTargetNotFound,
		},
		{
			name:            "foreign shopping plan",
			expectedVersion: 3,
			actor:           "user-1",
			mutate: func(target *PlanTarget) {
				target.PlanID = "plan-2"
			},
			want: ErrTargetNotFound,
		},
		{
			name:            "already removed",
			expectedVersion: 3,
			actor:           "user-1",
			mutate: func(target *PlanTarget) {
				removedAt := now
				target.RemovedAt = &removedAt
			},
			want: ErrTargetNotFound,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			curation := baseCuration
			target := baseTarget
			test.mutate(&target)
			err := curation.RemoveTarget(
				&target,
				test.actor,
				test.expectedVersion,
				now.Add(time.Minute),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if curation.Version != baseCuration.Version ||
				target.Version != baseTarget.Version {
				t.Fatalf(
					"failed removal mutated state: curation=%#v target=%#v",
					curation,
					target,
				)
			}
		})
	}
}

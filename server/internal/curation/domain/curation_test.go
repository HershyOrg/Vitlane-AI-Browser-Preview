package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewCurationStartsInPlanning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	curation, err := NewCuration(NewCurationInput{
		ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if curation.ID == CurationID(curation.ShoppingPlanID) {
		t.Fatal("test fixture must prove curation identity is not the plan ID alias")
	}
	if curation.Phase != CurationPhasePlanning ||
		curation.Version != 1 ||
		!curation.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected new curation: %#v", curation)
	}
}

func TestCurationOnlyTransitionsPlanningToCurating(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	curation, err := NewCuration(NewCurationInput{
		ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := curation.StartCurating(1, 1, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if curation.Phase != CurationPhaseCurating || curation.Version != 2 {
		t.Fatalf("unexpected transition: %#v", curation)
	}
	if err := curation.StartCurating(1, 1, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("transition replay must be harmless: %v", err)
	}

	invalid := curation
	invalid.Phase = "PURCHASE"
	if err := invalid.Validate(); !errors.Is(err, ErrCurationPhaseInvalid) {
		t.Fatalf("purchase leaked into curation phase: %v", err)
	}
}

func TestStartCuratingRequiresTargetsAndCurrentVersion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	fixture := func() Curation {
		curation, err := NewCuration(NewCurationInput{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return curation
	}

	empty := fixture()
	if err := empty.StartCurating(0, 1, now); !errors.Is(
		err, ErrCurationTargetsRequired,
	) {
		t.Fatalf("expected target requirement, got %v", err)
	}
	stale := fixture()
	if err := stale.StartCurating(1, 2, now); !errors.Is(
		err, ErrVersionConflict,
	) {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestArchiveDoesNotChangeCurationPhase(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	curation, err := NewCuration(NewCurationInput{
		ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := curation.Archive("user-1", 1, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if curation.Phase != CurationPhasePlanning ||
		curation.ArchivedAt == nil ||
		curation.Version != 2 {
		t.Fatalf("archive changed phase semantics: %#v", curation)
	}
	if err := curation.StartCurating(1, 2, now.Add(2*time.Minute)); !errors.Is(
		err, ErrCurationArchived,
	) {
		t.Fatalf("archived curation started: %v", err)
	}
}

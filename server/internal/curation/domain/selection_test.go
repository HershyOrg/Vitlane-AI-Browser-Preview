package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewCurationSelectionKeepsResolvedLineage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)

	selection, err := NewCurationSelection(
		"selection-1", "user-1", "curation-independent-1", "target-actual-1",
		"session-1", "candidate-1", "configuration-1",
		"sha256:configuration-1", 2, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if selection.CurationID != "curation-independent-1" ||
		selection.PlanTargetID != "target-actual-1" ||
		selection.CandidateConfigurationID != "configuration-1" ||
		selection.CandidateConfigurationHash != "sha256:configuration-1" ||
		selection.Version != 1 ||
		!selection.SelectedAt.Equal(now) ||
		!selection.UpdatedAt.Equal(now) {
		t.Fatalf("selection=%#v", selection)
	}
}

func TestNewCurationSelectionRejectsIncompleteLineageAndQuantity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name       string
		targetID   string
		configHash string
		quantity   int64
		now        time.Time
	}{
		{name: "target", targetID: "", configHash: "hash", quantity: 1, now: now},
		{name: "configuration hash", targetID: "target", configHash: "", quantity: 1, now: now},
		{name: "quantity low", targetID: "target", configHash: "hash", quantity: 0, now: now},
		{name: "quantity high", targetID: "target", configHash: "hash", quantity: 100, now: now},
		{name: "time", targetID: "target", configHash: "hash", quantity: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCurationSelection(
				"selection", "user", "curation", test.targetID,
				"session", "candidate", "configuration", test.configHash,
				test.quantity, test.now,
			)
			if !errors.Is(err, ErrSelectionInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCurationSelectionRevisionAndRemovalUseOptimisticVersion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	selection, err := NewCurationSelection(
		"selection", "user", "curation", "target", "session", "candidate",
		"configuration-1", "hash-1", 1, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	revisedAt := now.Add(time.Minute)
	if err := selection.Revise(
		"configuration-2", "hash-2", 3, 1, revisedAt,
	); err != nil {
		t.Fatal(err)
	}
	if selection.CandidateConfigurationID != "configuration-2" ||
		selection.CandidateConfigurationHash != "hash-2" ||
		selection.Quantity != 3 || selection.Version != 2 {
		t.Fatalf("revised selection=%#v", selection)
	}
	if err := selection.Revise(
		"configuration-3", "hash-3", 4, 1, revisedAt,
	); !errors.Is(err, ErrSelectionVersionConflict) {
		t.Fatalf("stale revision err=%v", err)
	}

	removedAt := revisedAt.Add(time.Minute)
	if err := selection.Remove(2, removedAt); err != nil {
		t.Fatal(err)
	}
	if selection.RemovedAt == nil || !selection.RemovedAt.Equal(removedAt) ||
		selection.Version != 3 {
		t.Fatalf("removed selection=%#v", selection)
	}
	if err := selection.Remove(3, removedAt); !errors.Is(
		err, ErrSelectionVersionConflict,
	) {
		t.Fatalf("repeated removal err=%v", err)
	}
}

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type targetSelectionRemovalTransactionKey struct{}

type targetSelectionRemovalTestRepository struct {
	selections map[string]curationdomain.CurationSelection
	calls      int
}

func (r *targetSelectionRemovalTestRepository) SoftRemoveActiveSelectionsForTarget(
	ctx context.Context,
	userID, curationID, targetID string,
	removedAt time.Time,
) error {
	if ctx.Value(targetSelectionRemovalTransactionKey{}) != true {
		return errors.New("missing ambient transaction")
	}
	r.calls++
	for id, selection := range r.selections {
		if selection.UserID != userID ||
			selection.CurationID != curationID ||
			selection.PlanTargetID != targetID ||
			selection.RemovedAt != nil {
			continue
		}
		if err := selection.Remove(selection.Version, removedAt); err != nil {
			return err
		}
		r.selections[id] = selection
	}
	return nil
}

func TestRemoveActiveSelectionsForTargetSoftRemovesOnlyActiveLineage(
	t *testing.T,
) {
	t.Parallel()
	selectedAt := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	removedAt := selectedAt.Add(time.Minute)
	repository := &targetSelectionRemovalTestRepository{
		selections: map[string]curationdomain.CurationSelection{
			"selection-1": targetSelectionRemovalFixture(
				t, "selection-1", "user-1", "curation-1", "target-1",
				selectedAt,
			),
			"selection-2": targetSelectionRemovalFixture(
				t, "selection-2", "user-1", "curation-1", "target-1",
				selectedAt,
			),
			"other-target": targetSelectionRemovalFixture(
				t, "other-target", "user-1", "curation-1", "target-2",
				selectedAt,
			),
			"other-user": targetSelectionRemovalFixture(
				t, "other-user", "user-2", "curation-1", "target-1",
				selectedAt,
			),
		},
	}
	alreadyRemoved := repository.selections["selection-2"]
	if err := alreadyRemoved.Remove(1, selectedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	repository.selections["selection-2"] = alreadyRemoved
	service := NewSelectionService(
		repository,
		&selectionTestClock{now: removedAt},
		&selectionTestIDs{},
	)
	ctx := context.WithValue(
		context.Background(),
		targetSelectionRemovalTransactionKey{},
		true,
	)

	if err := service.RemoveActiveSelectionsForTarget(
		ctx,
		" user-1 ",
		" curation-1 ",
		" target-1 ",
		removedAt,
	); err != nil {
		t.Fatal(err)
	}

	removed := repository.selections["selection-1"]
	if repository.calls != 1 ||
		removed.RemovedAt == nil ||
		!removed.RemovedAt.Equal(removedAt) ||
		removed.UpdatedAt != removedAt ||
		removed.Version != 2 {
		t.Fatalf("removed=%#v calls=%d", removed, repository.calls)
	}
	preservedRemoved := repository.selections["selection-2"]
	if preservedRemoved.Version != 2 ||
		preservedRemoved.RemovedAt == nil ||
		!preservedRemoved.RemovedAt.Equal(selectedAt.Add(time.Second)) {
		t.Fatalf("terminal selection changed=%#v", preservedRemoved)
	}
	if repository.selections["other-target"].RemovedAt != nil ||
		repository.selections["other-user"].RemovedAt != nil {
		t.Fatalf("unrelated lineage changed=%#v", repository.selections)
	}
}

func TestRemoveActiveSelectionsForTargetRejectsInvalidBoundaryInput(
	t *testing.T,
) {
	t.Parallel()
	repository := &targetSelectionRemovalTestRepository{}
	service := NewSelectionService(
		repository,
		&selectionTestClock{},
		&selectionTestIDs{},
	)
	if err := service.RemoveActiveSelectionsForTarget(
		context.Background(),
		"user-1",
		"curation-1",
		"",
		time.Now().UTC(),
	); !errors.Is(err, curationdomain.ErrSelectionInvalid) {
		t.Fatalf("error=%v", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls=%d", repository.calls)
	}
}

func targetSelectionRemovalFixture(
	t *testing.T,
	id, userID, curationID, targetID string,
	now time.Time,
) curationdomain.CurationSelection {
	t.Helper()
	selection, err := curationdomain.NewCurationSelection(
		id,
		userID,
		curationID,
		targetID,
		"session-"+id,
		"candidate-"+id,
		"configuration-"+id,
		"hash-"+id,
		1,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

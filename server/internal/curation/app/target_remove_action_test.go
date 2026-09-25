package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

const targetRemoveTestTargetID = "33333333-3333-4333-8333-333333333333"

type targetRemoveTransactionKey struct{}

type targetSelectionRemovalCall struct {
	userID     string
	curationID string
	targetID   string
	removedAt  time.Time
}

type targetRemoveTestRepository struct {
	*actionTestRepository
	target             curationdomain.PlanTarget
	writes             []string
	selectionRemovals  []targetSelectionRemovalCall
	selectionsRemoved  bool
	targetSaveErr      error
	selectionRemoveErr error
	curationSaveErr    error
}

func (r *targetRemoveTestRepository) GetCurationTarget(
	_ context.Context,
	userID, curationID, targetID string,
	_ bool,
) (curationdomain.PlanTarget, error) {
	if string(r.target.UserID) != userID ||
		string(r.target.CurationID) != curationID ||
		string(r.target.ID) != targetID {
		return curationdomain.PlanTarget{}, curationdomain.ErrTargetNotFound
	}
	return r.target, nil
}

func (r *targetRemoveTestRepository) SaveCurationTargetRemoval(
	_ context.Context,
	previousVersion int64,
	target curationdomain.PlanTarget,
) error {
	if r.targetSaveErr != nil {
		return r.targetSaveErr
	}
	if r.target.Version != previousVersion {
		return curationdomain.ErrVersionConflict
	}
	r.writes = append(r.writes, "target")
	r.target = target
	return nil
}

func (r *targetRemoveTestRepository) SaveCuration(
	_ context.Context,
	previousVersion int64,
	curation curationdomain.Curation,
) error {
	if r.curationSaveErr != nil {
		return r.curationSaveErr
	}
	if r.curation.Version != previousVersion {
		return curationdomain.ErrVersionConflict
	}
	r.writes = append(r.writes, "curation")
	r.curation = curation
	return nil
}

func (r *targetRemoveTestRepository) InsertCurationAction(
	ctx context.Context,
	action curationdomain.CurationAction,
) (bool, error) {
	inserted, err := r.actionTestRepository.InsertCurationAction(ctx, action)
	if err == nil && inserted {
		r.writes = append(r.writes, "action")
	}
	return inserted, err
}

type targetRemoveTestSelectionRemover struct {
	repository *targetRemoveTestRepository
}

func (r targetRemoveTestSelectionRemover) RemoveActiveSelectionsForTarget(
	ctx context.Context,
	userID, curationID, targetID string,
	removedAt time.Time,
) error {
	if ctx.Value(targetRemoveTransactionKey{}) != true {
		return errors.New("selection removal ran outside ambient transaction")
	}
	if r.repository.selectionRemoveErr != nil {
		return r.repository.selectionRemoveErr
	}
	r.repository.writes = append(r.repository.writes, "selections")
	r.repository.selectionRemovals = append(
		r.repository.selectionRemovals,
		targetSelectionRemovalCall{
			userID:     userID,
			curationID: curationID,
			targetID:   targetID,
			removedAt:  removedAt,
		},
	)
	r.repository.selectionsRemoved = true
	return nil
}

type targetRemoveRollbackTransactor struct {
	repository *targetRemoveTestRepository
}

func (t targetRemoveRollbackTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	actions := make(map[string]curationdomain.CurationAction)
	for key, value := range t.repository.actions {
		actions[key] = value
	}
	order := slices.Clone(t.repository.order)
	writes := slices.Clone(t.repository.writes)
	selectionRemovals := slices.Clone(t.repository.selectionRemovals)
	selectionsRemoved := t.repository.selectionsRemoved
	curation := t.repository.curation
	target := t.repository.target
	err := fn(context.WithValue(
		ctx,
		targetRemoveTransactionKey{},
		true,
	))
	if err != nil {
		t.repository.actions = actions
		t.repository.order = order
		t.repository.writes = writes
		t.repository.selectionRemovals = selectionRemovals
		t.repository.selectionsRemoved = selectionsRemoved
		t.repository.curation = curation
		t.repository.target = target
	}
	return err
}

func TestExecuteTargetRemoveActionCommitsActionTargetAndCurationOnce(
	t *testing.T,
) {
	t.Parallel()
	repository := newTargetRemoveTestRepository()
	service := newTargetRemoveTestService(repository)
	input := targetRemoveActionInput(actionTestActionID, 3)

	first, err := service.ExecuteTargetRemoveAction(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replay ||
		first.Action.Type != curationdomain.CurationActionTargetRemove ||
		first.Action.SourceRefType !=
			curationdomain.CurationActionSourcePlanTarget ||
		first.TargetID != targetRemoveTestTargetID ||
		first.CurationVersion != 4 ||
		repository.curation.Version != 4 ||
		repository.target.RemovedAt == nil ||
		repository.target.RemovedByUserID == nil ||
		repository.target.Version != 2 ||
		!repository.selectionsRemoved ||
		len(repository.selectionRemovals) != 1 ||
		repository.selectionRemovals[0].userID != "user-1" ||
		repository.selectionRemovals[0].curationID != actionTestCurationID ||
		repository.selectionRemovals[0].targetID != targetRemoveTestTargetID ||
		!repository.selectionRemovals[0].removedAt.Equal(first.RemovedAt) ||
		!slices.Equal(
			repository.writes,
			[]string{"action", "target", "selections", "curation"},
		) {
		t.Fatalf("first=%#v repository=%#v", first, repository)
	}

	replay, err := service.ExecuteTargetRemoveAction(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay ||
		replay.Action.ID != first.Action.ID ||
		replay.TargetID != first.TargetID ||
		replay.CurationVersion != first.CurationVersion ||
		!replay.RemovedAt.Equal(first.RemovedAt) ||
		len(repository.actions) != 1 ||
		len(repository.selectionRemovals) != 1 ||
		!slices.Equal(
			repository.writes,
			[]string{"action", "target", "selections", "curation"},
		) {
		t.Fatalf("replay=%#v repository=%#v", replay, repository)
	}

	different := input
	different.TargetID = "44444444-4444-4444-8444-444444444444"
	if _, err := service.ExecuteTargetRemoveAction(
		context.Background(),
		different,
	); !errors.Is(err, curationdomain.ErrIdempotencyKeyReused) {
		t.Fatalf("different payload error=%v", err)
	}
}

func TestExecuteTargetRemoveActionRejectsStaleAndForeignTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  ExecuteTargetRemoveActionInput
		mutate func(*targetRemoveTestRepository)
		want   error
	}{
		{
			name:  "stale curation",
			input: targetRemoveActionInput(actionTestActionID, 2),
			mutate: func(*targetRemoveTestRepository) {
			},
			want: curationdomain.ErrVersionConflict,
		},
		{
			name:  "foreign target lineage",
			input: targetRemoveActionInput(actionTestActionID, 3),
			mutate: func(repository *targetRemoveTestRepository) {
				repository.target.CurationID = "curation-other"
			},
			want: curationdomain.ErrTargetNotFound,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newTargetRemoveTestRepository()
			test.mutate(repository)
			service := newTargetRemoveTestService(repository)
			_, err := service.ExecuteTargetRemoveAction(
				context.Background(),
				test.input,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if len(repository.actions) != 0 ||
				repository.curation.Version != 3 ||
				repository.target.RemovedAt != nil {
				t.Fatalf("rejected command mutated repository=%#v", repository)
			}
		})
	}
}

func TestExecuteTargetRemoveActionRollsBackAllWrites(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("save curation failed")
	repository := newTargetRemoveTestRepository()
	repository.curationSaveErr = sentinel
	service := newTargetRemoveTestService(repository)

	_, err := service.ExecuteTargetRemoveAction(
		context.Background(),
		targetRemoveActionInput(actionTestActionID, 3),
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v want=%v", err, sentinel)
	}
	if len(repository.actions) != 0 ||
		len(repository.writes) != 0 ||
		len(repository.selectionRemovals) != 0 ||
		repository.selectionsRemoved ||
		repository.curation.Version != 3 ||
		repository.target.RemovedAt != nil ||
		repository.target.Version != 1 {
		t.Fatalf("rollback leaked writes: %#v", repository)
	}
}

func newTargetRemoveTestRepository() *targetRemoveTestRepository {
	actionRepository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	return &targetRemoveTestRepository{
		actionTestRepository: actionRepository,
		target: curationdomain.PlanTarget{
			ID:         targetRemoveTestTargetID,
			CurationID: actionRepository.curation.ID,
			UserID:     actionRepository.curation.UserID,
			PlanID:     actionRepository.curation.ShoppingPlanID,
			Version:    1,
			CreatedAt:  actionRepository.curation.CreatedAt,
			UpdatedAt:  actionRepository.curation.UpdatedAt,
		},
	}
}

func newTargetRemoveTestService(
	repository *targetRemoveTestRepository,
) *Service {
	service := NewService(
		repository,
		&memorySessions{},
		targetRemoveRollbackTransactor{repository: repository},
		fixedClock{
			now: time.Date(2026, 7, 31, 7, 8, 9, 0, time.UTC),
		},
		&sequenceIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	service.EnableTargetSelectionRemover(
		targetRemoveTestSelectionRemover{repository: repository},
	)
	return service
}

func targetRemoveActionInput(
	actionID string,
	expectedVersion int64,
) ExecuteTargetRemoveActionInput {
	return ExecuteTargetRemoveActionInput{
		ActionID:                actionID,
		UserID:                  "user-1",
		CurationID:              actionTestCurationID,
		TargetID:                targetRemoveTestTargetID,
		ExpectedCurationVersion: expectedVersion,
	}
}

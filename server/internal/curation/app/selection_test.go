package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type selectionTestRepository struct {
	resolved              ResolvedSelectionConfiguration
	resolveErr            error
	resolvedCurationID    string
	resolvedConfiguration string
	selections            map[string]curationdomain.CurationSelection
	commands              map[string]SelectionCommand
	createCalls           int
	updateCalls           int
	removeCalls           int
	actionInputs          []SelectionMutationActionInput
}

func newSelectionTestRepository() *selectionTestRepository {
	return &selectionTestRepository{
		selections: map[string]curationdomain.CurationSelection{},
		commands:   map[string]SelectionCommand{},
	}
}

func (r *selectionTestRepository) GetCartView(
	_ context.Context,
	_ string,
	curationID string,
) (curationdomain.CartView, error) {
	return curationdomain.CartView{CurationID: curationID}, nil
}

func (r *selectionTestRepository) GetSelection(
	_ context.Context,
	userID, curationID, selectionID string,
	_ bool,
) (curationdomain.CurationSelection, error) {
	selection, found := r.selections[selectionID]
	if !found || selection.UserID != userID || selection.CurationID != curationID {
		return curationdomain.CurationSelection{},
			curationdomain.ErrSelectionNotFound
	}
	return selection, nil
}

func (r *selectionTestRepository) FindSelectionCommand(
	_ context.Context,
	userID, commandID string,
) (SelectionCommand, bool, error) {
	command, found := r.commands[commandID]
	if !found || command.UserID != userID {
		return SelectionCommand{}, false, nil
	}
	return command, true, nil
}

func (r *selectionTestRepository) ResolveSelectionConfiguration(
	_ context.Context,
	_ string,
	curationID, configurationID string,
) (ResolvedSelectionConfiguration, error) {
	r.resolvedCurationID = curationID
	r.resolvedConfiguration = configurationID
	if r.resolveErr != nil {
		return ResolvedSelectionConfiguration{}, r.resolveErr
	}
	return r.resolved, nil
}

func (r *selectionTestRepository) CreateSelection(
	_ context.Context,
	mutation SelectionMutation,
) error {
	r.createCalls++
	r.selections[mutation.Selection.ID] = mutation.Selection
	r.commands[mutation.Command.ClientCommandID] = mutation.Command
	return nil
}

func (r *selectionTestRepository) UpdateSelection(
	_ context.Context,
	mutation SelectionMutation,
) error {
	r.updateCalls++
	r.selections[mutation.Selection.ID] = mutation.Selection
	r.commands[mutation.Command.ClientCommandID] = mutation.Command
	return nil
}

func (r *selectionTestRepository) RemoveSelection(
	_ context.Context,
	mutation SelectionMutation,
) error {
	r.removeCalls++
	r.selections[mutation.Selection.ID] = mutation.Selection
	r.commands[mutation.Command.ClientCommandID] = mutation.Command
	return nil
}

func (r *selectionTestRepository) LockSelectionSnapshots(
	_ context.Context,
	_, _ string,
	references []SelectionReference,
) ([]SelectionSnapshotResult, error) {
	return make([]SelectionSnapshotResult, len(references)), nil
}

type selectionTestTransactor struct{}

func (selectionTestTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	return fn(ctx)
}

type selectionTestActionRecorder struct {
	repository *selectionTestRepository
}

func (r selectionTestActionRecorder) RecordSelectionMutationAction(
	_ context.Context,
	input SelectionMutationActionInput,
) error {
	r.repository.actionInputs = append(r.repository.actionInputs, input)
	return nil
}

type selectionTestClock struct {
	now time.Time
}

func (c *selectionTestClock) Now() time.Time {
	return c.now
}

type selectionTestIDs struct {
	next int
}

func (i *selectionTestIDs) NewID() string {
	i.next++
	if i.next == 1 {
		return "11111111-1111-4111-8111-111111111111"
	}
	return "22222222-2222-4222-8222-222222222222"
}

func newSelectionTestService(
	repository *selectionTestRepository,
	clock *selectionTestClock,
) *SelectionService {
	service := NewSelectionService(repository, clock, &selectionTestIDs{})
	service.EnableTransactor(selectionTestTransactor{})
	service.EnableSelectionMutationActions(selectionTestActionRecorder{
		repository: repository,
	})
	return service
}

func TestCreateSelectionUsesResolvedConfigurationLineageAndReplays(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	repository := newSelectionTestRepository()
	repository.resolved = ResolvedSelectionConfiguration{
		CurationID:        "curation-independent-1",
		PlanTargetID:      "target-actual-1",
		SessionID:         "session-1",
		CandidateID:       "candidate-1",
		ConfigurationID:   "configuration-1",
		ConfigurationHash: "sha256:configuration-1",
	}
	service := newSelectionTestService(
		repository,
		&selectionTestClock{now: now},
	)
	input := CreateSelectionInput{
		UserID:                  "user-1",
		CurationID:              "curation-independent-1",
		ConfigurationID:         "configuration-1",
		Quantity:                2,
		ClientCommandID:         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ExpectedCurationVersion: 1,
	}

	created, err := service.CreateSelection(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Replay ||
		created.Selection.CurationID != "curation-independent-1" ||
		created.Selection.PlanTargetID != "target-actual-1" ||
		created.Selection.PlanTargetID == "DERIVE" ||
		created.Selection.CandidateConfigurationID != "configuration-1" ||
		created.Selection.CandidateConfigurationHash !=
			"sha256:configuration-1" {
		t.Fatalf("created=%#v", created)
	}
	if repository.resolvedCurationID != "curation-independent-1" ||
		repository.resolvedConfiguration != "configuration-1" ||
		repository.createCalls != 1 ||
		len(repository.actionInputs) != 1 ||
		repository.actionInputs[0].ActionID != input.ClientCommandID ||
		repository.actionInputs[0].SelectionID != created.Selection.ID {
		t.Fatalf(
			"resolve curation=%q configuration=%q creates=%d actions=%#v",
			repository.resolvedCurationID,
			repository.resolvedConfiguration,
			repository.createCalls,
			repository.actionInputs,
		)
	}
	var response curationdomain.CurationSelection
	if err := json.Unmarshal(
		repository.commands[input.ClientCommandID].ResponseSnapshot,
		&response,
	); err != nil {
		t.Fatal(err)
	}
	if response.PlanTargetID != "target-actual-1" {
		t.Fatalf("command snapshot=%#v", response)
	}

	replayed, err := service.CreateSelection(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replay || replayed.Selection.ID != created.Selection.ID ||
		repository.createCalls != 1 {
		t.Fatalf("replayed=%#v creates=%d", replayed, repository.createCalls)
	}

	input.Quantity = 3
	if _, err := service.CreateSelection(
		context.Background(),
		input,
	); !errors.Is(err, curationdomain.ErrSelectionCommandConflict) {
		t.Fatalf("conflicting command err=%v", err)
	}
}

func TestCreateSelectionRejectsRepositoryLineageMismatch(t *testing.T) {
	t.Parallel()
	repository := newSelectionTestRepository()
	repository.resolved = ResolvedSelectionConfiguration{
		CurationID:        "plan-id-is-not-curation-id",
		PlanTargetID:      "target-1",
		SessionID:         "session-1",
		CandidateID:       "candidate-1",
		ConfigurationID:   "configuration-1",
		ConfigurationHash: "sha256:configuration-1",
	}
	service := newSelectionTestService(
		repository,
		&selectionTestClock{
			now: time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC),
		},
	)

	_, err := service.CreateSelection(
		context.Background(),
		CreateSelectionInput{
			UserID:                  "user-1",
			CurationID:              "curation-independent-1",
			ConfigurationID:         "configuration-1",
			Quantity:                1,
			ClientCommandID:         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			ExpectedCurationVersion: 1,
		},
	)
	if !errors.Is(err, curationdomain.ErrSelectionInvalid) {
		t.Fatalf("err=%v", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("create calls=%d", repository.createCalls)
	}
}

func TestCreateSelectionRequiresExplicitValidQuantity(t *testing.T) {
	t.Parallel()
	repository := newSelectionTestRepository()
	service := newSelectionTestService(
		repository,
		&selectionTestClock{
			now: time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC),
		},
	)

	for _, quantity := range []int64{0, -1, 100} {
		_, err := service.CreateSelection(
			context.Background(),
			CreateSelectionInput{
				UserID:                  "user-1",
				CurationID:              "curation-independent-1",
				ConfigurationID:         "configuration-1",
				Quantity:                quantity,
				ClientCommandID:         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				ExpectedCurationVersion: 1,
			},
		)
		if !errors.Is(err, curationdomain.ErrSelectionInvalid) {
			t.Fatalf("quantity=%d err=%v", quantity, err)
		}
	}
	if repository.createCalls != 0 {
		t.Fatalf("invalid quantity reached persistence: %d", repository.createCalls)
	}
}

func TestUpdateSelectionRejectsConfigurationFromAnotherCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	repository := newSelectionTestRepository()
	current, err := curationdomain.NewCurationSelection(
		"selection-1", "user-1", "curation-1", "target-1",
		"session-1", "candidate-1", "configuration-1", "hash-1", 1, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repository.selections[current.ID] = current
	repository.resolved = ResolvedSelectionConfiguration{
		CurationID:        "curation-1",
		PlanTargetID:      "target-1",
		SessionID:         "session-1",
		CandidateID:       "candidate-other",
		ConfigurationID:   "configuration-2",
		ConfigurationHash: "hash-2",
	}
	service := newSelectionTestService(
		repository,
		&selectionTestClock{now: now.Add(time.Minute)},
	)

	_, err = service.UpdateSelection(
		context.Background(),
		UpdateSelectionInput{
			UserID:                  "user-1",
			CurationID:              "curation-1",
			SelectionID:             "selection-1",
			ExpectedVersion:         1,
			Quantity:                2,
			ConfigurationID:         "configuration-2",
			ClientCommandID:         "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			ExpectedCurationVersion: 1,
		},
	)
	if !errors.Is(err, curationdomain.ErrSelectionInvalid) {
		t.Fatalf("err=%v", err)
	}
	if repository.updateCalls != 0 {
		t.Fatalf("update calls=%d", repository.updateCalls)
	}
}

func TestRemoveSelectionPersistsSoftRemovalSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	repository := newSelectionTestRepository()
	current, err := curationdomain.NewCurationSelection(
		"selection-1", "user-1", "curation-1", "target-1",
		"session-1", "candidate-1", "configuration-1", "hash-1", 1, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repository.selections[current.ID] = current
	removedAt := now.Add(time.Minute)
	service := newSelectionTestService(
		repository,
		&selectionTestClock{now: removedAt},
	)

	result, err := service.RemoveSelection(
		context.Background(),
		RemoveSelectionInput{
			UserID:                  "user-1",
			CurationID:              "curation-1",
			SelectionID:             "selection-1",
			ExpectedVersion:         1,
			ClientCommandID:         "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
			ExpectedCurationVersion: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.RemovedAt == nil ||
		!result.Selection.RemovedAt.Equal(removedAt) ||
		result.Selection.Version != 2 || repository.removeCalls != 1 {
		t.Fatalf("result=%#v removes=%d", result, repository.removeCalls)
	}
}

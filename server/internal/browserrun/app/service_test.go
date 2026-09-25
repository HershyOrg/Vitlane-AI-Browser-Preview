package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
)

type testTransactor struct{}

func (testTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testIDs struct{ next int }

func (g *testIDs) NewID() string {
	g.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", g.next)
}

type testRepository struct {
	candidates map[string]browserdomain.CandidateReference
	runs       map[string]browserdomain.Run
	commands   map[string]CommandRecord
	events     []Event
}

func newTestRepository() *testRepository {
	return &testRepository{
		candidates: map[string]browserdomain.CandidateReference{},
		runs:       map[string]browserdomain.Run{}, commands: map[string]CommandRecord{},
	}
}

func (r *testRepository) ResolveCandidate(_ context.Context, user, curation, candidate string) (browserdomain.CandidateReference, error) {
	value, ok := r.candidates[user+"/"+curation+"/"+candidate]
	if !ok {
		return browserdomain.CandidateReference{}, browserdomain.ErrCandidateNotFound
	}
	return value, nil
}

func (r *testRepository) FindCommand(_ context.Context, user, kind, key string) (CommandRecord, bool, error) {
	value, ok := r.commands[user+"/"+key]
	return value, ok, nil
}

func (r *testRepository) InsertRun(_ context.Context, run browserdomain.Run) error {
	r.runs[run.ID] = run
	return nil
}

func (r *testRepository) GetRun(_ context.Context, user, runID string, _ bool) (browserdomain.Run, error) {
	run, ok := r.runs[runID]
	if !ok || run.UserID != user {
		return browserdomain.Run{}, browserdomain.ErrRunNotFound
	}
	return run, nil
}

func (r *testRepository) UpdateRun(_ context.Context, run browserdomain.Run, expected int64) error {
	old, ok := r.runs[run.ID]
	if !ok || old.Version != expected {
		return browserdomain.ErrVersionConflict
	}
	r.runs[run.ID] = run
	return nil
}

func (r *testRepository) AppendEvent(_ context.Context, event Event) error {
	r.events = append(r.events, event)
	return nil
}

func (r *testRepository) InsertCommand(_ context.Context, command CommandRecord) error {
	r.commands[command.UserID+"/"+command.IdempotencyKey] = command
	return nil
}

func TestServiceCreatesFromServerCandidateAndReplaysCommands(t *testing.T) {
	repository := newTestRepository()
	candidate, err := browserdomain.NewCandidateReference(
		"user-1", "curation-1", "candidate-1", "https://shop.example/products/1#ignored",
	)
	if err != nil {
		t.Fatal(err)
	}
	repository.candidates["user-1/curation-1/candidate-1"] = candidate
	service := NewService(repository, testTransactor{}, testClock{time.Now().UTC()}, &testIDs{})
	created, err := service.Create(context.Background(), CreateInput{
		UserID: "user-1", CurationID: "curation-1", CandidateID: "candidate-1",
		IdempotencyKey: "create-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Run.ProductURL != "https://shop.example/products/1" || created.Replay {
		t.Fatalf("created=%+v", created)
	}
	replayed, err := service.Create(context.Background(), CreateInput{
		UserID: "user-1", CurationID: "curation-1", CandidateID: "candidate-1",
		IdempotencyKey: "create-1",
	})
	if err != nil || !replayed.Replay || replayed.Run.ID != created.Run.ID || len(repository.events) != 1 {
		t.Fatalf("replay=%+v events=%d err=%v", replayed, len(repository.events), err)
	}
	if _, err := service.ApproveNavigation(context.Background(), CommandInput{
		UserID: "user-1", RunID: created.Run.ID, ExpectedVersion: 1,
		IdempotencyKey: "create-1",
	}); !errors.Is(err, browserdomain.ErrIdempotencyConflict) {
		t.Fatalf("cross-command idempotency reuse must conflict: %v", err)
	}
	approved, err := service.ApproveNavigation(context.Background(), CommandInput{
		UserID: "user-1", RunID: created.Run.ID, ExpectedVersion: 1,
		IdempotencyKey: "navigation-1",
	})
	if err != nil || approved.Run.State != browserdomain.StateNavigationApproved {
		t.Fatalf("navigation=%+v err=%v", approved, err)
	}
	replayed, err = service.ApproveNavigation(context.Background(), CommandInput{
		UserID: "user-1", RunID: created.Run.ID, ExpectedVersion: 1,
		IdempotencyKey: "navigation-1",
	})
	if err != nil || !replayed.Replay || len(repository.events) != 2 {
		t.Fatalf("navigation replay=%+v events=%d err=%v", replayed, len(repository.events), err)
	}
}

func TestServiceRejectsIdempotencyReuseWithDifferentMeaning(t *testing.T) {
	repository := newTestRepository()
	for _, candidateID := range []string{"candidate-1", "candidate-2"} {
		candidate, err := browserdomain.NewCandidateReference(
			"user-1", "curation-1", candidateID, "https://shop.example/products/"+candidateID,
		)
		if err != nil {
			t.Fatal(err)
		}
		repository.candidates["user-1/curation-1/"+candidateID] = candidate
	}
	service := NewService(repository, testTransactor{}, testClock{time.Now().UTC()}, &testIDs{})
	if _, err := service.Create(context.Background(), CreateInput{
		UserID: "user-1", CurationID: "curation-1", CandidateID: "candidate-1",
		IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Create(context.Background(), CreateInput{
		UserID: "user-1", CurationID: "curation-1", CandidateID: "candidate-2",
		IdempotencyKey: "same-key",
	})
	if !errors.Is(err, browserdomain.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

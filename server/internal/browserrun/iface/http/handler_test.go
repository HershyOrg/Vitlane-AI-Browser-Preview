package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	browserapp "github.com/vitlane/vitlane/server/internal/browserrun/app"
	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type handlerTransactor struct{}

func (handlerTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type handlerClock struct{ now time.Time }

func (c handlerClock) Now() time.Time { return c.now }

type handlerIDs struct{ next int }

func (g *handlerIDs) NewID() string {
	g.next++
	return fmt.Sprintf("10000000-0000-4000-8000-%012d", g.next)
}

type handlerRepository struct {
	candidate browserdomain.CandidateReference
	runs      map[string]browserdomain.Run
	commands  map[string]browserapp.CommandRecord
}

func (r *handlerRepository) ResolveCandidate(_ context.Context, user, curation, candidate string) (browserdomain.CandidateReference, error) {
	if r.candidate.UserID != user || r.candidate.CurationID != curation || r.candidate.CandidateID != candidate {
		return browserdomain.CandidateReference{}, browserdomain.ErrCandidateNotFound
	}
	return r.candidate, nil
}

func (r *handlerRepository) FindCommand(_ context.Context, user, kind, key string) (browserapp.CommandRecord, bool, error) {
	value, ok := r.commands[user+"/"+key]
	return value, ok, nil
}

func (r *handlerRepository) InsertRun(_ context.Context, run browserdomain.Run) error {
	r.runs[run.ID] = run
	return nil
}

func (r *handlerRepository) GetRun(_ context.Context, user, id string, _ bool) (browserdomain.Run, error) {
	run, ok := r.runs[id]
	if !ok || run.UserID != user {
		return browserdomain.Run{}, browserdomain.ErrRunNotFound
	}
	return run, nil
}

func (r *handlerRepository) UpdateRun(_ context.Context, run browserdomain.Run, expected int64) error {
	old, ok := r.runs[run.ID]
	if !ok || old.Version != expected {
		return browserdomain.ErrVersionConflict
	}
	r.runs[run.ID] = run
	return nil
}

func (r *handlerRepository) AppendEvent(context.Context, browserapp.Event) error { return nil }

func (r *handlerRepository) InsertCommand(_ context.Context, command browserapp.CommandRecord) error {
	r.commands[command.UserID+"/"+command.IdempotencyKey] = command
	return nil
}

func TestCreateRejectsClientURLAndUsesStoredCandidate(t *testing.T) {
	candidate, err := browserdomain.NewCandidateReference(
		"user-1", "curation-1", "candidate-1", "https://merchant.example/products/server-choice",
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := &handlerRepository{
		candidate: candidate, runs: map[string]browserdomain.Run{},
		commands: map[string]browserapp.CommandRecord{},
	}
	service := browserapp.NewService(
		repository, handlerTransactor{}, handlerClock{time.Now().UTC()}, &handlerIDs{},
	)
	handler := NewHandler(service)
	mux := stdhttp.NewServeMux()
	mux.HandleFunc(
		"POST /api/v1/curations/{curationId}/candidates/{candidateId}/browser-runs",
		handler.Create,
	)

	unsafe := authenticatedRequest(t, stdhttp.MethodPost,
		"/api/v1/curations/curation-1/candidates/candidate-1/browser-runs",
		[]byte(`{"productUrl":"https://attacker.example/item"}`))
	unsafe.Header.Set("Idempotency-Key", "create-unsafe")
	unsafeResponse := httptest.NewRecorder()
	mux.ServeHTTP(unsafeResponse, unsafe)
	if unsafeResponse.Code != stdhttp.StatusBadRequest {
		t.Fatalf("client URL status=%d body=%s", unsafeResponse.Code, unsafeResponse.Body.String())
	}

	request := authenticatedRequest(t, stdhttp.MethodPost,
		"/api/v1/curations/curation-1/candidates/candidate-1/browser-runs", []byte(`{}`))
	request.Header.Set("Idempotency-Key", "create-safe")
	responseRecorder := httptest.NewRecorder()
	mux.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != stdhttp.StatusCreated {
		t.Fatalf("create status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var body response
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != browserapp.SchemaVersion ||
		body.Run.ProductURL != "https://merchant.example/products/server-choice" {
		t.Fatalf("response=%+v", body)
	}
}

func TestCreateRequiresAuthenticatedOwner(t *testing.T) {
	handler := NewHandler(browserapp.NewService(
		&handlerRepository{runs: map[string]browserdomain.Run{}, commands: map[string]browserapp.CommandRecord{}},
		handlerTransactor{}, handlerClock{time.Now().UTC()}, &handlerIDs{},
	))
	request := httptest.NewRequest(stdhttp.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Idempotency-Key", "create")
	responseRecorder := httptest.NewRecorder()
	handler.Create(responseRecorder, request)
	if responseRecorder.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func authenticatedRequest(t *testing.T, method, target string, body []byte) *stdhttp.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
}

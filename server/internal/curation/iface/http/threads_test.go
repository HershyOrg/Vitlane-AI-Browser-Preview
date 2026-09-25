package http

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// listOnlyThreadRepository answers the list read; any other method panics
// through the nil embedded port.
type listOnlyThreadRepository struct {
	a.ThreadRepository
	user, curation string
}

func (r *listOnlyThreadRepository) ListThreads(_ context.Context, user, curation string) (d.ControlMode, []d.CurationThread, error) {
	r.user, r.curation = user, curation
	return d.ControlMode{Mode: "MANUAL", Version: 4}, []d.CurationThread{{SchemaVersion: d.ThreadSchema, ID: "thread-1", Status: "RUNNING", Actions: []d.CurationAction{}}}, nil
}

// The Web restores the composer mode and the request Threads from this one read
// instead of polling GET /control-mode beside it (ADR-0081).
func TestThreadListCarriesTheControlMode(t *testing.T) {
	t.Parallel()

	repo := &listOnlyThreadRepository{}
	handler := NewThreadHandler(a.NewThreadService(nil, repo, nil, nil))
	request := httptest.NewRequest("GET", "/api/v1/curations/curation-1/threads", nil)
	request.SetPathValue("curationId", "curation-1")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
	recorder := httptest.NewRecorder()

	handler.List(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		SchemaVersion string             `json:"schemaVersion"`
		ControlMode   *d.ControlMode     `json:"controlMode"`
		Threads       []d.CurationThread `json:"threads"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != d.ThreadSchema || body.ControlMode == nil || *body.ControlMode != (d.ControlMode{Mode: "MANUAL", Version: 4}) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	if len(body.Threads) != 1 || body.Threads[0].ID != "thread-1" {
		t.Fatalf("threads=%+v", body.Threads)
	}
	if repo.user != "user-1" || repo.curation != "curation-1" {
		t.Fatalf("list read user=%q curation=%q", repo.user, repo.curation)
	}
}

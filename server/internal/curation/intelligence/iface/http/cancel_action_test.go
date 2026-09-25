package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type fakeThreadCanceller struct {
	cancelled int
	handled   bool
	err       error
	calls     []string
}

func (f *fakeThreadCanceller) CancelActionByID(_ context.Context, userID, actionID string) (int, bool, error) {
	f.calls = append(f.calls, userID+"/"+actionID)
	return f.cancelled, f.handled, f.err
}

func cancelRequest(t *testing.T, handler *Handler) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(nethttp.MethodPost, "/api/v1/curation-actions/action-1/cancel", nil)
	request.SetPathValue("actionId", "action-1")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
	recorder := httptest.NewRecorder()
	handler.CancelAction(recorder, request)
	return recorder
}

// A Thread-owned Action is cancelled through the Thread so the Thread stops
// reporting active in the same transaction; the legacy response shape stays.
func TestCancelActionSettlesOwningThread(t *testing.T) {
	threads := &fakeThreadCanceller{cancelled: 2, handled: true}
	handler := NewHandler(nil)
	handler.SetThreadCanceller(threads)

	recorder := cancelRequest(t, handler)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body cancelActionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CancelledJobs != 2 {
		t.Fatalf("cancelledJobs = %d, want 2", body.CancelledJobs)
	}
	if len(threads.calls) != 1 || threads.calls[0] != "user-1/action-1" {
		t.Fatalf("thread canceller calls = %v", threads.calls)
	}
}

func TestCancelActionReportsThreadConflict(t *testing.T) {
	threads := &fakeThreadCanceller{handled: true, err: fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)}
	handler := NewHandler(nil)
	handler.SetThreadCanceller(threads)

	recorder := cancelRequest(t, handler)

	if recorder.Code != nethttp.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
}

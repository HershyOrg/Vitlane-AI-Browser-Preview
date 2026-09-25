package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	autoapp "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	autodomain "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type executorStub struct {
	input autoapp.Input
}

func (s *executorStub) Execute(_ context.Context, input autoapp.Input) (autoapp.Result, error) {
	s.input = input
	return autoapp.Result{
		Status: "EXECUTED", Decision: autodomain.DecisionResearchAgain,
		Source: autodomain.SourceManaged, TargetID: "target-1", ReasonCode: "MODEL_TARGET_MATCH",
	}, nil
}

func TestExecuteUsesAuthenticatedPrincipalAndIdempotencyKey(t *testing.T) {
	service := &executorStub{}
	handler := NewHandler(service)
	requestID := "11111111-1111-4111-8111-111111111111"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/curations/curation-1/auto-research", strings.NewReader(`{
		"request":"여행용 어뎁터 좀더 조사해봐",
		"expectedCurationVersion":4,
		"clientRequestId":"11111111-1111-4111-8111-111111111111"
	}`))
	request.SetPathValue("curationId", "curation-1")
	request.Header.Set("Idempotency-Key", requestID)
	request = request.WithContext(sharedapp.WithWebPrincipal(request.Context(), sharedapp.WebPrincipal{
		UserID: "user-1", AuthSessionID: "session-1",
	}))
	handler.Execute(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.input.UserID != "user-1" || service.input.CurationID != "curation-1" ||
		service.input.ClientRequestID != requestID {
		t.Fatalf("unexpected input: %#v", service.input)
	}
	var response autoapp.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TargetID != "target-1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type fakeAgentWorkStarter struct {
	input  researchapp.StartResearchInput
	result researchapp.StartResearchResult
	err    error
	calls  int
}

func TestActionLimitReasonCodesRemainMachineReadable(t *testing.T) {
	for _, err := range []error{
		researchdomain.ErrResearchActionInProgress,
		researchdomain.ErrTooManyActiveActions,
	} {
		if code := reasonCode(err); code != err.Error() {
			t.Fatalf("reasonCode(%v) = %q", err, code)
		}
	}
}

func (f *fakeAgentWorkStarter) StartResearch(
	_ context.Context,
	input researchapp.StartResearchInput,
) (researchapp.StartResearchResult, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

func TestStartResearchIsBehaviorFirstAndSessionBound(t *testing.T) {
	starter := &fakeAgentWorkStarter{
		result: researchapp.StartResearchResult{
			Tasks: []researchapp.ResearchTask{{RoundID: "round-1"}},
		},
	}
	handler := &Handler{research: starter}
	request := httptest.NewRequest(
		nethttp.MethodPost, "/",
		strings.NewReader(
			`{"curationId":"curation-1",`+
				`"curationActionId":"11111111-1111-4111-8111-111111111111",`+
				`"expectedCurationVersion":3,`+
				`"sessionIds":["session-1"]}`,
		),
	)
	request.Header.Set(
		"Idempotency-Key", "11111111-1111-4111-8111-111111111111",
	)
	request.SetPathValue("planId", "plan-1")
	request = request.WithContext(sharedapp.WithWebPrincipal(
		request.Context(),
		sharedapp.WebPrincipal{
			UserID: "user-1", AuthSessionID: "auth-session-1",
		},
	))
	response := httptest.NewRecorder()

	handler.StartResearch(response, request)

	if response.Code != nethttp.StatusCreated {
		t.Fatalf(
			"unexpected status=%d body=%s",
			response.Code, response.Body.String(),
		)
	}
	if starter.calls != 1 ||
		starter.input.UserID != "user-1" ||
		starter.input.AuthSessionID != "auth-session-1" ||
		starter.input.PlanID != "plan-1" ||
		starter.input.CurationID != "curation-1" ||
		starter.input.CurationActionID != "11111111-1111-4111-8111-111111111111" ||
		starter.input.ExpectedCurationVersion != 3 ||
		len(starter.input.SessionIDs) != 1 ||
		starter.input.SessionIDs[0] != "session-1" {
		t.Fatalf("unexpected command: %#v", starter.input)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// ADR-0038: the response carries the opened rounds, and progress arrives
	// through the workspace projection rather than a work reference.
	if _, ok := body["tasks"]; !ok {
		t.Fatalf("missing tasks projection key: %#v", body)
	}
}

func TestStartResearchRejectsConnectionSelection(t *testing.T) {
	starter := &fakeAgentWorkStarter{}
	handler := &Handler{research: starter}
	request := httptest.NewRequest(
		nethttp.MethodPost, "/",
		strings.NewReader(
			`{"curationId":"curation-1","curationActionId":"action-1",`+
				`"expectedCurationVersion":3,"sessionIds":["session-1"],`+
				`"connectionId":"connection-1"}`,
		),
	)
	request.Header.Set("Idempotency-Key", "request-1")
	request.SetPathValue("planId", "plan-1")
	request = request.WithContext(sharedapp.WithWebPrincipal(
		request.Context(),
		sharedapp.WebPrincipal{
			UserID: "user-1", AuthSessionID: "auth-session-1",
		},
	))
	response := httptest.NewRecorder()

	handler.StartResearch(response, request)

	if response.Code != nethttp.StatusBadRequest || starter.calls != 0 {
		t.Fatalf(
			"connection selection was accepted: status=%d calls=%d body=%s",
			response.Code, starter.calls, response.Body.String(),
		)
	}
}

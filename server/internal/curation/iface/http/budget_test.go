package http

import (
	"net/http"
	"strings"
	"testing"
)

func TestBudgetHTTPRequiresExplicitExpectedVersion(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodPatch, "/api/v1/curations/id/budget", h.Budget},
	} {
		for _, body := range []string{`{}`, `{"expectedVersion":null}`} {
			response := performJSON(t, tc.handler, tc.method, tc.path, body)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "BUDGET_INVALID") {
				t.Fatalf("%s response %d %s", tc.path, response.Code, response.Body.String())
			}
		}
	}
}

func TestSeparateBudgetAIProposalIsRetiredWithoutModelCall(t *testing.T) {
	h := &Handler{}
	response := performJSON(t, http.HandlerFunc(h.BudgetProposal), http.MethodPost, "/api/v1/curations/id/budget/proposals", `{}`)
	if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "CURATION_BUDGET_PROPOSAL_RETIRED") {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

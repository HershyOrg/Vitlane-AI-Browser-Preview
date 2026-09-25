package app

import (
	"context"
	"errors"
	"testing"

	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type guardSessions struct {
	status shoppingsessiondomain.SessionStatus
	err    error
}

func (s guardSessions) CreateReady(context.Context, shoppingsessionapp.CreateReadyInput) (shoppingsessiondomain.ShoppingSession, error) {
	return shoppingsessiondomain.ShoppingSession{}, nil
}
func (s guardSessions) FindByTarget(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error) {
	return shoppingsessiondomain.ShoppingSession{Status: s.status}, s.err
}
func (s guardSessions) Get(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error) {
	return shoppingsessiondomain.ShoppingSession{Status: s.status}, s.err
}

// A Target whose Round is open refuses Target-scoped writes with a retryable
// conflict; a reviewing Target and a Target that never had a session pass.
func TestGuardTargetResearchRefusesOnlyOpenRounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status shoppingsessiondomain.SessionStatus
		err    error
		refuse bool
	}{
		{"researching", shoppingsessiondomain.SessionStatusResearching, nil, true},
		{"reviewing", shoppingsessiondomain.SessionStatusReviewing, nil, false},
		{"no session", "", errors.New("not found"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{sessions: guardSessions{status: tc.status, err: tc.err}}
			err := service.guardTargetResearch(context.Background(), "user", "target")
			if tc.refuse {
				failure, ok := fault.As(err)
				if !ok || failure.Code != fault.Conflict || failure.Reason != ResearchInProgress {
					t.Fatalf("expected RESEARCH_IN_PROGRESS conflict, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
	if err := (&Service{}).guardTargetResearch(context.Background(), "user", "target"); err != nil {
		t.Fatalf("no session port means no guard: %v", err)
	}
}

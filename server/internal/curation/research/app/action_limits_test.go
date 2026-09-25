package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

// newBusyPlanResearchFixture wires a real command against a plan whose own
// intelligence work is still running.
func newBusyPlanResearchFixture(t *testing.T) (*Service, *testJobCreator) {
	t.Helper()
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	target := shoppingsessionapp.TargetSnapshot{
		ID: "target-1", PlanID: "plan-1", CurationID: "curation-1",
		Title:            "캠핑 의자",
		NormalizedIntent: "가벼운 캠핑 의자", Category: "camping-chair",
		AllocatedBudget:  mustMoney(t, "100", "USD"),
		TargetHash:       "target-hash",
		TargetHashSchema: "vitlane.plan-target.v1",
	}
	targetSnapshot, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	scopeSnapshot, err := json.Marshal(shoppingsessionapp.ResearchScopeSnapshot{
		Category: "camping-chair", Country: "KR", URLMode: "NONE",
	})
	if err != nil {
		t.Fatal(err)
	}
	shoppingRepository := &memoryShoppingRepository{
		session: shoppingsessiondomain.ShoppingSession{
			ID: "session-1", PlanTargetID: "target-1", UserID: "user-1",
			TargetSnapshot:        targetSnapshot,
			ResearchScopeSnapshot: scopeSnapshot,
			Status:                shoppingsessiondomain.SessionStatusReady,
			Version:               1,
		},
	}
	researchRepository := &memoryResearchRepository{
		candidates: map[string]researchdomain.Candidate{},
	}
	ids := &testIDs{}
	service := NewService(
		researchRepository,
		testPlanningService{result: planResultForSession(
			shoppingRepository.session, curationdomain.CurationPhasePlanning,
		)},
		shoppingsessionapp.NewService(shoppingRepository, testClock{now: now}, ids),
		testTransactor{}, testClock{now: now}, ids,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	jobCreator := &testJobCreator{
		repository: researchRepository, planActive: true,
	}
	service.EnableIntelligenceWork(jobCreator)
	return service, jobCreator
}

func TestGuardActionConcurrencyRejectsASecondActionOnTheSamePlan(t *testing.T) {
	service := &Service{intelligenceWork: &testJobCreator{planActive: true}}

	err := service.guardActionConcurrency(context.Background(), "user-1", "plan-1")

	if !errors.Is(err, researchdomain.ErrResearchActionInProgress) {
		t.Fatalf("a plan already working must reject the next action: %v", err)
	}
}

func TestGuardActionConcurrencyRejectsBeyondTheUserCeiling(t *testing.T) {
	service := &Service{intelligenceWork: &testJobCreator{
		activeCount: researchdomain.MaxConcurrentUserActions,
	}}

	err := service.guardActionConcurrency(context.Background(), "user-1", "plan-2")

	if !errors.Is(err, researchdomain.ErrTooManyActiveActions) {
		t.Fatalf("the user ceiling must reject the next action: %v", err)
	}
}

func TestGuardActionConcurrencyAdmitsAnIdlePlanBelowTheCeiling(t *testing.T) {
	service := &Service{intelligenceWork: &testJobCreator{
		activeCount: researchdomain.MaxConcurrentUserActions - 1,
	}}

	if err := service.guardActionConcurrency(
		context.Background(), "user-1", "plan-2",
	); err != nil {
		t.Fatalf("an idle plan below the ceiling must be admitted: %v", err)
	}
}

// Server-issued work is the continuation of an action whose own job is still
// RUNNING. Applying the plan limit to it would stop a plan from finishing the
// action the user already started, so the exemption is asserted against a
// fully wired command rather than the guard alone.
func TestStartResearchExemptsServerIssuedWorkFromThePlanLimit(t *testing.T) {
	service, jobCreator := newBusyPlanResearchFixture(t)

	input := StartResearchInput{
		UserID: "user-1", PlanID: "plan-1", CurationID: "curation-1",
		CurationActionID:        "11111111-1111-4111-8111-111111111111",
		ExpectedCurationVersion: 1,
		SessionIDs:              []string{"session-1"},
		IdempotencyKey:          "11111111-1111-4111-8111-111111111111",
		ServerIssued:            true,
	}

	result, err := service.StartResearch(context.Background(), input)

	if err != nil {
		t.Fatalf("server-issued work must not hit the action limits: %v", err)
	}
	if len(result.Tasks) != 1 || len(jobCreator.calls) != 1 {
		t.Fatalf(
			"the derived round and its job must still be created: %#v %#v",
			result.Tasks, jobCreator.calls,
		)
	}
}

// The same command from the user is refused, which is what makes the exemption
// above a real exemption rather than an absent limit.
func TestStartResearchAppliesThePlanLimitToAUserCommand(t *testing.T) {
	service, _ := newBusyPlanResearchFixture(t)

	_, err := service.StartResearch(context.Background(), StartResearchInput{
		UserID: "user-1", AuthSessionID: "auth-session-1",
		PlanID: "plan-1", CurationID: "curation-1",
		CurationActionID:        "22222222-2222-4222-8222-222222222222",
		ExpectedCurationVersion: 1,
		SessionIDs:              []string{"session-1"},
		IdempotencyKey:          "22222222-2222-4222-8222-222222222222",
	})

	if !errors.Is(err, researchdomain.ErrResearchActionInProgress) {
		t.Fatalf("a user command on a busy plan must be refused: %v", err)
	}
}

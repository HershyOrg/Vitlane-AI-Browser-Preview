package app

import (
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

func TestBuildPlanJourneyProjectsOnlyCurationOwnedStages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		phase     curationdomain.CurationPhase
		sessions  []shoppingsessiondomain.ShoppingSession
		wantStage string
	}{
		{
			name:  "planning",
			phase: curationdomain.CurationPhasePlanning, wantStage: "PLANNING",
		},
		{
			name:  "curating ready",
			phase: curationdomain.CurationPhaseCurating,
			sessions: []shoppingsessiondomain.ShoppingSession{{
				Status: shoppingsessiondomain.SessionStatusReady,
			}},
			wantStage: "READY",
		},
		{
			name:  "researching takes precedence",
			phase: curationdomain.CurationPhaseCurating,
			sessions: []shoppingsessiondomain.ShoppingSession{
				{Status: shoppingsessiondomain.SessionStatusReady},
				{Status: shoppingsessiondomain.SessionStatusResearching},
			},
			wantStage: "RESEARCHING",
		},
		{
			name:  "reviewing",
			phase: curationdomain.CurationPhaseCurating,
			sessions: []shoppingsessiondomain.ShoppingSession{{
				Status: shoppingsessiondomain.SessionStatusReviewing,
			}},
			wantStage: "REVIEWING",
		},
		{
			name:  "empty curating remains valid",
			phase: curationdomain.CurationPhaseCurating, wantStage: "CURATING",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			journey := BuildPlanJourney(
				curationdomain.PlanSnapshot{ID: "plan-1"},
				curationdomain.Curation{Phase: test.phase},
				test.sessions,
			)
			if journey.CurrentStep != JourneyStepCuration ||
				journey.CurrentStage != test.wantStage ||
				journey.ResumePath != "/plans/plan-1" {
				t.Fatalf("unexpected journey: %+v", journey)
			}
		})
	}
}

func TestBuildPlanJourneyDoesNotTreatAgencyOrderAsACurationStep(t *testing.T) {
	t.Parallel()

	journey := BuildPlanJourney(
		curationdomain.PlanSnapshot{ID: "plan-1"},
		curationdomain.Curation{Phase: curationdomain.CurationPhaseCurating},
		[]shoppingsessiondomain.ShoppingSession{{
			Status: shoppingsessiondomain.SessionStatusReady,
		}},
	)

	if len(journey.Steps) != 2 {
		t.Fatalf("unexpected journey shape: %+v", journey.Steps)
	}
	if journey.Steps[0].Key != JourneyStepIntent ||
		journey.Steps[0].Path != "/plans/plan-1" ||
		!journey.Steps[0].ReadOnly {
		t.Fatalf("intent should be read-only and navigable: %+v", journey.Steps)
	}
	if journey.Steps[1].Key != JourneyStepCuration ||
		journey.Steps[1].State != JourneyStepCurrent ||
		journey.Steps[1].Path != "/plans/plan-1" {
		t.Fatalf("curation should be current: %+v", journey.Steps)
	}
}

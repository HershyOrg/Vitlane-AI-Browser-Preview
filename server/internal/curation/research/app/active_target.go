package app

import (
	"context"
	"encoding/json"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

// activeSessionPlan is the write-side boundary between Research and Curation.
// Planning only projects active Targets and their Sessions, so a soft-removed
// Target cannot be mutated through its still-preserved historical Session.
func (s *Service) activeSessionPlan(
	ctx context.Context,
	userID string,
	session shoppingsessiondomain.ShoppingSession,
	inactiveError error,
) (curationapp.PlanResult, error) {
	var snapshot struct {
		ID     string `json:"id"`
		PlanID string `json:"planId"`
	}
	if err := json.Unmarshal(session.TargetSnapshot, &snapshot); err != nil {
		return curationapp.PlanResult{}, err
	}
	if snapshot.ID == "" || snapshot.PlanID == "" ||
		snapshot.ID != string(session.PlanTargetID) ||
		userID != string(session.UserID) {
		return curationapp.PlanResult{}, inactiveError
	}

	plan, err := s.plans.Get(ctx, userID, snapshot.PlanID)
	if err != nil {
		return curationapp.PlanResult{}, err
	}
	if string(plan.Plan.ID) != snapshot.PlanID ||
		string(plan.Plan.UserID) != userID ||
		plan.Curation.ArchivedAt != nil ||
		plan.Curation.Phase != curationdomain.CurationPhaseCurating {
		return curationapp.PlanResult{}, inactiveError
	}

	targetActive := false
	for _, target := range plan.Targets {
		if string(target.ID) == snapshot.ID &&
			string(target.PlanID) == snapshot.PlanID &&
			target.RemovedAt == nil {
			targetActive = true
			break
		}
	}
	if !targetActive {
		return curationapp.PlanResult{}, inactiveError
	}

	sessionActive := false
	for _, active := range plan.Sessions {
		if active.ID == session.ID &&
			active.PlanTargetID == session.PlanTargetID &&
			active.UserID == session.UserID {
			sessionActive = true
			break
		}
	}
	if !sessionActive {
		return curationapp.PlanResult{}, inactiveError
	}
	return plan, nil
}

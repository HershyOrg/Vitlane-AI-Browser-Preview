package app

import (
	"context"
	"slices"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type StartPlanningDerivedResearchInput struct {
	UserID                  string
	PlanID                  string
	CurationID              string
	ExpectedCurationVersion int64
	ActionReplay            bool
	SessionIDs              []string
}

type StartPlanningDerivedResearchResult struct {
	ResearchRoundIDs []string
}

// StartPlanningDerivedResearch materializes only the ResearchRounds for the
// Sessions created by an accepted Planning proposal. The caller attaches one
// IntelligenceJob to each returned Round in the same ambient transaction.
func (s *Service) StartPlanningDerivedResearch(
	ctx context.Context,
	input StartPlanningDerivedResearchInput,
) (StartPlanningDerivedResearchResult, error) {
	var result StartPlanningDerivedResearchResult
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		plan, err := s.plans.Get(txContext, input.UserID, input.PlanID)
		if err != nil {
			return err
		}
		firstStart := !input.ActionReplay &&
			plan.Curation.Phase == curationdomain.CurationPhasePlanning &&
			plan.Curation.Version == input.ExpectedCurationVersion
		replayedStart := input.ActionReplay &&
			plan.Curation.Phase == curationdomain.CurationPhaseCurating &&
			plan.Curation.Version == input.ExpectedCurationVersion+1
		if plan.Curation.ArchivedAt != nil ||
			(!firstStart && !replayedStart) ||
			len(plan.Targets) == 0 {
			return curationdomain.ErrPlanNotConfirmed
		}
		if string(plan.Curation.ID) != input.CurationID {
			return curationdomain.ErrCurationNotFound
		}
		sessionByID := make(
			map[string]shoppingsessiondomain.ShoppingSession, len(plan.Sessions),
		)
		activeTargetIDs := make(map[string]struct{}, len(plan.Targets))
		for _, target := range plan.Targets {
			activeTargetIDs[string(target.ID)] = struct{}{}
		}
		sessionTargetIDs := make(map[string]struct{}, len(plan.Sessions))
		for _, session := range plan.Sessions {
			targetID := string(session.PlanTargetID)
			if _, active := activeTargetIDs[targetID]; !active {
				return shoppingsessiondomain.ErrSessionNotFound
			}
			if _, duplicate := sessionTargetIDs[targetID]; duplicate {
				return shoppingsessiondomain.ErrResearchRoundMismatch
			}
			sessionTargetIDs[targetID] = struct{}{}
			sessionByID[string(session.ID)] = session
		}
		sessionIDs := uniqueStrings(input.SessionIDs)
		if len(sessionIDs) == 0 ||
			len(sessionIDs) != len(input.SessionIDs) ||
			len(sessionIDs) != len(plan.Targets) ||
			len(sessionByID) != len(plan.Sessions) ||
			len(plan.Sessions) != len(plan.Targets) {
			return researchdomain.ErrInvalidResearchCommand
		}
		slices.Sort(sessionIDs)
		roundIDs := make([]string, 0, len(sessionIDs))
		for _, sessionID := range sessionIDs {
			session, ok := sessionByID[sessionID]
			if !ok {
				return shoppingsessiondomain.ErrSessionNotFound
			}
			if session.Status == shoppingsessiondomain.SessionStatusReady {
				session, err = s.sessions.GetForUpdate(
					txContext, input.UserID, sessionID,
				)
				if err != nil {
					return err
				}
			}
			var round researchdomain.ResearchRound
			switch session.Status {
			case shoppingsessiondomain.SessionStatusReady:
				if replayedStart {
					if session.CurrentResearchRoundID == nil {
						return shoppingsessiondomain.ErrResearchRoundMismatch
					}
					round, err = s.repository.GetRound(
						txContext, input.UserID,
						*session.CurrentResearchRoundID, true,
					)
					if err != nil {
						return err
					}
					if round.Status != researchdomain.RoundStatusFailed {
						return researchdomain.ErrRoundClosed
					}
					break
				}
				roundNumber, err := s.repository.NextRoundNumber(
					txContext, sessionID,
				)
				if err != nil {
					return err
				}
				roundID := s.ids.NewID()
				contextSnapshot, err := buildContextSnapshot(
					roundID, input.PlanID, roundNumber,
					plan.Plan.ExecutionMode, session,
				)
				if err == nil {
					contextSnapshot, err = s.attachPurchaseFeedback(txContext, input.UserID, input.PlanID, contextSnapshot)
				}
				if err != nil {
					return err
				}
				round, err = researchdomain.NewRound(
					roundID, sessionID, input.UserID,
					roundNumber, contextSnapshot, s.clock.Now(),
				)
				if err != nil {
					return err
				}
				if err := s.repository.CreateRound(txContext, round); err != nil {
					return err
				}
				if _, err := s.sessions.StartResearch(
					txContext, input.UserID, sessionID, round.ID,
				); err != nil {
					return err
				}
			case shoppingsessiondomain.SessionStatusResearching:
				if session.CurrentResearchRoundID == nil {
					return shoppingsessiondomain.ErrResearchRoundMismatch
				}
				round, err = s.repository.GetRound(
					txContext, input.UserID,
					*session.CurrentResearchRoundID, true,
				)
				if err != nil {
					return err
				}
				if round.Status != researchdomain.RoundStatusRequested {
					return researchdomain.ErrRoundClosed
				}
			case shoppingsessiondomain.SessionStatusReviewing:
				if !replayedStart ||
					session.CurrentResearchRoundID == nil {
					return shoppingsessiondomain.ErrResearchRoundMismatch
				}
				round, err = s.repository.GetRound(
					txContext, input.UserID,
					*session.CurrentResearchRoundID, true,
				)
				if err != nil {
					return err
				}
				if !isCompletedInitialResearchRoundStatus(round.Status) {
					return researchdomain.ErrRoundClosed
				}
			default:
				return shoppingsessiondomain.ErrSessionNotReady
			}
			roundIDs = append(roundIDs, round.ID)
		}
		if firstStart {
			if _, err := s.plans.StartCurating(
				txContext, input.UserID, input.PlanID,
				input.ExpectedCurationVersion,
			); err != nil {
				return err
			}
		}
		result.ResearchRoundIDs = roundIDs
		return nil
	})
	return result, err
}

func isCompletedInitialResearchRoundStatus(
	status researchdomain.RoundStatus,
) bool {
	return status == researchdomain.RoundStatusResultsReady ||
		status == researchdomain.RoundStatusNoResults ||
		status == researchdomain.RoundStatusFailed
}

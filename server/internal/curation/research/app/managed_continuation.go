package app

import (
	"context"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type StartReadySessionResearchInput struct {
	UserID           string
	PlanID           string
	CurationActionID string
	IdempotencyKey   string
}

type StartReadySessionResearchResult struct {
	RoundIDs []string
}

// StartReadySessionResearch opens the first ResearchRound for every session
// that is still READY on a curation which is already CURATING.
//
// StartPlanningDerivedResearch cannot serve this case: it exists to perform the
// PLANNING -> CURATING transition and guards on that exact phase. Adding a
// Target to a curation that is already curating produces a READY session with
// no round, and without this the target would sit there forever waiting for a
// user action that the managed path is supposed to remove.
func (s *Service) StartReadySessionResearch(
	ctx context.Context,
	input StartReadySessionResearchInput,
) (StartReadySessionResearchResult, error) {
	var result StartReadySessionResearchResult
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		plan, err := s.plans.Get(txContext, input.UserID, input.PlanID)
		if err != nil {
			return err
		}
		if plan.Curation.ArchivedAt != nil ||
			plan.Curation.Phase != curationdomain.CurationPhaseCurating {
			return shoppingsessiondomain.ErrSessionNotFound
		}
		activeTargets := make(map[string]struct{}, len(plan.Targets))
		for _, target := range plan.Targets {
			if target.RemovedAt == nil {
				activeTargets[string(target.ID)] = struct{}{}
			}
		}

		roundIDs := make([]string, 0, len(plan.Sessions))
		for _, snapshot := range plan.Sessions {
			if snapshot.Status != shoppingsessiondomain.SessionStatusReady {
				continue
			}
			if _, active := activeTargets[string(snapshot.PlanTargetID)]; !active {
				// A removed Target must not acquire new research.
				continue
			}
			session, err := s.sessions.GetForUpdate(
				txContext, input.UserID, string(snapshot.ID),
			)
			if err != nil {
				return err
			}
			// Re-check under the lock: a concurrent start may have moved this
			// session on between the read above and here.
			if session.Status != shoppingsessiondomain.SessionStatusReady {
				continue
			}
			roundNumber, err := s.repository.NextRoundNumber(
				txContext, string(session.ID),
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
			round, err := researchdomain.NewRound(
				roundID, string(session.ID), input.UserID,
				roundNumber, contextSnapshot, s.clock.Now(),
			)
			if err != nil {
				return err
			}
			if err := s.repository.CreateRound(txContext, round); err != nil {
				return err
			}
			if _, err := s.sessions.StartResearch(
				txContext, input.UserID, string(session.ID), round.ID,
			); err != nil {
				return err
			}
			roundIDs = append(roundIDs, round.ID)
		}
		if len(roundIDs) == 0 {
			return nil
		}
		result.RoundIDs = roundIDs
		if err := s.attachResearchJobs(
			txContext,
			CreateResearchJobInput{
				UserID: input.UserID, CurationID: string(plan.Curation.ID),
				CurationActionID: input.CurationActionID,
				PlanID:           input.PlanID,
				Provider:         string(plan.Plan.AgentMode),
				ModelKey:         plan.Plan.ModelKey,
			},
			roundIDs,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return StartReadySessionResearchResult{}, err
	}
	return result, nil
}

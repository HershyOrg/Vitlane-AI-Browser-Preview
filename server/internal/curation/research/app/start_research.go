package app

import (
	"context"
	"strings"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type StartResearchInput struct {
	UserID                  string
	AuthSessionID           string
	PlanID                  string
	CurationID              string
	CurationActionID        string
	ExpectedCurationVersion int64
	SessionIDs              []string
	IdempotencyKey          string
	// ServerIssued marks a command the Server issued on the user's behalf.
	// Such a command carries no AuthSession because the user's request has
	// already finished; authority comes from the job that is driving it.
	ServerIssued bool
}

type StartResearchResult struct {
	Tasks []ResearchTask `json:"tasks"`
}

// StartResearch is the behavior-first initial Research command. It creates or
// reuses the exact requested rounds and their IntelligenceJobs in the same
// product transaction, so a replayed command never opens a second job.
func (s *Service) StartResearch(
	ctx context.Context,
	input StartResearchInput,
) (StartResearchResult, error) {
	if strings.TrimSpace(input.UserID) == "" ||
		(!input.ServerIssued &&
			strings.TrimSpace(input.AuthSessionID) == "") ||
		strings.TrimSpace(input.PlanID) == "" ||
		strings.TrimSpace(input.CurationID) == "" ||
		strings.TrimSpace(input.CurationActionID) == "" ||
		input.CurationActionID != input.IdempotencyKey ||
		input.ExpectedCurationVersion < 1 ||
		strings.TrimSpace(input.IdempotencyKey) == "" {
		return StartResearchResult{},
			researchdomain.ErrInvalidResearchCommand
	}
	var result StartResearchResult
	err := s.transactor.WithinTransaction(
		ctx,
		func(txContext context.Context) error {
			// A replayed command must still converge on its stored result, so
			// the limit is checked before anything is recorded but only for a
			// command the user is issuing now.
			if !input.ServerIssued {
				if err := s.guardActionConcurrency(
					txContext, input.UserID, input.PlanID,
				); err != nil {
					return err
				}
			}
			subjectID := input.CurationID
			recordAction := s.plans.RecordCurationAction
			if input.ServerIssued {
				// The planning Job is still RUNNING until this continuation
				// returns. It is the authority for this action, not competing
				// foreground work.
				recordAction =
					s.plans.RecordManagedContinuationCurationAction
			}
			recordedAction, err := recordAction(
				txContext,
				curationapp.RecordCurationActionInput{
					ActionID:                input.CurationActionID,
					UserID:                  input.UserID,
					CurationID:              input.CurationID,
					Type:                    curationdomain.CurationActionPlanningStartCurating,
					SubjectType:             curationdomain.CurationActionSubjectCuration,
					SubjectID:               &subjectID,
					ExpectedCurationVersion: input.ExpectedCurationVersion,
					SourceRefType:           curationdomain.CurationActionSourceResearchStartRequest,
					SourceRefID:             input.IdempotencyKey,
				},
			)
			if err != nil {
				return err
			}
			started, err := s.StartPlanningDerivedResearch(
				txContext,
				StartPlanningDerivedResearchInput{
					UserID: input.UserID, PlanID: input.PlanID,
					CurationID:              input.CurationID,
					ExpectedCurationVersion: input.ExpectedCurationVersion,
					ActionReplay:            recordedAction.Replay,
					SessionIDs:              append([]string(nil), input.SessionIDs...),
				},
			)
			if err != nil {
				return err
			}
			tasks := make([]ResearchTask, 0, len(started.ResearchRoundIDs))
			for _, roundID := range started.ResearchRoundIDs {
				round, err := s.repository.GetRound(
					txContext, input.UserID, roundID, false,
				)
				if err != nil {
					return err
				}
				if round.Status != researchdomain.RoundStatusRequested &&
					(!recordedAction.Replay ||
						!isCompletedInitialResearchRoundStatus(round.Status)) {
					return researchdomain.ErrRoundClosed
				}
				task, err := taskFromRound(round)
				if err != nil {
					return err
				}
				if _, found, err := s.feedbackForRound(
					txContext, input.UserID, round.ID,
				); err != nil {
					return err
				} else {
					task.FeedbackRequired = found
				}
				tasks = append(tasks, task)
			}
			// The plan owns the provider snapshot, so every round this action
			// opens uses the provider selected at submission.
			plan, err := s.plans.Get(txContext, input.UserID, input.PlanID)
			if err != nil {
				return err
			}
			// ADR-0038: the retired external Agent path accepts no new work.
			// Replays of actions recorded before the cutover still resolve.
			if !recordedAction.Replay &&
				plan.Plan.AgentMode == curationdomain.AgentModeExternal {
				return curationdomain.ErrExternalAgentRetired
			}
			result = StartResearchResult{Tasks: tasks}
			return s.attachResearchJobs(
				txContext,
				CreateResearchJobInput{
					UserID: input.UserID, CurationID: input.CurationID,
					CurationActionID: input.CurationActionID,
					PlanID:           input.PlanID,
					Provider:         string(plan.Plan.AgentMode),
					ModelKey:         plan.Plan.ModelKey,
				},
				started.ResearchRoundIDs,
			)
		},
	)
	return result, err
}

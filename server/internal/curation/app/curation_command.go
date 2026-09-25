package app

import (
	"context"
	"strings"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

// ExecuteExpansionActionInput is the typed command boundary for both
// PLANNING_ADD_TARGETS and CURATION_ADD_TARGETS. The Action ID is also the
// command idempotency key so a browser retry cannot fork product work.
type ExecuteExpansionActionInput struct {
	ActionID                string
	UserID                  string
	AuthSessionID           string
	CurationID              string
	PlanID                  string
	Type                    curationdomain.CurationActionType
	Instruction             string
	ExpectedCurationVersion int64
}

// ExecuteExpansionAction records the immutable user action before creating
// the Planning run and AgentControl work. All three writes join the same
// ambient PostgreSQL transaction.
func (s *Service) ExecuteExpansionAction(
	ctx context.Context,
	input ExecuteExpansionActionInput,
) (ExpansionResult, error) {
	actionID := strings.TrimSpace(input.ActionID)
	var subjectType curationdomain.CurationActionSubjectType
	var subjectID *string
	switch input.Type {
	case curationdomain.CurationActionPlanningAddTargets:
		subjectType = curationdomain.CurationActionSubjectTargetList
		value := strings.TrimSpace(input.CurationID)
		subjectID = &value
	case curationdomain.CurationActionCurationAddTargets:
		subjectType = curationdomain.CurationActionSubjectCuration
		value := strings.TrimSpace(input.CurationID)
		subjectID = &value
	default:
		return ExpansionResult{}, curationdomain.ErrCurationActionUnavailable
	}

	var result ExpansionResult
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if _, err := s.RecordCurationAction(
			txContext,
			RecordCurationActionInput{
				ActionID:                actionID,
				UserID:                  input.UserID,
				CurationID:              input.CurationID,
				Type:                    input.Type,
				SubjectType:             subjectType,
				SubjectID:               subjectID,
				Body:                    strings.TrimSpace(input.Instruction),
				ExpectedCurationVersion: input.ExpectedCurationVersion,
				SourceRefType:           curationdomain.CurationActionSourceCurationRunRequest,
				SourceRefID:             actionID,
			},
		); err != nil {
			return err
		}
		var err error
		result, err = s.CreateExpansion(
			txContext,
			CreateExpansionInput{
				UserID:           input.UserID,
				AuthSessionID:    input.AuthSessionID,
				PlanID:           input.PlanID,
				CurationID:       input.CurationID,
				CurationActionID: actionID,
				Instruction:      input.Instruction,
				IdempotencyKey:   actionID,
			},
		)
		return err
	})
	return result, err
}

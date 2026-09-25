package app

import (
	"context"
	"strings"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

// ADR-0038 entry points. These are the Server's own path onto a PlanningTask:
// authority comes from the IntelligenceJob that already points at this exact
// task, so there is no claim handle and no scope set to narrow. Validation of
// what may be submitted is unchanged — the same proposal rules apply whichever
// intelligence produced it.

// IntelligencePlanningRepository finds a proposal by its intelligence
// provenance, so a replayed submission converges on the stored result instead
// of being written twice.
type IntelligencePlanningRepository interface {
	FindIntelligenceProposal(
		context.Context, string, string, string,
	) (curationdomain.PlanningProposal, bool, error)
}

type CreatePlanningJobInput struct {
	UserID           string
	CurationID       string
	CurationActionID string
	PlanID           string
	PlanningTaskID   string
	Provider         string
	ModelKey         string
}

// IntelligenceJobRef is the browser-safe reference a planning command returns.
// Unlike the agent work reference it replaces, it carries no activation
// material at all: the browser polls the workspace for progress.
type IntelligenceJobRef struct {
	JobID  string `json:"jobId"`
	Replay bool   `json:"replay"`
}

// IntelligenceWorkCreator is implemented by the intelligence adapter. Curation
// owns the ambient transaction in which it creates the exact Task; the
// adapter joins it so the task and its job commit together.
//
// The activity reads are on the same port for the same reason: they must run
// inside that transaction, or two actions submitted together could both pass a
// limit neither of them is actually within.
type IntelligenceWorkCreator interface {
	CreatePlanningJob(
		context.Context, CreatePlanningJobInput,
	) (IntelligenceJobRef, error)
	LockActionAdmission(ctx context.Context, userID string) error
	PlanHasActiveWork(
		ctx context.Context, userID string, planID string,
	) (bool, error)
	ActiveActionCount(ctx context.Context, userID string) (int, error)
}

func (s *Service) EnableIntelligenceWork(creator IntelligenceWorkCreator) {
	s.intelligenceWork = creator
}

// guardActionConcurrency is the admission every action-creating command shares.
//
// A plan runs one action at a time because the next action's context is built
// from the current targets: starting a second one while the first is still
// producing them would plan against a state that is about to change. The
// global ceiling is separate — it bounds one user's share of the provider, not
// the coherence of any single plan.
func (s *Service) guardActionConcurrency(
	ctx context.Context,
	userID string,
	planID string,
) error {
	if s.intelligenceWork == nil {
		return nil
	}
	// Serialize admission for this user until the surrounding product
	// transaction commits. A count without this lock lets two concurrent
	// commands both observe two active actions and both create a fourth.
	if err := s.intelligenceWork.LockActionAdmission(ctx, userID); err != nil {
		return err
	}
	active, err := s.intelligenceWork.PlanHasActiveWork(ctx, userID, planID)
	if err != nil {
		return err
	}
	if active {
		return curationdomain.ErrExpansionInProgress
	}
	count, err := s.intelligenceWork.ActiveActionCount(ctx, userID)
	if err != nil {
		return err
	}
	if count >= curationdomain.MaxConcurrentUserActions {
		return curationdomain.ErrTooManyActiveActions
	}
	return nil
}

func (s *Service) attachPlanningJob(
	ctx context.Context,
	userID string,
	curationID string,
	curationActionID string,
	planID string,
	agentMode string,
	modelKey string,
	task curationdomain.PlanningTask,
) (*IntelligenceJobRef, error) {
	if s.intelligenceWork == nil {
		return nil, nil
	}
	created, err := s.intelligenceWork.CreatePlanningJob(
		ctx,
		CreatePlanningJobInput{
			UserID: userID, CurationID: curationID,
			CurationActionID: curationActionID, PlanID: planID,
			PlanningTaskID: string(task.ID),
			Provider:       agentMode, ModelKey: modelKey,
		},
	)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// GetPlanningContextForIntelligence reads the exact task a job was created for.
// The job's own row already proves which task it may run — the schema allows
// only one job per task — so this call verifies user ownership and nothing
// further.
func (s *Service) GetPlanningContextForIntelligence(
	ctx context.Context,
	userID string,
	taskID string,
) (PlanningContext, error) {
	repository, ok := s.repository.(PlanningTaskRepository)
	if !ok {
		return PlanningContext{}, curationdomain.ErrPlanningTaskNotFound
	}
	task, err := repository.GetPlanningTaskByID(
		ctx, userID, strings.TrimSpace(taskID), false,
	)
	if err != nil {
		return PlanningContext{}, err
	}
	record, err := s.repository.Get(ctx, userID, string(task.PlanID), false)
	if err != nil {
		return PlanningContext{}, err
	}
	return s.buildPlanningContext(ctx, record, task)
}

// SubmitPlanningProposalForIntelligence records a proposal produced by an
// intelligence provider. It goes through the same validation as every other
// proposal; only the provenance differs.
func (s *Service) SubmitPlanningProposalForIntelligence(
	ctx context.Context,
	userID string,
	jobID string,
	input SubmitPlanningProposalInput,
) (SubmitPlanningProposalResult, error) {
	if strings.TrimSpace(jobID) == "" {
		return SubmitPlanningProposalResult{}, curationdomain.ErrProposalInvalid
	}
	repository, ok := s.repository.(PlanningTaskRepository)
	if !ok {
		return SubmitPlanningProposalResult{}, curationdomain.ErrPlanningTaskNotFound
	}
	task, err := repository.GetPlanningTaskByID(
		ctx, userID, strings.TrimSpace(input.TaskID), false,
	)
	if err != nil {
		return SubmitPlanningProposalResult{}, err
	}
	input.UserID = userID
	input.PlanID = string(task.PlanID)
	input.IntelligenceJobID = jobID
	return s.submitPlanningProposal(ctx, input)
}

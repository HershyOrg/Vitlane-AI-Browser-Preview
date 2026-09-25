package app

import (
	"context"
	"errors"
	"strings"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

// PlanningTaskRepository reads a task by its own identity. ADR-0038 removed the
// agent work order that used to scope this lookup: a job exists for exactly one
// task, so ownership by user is the whole check.
type PlanningTaskRepository interface {
	GetPlanningTaskByID(
		context.Context,
		string,
		string,
		bool,
	) (curationdomain.PlanningTask, error)
	GetPlanningProposalResult(
		context.Context,
		string,
		string,
		string,
	) (curationdomain.PlanningProposal, error)
}

type RejectedProposalError struct {
	Cause error
}

func (e *RejectedProposalError) Error() string {
	if e == nil || e.Cause == nil {
		return curationdomain.ErrProposalInvalid.Error()
	}
	return e.Cause.Error()
}

func (e *RejectedProposalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// CancelPlanningTask closes a task whose work will not run, keeping the task
// and its CurationRun consistent in one transaction.
func (s *Service) CancelPlanningTask(
	ctx context.Context,
	userID string,
	planningTaskID string,
) error {
	repository, ok := s.repository.(PlanningTaskRepository)
	if !ok {
		return curationdomain.ErrPlanningTaskNotFound
	}
	return s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		task, err := repository.GetPlanningTaskByID(
			txContext, strings.TrimSpace(userID),
			strings.TrimSpace(planningTaskID), true,
		)
		if err != nil {
			return err
		}
		if task.Status == curationdomain.PlanningTaskStatusCancelled {
			return nil
		}
		if task.Status != curationdomain.PlanningTaskStatusRequested {
			return curationdomain.ErrPlanningTaskClosed
		}
		now := s.clock.Now()
		task.Status = curationdomain.PlanningTaskStatusCancelled
		task.CompletedAt = &now
		task.UpdatedAt = now
		if err := s.repository.UpdatePlanningTask(txContext, task); err != nil {
			return err
		}
		curationRepository, ok := s.repository.(CurationRepository)
		if !ok {
			return nil
		}
		run, err := curationRepository.GetCurationRunByTask(
			txContext, string(task.UserID), string(task.PlanID),
			string(task.ID), true,
		)
		if errors.Is(err, curationdomain.ErrCurationRunNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		run.Status = curationdomain.CurationRunCancelled
		run.UpdatedAt = now
		run.CompletedAt = &now
		return curationRepository.UpdateCurationRun(txContext, run)
	})
}

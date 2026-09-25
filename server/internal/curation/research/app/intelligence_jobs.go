package app

import (
	"context"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// CreateResearchJobInput opens one intelligence job for one exact round. Fan-out
// creates one input per round rather than a batch: ADR-0038 keeps each Target's
// research on its own failure and retry boundary, so one round timing out never
// takes its siblings with it.
type CreateResearchJobInput struct {
	UserID           string
	CurationID       string
	CurationActionID string
	PlanID           string
	ResearchRoundID  string
	Provider         string
	ModelKey         string
}

// IntelligenceJobCreator is implemented by the intelligence adapter. Research
// owns the product transaction; the adapter joins it so a round and the job
// that runs it commit together.
type IntelligenceJobCreator interface {
	CreateResearchJob(context.Context, CreateResearchJobInput) error
	// PlanHasActiveWork and ActiveActionCount enforce the action limits in the
	// same transaction that would open the next round's job.
	LockActionAdmission(ctx context.Context, userID string) error
	PlanHasActiveWork(
		ctx context.Context, userID string, planID string,
	) (bool, error)
	ActiveActionCount(ctx context.Context, userID string) (int, error)
}

// guardActionConcurrency admits a user-initiated research command.
//
// Server-issued work is exempt on purpose: the planning job that produces it is
// still RUNNING when it starts the rounds it derived, so applying the same
// limit here would make a plan unable to continue its own action.
func (s *Service) guardActionConcurrency(
	ctx context.Context,
	userID string,
	planID string,
) error {
	if s.intelligenceWork == nil {
		return nil
	}
	if err := s.intelligenceWork.LockActionAdmission(ctx, userID); err != nil {
		return err
	}
	active, err := s.intelligenceWork.PlanHasActiveWork(ctx, userID, planID)
	if err != nil {
		return err
	}
	if active {
		return researchdomain.ErrResearchActionInProgress
	}
	count, err := s.intelligenceWork.ActiveActionCount(ctx, userID)
	if err != nil {
		return err
	}
	if count >= researchdomain.MaxConcurrentUserActions {
		return researchdomain.ErrTooManyActiveActions
	}
	return nil
}

func (s *Service) EnableIntelligenceWork(creator IntelligenceJobCreator) {
	s.intelligenceWork = creator
}

// attachResearchJobs opens a job per round. It is a no-op when intelligence is
// not wired, which is what keeps the legacy agent path working unchanged during
// the transition.
func (s *Service) attachResearchJobs(
	ctx context.Context,
	input CreateResearchJobInput,
	roundIDs []string,
) error {
	if s.intelligenceWork == nil {
		return nil
	}
	for _, roundID := range roundIDs {
		perRound := input
		perRound.ResearchRoundID = roundID
		if err := s.intelligenceWork.CreateResearchJob(
			ctx, perRound,
		); err != nil {
			return err
		}
	}
	return nil
}

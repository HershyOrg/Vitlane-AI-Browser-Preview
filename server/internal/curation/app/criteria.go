package app

import (
	"context"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
)

type CriteriaCommand struct {
	SchemaVersion           string                `json:"schemaVersion"`
	ExpectedCriteriaVersion int64                 `json:"expectedCriteriaVersion"`
	ExpectedCurationVersion int64                 `json:"expectedCurationVersion"`
	IdempotencyKey          string                `json:"idempotencyKey"`
	Criteria                d.TargetCriteriaSetV1 `json:"criteria"`
}
type CriteriaRepository interface {
	ReadCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error)
	ReadCriteriaForUpdate(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error)
	SaveCriteria(context.Context, string, string, string, CriteriaCommand) (d.TargetCriteriaSetV1, error)
	InsertInitialCriteria(context.Context, []d.PlanTarget, []TargetInput) error
}

func (s *Service) TargetCriteria(ctx context.Context, user, curation, target string) (*d.TargetCriteriaSetV1, error) {
	r, ok := s.repository.(CriteriaRepository)
	if !ok {
		return nil, fault.New(fault.InternalFailure, "RESEARCH_CRITERIA_UNAVAILABLE", false)
	}
	return r.ReadCriteria(ctx, user, curation, target)
}
func (s *Service) ChangeTargetCriteria(ctx context.Context, user, curation, target string, c CriteriaCommand) (d.TargetCriteriaSetV1, error) {
	if !uuidPattern.MatchString(curation) || !uuidPattern.MatchString(target) || c.SchemaVersion != "vitlane.criteria-command.v1" || len(c.IdempotencyKey) < 1 || len(c.IdempotencyKey) > 180 || c.ExpectedCriteriaVersion < 0 || c.ExpectedCurationVersion < 1 {
		return d.TargetCriteriaSetV1{}, fault.New(fault.InvalidInput, "RESEARCH_CRITERIA_INVALID", false)
	}
	r, ok := s.repository.(CriteriaRepository)
	if !ok {
		return d.TargetCriteriaSetV1{}, fault.New(fault.InternalFailure, "RESEARCH_CRITERIA_UNAVAILABLE", false)
	}
	c.Criteria.SchemaVersion = d.CriteriaSchema
	c.Criteria.Version = c.ExpectedCriteriaVersion + 1
	if err := c.Criteria.Validate(); err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	// The research worker fixes its execution checkpoint under the Round
	// lock; every other writer waits until the Round finalizes or is cancelled.
	if !strings.HasPrefix(c.IdempotencyKey, "research:") && ThreadExecutionFrom(ctx).JobID == "" {
		if err := s.guardTargetResearch(ctx, user, target); err != nil {
			return d.TargetCriteriaSetV1{}, err
		}
	}
	var result d.TargetCriteriaSetV1
	err := s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		var err error
		result, err = r.SaveCriteria(tx, user, curation, target, c)
		return err
	})
	return result, err
}

// ResearchInProgress is the reason a Target-scoped write (criteria, budget,
// card reactions) is refused while the Target's Round is open (ADR-0083): the
// staged publication may not be interleaved with other writers, and only an
// explicit cancel ends the Round early.
const ResearchInProgress = "RESEARCH_IN_PROGRESS"

func (s *Service) guardTargetResearch(ctx context.Context, user, target string) error {
	if s.sessions == nil || strings.TrimSpace(target) == "" {
		return nil
	}
	session, err := s.sessions.FindByTarget(ctx, user, target)
	if err != nil {
		// A Target without a session has never been researched.
		return nil
	}
	if string(session.Status) == "RESEARCHING" {
		return fault.New(fault.Conflict, ResearchInProgress, true)
	}
	return nil
}

type ContentLocaleReader interface {
	ContentLocale(context.Context, string) (string, error)
}

// ContentLocale returns "" when neither the account nor a requesting browser
// names a language; callers then keep the language already recorded.
func (s *Service) ContentLocale(ctx context.Context, user string) (string, error) {
	if r, ok := s.researchSelections.(ContentLocaleReader); ok {
		return r.ContentLocale(ctx, user)
	}
	return "", nil
}

type planLocaleRepository interface {
	SavePlanContentLocale(context.Context, string, string, string) error
	ReadPlanContentLocale(context.Context, string, string) (string, error)
}

func (s *Service) PlanContentLocale(ctx context.Context, user, plan string) (string, error) {
	if r, ok := s.repository.(planLocaleRepository); ok {
		return r.ReadPlanContentLocale(ctx, user, plan)
	}
	return "ko-KR", nil
}

// LockTargetCriteria participates in the caller's transaction so the research
// checkpoint and a concurrent settings edit have a single serialization point.
func (s *Service) LockTargetCriteria(ctx context.Context, user, curation, target string) (*d.TargetCriteriaSetV1, error) {
	r, ok := s.repository.(CriteriaRepository)
	if !ok {
		return nil, fault.New(fault.InternalFailure, "RESEARCH_CRITERIA_UNAVAILABLE", false)
	}
	return r.ReadCriteriaForUpdate(ctx, user, curation, target)
}

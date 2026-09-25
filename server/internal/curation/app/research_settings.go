package app

import (
	"context"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type ResearchSelectionRecorder interface {
	RememberResearchSelection(context.Context, string, string, string) error
}

type ResearchSettingsRepository interface {
	ReadResearchSettings(context.Context, string, string, bool) (curationdomain.ResearchSettings, error)
	SaveResearchSettings(context.Context, string, string, string, int64) (curationdomain.ResearchSettings, error)
}

func (s *Service) EnableResearchSelectionRecorder(recorder ResearchSelectionRecorder) {
	s.researchSelections = recorder
}

func (s *Service) ResearchSettings(ctx context.Context, user, curation string) (curationdomain.ResearchSettings, error) {
	repo, ok := s.repository.(ResearchSettingsRepository)
	if !ok {
		return curationdomain.ResearchSettings{}, fault.New(fault.InternalFailure, "RESEARCH_SETTINGS_UNAVAILABLE", false)
	}
	return repo.ReadResearchSettings(ctx, user, curation, false)
}

func (s *Service) ResearchSettingsForPlan(ctx context.Context, user, plan string) (curationdomain.ResearchSettings, error) {
	repo, ok := s.repository.(ResearchSettingsRepository)
	if !ok {
		return curationdomain.ResearchSettings{}, fault.New(fault.InternalFailure, "RESEARCH_SETTINGS_UNAVAILABLE", false)
	}
	return repo.ReadResearchSettings(ctx, user, plan, true)
}

func (s *Service) ChangeResearchSettings(ctx context.Context, user, curation, country string, expected int64) (curationdomain.ResearchSettings, error) {
	if !curationdomain.SupportedResearchCountry(country) || expected < 0 {
		return curationdomain.ResearchSettings{}, fault.New(fault.InvalidInput, "RESEARCH_SETTINGS_INVALID", false)
	}
	repo, ok := s.repository.(ResearchSettingsRepository)
	if !ok {
		return curationdomain.ResearchSettings{}, fault.New(fault.InternalFailure, "RESEARCH_SETTINGS_UNAVAILABLE", false)
	}
	var result curationdomain.ResearchSettings
	err := s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		var err error
		result, err = repo.SaveResearchSettings(tx, user, curation, country, expected)
		if err != nil {
			return err
		}
		if s.researchSelections != nil {
			return s.researchSelections.RememberResearchSelection(tx, user, country, "")
		}
		return nil
	})
	return result, err
}

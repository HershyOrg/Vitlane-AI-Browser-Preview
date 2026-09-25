package app

import (
	"context"
	"encoding/json"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type researchSettingsReader interface {
	ResearchSettingsForPlan(context.Context, string, string) (curationdomain.ResearchSettings, error)
}

// Only new Round construction calls this method. Reads and worker retries
// consume the saved scope instead of consulting current user preferences.
func (s *Service) attachResearchSettings(ctx context.Context, user, plan string, raw json.RawMessage) (json.RawMessage, error) {
	reader, ok := s.plans.(researchSettingsReader)
	if !ok {
		return raw, nil
	}
	settings, err := reader.ResearchSettingsForPlan(ctx, user, plan)
	if err != nil {
		return nil, err
	}
	var snapshot ResearchContext
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	snapshot.ResearchSettings = &settings
	snapshot.ResearchScope.Country = settings.Country
	return json.Marshal(snapshot)
}

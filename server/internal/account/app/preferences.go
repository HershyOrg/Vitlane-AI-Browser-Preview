package app

import (
	"context"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type PreferencesRepository interface {
	ReadPreferences(context.Context, string) (accountdomain.UserPreferences, error)
	PatchPreferences(context.Context, string, accountdomain.PreferencesPatch, *int64) (accountdomain.UserPreferences, error)
}

type PreferencesService struct{ Repository PreferencesRepository }

func (s *PreferencesService) Read(ctx context.Context, user string) (accountdomain.UserPreferences, error) {
	return s.Repository.ReadPreferences(ctx, user)
}

func (s *PreferencesService) Patch(ctx context.Context, user string, patch accountdomain.PreferencesPatch, expected *int64) (accountdomain.UserPreferences, error) {
	if err := patch.Validate(); err != nil || (expected != nil && *expected < 0) {
		return accountdomain.UserPreferences{}, fault.New(fault.InvalidInput, "USER_PREFERENCES_INVALID", false)
	}
	return s.Repository.PatchPreferences(ctx, user, patch, expected)
}

// RememberResearchSelection is called only inside an accepted user command's
// transaction. Worker completion and replay never call this port.
func (s *PreferencesService) RememberResearchSelection(ctx context.Context, user, country, currency string) error {
	p := accountdomain.PreferencesPatch{}
	if country != "" {
		p.ResearchCountry = &country
	}
	if currency != "" {
		p.PreferredCurrency = &currency
	}
	_, err := s.Patch(ctx, user, p, nil)
	return err
}

// RequestPreferenceDefaults is what an unselected account sees for this
// request (ADR-0080). Without Web request evidence it is English/USD/US.
func RequestPreferenceDefaults(ctx context.Context) accountdomain.PreferenceDefaults {
	hint, _ := sharedapp.LocaleHintFrom(ctx)
	return accountdomain.DefaultsFor(hint.UILocale(), hint.BrowserLocale())
}

// ContentLocale is copied only when accepting a new generation request. An
// explicit account language wins, then the language the requesting browser
// shows. Background work has no browser, so it returns "" and the caller keeps
// the language its originating request recorded.
func (s *PreferencesService) ContentLocale(ctx context.Context, user string) (string, error) {
	p, err := s.Read(ctx, user)
	if err != nil {
		return "", err
	}
	if p.UILocale != "" {
		return p.UILocale, nil
	}
	if hint, ok := sharedapp.LocaleHintFrom(ctx); ok {
		return hint.UILocale(), nil
	}
	return "", nil
}

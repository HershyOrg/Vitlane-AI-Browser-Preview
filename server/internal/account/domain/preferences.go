package domain

import "errors"

var ErrPreferencesInvalid = errors.New("USER_PREFERENCES_INVALID")

// Empty stored fields mean no explicit selection. Defaults are a projection,
// never an implicit user command.
type UserPreferences struct {
	SchemaVersion     string `json:"schemaVersion"`
	Version           int64  `json:"version"`
	UILocale          string `json:"uiLocale,omitempty"`
	PreferredCurrency string `json:"preferredCurrency,omitempty"`
	ResearchCountry   string `json:"researchCountry,omitempty"`
}

type PreferencesPatch struct {
	UILocale          *string `json:"uiLocale,omitempty"`
	PreferredCurrency *string `json:"preferredCurrency,omitempty"`
	ResearchCountry   *string `json:"researchCountry,omitempty"`
}

func (p PreferencesPatch) Validate() error {
	if p.UILocale == nil && p.PreferredCurrency == nil && p.ResearchCountry == nil {
		return ErrPreferencesInvalid
	}
	if p.UILocale != nil && *p.UILocale != "ko-KR" && *p.UILocale != "en-US" {
		return ErrPreferencesInvalid
	}
	if p.PreferredCurrency != nil && *p.PreferredCurrency != "KRW" && *p.PreferredCurrency != "USD" {
		return ErrPreferencesInvalid
	}
	if p.ResearchCountry != nil && *p.ResearchCountry != "KR" && *p.ResearchCountry != "US" {
		return ErrPreferencesInvalid
	}
	return nil
}

func (p UserPreferences) Apply(patch PreferencesPatch) (UserPreferences, error) {
	if err := patch.Validate(); err != nil {
		return p, err
	}
	if patch.UILocale != nil {
		p.UILocale = *patch.UILocale
	}
	if patch.PreferredCurrency != nil {
		p.PreferredCurrency = *patch.PreferredCurrency
	}
	if patch.ResearchCountry != nil {
		p.ResearchCountry = *patch.ResearchCountry
	}
	p.SchemaVersion = "vitlane.user-preferences.v1"
	p.Version++
	return p, nil
}

// PreferenceDefaults fill the fields an account has not selected. They come
// from the request's browser evidence and are projected, never stored.
type PreferenceDefaults struct {
	UILocale          string
	PreferredCurrency string
	ResearchCountry   string
}

// DefaultsFor takes the resolved display language and the browser language
// alone. Currency and country follow the browser so that switching the display
// language never moves them: a Korean browser starts in KRW/KR, others USD/US.
func DefaultsFor(uiLocale, browserLocale string) PreferenceDefaults {
	defaults := PreferenceDefaults{UILocale: "en-US", PreferredCurrency: "USD", ResearchCountry: "US"}
	if uiLocale == "ko-KR" {
		defaults.UILocale = "ko-KR"
	}
	if browserLocale == "ko-KR" {
		defaults.PreferredCurrency = "KRW"
		defaults.ResearchCountry = "KR"
	}
	return defaults
}

func (p UserPreferences) Effective(defaults PreferenceDefaults) UserPreferences {
	p.SchemaVersion = "vitlane.user-preferences.v1"
	if p.UILocale == "" {
		p.UILocale = defaults.UILocale
	}
	if p.PreferredCurrency == "" {
		p.PreferredCurrency = defaults.PreferredCurrency
	}
	if p.ResearchCountry == "" {
		p.ResearchCountry = defaults.ResearchCountry
	}
	return p
}

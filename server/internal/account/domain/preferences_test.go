package domain

import "testing"

func TestPreferencesDefaultsFollowTheBrowserAndDoNotCreateSelections(t *testing.T) {
	stored := UserPreferences{}
	korean := stored.Effective(DefaultsFor("ko-KR", "ko-KR"))
	if korean.UILocale != "ko-KR" || korean.PreferredCurrency != "KRW" || korean.ResearchCountry != "KR" || korean.Version != 0 {
		t.Fatalf("unexpected Korean browser defaults: %+v", korean)
	}
	english := stored.Effective(DefaultsFor("en-US", "en-US"))
	if english.UILocale != "en-US" || english.PreferredCurrency != "USD" || english.ResearchCountry != "US" {
		t.Fatalf("unexpected English browser defaults: %+v", english)
	}
	// A Korean page seen from an English browser changes only the language.
	shown := stored.Effective(DefaultsFor("ko-KR", "en-US"))
	if shown.UILocale != "ko-KR" || shown.PreferredCurrency != "USD" || shown.ResearchCountry != "US" {
		t.Fatalf("display language moved regional defaults: %+v", shown)
	}
	if unknown := DefaultsFor("ja-JP", ""); unknown != (PreferenceDefaults{UILocale: "en-US", PreferredCurrency: "USD", ResearchCountry: "US"}) {
		t.Fatalf("unsupported signals must fall back to English/USD/US: %+v", unknown)
	}
	if stored.UILocale != "" || stored.PreferredCurrency != "" || stored.ResearchCountry != "" {
		t.Fatal("projection persisted defaults")
	}
	en := "en-US"
	stored, err := stored.Apply(PreferencesPatch{UILocale: &en})
	if err != nil {
		t.Fatal(err)
	}
	us := "US"
	stored, err = stored.Apply(PreferencesPatch{ResearchCountry: &us})
	if err != nil {
		t.Fatal(err)
	}
	projected := stored.Effective(DefaultsFor("ko-KR", "ko-KR"))
	if stored.Version != 2 || stored.UILocale != en || stored.PreferredCurrency != "" || projected.PreferredCurrency != "KRW" || projected.UILocale != en || projected.ResearchCountry != us {
		t.Fatalf("country update changed independent settings or explicit selections lost to defaults: %+v %+v", stored, projected)
	}
	kr := "KR"
	stored, _ = stored.Apply(PreferencesPatch{ResearchCountry: &kr})
	if stored.ResearchCountry != "KR" || stored.UILocale != en {
		t.Fatal("last selection was not retained independently")
	}
}

func TestPreferencesRejectEmptyAndUnsupportedValuesWithoutMutation(t *testing.T) {
	bad := "JP"
	empty := ""
	locale := "ko"
	currency := "krw"
	for _, patch := range []PreferencesPatch{{}, {ResearchCountry: &bad}, {UILocale: &locale}, {PreferredCurrency: &currency}, {UILocale: &empty}} {
		original := UserPreferences{Version: 9, UILocale: "en-US", PreferredCurrency: "USD", ResearchCountry: "US"}
		result, err := original.Apply(patch)
		if err == nil || result != original {
			t.Fatalf("invalid patch mutated state: %+v", patch)
		}
	}
}

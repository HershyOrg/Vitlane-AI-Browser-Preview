package app

import (
	"context"
	"testing"
)

func TestLocaleHintResolvesChoiceThenSeenThenBrowserThenEnglish(t *testing.T) {
	for _, tc := range []struct {
		hint             LocaleHint
		uiLocale, region string
	}{
		{LocaleHint{}, "en-US", "en-US"},
		{LocaleHint{Browser: "ko-KR"}, "ko-KR", "ko-KR"},
		{LocaleHint{Seen: "ko-KR", Browser: "en-US"}, "ko-KR", "en-US"},
		{LocaleHint{Seen: "en-US", Browser: "ko-KR"}, "en-US", "ko-KR"},
		{LocaleHint{Choice: "en-US", Seen: "ko-KR", Browser: "ko-KR"}, "en-US", "ko-KR"},
		{LocaleHint{Choice: "ko", Seen: "EN-us"}, "en-US", "en-US"},
	} {
		if got := tc.hint.UILocale(); got != tc.uiLocale {
			t.Errorf("%+v UILocale = %q, want %q", tc.hint, got, tc.uiLocale)
		}
		if got := tc.hint.BrowserLocale(); got != tc.region {
			t.Errorf("%+v BrowserLocale = %q, want %q", tc.hint, got, tc.region)
		}
	}
}

func TestLocaleHintIsAbsentOutsideAWebRequest(t *testing.T) {
	if _, ok := LocaleHintFrom(context.Background()); ok {
		t.Fatal("background context reported a locale hint")
	}
	hint := LocaleHint{Browser: "ko-KR"}
	got, ok := LocaleHintFrom(WithLocaleHint(context.Background(), hint))
	if !ok || got != hint {
		t.Fatalf("hint = %+v ok=%v", got, ok)
	}
}

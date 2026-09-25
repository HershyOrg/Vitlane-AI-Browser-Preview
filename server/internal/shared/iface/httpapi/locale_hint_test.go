package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

func TestBrowserLocaleFollowsPreferenceOrderBetweenKoreanAndEnglish(t *testing.T) {
	for header, want := range map[string]string{
		"":                                    "",
		"ko-KR,ko;q=0.9,en-US;q=0.8,en;q=0.7": "ko-KR",
		"en-US,en;q=0.9,ko;q=0.8":             "en-US",
		"ja,ko;q=0.9,en;q=0.8":                "ko-KR",
		"en;q=0.5, ko;q=0.8":                  "ko-KR",
		"KO":                                  "ko-KR",
		"ko;q=0, en":                          "en-US",
		"ko;q=abc, en-GB":                     "en-US",
		"fr-FR,fr;q=0.9,*;q=0.5":              "",
	} {
		if got := BrowserLocale(header); got != want {
			t.Errorf("BrowserLocale(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestLocaleHintReadsOnlyValidChoiceAndSeenCookies(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "ko-KR,ko;q=0.9")
	request.AddCookie(&http.Cookie{Name: "vt_locale", Value: "en-US"})
	request.AddCookie(&http.Cookie{Name: LocaleSeenCookie, Value: "en"})
	hint := LocaleHintFromRequest(request)
	if hint != (sharedapp.LocaleHint{Browser: "ko-KR"}) || hint.UILocale() != "ko-KR" {
		t.Fatalf("legacy or malformed cookie changed the hint: %+v", hint)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "ko-KR")
	request.AddCookie(&http.Cookie{Name: LocaleSeenCookie, Value: "en-US"})
	request.AddCookie(&http.Cookie{Name: LocaleChoiceCookie, Value: "ko-KR"})
	hint = LocaleHintFromRequest(request)
	if hint != (sharedapp.LocaleHint{Choice: "ko-KR", Seen: "en-US", Browser: "ko-KR"}) {
		t.Fatalf("hint = %+v", hint)
	}
}

func TestMiddlewareAttachesLocaleHint(t *testing.T) {
	var got sharedapp.LocaleHint
	var found bool
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, found = sharedapp.LocaleHintFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), fixedIDs{}, slog.Default(), time.Second)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences", nil)
	request.Header.Set("Accept-Language", "en-US,ko;q=0.5")
	request.AddCookie(&http.Cookie{Name: LocaleSeenCookie, Value: "ko-KR"})
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !found || got != (sharedapp.LocaleHint{Seen: "ko-KR", Browser: "en-US"}) {
		t.Fatalf("hint = %+v found=%v", got, found)
	}
}

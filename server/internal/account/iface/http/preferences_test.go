package http

import (
	"context"
	"encoding/json"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type preferenceStore map[string]accountdomain.UserPreferences

func (s preferenceStore) ReadPreferences(_ context.Context, user string) (accountdomain.UserPreferences, error) {
	return s[user], nil
}
func (s preferenceStore) PatchPreferences(_ context.Context, user string, p accountdomain.PreferencesPatch, v *int64) (accountdomain.UserPreferences, error) {
	stored := s[user]
	if v != nil && *v != stored.Version {
		return stored, fault.New(fault.Conflict, "USER_PREFERENCES_VERSION_CONFLICT", false)
	}
	result, err := stored.Apply(p)
	if err == nil {
		s[user] = result
	}
	return result, err
}

func TestPreferencesHTTPUsesPrincipalAndVersionedPartialUpdates(t *testing.T) {
	store := preferenceStore{}
	h := NewHandler(nil)
	h.EnablePreferences(&accountapp.PreferencesService{Repository: store})
	call := func(user, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/me/preferences", strings.NewReader(body))
		if user != "" {
			r = r.WithContext(sharedapp.WithAuthenticatedUserID(r.Context(), user))
		}
		w := httptest.NewRecorder()
		h.Preferences(w, r)
		return w
	}
	if w := call("", http.MethodGet, ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w := call("alice", http.MethodPatch, `{"schemaVersion":"vitlane.user-preferences.v1","expectedVersion":0,"uiLocale":"en-US"}`); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if w := call("alice", http.MethodPatch, `{"schemaVersion":"vitlane.user-preferences.v1","expectedVersion":0,"researchCountry":"US"}`); w.Code != 409 {
		t.Fatalf("stale write: %d", w.Code)
	}
	if w := call("alice", http.MethodPatch, `{"schemaVersion":"vitlane.user-preferences.v1","expectedVersion":1,"researchCountry":"US","userId":"bob"}`); w.Code != 400 {
		t.Fatalf("body principal accepted: %d", w.Code)
	}
	if w := call("alice", http.MethodPatch, `{"schemaVersion":"vitlane.user-preferences.v1","researchCountry":"US"}`); w.Code != 400 {
		t.Fatalf("missing version accepted: %d", w.Code)
	}
	w := call("bob", http.MethodGet, "")
	var body struct {
		Effective accountdomain.UserPreferences `json:"effective"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Effective.UILocale != "en-US" || body.Effective.ResearchCountry != "US" || body.Effective.Version != 0 || len(store) != 1 {
		t.Fatalf("cross-user or implicit default write: %+v", body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preferences can be cached")
	}
}

func TestPreferencesHTTPProjectsUnselectedFieldsFromTheRequestBrowser(t *testing.T) {
	store := preferenceStore{"alice": {SchemaVersion: "vitlane.user-preferences.v1", Version: 1, UILocale: "en-US"}}
	h := NewHandler(nil)
	h.EnablePreferences(&accountapp.PreferencesService{Repository: store})
	read := func(user string, hint sharedapp.LocaleHint) accountdomain.UserPreferences {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences", nil)
		ctx := sharedapp.WithLocaleHint(sharedapp.WithAuthenticatedUserID(r.Context(), user), hint)
		w := httptest.NewRecorder()
		h.Preferences(w, r.WithContext(ctx))
		var body struct {
			Effective accountdomain.UserPreferences `json:"effective"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
		return body.Effective
	}
	for _, tc := range []struct {
		name, user                string
		hint                      sharedapp.LocaleHint
		locale, currency, country string
	}{
		{"korean browser", "bob", sharedapp.LocaleHint{Browser: "ko-KR"}, "ko-KR", "KRW", "KR"},
		{"english browser", "bob", sharedapp.LocaleHint{Browser: "en-US"}, "en-US", "USD", "US"},
		{"korean landing in english browser", "bob", sharedapp.LocaleHint{Seen: "ko-KR", Browser: "en-US"}, "ko-KR", "USD", "US"},
		{"explicit browser choice", "bob", sharedapp.LocaleHint{Choice: "en-US", Seen: "ko-KR", Browser: "ko-KR"}, "en-US", "KRW", "KR"},
		{"account selection wins", "alice", sharedapp.LocaleHint{Choice: "ko-KR", Browser: "ko-KR"}, "en-US", "KRW", "KR"},
	} {
		got := read(tc.user, tc.hint)
		if got.UILocale != tc.locale || got.PreferredCurrency != tc.currency || got.ResearchCountry != tc.country {
			t.Errorf("%s: effective = %+v", tc.name, got)
		}
	}
	if _, stored := store["bob"]; stored || store["alice"].Version != 1 {
		t.Fatalf("a read persisted a projected default: %+v", store)
	}
}

func TestContentLocaleUsesAccountThenRequestAndLeavesBackgroundUnresolved(t *testing.T) {
	store := preferenceStore{"alice": {Version: 1, UILocale: "en-US"}}
	service := &accountapp.PreferencesService{Repository: store}
	korean := sharedapp.WithLocaleHint(context.Background(), sharedapp.LocaleHint{Browser: "ko-KR"})
	for _, tc := range []struct {
		name string
		ctx  context.Context
		user string
		want string
	}{
		{"explicit account", korean, "alice", "en-US"},
		{"request browser", korean, "bob", "ko-KR"},
		{"background work", context.Background(), "bob", ""},
		{"background explicit account", context.Background(), "alice", "en-US"},
	} {
		if got, err := service.ContentLocale(tc.ctx, tc.user); err != nil || got != tc.want {
			t.Errorf("%s: ContentLocale = %q err=%v, want %q", tc.name, got, err, tc.want)
		}
	}
}

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProductionSessionCookiePolicy(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 25, 0, 0, 0, 0, time.UTC)
	response := httptest.NewRecorder()

	(CookiePolicy{Secure: true}).setSession(response, "opaque-token", expiresAt)

	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count=%d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value != "opaque-token" {
		t.Fatalf("unexpected session cookie: %#v", cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("session cookie must be HttpOnly and Secure: %#v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite=%d, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" || !cookie.Expires.Equal(expiresAt) || cookie.MaxAge <= 0 {
		t.Fatalf("unexpected session lifetime policy: %#v", cookie)
	}
}

func TestLoginAttemptCookieIsCallbackScoped(t *testing.T) {
	response := httptest.NewRecorder()

	(CookiePolicy{Secure: true}).setBinding(
		response,
		"browser-binding",
		time.Now().Add(10*time.Minute),
	)

	cookie := response.Result().Cookies()[0]
	if cookie.Name != bindingCookieName ||
		cookie.Path != "/api/v1/auth/google/callback" {
		t.Fatalf("unexpected login attempt scope: %#v", cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected login attempt policy: %#v", cookie)
	}
}

func TestRequestSessionTokenAcceptsOneCredentialMode(t *testing.T) {
	bearer := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	bearer.Header.Set("Authorization", "Bearer native-token")
	if token, ok := requestSessionToken(bearer); !ok || token != "native-token" {
		t.Fatalf("bearer token = (%q, %v)", token, ok)
	}

	cookie := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	cookie.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "browser-token"})
	if token, ok := requestSessionToken(cookie); !ok || token != "browser-token" {
		t.Fatalf("cookie token = (%q, %v)", token, ok)
	}

	ambiguous := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	ambiguous.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "browser-token"})
	ambiguous.Header.Set("Authorization", "Bearer native-token")
	if token, ok := requestSessionToken(ambiguous); ok || token != "" {
		t.Fatalf("ambiguous credentials accepted: (%q, %v)", token, ok)
	}
}

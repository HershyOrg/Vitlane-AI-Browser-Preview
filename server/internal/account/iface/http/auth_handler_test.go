package http

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

func TestMobileAuthStartRejectsUnknownRedirectAndInvalidChallenge(t *testing.T) {
	handler := NewAuthHandler(
		nil, CookiePolicy{}, NewEmailAllowlist(nil), NewEmailAllowlist(nil),
		AuthFeatures{MobileRedirectURIs: []string{"vitlane://auth/callback"}},
	)
	challenge := sha256.Sum256([]byte("verifier"))
	encoded := base64.RawURLEncoding.EncodeToString(challenge[:])
	for _, target := range []string{
		"/api/v1/auth/mobile/google/start?redirect_uri=evil%3A%2F%2Fauth%2Fcallback&code_challenge=" + encoded,
		"/api/v1/auth/mobile/google/start?redirect_uri=vitlane%3A%2F%2Fauth%2Fcallback&code_challenge=bad",
	} {
		response := httptest.NewRecorder()
		handler.BeginMobileGoogleLogin(
			response, httptest.NewRequest(stdhttp.MethodGet, target, nil),
		)
		if response.Code != stdhttp.StatusBadRequest {
			t.Fatalf("target %q status=%d", target, response.Code)
		}
	}
}

// denyingRateLimiter refuses every request so a handler's 429 path can be
// exercised without a database.
type denyingRateLimiter struct {
	rules []accountapp.RateLimitRule
}

func (l *denyingRateLimiter) Acquire(
	_ context.Context,
	rules []accountapp.RateLimitRule,
	_ time.Time,
) (accountapp.RateLimitDecision, error) {
	l.rules = append([]accountapp.RateLimitRule(nil), rules...)
	return accountapp.RateLimitDecision{
		Allowed: false, Policy: accountapp.RatePolicyGoogleLoginBegin,
		RetryAfter: 17 * time.Second,
	}, nil
}

type authHandlerClock struct{ now time.Time }

func (c authHandlerClock) Now() time.Time { return c.now }

type authHandlerProvider struct{}

func (authHandlerProvider) AuthorizationURL(
	accountapp.AuthorizationRequest,
) (string, error) {
	return "https://provider.example", nil
}

func (authHandlerProvider) ExchangeAndVerifyCode(
	context.Context,
	accountapp.CodeExchangeRequest,
) (accountdomain.VerifiedIdentity, error) {
	return accountdomain.VerifiedIdentity{}, nil
}

func TestAuthCapabilitiesExposeOnlyConfiguredLoginMethods(t *testing.T) {
	handler := NewAuthHandler(
		nil, CookiePolicy{}, NewEmailAllowlist(nil), NewEmailAllowlist(nil),
		AuthFeatures{
			GoogleEnabled:             false,
			DevelopmentSessionEnabled: true,
			DevelopmentUserID:         "e5000000-0000-4000-8000-000000000001",
		},
	)
	response := httptest.NewRecorder()
	handler.Capabilities(
		response,
		httptest.NewRequest(stdhttp.MethodGet, "/api/v1/auth/capabilities", nil),
	)

	if response.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var body struct {
		GoogleEnabled      bool `json:"googleEnabled"`
		LocalReviewEnabled bool `json:"localReviewEnabled"`
		LocalReviewSeeded  bool `json:"localReviewSeeded"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.GoogleEnabled || !body.LocalReviewEnabled || !body.LocalReviewSeeded {
		t.Fatalf("unexpected capabilities: %+v", body)
	}
}

func TestUnavailableGoogleLoginReturnsToLoginScreen(t *testing.T) {
	service := accountapp.NewAuthenticationService(nil, nil, nil, nil, nil, nil)
	handler := NewAuthHandler(
		service, CookiePolicy{}, NewEmailAllowlist(nil), NewEmailAllowlist(nil),
		AuthFeatures{},
	)
	response := httptest.NewRecorder()
	handler.BeginGoogleLogin(
		response,
		httptest.NewRequest(
			stdhttp.MethodGet, "/api/v1/auth/google/start?returnTo=/account", nil,
		),
	)

	if response.Code != stdhttp.StatusSeeOther {
		t.Fatalf("expected 303, got %d", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/login?error=AUTH_PROVIDER_FAILED" {
		t.Fatalf("unexpected redirect: %s", location)
	}
}

func TestAuthRateLimitReturns429WithRetryAfter(t *testing.T) {
	response := httptest.NewRecorder()
	writeAuthRateLimit(response, &accountapp.RateLimitError{
		Policy:     accountapp.RatePolicyGoogleLoginBegin,
		RetryAfter: 19 * time.Second,
	})

	if response.Code != stdhttp.StatusTooManyRequests ||
		response.Header().Get("Retry-After") != "19" {
		t.Fatalf(
			"status=%d Retry-After=%q",
			response.Code, response.Header().Get("Retry-After"),
		)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != accountapp.ErrRateLimited.Error() {
		t.Fatalf("error code=%q", body.Error.Code)
	}
}

func TestRateLimitedGoogleCallbackPreservesBrowserBinding(t *testing.T) {
	service := accountapp.NewAuthenticationService(
		nil, authHandlerProvider{}, nil,
		authHandlerClock{now: time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	service.EnableRateLimiter(&denyingRateLimiter{})
	handler := NewAuthHandler(
		service, CookiePolicy{}, NewEmailAllowlist(nil), NewEmailAllowlist(nil),
		AuthFeatures{},
	)
	request := httptest.NewRequest(
		stdhttp.MethodGet,
		"/api/v1/auth/google/callback?code=code&state=state&iss=https%3A%2F%2Faccounts.google.com",
		nil,
	)
	request.AddCookie(&stdhttp.Cookie{Name: bindingCookieName, Value: "binding"})
	response := httptest.NewRecorder()

	handler.CompleteGoogleLogin(response, request)

	if response.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("status=%d", response.Code)
	}
	if cookies := response.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("rate-limited callback cleared binding cookie: %#v", cookies)
	}
}

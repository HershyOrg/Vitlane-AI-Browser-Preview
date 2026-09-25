package http

import (
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnalyticsIdentityRequiresConsentAndExcludesOperators(t *testing.T) {
	h := &AuthHandler{marketingAdminEmails: NewEmailAllowlist([]string{"operator@example.com"})}
	calls := 0
	h.EnableAnalyticsIdentity(func(string) string { calls++; return "pseudonym" })
	user := accountdomain.User{ID: "private-id", Email: "user@example.com"}
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	if got := h.optionalAnalyticsIdentity(r, user); got != "" || calls != 0 {
		t.Fatal("identity before consent")
	}
	r.AddCookie(&http.Cookie{Name: "vt_analytics", Value: "v1.allowed"})
	if got := h.optionalAnalyticsIdentity(r, user); got != "pseudonym" || calls != 1 {
		t.Fatal("missing consented identity")
	}
	user.Email = "operator@example.com"
	if got := h.optionalAnalyticsIdentity(r, user); got != "" || calls != 1 {
		t.Fatal("operator identity exported")
	}
}

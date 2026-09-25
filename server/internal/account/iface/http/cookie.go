package http

import (
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "vitlane_session"
	bindingCookieName = "vitlane_login_attempt"
)

type CookiePolicy struct {
	Secure bool
}

func (p CookiePolicy) setSession(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: int((7 * 24 * time.Hour).Seconds()),
	})
}

func (p CookiePolicy) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (p CookiePolicy) setBinding(w http.ResponseWriter, binding string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: bindingCookieName, Value: binding,
		Path:     "/api/v1/auth/google/callback",
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: int((10 * time.Minute).Seconds()),
	})
}

func (p CookiePolicy) clearBinding(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: bindingCookieName, Value: "",
		Path:     "/api/v1/auth/google/callback",
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// requestSessionToken keeps browser cookies and native bearer sessions on the
// same server-side AuthSession model. Supplying both is rejected so a request
// can never authenticate as an ambiguous principal.
func requestSessionToken(r *http.Request) (string, bool) {
	cookieToken := cookieValue(r, sessionCookieName)
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization == "" {
		return cookieToken, cookieToken != ""
	}
	parts := strings.Fields(authorization)
	if cookieToken != "" || len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return parts[1], true
}

package http

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type AuthHandler struct {
	analyticsIdentity        func(string) string
	service                  *accountapp.AuthenticationService
	cookies                  CookiePolicy
	marketingAdminEmails     EmailAllowlist
	settlementOperatorEmails EmailAllowlist
	features                 AuthFeatures
	trustedProxies           []netip.Prefix
}

func (h *AuthHandler) EnableAnalyticsIdentity(identity func(string) string) {
	h.analyticsIdentity = identity
}

func (h *AuthHandler) EnableTrustedProxies(prefixes []netip.Prefix) {
	h.trustedProxies = append([]netip.Prefix(nil), prefixes...)
}

type AuthFeatures struct {
	GoogleEnabled             bool
	DevelopmentSessionEnabled bool
	DevelopmentUserID         string
	DevelopmentProfiles       []DevelopmentProfile
	MobileRedirectURIs        []string
	// MerchantEffectLive는 배포 전역 모드다(ADR-0052 Step 6 activation) —
	// 모드 배너의 단일 소스(ADR-0057 D3).
	MerchantEffectLive bool
}

type DevelopmentProfile struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	UserID      string `json:"-"`
	Operator    bool   `json:"operator"`
	Resettable  bool   `json:"resettable"`
	StartPath   string `json:"startPath,omitempty"`
}

func NewAuthHandler(
	service *accountapp.AuthenticationService,
	cookies CookiePolicy,
	marketingAdminEmails EmailAllowlist,
	settlementOperatorEmails EmailAllowlist,
	features AuthFeatures,
) *AuthHandler {
	return &AuthHandler{
		service: service, cookies: cookies, marketingAdminEmails: marketingAdminEmails,
		settlementOperatorEmails: settlementOperatorEmails,
		features:                 features,
	}
}

func (h *AuthHandler) Capabilities(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	profiles := h.features.DevelopmentProfiles
	if !h.features.DevelopmentSessionEnabled {
		profiles = nil
	}
	merchantEffectMode := "SANDBOX"
	if h.features.MerchantEffectLive {
		merchantEffectMode = "LIVE"
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"googleEnabled":       h.features.GoogleEnabled,
		"mobileGoogleEnabled": h.features.GoogleEnabled && len(h.features.MobileRedirectURIs) > 0,
		"localReviewEnabled":  h.features.DevelopmentSessionEnabled,
		"localReviewSeeded": h.features.DevelopmentSessionEnabled &&
			(h.features.DevelopmentUserID != "" || len(profiles) > 0),
		"localReviewProfiles": profiles,
		"merchantEffectMode":  merchantEffectMode,
	})
}

func (h *AuthHandler) BeginMobileGoogleLogin(w http.ResponseWriter, r *http.Request) {
	redirectURI := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	challenge := strings.TrimSpace(r.URL.Query().Get("code_challenge"))
	if !h.mobileRedirectAllowed(redirectURI) || !validMobileChallenge(challenge) {
		httpapi.WriteError(w, http.StatusBadRequest, "MOBILE_AUTH_INVALID", "모바일 로그인 요청이 올바르지 않습니다.")
		return
	}
	returnTo := "/api/v1/auth/mobile/complete?" + url.Values{
		"redirect_uri":   []string{redirectURI},
		"code_challenge": []string{challenge},
	}.Encode()
	result, err := h.service.BeginGoogleLogin(r.Context(), accountapp.BeginGoogleLoginInput{
		ReturnPath: returnTo,
		Origin:     strings.TrimSpace(r.Header.Get("Origin")),
		Source:     registrationClientKey(r, h.trustedProxies),
	})
	if err != nil {
		if errors.Is(err, accountapp.ErrRateLimited) {
			writeAuthRateLimit(w, err)
			return
		}
		httpapi.WriteError(w, http.StatusServiceUnavailable, accountapp.AuthenticationReasonCode(err), "Google 로그인을 시작하지 못했습니다.")
		return
	}
	h.cookies.setBinding(w, result.BrowserBinding, result.ExpiresAt)
	http.Redirect(w, r, result.AuthorizationURL, http.StatusFound)
}

func (h *AuthHandler) BeginGoogleLogin(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.BeginGoogleLogin(r.Context(), accountapp.BeginGoogleLoginInput{
		ReturnPath: r.URL.Query().Get("returnTo"),
		Origin:     strings.TrimSpace(r.Header.Get("Origin")),
		Source:     registrationClientKey(r, h.trustedProxies),
		// fresh=1 drives operator re-authentication for sensitive actions
		// (ADR-0040 §10); it is harmless on ordinary logins.
		ForceFresh: r.URL.Query().Get("fresh") == "1",
	})
	if err != nil {
		if errors.Is(err, accountapp.ErrRateLimited) {
			writeAuthRateLimit(w, err)
			return
		}
		http.Redirect(
			w, r, loginErrorLocation(accountapp.AuthenticationReasonCode(err)),
			http.StatusSeeOther,
		)
		return
	}
	h.cookies.setBinding(w, result.BrowserBinding, result.ExpiresAt)
	http.Redirect(w, r, result.AuthorizationURL, http.StatusFound)
}

func (h *AuthHandler) CompleteGoogleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	result, err := h.service.CompleteGoogleLogin(r.Context(), accountapp.CompleteGoogleLoginInput{
		Code:                 r.URL.Query().Get("code"),
		State:                r.URL.Query().Get("state"),
		Issuer:               r.URL.Query().Get("iss"),
		ProviderError:        r.URL.Query().Get("error"),
		BrowserBinding:       cookieValue(r, bindingCookieName),
		PreviousSessionToken: cookieValue(r, sessionCookieName),
		Origin:               strings.TrimSpace(r.Header.Get("Origin")),
		Source:               registrationClientKey(r, h.trustedProxies),
	})
	if err != nil {
		if errors.Is(err, accountapp.ErrRateLimited) {
			writeAuthRateLimit(w, err)
			return
		}
		h.cookies.clearBinding(w)
		if h.mobileCompletionReturnPath(result.ReturnPath) {
			target := result.ReturnPath
			separator := "&"
			if !strings.Contains(target, "?") {
				separator = "?"
			}
			http.Redirect(w, r, target+separator+"error="+url.QueryEscape(accountapp.AuthenticationReasonCode(err)), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, loginErrorLocation(accountapp.AuthenticationReasonCode(err)), http.StatusSeeOther)
		return
	}
	h.cookies.clearBinding(w)
	h.cookies.setSession(w, result.SessionToken, result.ExpiresAt)
	http.Redirect(w, r, result.ReturnPath, http.StatusSeeOther)
}

func (h *AuthHandler) CompleteMobileLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	redirectURI := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	challengeText := strings.TrimSpace(r.URL.Query().Get("code_challenge"))
	if !h.mobileRedirectAllowed(redirectURI) || !validMobileChallenge(challengeText) {
		httpapi.WriteError(w, http.StatusBadRequest, "MOBILE_AUTH_INVALID", "모바일 로그인 요청이 올바르지 않습니다.")
		return
	}
	if reason := strings.TrimSpace(r.URL.Query().Get("error")); reason != "" {
		h.redirectMobile(w, r, redirectURI, "", reason)
		return
	}
	challenge, _ := base64.RawURLEncoding.DecodeString(challengeText)
	result, err := h.service.CreateMobileHandoff(
		r.Context(), cookieValue(r, sessionCookieName), challenge,
	)
	if err != nil {
		h.cookies.clearSession(w)
		h.redirectMobile(w, r, redirectURI, "", accountapp.AuthenticationReasonCode(err))
		return
	}
	h.cookies.clearSession(w)
	h.redirectMobile(w, r, redirectURI, result.Code, "")
}

type mobileHandoffExchangeRequest struct {
	Code     string `json:"code"`
	Verifier string `json:"verifier"`
}

func (h *AuthHandler) ExchangeMobileLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	var request mobileHandoffExchangeRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.ExchangeMobileHandoff(
		r.Context(), request.Code, request.Verifier,
	)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, accountdomain.ErrMobileAuthConsumed) {
			status = http.StatusConflict
		}
		httpapi.WriteError(w, status, accountapp.AuthenticationReasonCode(err), "모바일 로그인을 완료하지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"sessionToken": result.SessionToken,
		"expiresAt":    result.ExpiresAt,
		"user":         h.userResponse(r, result.User),
	})
}

func validMobileChallenge(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
}

func (h *AuthHandler) mobileRedirectAllowed(value string) bool {
	if value == "" {
		return false
	}
	for _, allowed := range h.features.MobileRedirectURIs {
		if value == allowed {
			return true
		}
	}
	return false
}

func (h *AuthHandler) mobileCompletionReturnPath(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Path != "/api/v1/auth/mobile/complete" {
		return false
	}
	return h.mobileRedirectAllowed(parsed.Query().Get("redirect_uri")) &&
		validMobileChallenge(parsed.Query().Get("code_challenge"))
}

func (h *AuthHandler) redirectMobile(
	w http.ResponseWriter,
	r *http.Request,
	redirectURI, code, reason string,
) {
	target, _ := url.Parse(redirectURI)
	query := target.Query()
	if code != "" {
		query.Set("code", code)
	}
	if reason != "" {
		query.Set("error", reason)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func writeAuthRateLimit(w http.ResponseWriter, err error) {
	w.Header().Set(
		"Retry-After", strconv.Itoa(accountapp.RateLimitRetryAfter(err)),
	)
	httpapi.WriteError(
		w, http.StatusTooManyRequests, accountapp.ErrRateLimited.Error(),
		"로그인 요청이 너무 많습니다. 잠시 후 다시 시도해 주세요.",
	)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := requestSessionToken(r)
	if !ok {
		httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
		return
	}
	user, err := h.service.AuthenticateSession(r.Context(), token)
	if err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"user": h.userResponse(r, user)})
}

func (h *AuthHandler) userResponse(r *http.Request, user accountdomain.User) map[string]any {
	return map[string]any{
		"analyticsUserId": h.optionalAnalyticsIdentity(r, user),
		"id":              user.ID, "email": user.Email, "displayName": user.DisplayName,
		"createdAt":      user.CreatedAt,
		"marketingAdmin": h.marketingAdminEmails.Allows(user.Email),
		"phase5Operator": h.settlementOperatorEmails.Allows(user.Email),
	}
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	token, _ := requestSessionToken(r)
	_ = h.service.LogoutCurrentSession(r.Context(), token)
	h.cookies.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	token, _ := requestSessionToken(r)
	if err := h.service.LogoutAllSessions(
		r.Context(), token,
	); err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
		return
	}
	h.cookies.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

type deleteAccountRequest struct {
	Confirmation string `json:"confirmation"`
}

func (h *AuthHandler) RequestAccountDeletion(w http.ResponseWriter, r *http.Request) {
	var request deleteAccountRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if request.Confirmation != "계정 삭제" {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "ACCOUNT_DELETION_CONFIRMATION_REQUIRED",
			"확인 문구로 ‘계정 삭제’를 입력해 주세요.",
		)
		return
	}
	token, _ := requestSessionToken(r)
	if err := h.service.RequestAccountDeletion(r.Context(), token); err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
		return
	}
	h.cookies.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

type developmentSessionRequest struct {
	UserID      string `json:"userId"`
	ProfileKey  string `json:"profileKey"`
	SessionMode string `json:"sessionMode,omitempty"`
}

func (h *AuthHandler) CreateDevelopmentSession(w http.ResponseWriter, r *http.Request) {
	var request developmentSessionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if request.SessionMode != "" && request.SessionMode != "cookie" &&
		request.SessionMode != "bearer" {
		httpapi.WriteError(w, http.StatusBadRequest, "DEV_SESSION_MODE_INVALID", "개발 세션 방식이 올바르지 않습니다.")
		return
	}
	if strings.TrimSpace(request.ProfileKey) != "" {
		profile, ok := h.developmentProfile(request.ProfileKey)
		if !ok {
			httpapi.WriteError(
				w, http.StatusNotFound, "LOCAL_REVIEW_PROFILE_NOT_FOUND",
				"로컬 검수 프로필을 찾을 수 없습니다.",
			)
			return
		}
		request.UserID = profile.UserID
	} else if request.UserID == "" {
		request.UserID = h.features.DevelopmentUserID
	}
	previousToken, _ := requestSessionToken(r)
	result, err := h.service.CreateDevelopmentSession(r.Context(), accountapp.DevelopmentSessionInput{
		UserID: request.UserID, PreviousSessionToken: previousToken,
	})
	if err != nil {
		if errors.Is(err, accountdomain.ErrSessionMissing) {
			httpapi.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "개발 사용자를 찾을 수 없습니다.")
			return
		}
		if httpapi.WriteFaultIfClassified(w, r, err, "개발 세션을 만들지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "개발 세션을 만들지 못했습니다.")
		return
	}
	response := map[string]any{"user": h.userResponse(r, result.User)}
	if request.SessionMode == "bearer" {
		response["sessionToken"] = result.SessionToken
		response["expiresAt"] = result.ExpiresAt
	} else {
		h.cookies.setSession(w, result.SessionToken, result.ExpiresAt)
	}
	httpapi.WriteJSON(w, http.StatusCreated, response)
}

func (h *AuthHandler) ResetDevelopmentProfile(w http.ResponseWriter, r *http.Request) {
	profile, ok := h.developmentProfile(r.PathValue("profileKey"))
	if !ok {
		httpapi.WriteError(
			w, http.StatusNotFound, "LOCAL_REVIEW_PROFILE_NOT_FOUND",
			"로컬 검수 프로필을 찾을 수 없습니다.",
		)
		return
	}
	if err := h.service.ResetDevelopmentUser(r.Context(), profile.UserID); err != nil {
		if errors.Is(err, accountdomain.ErrSessionMissing) {
			httpapi.WriteError(
				w, http.StatusNotFound, "USER_NOT_FOUND",
				"초기화할 로컬 검수 사용자를 찾을 수 없습니다.",
			)
			return
		}
		if httpapi.WriteFaultIfClassified(w, r, err, "로컬 검수 프로필을 초기화하지 못했습니다.") {
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"로컬 검수 프로필을 초기화하지 못했습니다.",
		)
		return
	}
	h.cookies.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) developmentProfile(key string) (DevelopmentProfile, bool) {
	key = strings.TrimSpace(key)
	for _, profile := range h.features.DevelopmentProfiles {
		if profile.Key == key {
			return profile, true
		}
	}
	return DevelopmentProfile{}, false
}

func loginErrorLocation(code string) string {
	return "/login?error=" + url.QueryEscape(code)
}

func (h *AuthHandler) optionalAnalyticsIdentity(r *http.Request, user accountdomain.User) string {
	if h.analyticsIdentity == nil || cookieValue(r, "vt_analytics") != "v1.allowed" ||
		h.marketingAdminEmails.Allows(user.Email) || h.settlementOperatorEmails.Allows(user.Email) {
		return ""
	}
	return h.analyticsIdentity(string(user.ID))
}

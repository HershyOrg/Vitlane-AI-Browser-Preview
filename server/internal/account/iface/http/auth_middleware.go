package http

import (
	"net/http"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type AuthMiddleware struct {
	service                  *accountapp.AuthenticationService
	marketingAdminEmails     EmailAllowlist
	settlementOperatorEmails EmailAllowlist
}

func NewAuthMiddleware(
	service *accountapp.AuthenticationService,
	marketingAdminEmails EmailAllowlist,
	settlementOperatorEmails EmailAllowlist,
) *AuthMiddleware {
	return &AuthMiddleware{
		service: service, marketingAdminEmails: marketingAdminEmails,
		settlementOperatorEmails: settlementOperatorEmails,
	}
}

func (m *AuthMiddleware) RequireSettlementOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestSessionToken(r)
		if !ok {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		record, err := m.service.AuthenticateSessionRecord(
			r.Context(), token,
		)
		if err != nil {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		if !m.settlementOperatorEmails.Allows(record.User.Email) {
			httpapi.WriteError(
				w, http.StatusForbidden, "PHASE5_OPERATOR_REQUIRED",
				"Phase 5 운영자 권한이 없습니다.",
			)
			return
		}
		ctx := sharedapp.WithWebPrincipal(r.Context(), sharedapp.WebPrincipal{
			UserID:        string(record.User.ID),
			AuthSessionID: string(record.Session.ID),
		})
		if m.marketingAdminEmails.Allows(record.User.Email) || m.settlementOperatorEmails.Allows(record.User.Email) {
			ctx = sharedapp.WithoutAnalytics(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOperator protects cross-product operational read models. Either
// existing operator allowlist may enter the shared operator workspace, while
// product-specific write routes keep their narrower middleware.
func (m *AuthMiddleware) RequireOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestSessionToken(r)
		if !ok {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		record, err := m.service.AuthenticateSessionRecord(
			r.Context(), token,
		)
		if err != nil {
			httpapi.WriteError(
				w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
			)
			return
		}
		if !m.marketingAdminEmails.Allows(record.User.Email) &&
			!m.settlementOperatorEmails.Allows(record.User.Email) {
			httpapi.WriteError(
				w, http.StatusForbidden, "OPERATOR_REQUIRED",
				"운영자 권한이 없습니다.",
			)
			return
		}
		ctx := sharedapp.WithWebPrincipal(r.Context(), sharedapp.WebPrincipal{
			UserID:        string(record.User.ID),
			AuthSessionID: string(record.Session.ID),
		})
		if m.marketingAdminEmails.Allows(record.User.Email) || m.settlementOperatorEmails.Allows(record.User.Email) {
			ctx = sharedapp.WithoutAnalytics(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestSessionToken(r)
		if !ok {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		record, err := m.service.AuthenticateSessionRecord(
			r.Context(), token,
		)
		if err != nil {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		ctx := sharedapp.WithWebPrincipal(r.Context(), sharedapp.WebPrincipal{
			UserID:        string(record.User.ID),
			AuthSessionID: string(record.Session.ID),
		})
		if m.marketingAdminEmails.Allows(record.User.Email) || m.settlementOperatorEmails.Allows(record.User.Email) {
			ctx = sharedapp.WithoutAnalytics(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) RequireSettlementUser(
	next http.Handler,
	allowlist EmailAllowlist,
	requireAllowlist bool,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestSessionToken(r)
		if !ok {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		record, err := m.service.AuthenticateSessionRecord(
			r.Context(), token,
		)
		if err != nil {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		if (requireAllowlist || !allowlist.Empty()) &&
			!allowlist.Allows(record.User.Email) {
			httpapi.WriteError(w, http.StatusForbidden, "PHASE5_ALLOWLIST_REQUIRED", "Phase 5 TEST 사용 권한이 없습니다.")
			return
		}
		ctx := sharedapp.WithWebPrincipal(r.Context(), sharedapp.WebPrincipal{
			UserID:        string(record.User.ID),
			AuthSessionID: string(record.Session.ID),
		})
		if m.marketingAdminEmails.Allows(record.User.Email) || m.settlementOperatorEmails.Allows(record.User.Email) {
			ctx = sharedapp.WithoutAnalytics(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireFreshOperator gates sensitive operator actions on a recent
// provider re-authentication (ADR-0040 §10). A stale session receives
// FRESH_AUTH_REQUIRED and re-enters Google login with fresh=1.
func (m *AuthMiddleware) RequireFreshOperator(
	next http.Handler,
	window time.Duration,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestSessionToken(r)
		if !ok {
			httpapi.WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
			return
		}
		record, err := m.service.AuthenticateSessionRecord(
			r.Context(), token,
		)
		if err != nil {
			httpapi.WriteError(
				w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
			)
			return
		}
		if !m.marketingAdminEmails.Allows(record.User.Email) &&
			!m.settlementOperatorEmails.Allows(record.User.Email) {
			httpapi.WriteError(
				w, http.StatusForbidden, "OPERATOR_REQUIRED",
				"운영자 권한이 없습니다.",
			)
			return
		}
		if time.Since(record.Session.AuthenticatedAt) > window {
			httpapi.WriteError(
				w, http.StatusForbidden, "FRESH_AUTH_REQUIRED",
				"민감 조작에는 최근 Google 재인증이 필요합니다. fresh=1로 다시 로그인해 주세요.",
			)
			return
		}
		ctx := sharedapp.WithWebPrincipal(r.Context(), sharedapp.WebPrincipal{
			UserID:        string(record.User.ID),
			AuthSessionID: string(record.Session.ID),
		})
		if m.marketingAdminEmails.Allows(record.User.Email) || m.settlementOperatorEmails.Allows(record.User.Email) {
			ctx = sharedapp.WithoutAnalytics(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

package app

import (
	"context"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

type AuthorizationRequest struct {
	State         string
	Nonce         string
	PKCEChallenge string
	// ForceFresh asks the provider to re-authenticate the person even with
	// a live Google session (max_age=0), for operator fresh auth.
	ForceFresh bool
}

type CodeExchangeRequest struct {
	Code         string
	PKCEVerifier string
}

type IdentityProvider interface {
	AuthorizationURL(AuthorizationRequest) (string, error)
	ExchangeAndVerifyCode(context.Context, CodeExchangeRequest) (accountdomain.VerifiedIdentity, error)
}

type SecretGenerator interface {
	Generate(size int) ([]byte, error)
}

type ExternalLoginRecord struct {
	User    accountdomain.User
	Session accountdomain.AuthSession
}

// MobileSessionGrant carries only freshly generated, already-hashed session
// material into the repository. The repository derives the user, expiry and
// provider-authentication time from the consumed handoff in one transaction.
type MobileSessionGrant struct {
	SessionID accountdomain.AuthSessionID
	TokenHash []byte
	CreatedAt time.Time
}

type MobileAuthRepository interface {
	CreateMobileAuthHandoff(
		context.Context,
		accountdomain.MobileAuthHandoff,
		[]byte,
	) error
	ExchangeMobileAuthHandoff(
		context.Context,
		[]byte,
		[]byte,
		MobileSessionGrant,
	) (ExternalLoginRecord, error)
}

type AuthRepository interface {
	CreateLoginAttempt(context.Context, accountdomain.OAuthLoginAttempt) error
	ConsumeLoginAttempt(
		ctx context.Context,
		provider accountdomain.IdentityProvider,
		stateHash, browserBindingHash []byte,
		now time.Time,
	) (accountdomain.OAuthLoginAttempt, error)
	CompleteExternalLogin(
		context.Context,
		accountdomain.User,
		accountdomain.ExternalIdentity,
		accountdomain.AuthSession,
		[]byte,
	) (ExternalLoginRecord, error)
	CreateDevelopmentSession(
		context.Context,
		*accountdomain.UserID,
		accountdomain.User,
		accountdomain.AuthSession,
		[]byte,
	) (ExternalLoginRecord, error)
	ResetDevelopmentUser(
		context.Context,
		accountdomain.UserID,
		time.Time,
	) error
	FindActiveAuthSession(
		context.Context,
		[]byte,
		time.Time,
	) (ExternalLoginRecord, error)
	RevokeAuthSession(context.Context, []byte, time.Time) error
	RevokeAllAuthSessions(context.Context, accountdomain.UserID, time.Time) error
	RequestAccountDeletion(context.Context, accountdomain.UserID, time.Time) error
}

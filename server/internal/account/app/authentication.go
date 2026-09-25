package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	loginAttemptTTL  = 10 * time.Minute
	mobileHandoffTTL = 2 * time.Minute
	authSessionTTL   = 7 * 24 * time.Hour
	// Operator sessions expire faster than user sessions (ADR-0040 §10): an
	// abandoned operator browser is a PII-reveal surface, not a lost cart.
	operatorAuthSessionTTL = 12 * time.Hour
	secretSize             = 32
)

var (
	ErrProviderUnavailable = errors.New("AUTH_PROVIDER_UNAVAILABLE")
	ErrProviderFailed      = errors.New("AUTH_PROVIDER_FAILED")
	mobilePKCEVerifier     = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type AuthenticationService struct {
	repository AuthRepository
	provider   IdentityProvider
	secrets    SecretGenerator
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
	logger     *slog.Logger
	limiter    AttemptRateLimiter
	isOperator func(email string) bool
}

func (s *AuthenticationService) EnableRateLimiter(limiter AttemptRateLimiter) {
	s.limiter = limiter
}

// EnableOperatorSessionPolicy shortens the session TTL for allowlisted
// operator emails at Google login time. Development sessions keep the
// default TTL; production only issues Google sessions.
func (s *AuthenticationService) EnableOperatorSessionPolicy(
	isOperator func(email string) bool,
) {
	s.isOperator = isOperator
}

func (s *AuthenticationService) sessionTTL(email string) time.Duration {
	if s.isOperator != nil && s.isOperator(email) {
		return operatorAuthSessionTTL
	}
	return authSessionTTL
}

func NewAuthenticationService(
	repository AuthRepository,
	provider IdentityProvider,
	secrets SecretGenerator,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
) *AuthenticationService {
	return &AuthenticationService{
		repository: repository, provider: provider, secrets: secrets,
		clock: clock, ids: ids, logger: logger,
	}
}

type BeginGoogleLoginInput struct {
	ReturnPath string
	Origin     string
	Source     string
	// ForceFresh drives operator fresh auth (ADR-0040 §10): the provider
	// must re-verify the person even with a live Google session.
	ForceFresh bool
}

type BeginGoogleLoginResult struct {
	AuthorizationURL string
	BrowserBinding   string
	ExpiresAt        time.Time
}

func (s *AuthenticationService) BeginGoogleLogin(
	ctx context.Context,
	input BeginGoogleLoginInput,
) (BeginGoogleLoginResult, error) {
	if s.provider == nil {
		return BeginGoogleLoginResult{}, ErrProviderUnavailable
	}
	returnPath, err := accountdomain.NormalizeReturnPath(input.ReturnPath)
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	if err := enforceRateLimit(
		ctx, s.limiter,
		googleLoginBeginRateLimitRules(input.Origin, input.Source),
		s.clock.Now(),
	); err != nil {
		return BeginGoogleLoginResult{}, err
	}
	state, err := s.generateToken()
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	binding, err := s.generateToken()
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	nonce, err := s.generateToken()
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	verifier, err := s.generateToken()
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	now := s.clock.Now()
	attempt, err := accountdomain.NewOAuthLoginAttempt(
		accountdomain.OAuthLoginAttemptID(s.ids.NewID()),
		accountdomain.IdentityProviderGoogle,
		hashToken(state), hashToken(binding), hashToken(nonce),
		verifier, returnPath, now, loginAttemptTTL,
	)
	if err != nil {
		return BeginGoogleLoginResult{}, err
	}
	if err := s.repository.CreateLoginAttempt(ctx, attempt); err != nil {
		return BeginGoogleLoginResult{}, err
	}
	challengeSum := sha256.Sum256([]byte(verifier))
	authorizationURL, err := s.provider.AuthorizationURL(AuthorizationRequest{
		State: state, Nonce: nonce,
		PKCEChallenge: base64.RawURLEncoding.EncodeToString(challengeSum[:]),
		ForceFresh:    input.ForceFresh,
	})
	if err != nil {
		return BeginGoogleLoginResult{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return BeginGoogleLoginResult{
		AuthorizationURL: authorizationURL,
		BrowserBinding:   binding,
		ExpiresAt:        attempt.ExpiresAt,
	}, nil
}

type CompleteGoogleLoginInput struct {
	Code                 string
	State                string
	Issuer               string
	ProviderError        string
	BrowserBinding       string
	PreviousSessionToken string
	Origin               string
	Source               string
}

type CompleteGoogleLoginResult struct {
	User         accountdomain.User
	SessionToken string
	ExpiresAt    time.Time
	ReturnPath   string
}

func (s *AuthenticationService) CompleteGoogleLogin(
	ctx context.Context,
	input CompleteGoogleLoginInput,
) (result CompleteGoogleLoginResult, err error) {
	reason := "INTERNAL_ERROR"
	defer func() {
		if err == nil {
			return
		}
		reason = AuthenticationReasonCode(err)
		s.logger.WarnContext(ctx, "login failed",
			"event", "auth.login_failed", "result", "failed",
			"reason_code", reason, "provider", accountdomain.IdentityProviderGoogle,
			"request_id", sharedapp.RequestID(ctx))
	}()

	if s.provider == nil {
		return result, ErrProviderUnavailable
	}
	if strings.TrimSpace(input.State) == "" ||
		strings.TrimSpace(input.BrowserBinding) == "" {
		return result, accountdomain.ErrLoginAttemptInvalid
	}
	if input.Issuer != "" && input.Issuer != "https://accounts.google.com" {
		return result, accountdomain.ErrLoginAttemptInvalid
	}
	now := s.clock.Now()
	if err := enforceRateLimit(
		ctx, s.limiter,
		googleLoginCompleteRateLimitRules(
			input.BrowserBinding, input.Origin, input.Source,
		),
		now,
	); err != nil {
		return result, err
	}
	attempt, err := s.repository.ConsumeLoginAttempt(
		ctx, accountdomain.IdentityProviderGoogle,
		hashToken(input.State), hashToken(input.BrowserBinding), now,
	)
	if err != nil {
		return result, err
	}
	// Preserve the validated, server-owned return path on errors that happen
	// after the attempt is consumed. The HTTP layer uses this only to return a
	// native browser flow to its allowlisted app URI.
	result.ReturnPath = attempt.ReturnPath
	if strings.TrimSpace(input.ProviderError) != "" {
		return result, ErrProviderFailed
	}
	if strings.TrimSpace(input.Code) == "" ||
		input.Issuer != "https://accounts.google.com" {
		return result, accountdomain.ErrLoginAttemptInvalid
	}
	verified, err := s.provider.ExchangeAndVerifyCode(ctx, CodeExchangeRequest{
		Code: input.Code, PKCEVerifier: attempt.PKCEVerifier,
	})
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	if err := verified.Validate(); err != nil {
		return result, err
	}
	nonceHash := hashToken(verified.Nonce)
	if len(nonceHash) != len(attempt.NonceHash) ||
		subtle.ConstantTimeCompare(nonceHash, attempt.NonceHash) != 1 {
		return result, accountdomain.ErrIdentityInvalid
	}
	sessionToken, err := s.generateToken()
	if err != nil {
		return result, err
	}
	user := accountdomain.NewExternalUser(
		accountdomain.UserID(s.ids.NewID()), verified.Email, verified.DisplayName, now,
	)
	identity, err := accountdomain.NewExternalIdentity(
		accountdomain.ExternalIdentityID(s.ids.NewID()), user.ID, verified, now,
	)
	if err != nil {
		return result, err
	}
	session, err := accountdomain.NewAuthSession(
		accountdomain.AuthSessionID(s.ids.NewID()), user.ID,
		hashToken(sessionToken), now, s.sessionTTL(verified.Email),
	)
	if err != nil {
		return result, err
	}
	if !verified.AuthTime.IsZero() {
		session.AuthenticatedAt = verified.AuthTime
	}
	var previousHash []byte
	if input.PreviousSessionToken != "" {
		previousHash = hashToken(input.PreviousSessionToken)
	}
	record, err := s.repository.CompleteExternalLogin(
		ctx, user, identity, session, previousHash,
	)
	if err != nil {
		return result, err
	}
	s.logger.InfoContext(ctx, "login completed",
		"event", "auth.login_completed", "result", "success",
		"provider", accountdomain.IdentityProviderGoogle,
		"user_id", record.User.ID, "request_id", sharedapp.RequestID(ctx))
	return CompleteGoogleLoginResult{
		User: record.User, SessionToken: sessionToken,
		ExpiresAt: record.Session.ExpiresAt, ReturnPath: attempt.ReturnPath,
	}, nil
}

type CreateMobileHandoffResult struct {
	Code      string
	ExpiresAt time.Time
}

// CreateMobileHandoff rotates the short-lived browser session into an opaque,
// one-time native exchange code. The source session is revoked atomically by
// the repository, so an abandoned auth browser does not leave a second active
// login behind.
func (s *AuthenticationService) CreateMobileHandoff(
	ctx context.Context,
	sessionToken string,
	verifierChallenge []byte,
) (CreateMobileHandoffResult, error) {
	repository, ok := s.repository.(MobileAuthRepository)
	if !ok || len(verifierChallenge) != sha256.Size {
		return CreateMobileHandoffResult{}, accountdomain.ErrMobileAuthInvalid
	}
	record, err := s.AuthenticateSessionRecord(ctx, sessionToken)
	if err != nil {
		return CreateMobileHandoffResult{}, err
	}
	code, err := s.generateToken()
	if err != nil {
		return CreateMobileHandoffResult{}, err
	}
	now := s.clock.Now()
	handoff, err := accountdomain.NewMobileAuthHandoff(
		s.ids.NewID(), record.User.ID, hashToken(code), verifierChallenge,
		record.Session.ExpiresAt, record.Session.AuthenticatedAt,
		now, mobileHandoffTTL,
	)
	if err != nil {
		return CreateMobileHandoffResult{}, err
	}
	if err := repository.CreateMobileAuthHandoff(
		ctx, handoff, hashToken(sessionToken),
	); err != nil {
		return CreateMobileHandoffResult{}, err
	}
	return CreateMobileHandoffResult{Code: code, ExpiresAt: handoff.ExpiresAt}, nil
}

type ExchangeMobileHandoffResult struct {
	User         accountdomain.User
	SessionToken string
	ExpiresAt    time.Time
}

func (s *AuthenticationService) ExchangeMobileHandoff(
	ctx context.Context,
	code, verifier string,
) (ExchangeMobileHandoffResult, error) {
	repository, ok := s.repository.(MobileAuthRepository)
	code = strings.TrimSpace(code)
	if !ok || code == "" || !mobilePKCEVerifier.MatchString(verifier) {
		return ExchangeMobileHandoffResult{}, accountdomain.ErrMobileAuthInvalid
	}
	decodedCode, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil || len(decodedCode) != secretSize {
		return ExchangeMobileHandoffResult{}, accountdomain.ErrMobileAuthInvalid
	}
	sessionToken, err := s.generateToken()
	if err != nil {
		return ExchangeMobileHandoffResult{}, err
	}
	now := s.clock.Now()
	challenge := sha256.Sum256([]byte(verifier))
	record, err := repository.ExchangeMobileAuthHandoff(
		ctx, hashToken(code), challenge[:], MobileSessionGrant{
			SessionID: accountdomain.AuthSessionID(s.ids.NewID()),
			TokenHash: hashToken(sessionToken), CreatedAt: now,
		},
	)
	if err != nil {
		return ExchangeMobileHandoffResult{}, err
	}
	return ExchangeMobileHandoffResult{
		User: record.User, SessionToken: sessionToken,
		ExpiresAt: record.Session.ExpiresAt,
	}, nil
}

type DevelopmentSessionInput struct {
	UserID               string
	PreviousSessionToken string
}

func (s *AuthenticationService) CreateDevelopmentSession(
	ctx context.Context,
	input DevelopmentSessionInput,
) (CompleteGoogleLoginResult, error) {
	now := s.clock.Now()
	sessionToken, err := s.generateToken()
	if err != nil {
		return CompleteGoogleLoginResult{}, err
	}
	user := accountdomain.NewUser(accountdomain.UserID(s.ids.NewID()), now)
	user.DisplayName = "개발 사용자"
	session, err := accountdomain.NewAuthSession(
		accountdomain.AuthSessionID(s.ids.NewID()), user.ID,
		hashToken(sessionToken), now, authSessionTTL,
	)
	if err != nil {
		return CompleteGoogleLoginResult{}, err
	}
	var requestedID *accountdomain.UserID
	if strings.TrimSpace(input.UserID) != "" {
		value := accountdomain.UserID(strings.TrimSpace(input.UserID))
		requestedID = &value
	}
	var previousHash []byte
	if input.PreviousSessionToken != "" {
		previousHash = hashToken(input.PreviousSessionToken)
	}
	record, err := s.repository.CreateDevelopmentSession(
		ctx, requestedID, user, session, previousHash,
	)
	if err != nil {
		return CompleteGoogleLoginResult{}, err
	}
	return CompleteGoogleLoginResult{
		User: record.User, SessionToken: sessionToken,
		ExpiresAt: record.Session.ExpiresAt, ReturnPath: "/",
	}, nil
}

func (s *AuthenticationService) ResetDevelopmentUser(
	ctx context.Context,
	userID string,
) error {
	value := accountdomain.UserID(strings.TrimSpace(userID))
	if value == "" {
		return accountdomain.ErrSessionMissing
	}
	return s.repository.ResetDevelopmentUser(ctx, value, s.clock.Now())
}

func (s *AuthenticationService) AuthenticateSession(
	ctx context.Context,
	token string,
) (accountdomain.User, error) {
	record, err := s.AuthenticateSessionRecord(ctx, token)
	if err != nil {
		return accountdomain.User{}, err
	}
	return record.User, nil
}

func (s *AuthenticationService) AuthenticateSessionRecord(
	ctx context.Context,
	token string,
) (ExternalLoginRecord, error) {
	if strings.TrimSpace(token) == "" {
		return ExternalLoginRecord{}, accountdomain.ErrSessionMissing
	}
	record, err := s.repository.FindActiveAuthSession(ctx, hashToken(token), s.clock.Now())
	if err != nil {
		return ExternalLoginRecord{}, err
	}
	return record, nil
}

func (s *AuthenticationService) LogoutCurrentSession(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.repository.RevokeAuthSession(ctx, hashToken(token), s.clock.Now())
}

func (s *AuthenticationService) LogoutAllSessions(ctx context.Context, token string) error {
	user, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return err
	}
	return s.repository.RevokeAllAuthSessions(ctx, user.ID, s.clock.Now())
}

func (s *AuthenticationService) RequestAccountDeletion(
	ctx context.Context,
	token string,
) error {
	user, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return err
	}
	return s.repository.RequestAccountDeletion(ctx, user.ID, s.clock.Now())
}

func (s *AuthenticationService) generateToken() (string, error) {
	value, err := s.secrets.Generate(secretSize)
	if err != nil {
		return "", fmt.Errorf("generate authentication secret: %w", err)
	}
	if len(value) < secretSize {
		return "", errors.New("authentication secret generator returned too few bytes")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func AuthenticationReasonCode(err error) string {
	if failure, ok := fault.As(err); ok {
		if failure.Reason != "" {
			return failure.Reason
		}
		return string(failure.Code)
	}
	switch {
	case errors.Is(err, ErrRateLimited):
		return ErrRateLimited.Error()
	case errors.Is(err, accountdomain.ErrLoginAttemptExpired):
		return accountdomain.ErrLoginAttemptExpired.Error()
	case errors.Is(err, accountdomain.ErrLoginAttemptInvalid),
		errors.Is(err, accountdomain.ErrLoginAttemptConsumed),
		errors.Is(err, accountdomain.ErrReturnPathInvalid):
		return accountdomain.ErrLoginAttemptInvalid.Error()
	case errors.Is(err, accountdomain.ErrMobileAuthExpired):
		return accountdomain.ErrMobileAuthExpired.Error()
	case errors.Is(err, accountdomain.ErrMobileAuthInvalid),
		errors.Is(err, accountdomain.ErrMobileAuthConsumed):
		return accountdomain.ErrMobileAuthInvalid.Error()
	case errors.Is(err, accountdomain.ErrSessionMissing),
		errors.Is(err, accountdomain.ErrSessionExpired),
		errors.Is(err, accountdomain.ErrSessionRevoked):
		return accountdomain.ErrSessionMissing.Error()
	case errors.Is(err, accountdomain.ErrIdentityInvalid),
		errors.Is(err, accountdomain.ErrEmailNotVerified):
		return accountdomain.ErrIdentityInvalid.Error()
	case errors.Is(err, ErrProviderFailed), errors.Is(err, ErrProviderUnavailable):
		return ErrProviderFailed.Error()
	default:
		return "INTERNAL_ERROR"
	}
}

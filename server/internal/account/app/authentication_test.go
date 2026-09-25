package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

type authSecrets struct {
	mu    sync.Mutex
	index byte
}

type authClock struct {
	now time.Time
}

func (c authClock) Now() time.Time { return c.now }

func (g *authSecrets) Generate(size int) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.index++
	return bytes.Repeat([]byte{g.index}, size), nil
}

type authIDs struct {
	mu    sync.Mutex
	index int
}

type authRateLimiter struct {
	deny  bool
	calls [][]RateLimitRule
}

func (l *authRateLimiter) Acquire(
	_ context.Context,
	rules []RateLimitRule,
	_ time.Time,
) (RateLimitDecision, error) {
	l.calls = append(l.calls, append([]RateLimitRule(nil), rules...))
	if l.deny {
		return RateLimitDecision{
			Allowed: false, Policy: rules[0].Policy,
			RetryAfter: 31 * time.Second,
		}, nil
	}
	return RateLimitDecision{Allowed: true}, nil
}

func (g *authIDs) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.index++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", g.index)
}

type fakeIdentityProvider struct {
	nonce      string
	subject    string
	authTime   time.Time
	forceFresh bool
}

func (p *fakeIdentityProvider) AuthorizationURL(input AuthorizationRequest) (string, error) {
	p.nonce = input.Nonce
	p.forceFresh = input.ForceFresh
	return "https://provider.example/authorize?state=" + input.State, nil
}

func (p *fakeIdentityProvider) ExchangeAndVerifyCode(
	_ context.Context,
	input CodeExchangeRequest,
) (accountdomain.VerifiedIdentity, error) {
	if input.Code != "valid-code" || input.PKCEVerifier == "" {
		return accountdomain.VerifiedIdentity{}, errors.New("invalid provider code")
	}
	return accountdomain.VerifiedIdentity{
		Provider: accountdomain.IdentityProviderGoogle,
		Subject:  p.subject, Email: "person@example.com",
		EmailVerified: true, DisplayName: "Person", Nonce: p.nonce,
		AuthTime: p.authTime,
	}, nil
}

type memoryAuthRepository struct {
	mu         sync.Mutex
	attempts   []accountdomain.OAuthLoginAttempt
	identities map[string]accountdomain.User
	sessions   map[string]ExternalLoginRecord
	handoffs   map[string]accountdomain.MobileAuthHandoff
}

func newMemoryAuthRepository() *memoryAuthRepository {
	return &memoryAuthRepository{
		identities: map[string]accountdomain.User{},
		sessions:   map[string]ExternalLoginRecord{},
		handoffs:   map[string]accountdomain.MobileAuthHandoff{},
	}
}

func (r *memoryAuthRepository) CreateLoginAttempt(
	_ context.Context, attempt accountdomain.OAuthLoginAttempt,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, attempt)
	return nil
}

func (r *memoryAuthRepository) ConsumeLoginAttempt(
	_ context.Context,
	_ accountdomain.IdentityProvider,
	stateHash, bindingHash []byte,
	now time.Time,
) (accountdomain.OAuthLoginAttempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.attempts {
		attempt := &r.attempts[index]
		if bytes.Equal(attempt.StateHash, stateHash) &&
			bytes.Equal(attempt.BrowserBindingHash, bindingHash) {
			if err := attempt.CanConsume(now); err != nil {
				return accountdomain.OAuthLoginAttempt{}, err
			}
			consumed := *attempt
			consumed.ConsumedAt = &now
			attempt.ConsumedAt = &now
			attempt.PKCEVerifier = ""
			attempt.SecretCleanedAt = &now
			return consumed, nil
		}
	}
	return accountdomain.OAuthLoginAttempt{}, accountdomain.ErrLoginAttemptInvalid
}

func TestAuthenticationRateLimitRejectsBeforeSecretCreationAndConsumption(
	t *testing.T,
) {
	now := time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{subject: "rate-limited-subject"}
	service := NewAuthenticationService(
		repository, provider, &authSecrets{}, authClock{now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	limiter := &authRateLimiter{deny: true}
	service.EnableRateLimiter(limiter)
	_, err := service.BeginGoogleLogin(
		context.Background(),
		BeginGoogleLoginInput{
			ReturnPath: "/", Origin: "https://vitlane.example",
			Source: "203.0.113.9",
		},
	)
	if !errors.Is(err, ErrRateLimited) || len(repository.attempts) != 0 {
		t.Fatalf("denied begin err=%v attempts=%d", err, len(repository.attempts))
	}

	limiter.deny = false
	begin, err := service.BeginGoogleLogin(
		context.Background(),
		BeginGoogleLoginInput{
			ReturnPath: "/", Origin: "https://vitlane.example",
			Source: "203.0.113.9",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := strings.TrimPrefix(
		begin.AuthorizationURL, "https://provider.example/authorize?state=",
	)
	limiter.deny = true
	_, err = service.CompleteGoogleLogin(
		context.Background(),
		CompleteGoogleLoginInput{
			Code: "valid-code", State: state,
			Issuer:         "https://accounts.google.com",
			BrowserBinding: begin.BrowserBinding,
			Origin:         "https://vitlane.example", Source: "203.0.113.9",
		},
	)
	if !errors.Is(err, ErrRateLimited) ||
		repository.attempts[0].ConsumedAt != nil {
		t.Fatalf("denied completion consumed attempt: err=%v attempt=%#v",
			err, repository.attempts[0])
	}
	limiter.deny = false
	if _, err := service.CompleteGoogleLogin(
		context.Background(),
		CompleteGoogleLoginInput{
			Code: "valid-code", State: state,
			Issuer:         "https://accounts.google.com",
			BrowserBinding: begin.BrowserBinding,
			Origin:         "https://vitlane.example", Source: "203.0.113.9",
		},
	); err != nil {
		t.Fatal(err)
	}
	if repository.attempts[0].PKCEVerifier != "" ||
		repository.attempts[0].SecretCleanedAt == nil {
		t.Fatalf("consumed login secret retained: %#v", repository.attempts[0])
	}
}

func (r *memoryAuthRepository) CompleteExternalLogin(
	_ context.Context,
	user accountdomain.User,
	identity accountdomain.ExternalIdentity,
	session accountdomain.AuthSession,
	previousHash []byte,
) (ExternalLoginRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(identity.Provider) + ":" + identity.ProviderSubject
	if existing, ok := r.identities[key]; ok {
		user = existing
		user.Email = identity.EmailSnapshot
		user.DisplayName = identity.DisplayNameSnapshot
	} else {
		r.identities[key] = user
	}
	session.UserID = user.ID
	if len(previousHash) > 0 {
		delete(r.sessions, string(previousHash))
	}
	record := ExternalLoginRecord{User: user, Session: session}
	r.sessions[string(session.TokenHash)] = record
	return record, nil
}

func (r *memoryAuthRepository) CreateDevelopmentSession(
	_ context.Context,
	requestedID *accountdomain.UserID,
	user accountdomain.User,
	session accountdomain.AuthSession,
	previousHash []byte,
) (ExternalLoginRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if requestedID != nil {
		for _, known := range r.identities {
			if known.ID == *requestedID {
				user = known
				break
			}
		}
	}
	session.UserID = user.ID
	if len(previousHash) > 0 {
		delete(r.sessions, string(previousHash))
	}
	record := ExternalLoginRecord{User: user, Session: session}
	r.sessions[string(session.TokenHash)] = record
	return record, nil
}

func (r *memoryAuthRepository) ResetDevelopmentUser(
	_ context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, record := range r.sessions {
		if record.User.ID != userID {
			continue
		}
		record.Session.RevokedAt = &now
		r.sessions[key] = record
	}
	return nil
}

func (r *memoryAuthRepository) FindActiveAuthSession(
	_ context.Context, tokenHash []byte, now time.Time,
) (ExternalLoginRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.sessions[string(tokenHash)]
	if !ok || record.Session.Active(now) != nil {
		return ExternalLoginRecord{}, accountdomain.ErrSessionMissing
	}
	return record, nil
}

func (r *memoryAuthRepository) RevokeAuthSession(
	_ context.Context, tokenHash []byte, now time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.sessions[string(tokenHash)]
	if ok {
		record.Session.RevokedAt = &now
		r.sessions[string(tokenHash)] = record
	}
	return nil
}

func (r *memoryAuthRepository) RevokeAllAuthSessions(
	_ context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, record := range r.sessions {
		if record.User.ID != userID {
			continue
		}
		record.Session.RevokedAt = &now
		r.sessions[key] = record
	}
	return nil
}

func (r *memoryAuthRepository) RequestAccountDeletion(
	ctx context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	return r.RevokeAllAuthSessions(ctx, userID, now)
}

func (r *memoryAuthRepository) CreateMobileAuthHandoff(
	_ context.Context,
	handoff accountdomain.MobileAuthHandoff,
	sourceTokenHash []byte,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.sessions[string(sourceTokenHash)]
	if !ok || record.Session.Active(handoff.CreatedAt) != nil ||
		record.User.ID != handoff.UserID {
		return accountdomain.ErrSessionMissing
	}
	now := handoff.CreatedAt
	record.Session.RevokedAt = &now
	r.sessions[string(sourceTokenHash)] = record
	r.handoffs[string(handoff.CodeHash)] = handoff
	return nil
}

func (r *memoryAuthRepository) ExchangeMobileAuthHandoff(
	_ context.Context,
	codeHash, verifierChallenge []byte,
	grant MobileSessionGrant,
) (ExternalLoginRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	handoff, ok := r.handoffs[string(codeHash)]
	if !ok {
		return ExternalLoginRecord{}, accountdomain.ErrMobileAuthInvalid
	}
	if err := handoff.CanConsume(grant.CreatedAt, verifierChallenge); err != nil {
		return ExternalLoginRecord{}, err
	}
	var user accountdomain.User
	for _, record := range r.sessions {
		if record.User.ID == handoff.UserID {
			user = record.User
			break
		}
	}
	if user.ID == "" {
		return ExternalLoginRecord{}, accountdomain.ErrMobileAuthInvalid
	}
	consumedAt := grant.CreatedAt
	handoff.ConsumedAt = &consumedAt
	r.handoffs[string(codeHash)] = handoff
	session := accountdomain.AuthSession{
		ID: grant.SessionID, UserID: user.ID,
		TokenHash:       append([]byte(nil), grant.TokenHash...),
		ExpiresAt:       handoff.SessionExpiresAt,
		AuthenticatedAt: handoff.AuthenticatedAt,
		CreatedAt:       grant.CreatedAt,
	}
	record := ExternalLoginRecord{User: user, Session: session}
	r.sessions[string(session.TokenHash)] = record
	return record, nil
}

func TestMobileSessionHandoffRotatesBrowserSessionAndRejectsReplay(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{subject: "mobile-subject", authTime: now.Add(-time.Minute)}
	service := NewAuthenticationService(
		repository, provider, &authSecrets{}, authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	browser := completeFakeLogin(t, service, provider)
	verifier := tokenForByte(91)
	challenge := sha256.Sum256([]byte(verifier))
	handoff, err := service.CreateMobileHandoff(
		context.Background(), browser.SessionToken, challenge[:],
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateSession(
		context.Background(), browser.SessionToken,
	); !errors.Is(err, accountdomain.ErrSessionMissing) {
		t.Fatalf("browser session remained active after handoff: %v", err)
	}
	if _, err := service.ExchangeMobileHandoff(
		context.Background(), handoff.Code, "wrong-verifier",
	); !errors.Is(err, accountdomain.ErrMobileAuthInvalid) {
		t.Fatalf("wrong verifier accepted: %v", err)
	}
	exchanged, err := service.ExchangeMobileHandoff(
		context.Background(), handoff.Code, verifier,
	)
	if err != nil {
		t.Fatal(err)
	}
	if exchanged.User.ID != browser.User.ID || exchanged.SessionToken == "" ||
		!exchanged.ExpiresAt.Equal(browser.ExpiresAt) {
		t.Fatalf("unexpected native session: %#v", exchanged)
	}
	if _, err := service.AuthenticateSession(
		context.Background(), exchanged.SessionToken,
	); err != nil {
		t.Fatalf("native bearer session is not active: %v", err)
	}
	if _, err := service.ExchangeMobileHandoff(
		context.Background(), handoff.Code, verifier,
	); !errors.Is(err, accountdomain.ErrMobileAuthConsumed) {
		t.Fatalf("handoff replay accepted: %v", err)
	}
}

func TestGoogleLoginSessionLifecycleAndReplayProtection(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{subject: "stable-google-subject"}
	service := NewAuthenticationService(
		repository, provider, &authSecrets{}, authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	begin, err := service.BeginGoogleLogin(context.Background(), BeginGoogleLoginInput{
		ReturnPath: "/plans/plan-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if begin.AuthorizationURL == "" || begin.BrowserBinding == "" {
		t.Fatalf("incomplete login start: %#v", begin)
	}
	state := tokenForByte(1)
	completed, err := service.CompleteGoogleLogin(context.Background(), CompleteGoogleLoginInput{
		Code: "valid-code", State: state, Issuer: "https://accounts.google.com",
		BrowserBinding: begin.BrowserBinding,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.ReturnPath != "/plans/plan-1" {
		t.Fatalf("unexpected return path %q", completed.ReturnPath)
	}
	authenticated, err := service.AuthenticateSession(context.Background(), completed.SessionToken)
	if err != nil || authenticated.ID != completed.User.ID {
		t.Fatalf("authenticate user=%#v err=%v", authenticated, err)
	}
	if _, err := service.CompleteGoogleLogin(context.Background(), CompleteGoogleLoginInput{
		Code: "valid-code", State: state, Issuer: "https://accounts.google.com",
		BrowserBinding: begin.BrowserBinding,
	}); !errors.Is(err, accountdomain.ErrLoginAttemptConsumed) {
		t.Fatalf("callback replay should fail, got %v", err)
	}
	if err := service.LogoutCurrentSession(context.Background(), completed.SessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateSession(
		context.Background(), completed.SessionToken,
	); !errors.Is(err, accountdomain.ErrSessionMissing) {
		t.Fatalf("revoked session should fail, got %v", err)
	}
}

func TestRepeatedGoogleSubjectKeepsUser(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{subject: "same-subject"}
	secrets := &authSecrets{}
	service := NewAuthenticationService(
		repository, provider, secrets, authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	first := completeFakeLogin(t, service, provider)
	second := completeFakeLogin(t, service, provider)
	if first.User.ID != second.User.ID {
		t.Fatalf("same subject created two users: %s != %s", first.User.ID, second.User.ID)
	}
}

func TestProviderCancellationConsumesLoginAttempt(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	service := NewAuthenticationService(
		repository, &fakeIdentityProvider{subject: "subject"}, &authSecrets{},
		authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	begin, err := service.BeginGoogleLogin(context.Background(), BeginGoogleLoginInput{})
	if err != nil {
		t.Fatal(err)
	}
	input := CompleteGoogleLoginInput{
		State: tokenForByte(1), BrowserBinding: begin.BrowserBinding,
		ProviderError: "access_denied",
	}
	if _, err := service.CompleteGoogleLogin(
		context.Background(), input,
	); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("provider cancellation should be mapped: %v", err)
	}
	if _, err := service.CompleteGoogleLogin(
		context.Background(), input,
	); !errors.Is(err, accountdomain.ErrLoginAttemptConsumed) {
		t.Fatalf("cancelled attempt should not replay: %v", err)
	}
}

func completeFakeLogin(
	t *testing.T,
	service *AuthenticationService,
	provider *fakeIdentityProvider,
) CompleteGoogleLoginResult {
	t.Helper()
	begin, err := service.BeginGoogleLogin(context.Background(), BeginGoogleLoginInput{})
	if err != nil {
		t.Fatal(err)
	}
	repository := service.repository.(*memoryAuthRepository)
	attempt := repository.attempts[len(repository.attempts)-1]
	_ = provider
	var state string
	for value := byte(1); value < 32; value++ {
		candidate := tokenForByte(value)
		if bytes.Equal(hashToken(candidate), attempt.StateHash) {
			state = candidate
			break
		}
	}
	result, err := service.CompleteGoogleLogin(context.Background(), CompleteGoogleLoginInput{
		Code: "valid-code", State: state, Issuer: "https://accounts.google.com",
		BrowserBinding: begin.BrowserBinding,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func tokenForByte(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

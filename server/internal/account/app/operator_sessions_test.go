package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// ADR-0040 §10: allowlisted operator emails get the shorter session TTL at
// Google login; everyone else keeps the default seven days.
func TestOperatorSessionPolicyShortensGoogleSessionTTL(t *testing.T) {
	now := time.Date(2026, 8, 8, 1, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{subject: "operator-subject"}
	service := NewAuthenticationService(
		repository, provider, &authSecrets{}, authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	standard := completeFakeLogin(t, service, provider)
	if got := standard.ExpiresAt.Sub(now); got != authSessionTTL {
		t.Fatalf("default session ttl=%v", got)
	}

	service.EnableOperatorSessionPolicy(func(email string) bool {
		return email == "person@example.com"
	})
	operator := completeFakeLogin(t, service, provider)
	if got := operator.ExpiresAt.Sub(now); got != operatorAuthSessionTTL {
		t.Fatalf("operator session ttl=%v", got)
	}
}

type fakeOperatorSessionRepository struct {
	sessions     []ActiveSessionView
	revoked      map[string]int64
	audits       []OperatorActionAudit
	revokedCount int64
}

func (r *fakeOperatorSessionRepository) ListActiveSessions(
	context.Context, time.Time, int,
) ([]ActiveSessionView, error) {
	return r.sessions, nil
}

func (r *fakeOperatorSessionRepository) RevokeUserSessions(
	_ context.Context, userID string, _ time.Time,
) (int64, error) {
	if r.revoked == nil {
		r.revoked = map[string]int64{}
	}
	r.revoked[userID] = r.revokedCount
	return r.revokedCount, nil
}

func (r *fakeOperatorSessionRepository) InsertOperatorActionAudit(
	_ context.Context, audit OperatorActionAudit,
) error {
	r.audits = append(r.audits, audit)
	return nil
}

type noopTransactor struct{}

func (noopTransactor) WithinTransaction(
	ctx context.Context, fn func(context.Context) error,
) error {
	return fn(ctx)
}

func TestRevokeUserSessionsRequiresReasonAndAudits(t *testing.T) {
	repository := &fakeOperatorSessionRepository{revokedCount: 3}
	service := NewOperatorSessionService(
		repository, noopTransactor{},
		authClock{now: time.Date(2026, 8, 8, 2, 0, 0, 0, time.UTC)}, &authIDs{},
	)

	if _, err := service.RevokeUserSessions(
		context.Background(), "operator-1", "subject-1", "short",
	); !errors.Is(err, ErrOperatorActionReasonInvalid) {
		t.Fatalf("short reason error=%v", err)
	}
	if len(repository.audits) != 0 {
		t.Fatalf("rejected action must not audit: %#v", repository.audits)
	}

	revoked, err := service.RevokeUserSessions(
		context.Background(), "operator-1", "subject-1",
		"account takeover suspected, cutting access",
	)
	if err != nil || revoked != 3 {
		t.Fatalf("revoked=%d err=%v", revoked, err)
	}
	if len(repository.audits) != 1 ||
		repository.audits[0].Action != "SESSION_REVOKE_ALL" ||
		repository.audits[0].SubjectUserID != "subject-1" ||
		repository.audits[0].OperatorUserID != "operator-1" {
		t.Fatalf("audit=%#v", repository.audits)
	}
}

// ADR-0040 §10: fresh=1 reaches the provider as a forced re-authentication
// and the provider-attested auth_time lands on the session.
func TestFreshLoginForcesProviderReauthentication(t *testing.T) {
	now := time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC)
	repository := newMemoryAuthRepository()
	provider := &fakeIdentityProvider{
		subject:  "fresh-subject",
		authTime: now.Add(-2 * time.Minute),
	}
	service := NewAuthenticationService(
		repository, provider, &authSecrets{}, authClock{now: now}, &authIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if _, err := service.BeginGoogleLogin(
		context.Background(), BeginGoogleLoginInput{ForceFresh: true},
	); err != nil {
		t.Fatal(err)
	}
	if !provider.forceFresh {
		t.Fatal("ForceFresh did not reach the identity provider")
	}

	completed := completeFakeLogin(t, service, provider)
	record, err := service.AuthenticateSessionRecord(
		context.Background(), completed.SessionToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Session.AuthenticatedAt.Equal(now.Add(-2 * time.Minute)) {
		t.Fatalf(
			"session authenticated_at=%v want provider auth_time",
			record.Session.AuthenticatedAt,
		)
	}
}

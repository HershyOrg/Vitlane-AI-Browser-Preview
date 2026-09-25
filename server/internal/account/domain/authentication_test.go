package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeReturnPath(t *testing.T) {
	valid, err := NormalizeReturnPath("/plans/plan-1?tab=scope")
	if err != nil || valid != "/plans/plan-1?tab=scope" {
		t.Fatalf("valid path=%q err=%v", valid, err)
	}
	for _, value := range []string{
		"https://evil.example/path", "//evil.example/path", "plans/relative",
	} {
		if _, err := NormalizeReturnPath(value); !errors.Is(err, ErrReturnPathInvalid) {
			t.Fatalf("path %q should be rejected, got %v", value, err)
		}
	}
}

func TestAuthSessionAbsoluteExpiryAndRevocation(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	session, err := NewAuthSession(
		"session-1", "user-1", make([]byte, 32), now, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Active(now.Add(59 * time.Minute)); err != nil {
		t.Fatalf("session should be active: %v", err)
	}
	if err := session.Active(now.Add(time.Hour)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("session should expire absolutely: %v", err)
	}
	revokedAt := now.Add(time.Minute)
	session.RevokedAt = &revokedAt
	if err := session.Active(now.Add(2 * time.Minute)); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("session should be revoked: %v", err)
	}
}

func TestVerifiedIdentityRequiresGoogleSubjectAndVerifiedEmail(t *testing.T) {
	identity := VerifiedIdentity{
		Provider: IdentityProviderGoogle, Subject: "google-subject",
		Email: "user@example.com", EmailVerified: true,
	}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	identity.EmailVerified = false
	if err := identity.Validate(); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("unverified email should fail: %v", err)
	}
}

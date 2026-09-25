package domain

import (
	"errors"
	"time"
)

var (
	ErrSessionInvalid = errors.New("AUTH_SESSION_INVALID")
	ErrSessionExpired = errors.New("AUTH_SESSION_EXPIRED")
	ErrSessionRevoked = errors.New("AUTH_SESSION_REVOKED")
	ErrSessionMissing = errors.New("AUTH_REQUIRED")
)

type AuthSessionID string

type AuthSession struct {
	ID        AuthSessionID
	UserID    UserID
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
	// AuthenticatedAt is when the identity provider last verified this
	// person, not when the session row was written.
	AuthenticatedAt time.Time
}

func NewAuthSession(
	id AuthSessionID,
	userID UserID,
	tokenHash []byte,
	now time.Time,
	ttl time.Duration,
) (AuthSession, error) {
	if id == "" || userID == "" || len(tokenHash) != 32 || ttl <= 0 {
		return AuthSession{}, ErrSessionInvalid
	}
	return AuthSession{
		ID: id, UserID: userID, TokenHash: append([]byte(nil), tokenHash...),
		ExpiresAt: now.Add(ttl), CreatedAt: now,
		// AuthenticatedAt starts at creation; a Google login overrides it
		// with the provider-attested auth_time (ADR-0040 §10).
		AuthenticatedAt: now,
	}, nil
}

func (s AuthSession) Active(now time.Time) error {
	if s.RevokedAt != nil {
		return ErrSessionRevoked
	}
	if !now.Before(s.ExpiresAt) {
		return ErrSessionExpired
	}
	return nil
}

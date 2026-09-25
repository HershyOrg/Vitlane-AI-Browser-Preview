package domain

import (
	"bytes"
	"errors"
	"strings"
	"time"
)

var (
	ErrMobileAuthInvalid  = errors.New("MOBILE_AUTH_INVALID")
	ErrMobileAuthExpired  = errors.New("MOBILE_AUTH_EXPIRED")
	ErrMobileAuthConsumed = errors.New("MOBILE_AUTH_CONSUMED")
)

// MobileAuthHandoff transfers a verified browser login to a native app without
// putting the long-lived session token in a redirect URL. Both secrets are
// stored as SHA-256 digests and the handoff can be consumed only once.
type MobileAuthHandoff struct {
	ID                string
	UserID            UserID
	CodeHash          []byte
	VerifierChallenge []byte
	SessionExpiresAt  time.Time
	AuthenticatedAt   time.Time
	ExpiresAt         time.Time
	ConsumedAt        *time.Time
	CreatedAt         time.Time
}

func NewMobileAuthHandoff(
	id string,
	userID UserID,
	codeHash, verifierChallenge []byte,
	sessionExpiresAt, authenticatedAt, now time.Time,
	ttl time.Duration,
) (MobileAuthHandoff, error) {
	if strings.TrimSpace(id) == "" || userID == "" ||
		len(codeHash) != 32 || len(verifierChallenge) != 32 || ttl <= 0 ||
		!now.Before(sessionExpiresAt) {
		return MobileAuthHandoff{}, ErrMobileAuthInvalid
	}
	expiresAt := now.Add(ttl)
	if sessionExpiresAt.Before(expiresAt) {
		expiresAt = sessionExpiresAt
	}
	return MobileAuthHandoff{
		ID: id, UserID: userID,
		CodeHash:          append([]byte(nil), codeHash...),
		VerifierChallenge: append([]byte(nil), verifierChallenge...),
		SessionExpiresAt:  sessionExpiresAt, AuthenticatedAt: authenticatedAt,
		ExpiresAt: expiresAt, CreatedAt: now,
	}, nil
}

func (h MobileAuthHandoff) CanConsume(
	now time.Time,
	verifierChallenge []byte,
) error {
	if h.ConsumedAt != nil {
		return ErrMobileAuthConsumed
	}
	if !now.Before(h.ExpiresAt) || !now.Before(h.SessionExpiresAt) {
		return ErrMobileAuthExpired
	}
	if len(verifierChallenge) != 32 ||
		!bytes.Equal(h.VerifierChallenge, verifierChallenge) {
		return ErrMobileAuthInvalid
	}
	return nil
}

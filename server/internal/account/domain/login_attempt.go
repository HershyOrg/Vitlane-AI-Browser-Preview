package domain

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

var (
	ErrLoginAttemptInvalid  = errors.New("AUTH_LOGIN_INVALID")
	ErrLoginAttemptExpired  = errors.New("AUTH_LOGIN_EXPIRED")
	ErrLoginAttemptConsumed = errors.New("AUTH_LOGIN_CONSUMED")
	ErrReturnPathInvalid    = errors.New("AUTH_RETURN_PATH_INVALID")
)

type OAuthLoginAttemptID string

type OAuthLoginAttempt struct {
	ID                 OAuthLoginAttemptID
	Provider           IdentityProvider
	StateHash          []byte
	BrowserBindingHash []byte
	NonceHash          []byte
	PKCEVerifier       string
	ReturnPath         string
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
	SecretCleanedAt    *time.Time
	CreatedAt          time.Time
}

func NewOAuthLoginAttempt(
	id OAuthLoginAttemptID,
	provider IdentityProvider,
	stateHash, browserBindingHash, nonceHash []byte,
	pkceVerifier, returnPath string,
	now time.Time,
	ttl time.Duration,
) (OAuthLoginAttempt, error) {
	if provider != IdentityProviderGoogle ||
		len(stateHash) != 32 || len(browserBindingHash) != 32 || len(nonceHash) != 32 ||
		strings.TrimSpace(pkceVerifier) == "" || ttl <= 0 {
		return OAuthLoginAttempt{}, ErrLoginAttemptInvalid
	}
	normalizedPath, err := NormalizeReturnPath(returnPath)
	if err != nil {
		return OAuthLoginAttempt{}, err
	}
	return OAuthLoginAttempt{
		ID:                 id,
		Provider:           provider,
		StateHash:          append([]byte(nil), stateHash...),
		BrowserBindingHash: append([]byte(nil), browserBindingHash...),
		NonceHash:          append([]byte(nil), nonceHash...),
		PKCEVerifier:       pkceVerifier,
		ReturnPath:         normalizedPath,
		ExpiresAt:          now.Add(ttl),
		CreatedAt:          now,
	}, nil
}

func (a OAuthLoginAttempt) CanConsume(now time.Time) error {
	if a.ConsumedAt != nil {
		return ErrLoginAttemptConsumed
	}
	if !now.Before(a.ExpiresAt) {
		return ErrLoginAttemptExpired
	}
	return nil
}

func NormalizeReturnPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" ||
		!strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(value, "//") {
		return "", ErrReturnPathInvalid
	}
	return parsed.RequestURI(), nil
}

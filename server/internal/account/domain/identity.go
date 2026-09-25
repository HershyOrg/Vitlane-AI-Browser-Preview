package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrIdentityInvalid       = errors.New("AUTH_IDENTITY_INVALID")
	ErrEmailNotVerified      = errors.New("AUTH_EMAIL_NOT_VERIFIED")
	ErrExternalIdentityFound = errors.New("EXTERNAL_IDENTITY_ALREADY_LINKED")
)

type ExternalIdentityID string
type IdentityProvider string

const IdentityProviderGoogle IdentityProvider = "GOOGLE"

type ExternalIdentity struct {
	ID                  ExternalIdentityID `json:"id"`
	UserID              UserID             `json:"userId"`
	Provider            IdentityProvider   `json:"provider"`
	ProviderSubject     string             `json:"-"`
	EmailSnapshot       string             `json:"email"`
	EmailVerified       bool               `json:"emailVerified"`
	DisplayNameSnapshot string             `json:"displayName"`
	CreatedAt           time.Time          `json:"createdAt"`
	UpdatedAt           time.Time          `json:"updatedAt"`
}

type VerifiedIdentity struct {
	Provider      IdentityProvider
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	Nonce         string
	// AuthTime is the provider-attested moment of authentication
	// (auth_time claim); zero when the provider did not supply it.
	AuthTime time.Time
}

func (v VerifiedIdentity) Validate() error {
	if v.Provider != IdentityProviderGoogle || strings.TrimSpace(v.Subject) == "" ||
		strings.TrimSpace(v.Email) == "" {
		return ErrIdentityInvalid
	}
	if !v.EmailVerified {
		return ErrEmailNotVerified
	}
	return nil
}

func NewExternalIdentity(
	id ExternalIdentityID,
	userID UserID,
	verified VerifiedIdentity,
	now time.Time,
) (ExternalIdentity, error) {
	if err := verified.Validate(); err != nil {
		return ExternalIdentity{}, err
	}
	return ExternalIdentity{
		ID:                  id,
		UserID:              userID,
		Provider:            verified.Provider,
		ProviderSubject:     strings.TrimSpace(verified.Subject),
		EmailSnapshot:       strings.TrimSpace(verified.Email),
		EmailVerified:       true,
		DisplayNameSnapshot: strings.TrimSpace(verified.DisplayName),
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil
}

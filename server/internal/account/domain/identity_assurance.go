package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrAssuranceInvalid = errors.New("IDENTITY_ASSURANCE_INVALID")
	ErrAssuranceExpired = errors.New("IDENTITY_ASSURANCE_EXPIRED")
)

type AssuranceLevel string

const (
	AssuranceDojangVerifiedAddress AssuranceLevel = "DOJANG_VERIFIED_ADDRESS"
	AssuranceDojangTestFaucet      AssuranceLevel = "DOJANG_TEST_FAUCET"
	AssuranceMockDojangVerified    AssuranceLevel = "MOCK_DOJANG_VERIFIED"
)

type IdentityAssuranceID string

// IdentityAssurance is KYC evidence. Wallet ownership is represented only by
// WalletOwnershipProof and must never be minted as an assurance.
type IdentityAssurance struct {
	ID           IdentityAssuranceID `json:"id"`
	WalletID     WalletID            `json:"walletId"`
	UserID       UserID              `json:"userId"`
	Level        AssuranceLevel      `json:"level"`
	IssuerRef    string              `json:"issuerRef"`
	SchemaRef    string              `json:"schemaRef"`
	EvidenceHash string              `json:"evidenceHash"`
	VerifiedAt   time.Time           `json:"verifiedAt"`
	ExpiresAt    time.Time           `json:"expiresAt"`
	RevokedAt    *time.Time          `json:"revokedAt,omitempty"`
	CreatedAt    time.Time           `json:"createdAt"`
}

func NewIdentityAssurance(
	id IdentityAssuranceID,
	walletID WalletID,
	userID UserID,
	level AssuranceLevel,
	issuerRef, schemaRef, evidenceHash string,
	now time.Time,
	lifetime time.Duration,
) (IdentityAssurance, error) {
	if id == "" || walletID == "" || userID == "" ||
		(level != AssuranceDojangVerifiedAddress &&
			level != AssuranceDojangTestFaucet &&
			level != AssuranceMockDojangVerified) ||
		strings.TrimSpace(issuerRef) == "" ||
		strings.TrimSpace(schemaRef) == "" ||
		strings.TrimSpace(evidenceHash) == "" ||
		lifetime <= 0 {
		return IdentityAssurance{}, ErrAssuranceInvalid
	}
	return IdentityAssurance{
		ID: id, WalletID: walletID, UserID: userID, Level: level,
		IssuerRef:    strings.TrimSpace(issuerRef),
		SchemaRef:    strings.TrimSpace(schemaRef),
		EvidenceHash: strings.TrimSpace(evidenceHash),
		VerifiedAt:   now, ExpiresAt: now.Add(lifetime), CreatedAt: now,
	}, nil
}

func (a IdentityAssurance) ValidAt(now time.Time) bool {
	return a.ID != "" && a.RevokedAt == nil &&
		!now.Before(a.VerifiedAt) && now.Before(a.ExpiresAt)
}

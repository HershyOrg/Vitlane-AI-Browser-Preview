package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrWalletRegistrationAttemptInvalid  = errors.New("WALLET_REGISTRATION_ATTEMPT_INVALID")
	ErrWalletRegistrationAttemptMissing  = errors.New("WALLET_REGISTRATION_ATTEMPT_NOT_FOUND")
	ErrWalletRegistrationAttemptExpired  = errors.New("WALLET_REGISTRATION_ATTEMPT_EXPIRED")
	ErrWalletRegistrationAttemptClosed   = errors.New("WALLET_REGISTRATION_ATTEMPT_CLOSED")
	ErrWalletRegistrationOperationReused = errors.New("WALLET_REGISTRATION_OPERATION_REUSED")
	ErrWalletOwnershipProofInvalid       = errors.New("WALLET_OWNERSHIP_PROOF_INVALID")
	ErrWalletOwnershipProofExpired       = errors.New("WALLET_OWNERSHIP_PROOF_EXPIRED")
	ErrWalletOwnershipProofNotFresh      = errors.New("WALLET_OWNERSHIP_PROOF_NOT_FRESH")
	ErrWalletOwnershipProofMissing       = errors.New("WALLET_OWNERSHIP_PROOF_NOT_FOUND")
	ErrWalletSignatureInvalid            = errors.New("WALLET_SIGNATURE_INVALID")
)

type WalletRegistrationAttemptID string
type WalletOwnershipProofID string
type WalletRegistrationAttemptStatus string
type WalletOwnershipStatus string
type WalletNextActionKind string

const (
	WalletRegistrationAttemptPending    WalletRegistrationAttemptStatus = "PENDING"
	WalletRegistrationAttemptCompleted  WalletRegistrationAttemptStatus = "COMPLETED"
	WalletRegistrationAttemptExpired    WalletRegistrationAttemptStatus = "EXPIRED"
	WalletRegistrationAttemptLocked     WalletRegistrationAttemptStatus = "LOCKED"
	WalletRegistrationAttemptCancelled  WalletRegistrationAttemptStatus = "CANCELLED"
	WalletRegistrationAttemptSuperseded WalletRegistrationAttemptStatus = "SUPERSEDED"

	WalletOwnershipValid          WalletOwnershipStatus = "VALID"
	WalletOwnershipReauthRequired WalletOwnershipStatus = "REAUTH_REQUIRED"
	WalletOwnershipRevoked        WalletOwnershipStatus = "REVOKED"

	WalletNextActionNone           WalletNextActionKind = "NONE"
	WalletNextActionRegister       WalletNextActionKind = "REGISTER"
	WalletNextActionReauthenticate WalletNextActionKind = "REAUTHENTICATE"
	WalletNextActionStartKYC       WalletNextActionKind = "START_KYC"
	WalletNextActionCheckResult    WalletNextActionKind = "CHECK_RESULT"
	WalletNextActionRecheck        WalletNextActionKind = "RECHECK"
	WalletNextActionContactSupport WalletNextActionKind = "CONTACT_SUPPORT"

	WalletOwnershipProofLifetime  = 24 * time.Hour
	WalletRegistrationAttemptTTL  = 10 * time.Minute
	WalletRegistrationMaxFailures = 5
)

type WalletRegistrationAttempt struct {
	ID                    WalletRegistrationAttemptID     `json:"id"`
	UserID                UserID                          `json:"userId"`
	Address               string                          `json:"address"`
	AddressKey            []byte                          `json:"-"`
	AccountID             string                          `json:"accountId"`
	ChainID               string                          `json:"chainId"`
	Origin                string                          `json:"origin"`
	Message               string                          `json:"message"`
	MessageHash           []byte                          `json:"-"`
	NonceHash             []byte                          `json:"-"`
	Status                WalletRegistrationAttemptStatus `json:"status"`
	FailureCount          int                             `json:"failureCount"`
	ClientOperationID     string                          `json:"clientOperationId"`
	RequestHash           string                          `json:"-"`
	CompletionOperationID string                          `json:"completionOperationId,omitempty"`
	CompletionRequestHash string                          `json:"-"`
	WalletID              *WalletID                       `json:"walletId,omitempty"`
	OwnershipProofID      *WalletOwnershipProofID         `json:"ownershipProofId,omitempty"`
	ExpiresAt             time.Time                       `json:"expiresAt"`
	CompletedAt           *time.Time                      `json:"completedAt,omitempty"`
	SecretCleanedAt       *time.Time                      `json:"-"`
	CreatedAt             time.Time                       `json:"createdAt"`
	UpdatedAt             time.Time                       `json:"updatedAt"`
}

func NewWalletRegistrationAttempt(
	id WalletRegistrationAttemptID,
	userID UserID,
	address, chainID, origin, message string,
	messageHash, nonceHash []byte,
	clientOperationID, requestHash string,
	now time.Time,
) (WalletRegistrationAttempt, error) {
	if id == "" || userID == "" || strings.TrimSpace(origin) == "" ||
		strings.TrimSpace(message) == "" || len(messageHash) == 0 ||
		len(nonceHash) == 0 || !validOperationID(clientOperationID) ||
		strings.TrimSpace(requestHash) == "" {
		return WalletRegistrationAttempt{}, ErrWalletRegistrationAttemptInvalid
	}
	checksummed, addressKey, err := CanonicalEVMAddress(address)
	if err != nil {
		return WalletRegistrationAttempt{}, err
	}
	chainID, err = CanonicalEVMChainID(chainID)
	if err != nil {
		return WalletRegistrationAttempt{}, err
	}
	return WalletRegistrationAttempt{
		ID: id, UserID: userID, Address: checksummed, AddressKey: addressKey,
		AccountID: chainID + ":" + checksummed, ChainID: chainID,
		Origin:  strings.TrimRight(strings.TrimSpace(origin), "/"),
		Message: message, MessageHash: append([]byte(nil), messageHash...),
		NonceHash:         append([]byte(nil), nonceHash...),
		Status:            WalletRegistrationAttemptPending,
		ClientOperationID: strings.TrimSpace(clientOperationID),
		RequestHash:       strings.TrimSpace(requestHash),
		ExpiresAt:         now.Add(WalletRegistrationAttemptTTL),
		CreatedAt:         now, UpdatedAt: now,
	}, nil
}

func (a WalletRegistrationAttempt) CanCompleteAt(now time.Time) error {
	if a.Status != WalletRegistrationAttemptPending {
		return ErrWalletRegistrationAttemptClosed
	}
	if !now.Before(a.ExpiresAt) {
		return ErrWalletRegistrationAttemptExpired
	}
	if a.FailureCount >= WalletRegistrationMaxFailures {
		return ErrWalletRegistrationAttemptClosed
	}
	return nil
}

type WalletOwnershipProof struct {
	ID                    WalletOwnershipProofID      `json:"id"`
	UserID                UserID                      `json:"userId"`
	WalletID              WalletID                    `json:"walletId"`
	RegistrationAttemptID WalletRegistrationAttemptID `json:"registrationAttemptId"`
	Address               string                      `json:"address"`
	AddressKey            []byte                      `json:"-"`
	AccountID             string                      `json:"accountId"`
	ChainID               string                      `json:"chainId"`
	Origin                string                      `json:"origin"`
	Method                string                      `json:"method"`
	MessageHash           string                      `json:"messageHash"`
	VerifiedAt            time.Time                   `json:"verifiedAt"`
	ValidUntil            time.Time                   `json:"validUntil"`
	RevokedAt             *time.Time                  `json:"revokedAt,omitempty"`
}

func NewWalletOwnershipProof(
	id WalletOwnershipProofID,
	walletID WalletID,
	attempt WalletRegistrationAttempt,
	messageHash string,
	now time.Time,
) (WalletOwnershipProof, error) {
	if id == "" || walletID == "" || attempt.ID == "" ||
		attempt.UserID == "" || strings.TrimSpace(messageHash) == "" ||
		attempt.Status != WalletRegistrationAttemptPending {
		return WalletOwnershipProof{}, ErrWalletOwnershipProofInvalid
	}
	return WalletOwnershipProof{
		ID: id, UserID: attempt.UserID, WalletID: walletID,
		RegistrationAttemptID: attempt.ID,
		Address:               attempt.Address, AddressKey: append([]byte(nil), attempt.AddressKey...),
		AccountID: attempt.AccountID, ChainID: attempt.ChainID,
		Origin: attempt.Origin, Method: "EIP191_PERSONAL_SIGN",
		MessageHash: messageHash, VerifiedAt: now,
		ValidUntil: now.Add(WalletOwnershipProofLifetime),
	}, nil
}

func (p WalletOwnershipProof) ValidAt(now time.Time) bool {
	return p.ID != "" && p.UserID != "" && p.WalletID != "" &&
		p.RevokedAt == nil && !now.Before(p.VerifiedAt) && now.Before(p.ValidUntil)
}

func (p WalletOwnershipProof) FreshAt(now time.Time, maximumAge time.Duration) bool {
	return maximumAge > 0 && p.ValidAt(now) &&
		now.Sub(p.VerifiedAt) <= maximumAge
}

type WalletOwnershipProjection struct {
	Status     WalletOwnershipStatus   `json:"status"`
	ProofID    *WalletOwnershipProofID `json:"proofId,omitempty"`
	VerifiedAt *time.Time              `json:"verifiedAt,omitempty"`
	ValidUntil *time.Time              `json:"validUntil,omitempty"`
	NextAction WalletNextAction        `json:"nextAction"`
}

type WalletNextAction struct {
	Kind      WalletNextActionKind `json:"kind"`
	URL       string               `json:"url,omitempty"`
	ExpiresAt *time.Time           `json:"expiresAt,omitempty"`
	Label     string               `json:"label,omitempty"`
}

func ProjectWalletOwnership(
	wallet Wallet,
	proof *WalletOwnershipProof,
	now time.Time,
) WalletOwnershipProjection {
	if !wallet.IsRegistered() {
		return WalletOwnershipProjection{
			Status: WalletOwnershipRevoked,
			NextAction: WalletNextAction{
				Kind: WalletNextActionRegister,
			},
		}
	}
	if proof == nil || wallet.CurrentOwnershipProofID == nil ||
		*wallet.CurrentOwnershipProofID != proof.ID || !proof.ValidAt(now) {
		return WalletOwnershipProjection{
			Status: WalletOwnershipReauthRequired,
			NextAction: WalletNextAction{
				Kind: WalletNextActionReauthenticate,
			},
		}
	}
	verifiedAt, validUntil := proof.VerifiedAt, proof.ValidUntil
	return WalletOwnershipProjection{
		Status: WalletOwnershipValid, ProofID: &proof.ID,
		VerifiedAt: &verifiedAt, ValidUntil: &validUntil,
		NextAction: WalletNextAction{Kind: WalletNextActionNone},
	}
}

func validOperationID(value string) bool {
	length := len(strings.TrimSpace(value))
	return length >= 8 && length <= 200
}

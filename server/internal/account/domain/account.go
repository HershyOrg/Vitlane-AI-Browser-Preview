package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrWalletAddressInvalid    = errors.New("WALLET_ADDRESS_INVALID")
	ErrChainIDInvalid          = errors.New("CHAIN_ID_INVALID")
	ErrWalletNotRegistered     = errors.New("WALLET_NOT_REGISTERED")
	ErrWalletAlreadyRegistered = errors.New("WALLET_ALREADY_REGISTERED")
	ErrBuyerProfileInvalid     = errors.New("BUYER_PROFILE_INVALID")
	ErrAccountStateInvalid     = errors.New("ACCOUNT_STATE_INVALID")
	walletAddressPattern       = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	evmChainIDPattern          = regexp.MustCompile(`^eip155:([1-9][0-9]*)$`)
)

type UserID string
type WalletID string
type WalletRegistrationStatus string

const (
	WalletRegistered   WalletRegistrationStatus = "REGISTERED"
	WalletDeregistered WalletRegistrationStatus = "DEREGISTERED"
)

type UserStatus string

const (
	UserStatusActive            UserStatus = "ACTIVE"
	UserStatusDeletionRequested UserStatus = "DELETION_REQUESTED"
)

type User struct {
	ID          UserID     `json:"id"`
	Status      UserStatus `json:"status"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func NewUser(id UserID, now time.Time) User {
	return User{ID: id, Status: UserStatusActive, CreatedAt: now, UpdatedAt: now}
}

func NewExternalUser(id UserID, email, displayName string, now time.Time) User {
	return User{
		ID: id, Status: UserStatusActive,
		Email: strings.TrimSpace(email), DisplayName: strings.TrimSpace(displayName),
		CreatedAt: now, UpdatedAt: now,
	}
}

type Wallet struct {
	ID                      WalletID                 `json:"id"`
	UserID                  UserID                   `json:"userId"`
	Address                 string                   `json:"address"`
	AddressKey              []byte                   `json:"-"`
	AccountID               string                   `json:"accountId"`
	ChainID                 string                   `json:"chainId"`
	RegistrationStatus      WalletRegistrationStatus `json:"registrationStatus"`
	CurrentOwnershipProofID *WalletOwnershipProofID  `json:"currentOwnershipProofId,omitempty"`
	IsDefault               bool                     `json:"isDefault"`
	RegisteredAt            time.Time                `json:"registeredAt"`
	DeregisteredAt          *time.Time               `json:"deregisteredAt,omitempty"`
	CreatedAt               time.Time                `json:"createdAt"`
	UpdatedAt               time.Time                `json:"updatedAt"`
}

const BuyerProfileKindTest = "TEST_PROFILE"

type BuyerProfileID string

type BuyerProfile struct {
	ID              BuyerProfileID `json:"id"`
	UserID          UserID         `json:"userId"`
	ProfileKind     string         `json:"profileKind"`
	Label           string         `json:"label"`
	FixtureKey      string         `json:"fixtureKey"`
	Country         string         `json:"country"`
	City            string         `json:"city"`
	Version         int64          `json:"version"`
	SnapshotHash    string         `json:"snapshotHash"`
	ContainsRealPII bool           `json:"containsRealPii"`
	IsDefault       bool           `json:"isDefault"`
	RetiredAt       *time.Time     `json:"retiredAt,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

func NewTestBuyerProfile(id BuyerProfileID, userID UserID, now time.Time) (BuyerProfile, error) {
	if id == "" || userID == "" {
		return BuyerProfile{}, ErrBuyerProfileInvalid
	}
	profile := BuyerProfile{
		ID: id, UserID: userID, ProfileKind: BuyerProfileKindTest,
		Label: "Vitlane TEST 배송 프로필", FixtureKey: "vitlane-test-us-v1",
		Country: "US", City: "Test City", Version: 1, ContainsRealPII: false,
		IsDefault: true, CreatedAt: now, UpdatedAt: now,
	}
	payload, err := json.Marshal(struct {
		Kind       string `json:"kind"`
		FixtureKey string `json:"fixtureKey"`
		Country    string `json:"country"`
		City       string `json:"city"`
		Version    int64  `json:"version"`
	}{
		profile.ProfileKind, profile.FixtureKey, profile.Country, profile.City, profile.Version,
	})
	if err != nil {
		return BuyerProfile{}, err
	}
	sum := sha256.Sum256(payload)
	profile.SnapshotHash = "0x" + hex.EncodeToString(sum[:])
	return profile, nil
}

type PolicyAcceptance struct {
	PolicyID      string    `json:"policyId"`
	PolicyVersion string    `json:"policyVersion"`
	AcceptedAt    time.Time `json:"acceptedAt"`
}

func NewRegisteredWallet(
	id WalletID,
	userID UserID,
	address, chainID string,
	now time.Time,
) (Wallet, error) {
	if id == "" || userID == "" {
		return Wallet{}, ErrAccountStateInvalid
	}
	checksummed, addressKey, err := CanonicalEVMAddress(address)
	if err != nil {
		return Wallet{}, err
	}
	chainID, err = CanonicalEVMChainID(chainID)
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{
		ID: id, UserID: userID, Address: checksummed,
		AddressKey: addressKey, AccountID: chainID + ":" + checksummed,
		ChainID: chainID, RegistrationStatus: WalletRegistered,
		RegisteredAt: now, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (w Wallet) IsRegistered() bool {
	return w.RegistrationStatus == WalletRegistered && w.DeregisteredAt == nil
}

func CanonicalEVMAddress(address string) (string, []byte, error) {
	address = strings.TrimSpace(address)
	if !walletAddressPattern.MatchString(address) || !common.IsHexAddress(address) {
		return "", nil, ErrWalletAddressInvalid
	}
	value := common.HexToAddress(address)
	key := append([]byte(nil), value.Bytes()...)
	if len(key) != common.AddressLength {
		return "", nil, ErrWalletAddressInvalid
	}
	return value.Hex(), key, nil
}

func CanonicalEVMChainID(chainID string) (string, error) {
	chainID = strings.TrimSpace(chainID)
	match := evmChainIDPattern.FindStringSubmatch(chainID)
	if len(match) != 2 {
		return "", ErrChainIDInvalid
	}
	value, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil || value == 0 {
		return "", ErrChainIDInvalid
	}
	return "eip155:" + strconv.FormatUint(value, 10), nil
}

package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type EncryptedPII struct {
	Ciphertext  []byte
	Nonce       []byte
	KeyVersion  string
	Fingerprint string
}

type PIICipher interface {
	Encrypt(context.Context, []byte, string) (EncryptedPII, error)
	Decrypt(context.Context, EncryptedPII, string) ([]byte, error)
}

type ShippingRepository interface {
	CreateShippingProfile(context.Context, accountdomain.ShippingProfile) (accountdomain.ShippingProfile, error)
	CreateOrderSheetShippingSnapshot(context.Context, accountdomain.ShippingSnapshot) (accountdomain.ShippingSnapshot, error)
	ListShippingProfiles(context.Context, string) ([]accountdomain.ShippingProfile, error)
	GetShippingProfile(context.Context, string, string) (accountdomain.ShippingProfile, error)
	RetireShippingProfile(context.Context, string, string, time.Time) error
	CreateDefaultShippingSnapshot(
		context.Context, string, string, string, time.Time,
	) (accountdomain.ShippingSnapshot, error)
	GetShippingSnapshot(context.Context, string) (accountdomain.ShippingSnapshot, error)
}

type ShippingService struct {
	repository ShippingRepository
	cipher     PIICipher
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewShippingService(
	repository ShippingRepository,
	cipher PIICipher,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *ShippingService {
	return &ShippingService{repository: repository, cipher: cipher, clock: clock, ids: ids}
}

type SaveShippingProfileInput struct {
	UserID  string
	Label   string
	Address accountdomain.ShippingAddress
}

func (s *ShippingService) SaveDefaultProfile(
	ctx context.Context,
	input SaveShippingProfileInput,
) (accountdomain.ShippingProfile, error) {
	address, err := input.Address.Normalize()
	if err != nil || strings.TrimSpace(input.UserID) == "" {
		return accountdomain.ShippingProfile{}, accountdomain.ErrShippingAddressInvalid
	}
	label := strings.TrimSpace(input.Label)
	if label == "" {
		label = "기본 배송 주소"
	}
	if len(label) > 80 {
		return accountdomain.ShippingProfile{}, accountdomain.ErrShippingAddressInvalid
	}
	payload, err := json.Marshal(address)
	if err != nil {
		return accountdomain.ShippingProfile{}, err
	}
	encrypted, err := s.cipher.Encrypt(ctx, payload, input.UserID)
	if err != nil {
		return accountdomain.ShippingProfile{}, err
	}
	now := s.clock.Now()
	return s.repository.CreateShippingProfile(ctx, accountdomain.ShippingProfile{
		ID: s.ids.NewID(), UserID: input.UserID,
		Label: label, Country: address.Country,
		MaskedSummary:    address.MaskedSummary(),
		EncryptedPayload: encrypted.Ciphertext, PayloadNonce: encrypted.Nonce,
		KeyVersion: encrypted.KeyVersion, PayloadHMAC: encrypted.Fingerprint,
		IsDefault: true,
		CreatedAt: now, UpdatedAt: now,
	})
}

func (s *ShippingService) ListProfiles(
	ctx context.Context,
	userID string,
) ([]accountdomain.ShippingProfile, error) {
	return s.repository.ListShippingProfiles(ctx, userID)
}

// DefaultAddress returns the current account profile only as an OrderSheet
// form default. It does not create a snapshot and therefore cannot make the
// account profile authoritative for an order.
func (s *ShippingService) DefaultAddress(
	ctx context.Context,
	userID string,
) (accountdomain.ShippingAddress, bool, error) {
	profiles, err := s.repository.ListShippingProfiles(ctx, strings.TrimSpace(userID))
	if err != nil {
		return accountdomain.ShippingAddress{}, false, err
	}
	for _, profile := range profiles {
		if !profile.IsDefault || profile.RetiredAt != nil {
			continue
		}
		address, revealErr := s.RevealProfile(ctx, userID, profile.ID)
		return address, revealErr == nil, revealErr
	}
	return accountdomain.ShippingAddress{}, false, nil
}

// CreateOrderSheetSnapshot stores the exact address entered in an
// OrderSheet without changing the user's account-level default profile.
func (s *ShippingService) CreateOrderSheetSnapshot(
	ctx context.Context,
	userID string,
	input accountdomain.ShippingAddress,
) (accountdomain.ShippingSnapshot, error) {
	address, err := input.Normalize()
	if err != nil || strings.TrimSpace(userID) == "" {
		return accountdomain.ShippingSnapshot{}, accountdomain.ErrShippingAddressInvalid
	}
	payload, err := json.Marshal(address)
	if err != nil {
		return accountdomain.ShippingSnapshot{}, err
	}
	encrypted, err := s.cipher.Encrypt(ctx, payload, userID)
	if err != nil {
		return accountdomain.ShippingSnapshot{}, err
	}
	now := s.clock.Now()
	return s.repository.CreateOrderSheetShippingSnapshot(ctx, accountdomain.ShippingSnapshot{
		ID: s.ids.NewID(), UserID: userID, ProfileVersion: 1,
		Country: address.Country, MaskedSummary: address.MaskedSummary(),
		EncryptedPayload: encrypted.Ciphertext, PayloadNonce: encrypted.Nonce,
		KeyVersion: encrypted.KeyVersion, SnapshotHMAC: encrypted.Fingerprint,
		CreatedAt: now,
	})
}

func (s *ShippingService) RetireProfile(
	ctx context.Context,
	userID, profileID string,
) error {
	return s.repository.RetireShippingProfile(ctx, userID, profileID, s.clock.Now())
}

func (s *ShippingService) RevealProfile(
	ctx context.Context,
	userID, profileID string,
) (accountdomain.ShippingAddress, error) {
	profile, err := s.repository.GetShippingProfile(ctx, userID, profileID)
	if err != nil {
		return accountdomain.ShippingAddress{}, err
	}
	return s.decryptAddress(ctx, EncryptedPII{
		Ciphertext:  profile.EncryptedPayload,
		Nonce:       profile.PayloadNonce,
		KeyVersion:  profile.KeyVersion,
		Fingerprint: profile.PayloadHMAC,
	}, profile.UserID)
}

func (s *ShippingService) CreateDefaultSnapshot(
	ctx context.Context,
	userID string,
) (accountdomain.ShippingSnapshot, error) {
	return s.repository.CreateDefaultShippingSnapshot(
		ctx, s.ids.NewID(), userID, "", s.clock.Now(),
	)
}

func (s *ShippingService) RevealSnapshot(
	ctx context.Context,
	snapshotID string,
) (accountdomain.ShippingAddress, error) {
	snapshot, err := s.repository.GetShippingSnapshot(ctx, snapshotID)
	if err != nil {
		return accountdomain.ShippingAddress{}, err
	}
	return s.revealSnapshot(ctx, snapshot)
}

func (s *ShippingService) RevealSnapshotForUser(
	ctx context.Context,
	userID, snapshotID string,
) (accountdomain.ShippingAddress, error) {
	snapshot, err := s.repository.GetShippingSnapshot(ctx, snapshotID)
	if err != nil {
		return accountdomain.ShippingAddress{}, err
	}
	if snapshot.UserID != strings.TrimSpace(userID) {
		return accountdomain.ShippingAddress{}, accountdomain.ErrShippingProfileMissing
	}
	return s.revealSnapshot(ctx, snapshot)
}

func (s *ShippingService) revealSnapshot(
	ctx context.Context,
	snapshot accountdomain.ShippingSnapshot,
) (accountdomain.ShippingAddress, error) {
	if !snapshot.Available() {
		return accountdomain.ShippingAddress{}, accountdomain.ErrShippingSnapshotPurged
	}
	return s.decryptAddress(ctx, EncryptedPII{
		Ciphertext:  snapshot.EncryptedPayload,
		Nonce:       snapshot.PayloadNonce,
		KeyVersion:  snapshot.KeyVersion,
		Fingerprint: snapshot.SnapshotHMAC,
	}, snapshot.UserID)
}

func (s *ShippingService) decryptAddress(
	ctx context.Context,
	encrypted EncryptedPII,
	userID string,
) (accountdomain.ShippingAddress, error) {
	payload, err := s.cipher.Decrypt(ctx, encrypted, userID)
	if err != nil {
		return accountdomain.ShippingAddress{}, err
	}
	var address accountdomain.ShippingAddress
	if err := json.Unmarshal(payload, &address); err != nil {
		return accountdomain.ShippingAddress{}, err
	}
	return address.Normalize()
}

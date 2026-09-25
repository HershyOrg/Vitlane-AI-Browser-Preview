package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type WalletRegistrationAttemptRecord struct {
	Attempt accountdomain.WalletRegistrationAttempt
	Nonce   string
}

type WalletRegistrationCompletion struct {
	Wallet         accountdomain.Wallet
	OwnershipProof accountdomain.WalletOwnershipProof
}

type WalletVerificationRepository interface {
	FindWalletRegistrationCreationReplay(
		context.Context,
		accountdomain.UserID,
		string,
		string,
		time.Time,
	) (WalletRegistrationAttemptRecord, bool, error)
	CreateWalletRegistrationAttempt(
		context.Context,
		accountdomain.WalletRegistrationAttempt,
		string,
	) (WalletRegistrationAttemptRecord, bool, error)
	FindWalletRegistrationAttempt(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletRegistrationAttemptID,
	) (accountdomain.WalletRegistrationAttempt, error)
	ExpireWalletRegistrationAttempt(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletRegistrationAttemptID,
		time.Time,
	) error
	FindWalletRegistrationCompletionReplay(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletRegistrationAttemptID,
		string,
		string,
	) (WalletRegistrationCompletion, bool, error)
	RecordWalletRegistrationFailure(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletRegistrationAttemptID,
		time.Time,
	) error
	FindWalletByAccount(
		context.Context,
		accountdomain.UserID,
		string,
		[]byte,
	) (accountdomain.Wallet, bool, error)
	FindWallet(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletID,
	) (accountdomain.Wallet, error)
	FindWalletOwnershipProof(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletID,
		accountdomain.WalletOwnershipProofID,
	) (accountdomain.WalletOwnershipProof, error)
	LockPaymentIdentity(
		context.Context,
		accountdomain.UserID,
		accountdomain.WalletID,
		accountdomain.WalletOwnershipProofID,
	) (
		accountdomain.Wallet,
		accountdomain.WalletOwnershipProof,
		error,
	)
	CompleteWalletRegistrationAttempt(
		context.Context,
		accountdomain.WalletRegistrationAttempt,
		accountdomain.Wallet,
		accountdomain.WalletOwnershipProof,
		string,
		string,
		time.Time,
	) (WalletRegistrationCompletion, bool, error)
}

type WalletVerificationService struct {
	repository WalletVerificationRepository
	secrets    SecretGenerator
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
	origin     string
	chainID    string
	limiter    AttemptRateLimiter
}

func (s *WalletVerificationService) EnableRateLimiter(limiter AttemptRateLimiter) {
	s.limiter = limiter
}

func NewWalletVerificationService(
	repository WalletVerificationRepository,
	secrets SecretGenerator,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	origin, chainID string,
) *WalletVerificationService {
	canonicalChainID, err := accountdomain.CanonicalEVMChainID(chainID)
	if err == nil {
		chainID = canonicalChainID
	}
	return &WalletVerificationService{
		repository: repository,
		secrets:    secrets,
		clock:      clock,
		ids:        ids,
		origin:     strings.TrimRight(strings.TrimSpace(origin), "/"),
		chainID:    chainID,
	}
}

type CreateRegistrationAttemptInput struct {
	UserID            string
	Address           string
	ChainID           string
	ClientOperationID string
	Origin            string
	Source            string
}

type WalletRegistrationAttemptView struct {
	ID          accountdomain.WalletRegistrationAttemptID     `json:"id"`
	Address     string                                        `json:"address"`
	AccountID   string                                        `json:"accountId"`
	ChainID     string                                        `json:"chainId"`
	Status      accountdomain.WalletRegistrationAttemptStatus `json:"status"`
	Message     string                                        `json:"message"`
	MessageHash string                                        `json:"messageHash"`
	Nonce       string                                        `json:"nonce"`
	ExpiresAt   time.Time                                     `json:"expiresAt"`
}

type WalletRegistrationAttemptResult struct {
	Attempt WalletRegistrationAttemptView `json:"attempt"`
	Replay  bool                          `json:"replay"`
}

func (s *WalletVerificationService) CreateRegistrationAttempt(
	ctx context.Context,
	input CreateRegistrationAttemptInput,
) (WalletRegistrationAttemptResult, error) {
	chainID, err := accountdomain.CanonicalEVMChainID(input.ChainID)
	if err != nil || chainID != s.chainID {
		return WalletRegistrationAttemptResult{}, accountdomain.ErrChainIDInvalid
	}
	address, addressKey, err := accountdomain.CanonicalEVMAddress(input.Address)
	if err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	if !validClientOperationID(input.ClientOperationID) {
		return WalletRegistrationAttemptResult{},
			accountdomain.ErrWalletRegistrationAttemptInvalid
	}
	now := s.clock.Now()
	requestHash := digestStrings(
		string(accountdomain.UserID(input.UserID)),
		hex.EncodeToString(addressKey),
		chainID,
		strings.TrimSpace(input.ClientOperationID),
	)
	replayed, found, err := s.repository.FindWalletRegistrationCreationReplay(
		ctx, accountdomain.UserID(input.UserID),
		strings.TrimSpace(input.ClientOperationID), requestHash, now,
	)
	if err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	if found {
		return registrationAttemptResult(replayed, true)
	}
	if err := enforceRateLimit(
		ctx, s.limiter,
		walletCreateRateLimitRules(input.UserID, address, input.Origin, input.Source),
		now,
	); err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	nonceBytes, err := s.secrets.Generate(32)
	if err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	attemptID := accountdomain.WalletRegistrationAttemptID(s.ids.NewID())
	message := walletRegistrationMessage(
		s.origin, address, chainID, nonce, string(attemptID), now,
		now.Add(accountdomain.WalletRegistrationAttemptTTL),
	)
	messageHash := sha256.Sum256([]byte(message))
	nonceHash := sha256.Sum256([]byte(nonce))
	attempt, err := accountdomain.NewWalletRegistrationAttempt(
		attemptID,
		accountdomain.UserID(input.UserID),
		address,
		chainID,
		s.origin,
		message,
		messageHash[:],
		nonceHash[:],
		input.ClientOperationID,
		requestHash,
		now,
	)
	if err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	record, replay, err := s.repository.CreateWalletRegistrationAttempt(
		ctx, attempt, nonce,
	)
	if err != nil {
		return WalletRegistrationAttemptResult{}, err
	}
	return registrationAttemptResult(record, replay)
}

func registrationAttemptResult(
	record WalletRegistrationAttemptRecord,
	replay bool,
) (WalletRegistrationAttemptResult, error) {
	switch record.Attempt.Status {
	case accountdomain.WalletRegistrationAttemptPending:
	case accountdomain.WalletRegistrationAttemptExpired:
		return WalletRegistrationAttemptResult{},
			accountdomain.ErrWalletRegistrationAttemptExpired
	default:
		return WalletRegistrationAttemptResult{},
			accountdomain.ErrWalletRegistrationAttemptClosed
	}
	return WalletRegistrationAttemptResult{
		Attempt: walletRegistrationAttemptView(record), Replay: replay,
	}, nil
}

type CompleteRegistrationAttemptInput struct {
	UserID            string
	AttemptID         string
	Nonce             string
	Signature         string
	ClientOperationID string
	Origin            string
	Source            string
}

type WalletRegistrationCompletionResult struct {
	Wallet         accountdomain.Wallet               `json:"wallet"`
	OwnershipProof accountdomain.WalletOwnershipProof `json:"ownershipProof"`
	Replay         bool                               `json:"replay"`
}

func (s *WalletVerificationService) CompleteRegistrationAttempt(
	ctx context.Context,
	input CompleteRegistrationAttemptInput,
) (WalletRegistrationCompletionResult, error) {
	if !validClientOperationID(input.ClientOperationID) {
		return WalletRegistrationCompletionResult{},
			accountdomain.ErrWalletRegistrationAttemptInvalid
	}
	userID := accountdomain.UserID(input.UserID)
	attemptID := accountdomain.WalletRegistrationAttemptID(input.AttemptID)
	completionRequestHash := digestStrings(
		string(userID),
		string(attemptID),
		strings.TrimSpace(input.Nonce),
		strings.TrimSpace(input.Signature),
		strings.TrimSpace(input.ClientOperationID),
	)
	replayed, found, err := s.repository.FindWalletRegistrationCompletionReplay(
		ctx,
		userID,
		attemptID,
		strings.TrimSpace(input.ClientOperationID),
		completionRequestHash,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	if found {
		return WalletRegistrationCompletionResult{
			Wallet:         replayed.Wallet,
			OwnershipProof: replayed.OwnershipProof,
			Replay:         true,
		}, nil
	}

	attempt, err := s.repository.FindWalletRegistrationAttempt(
		ctx, userID, attemptID,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	if attempt.Status == accountdomain.WalletRegistrationAttemptCompleted {
		replayed, found, err = s.repository.FindWalletRegistrationCompletionReplay(
			ctx,
			userID,
			attemptID,
			strings.TrimSpace(input.ClientOperationID),
			completionRequestHash,
		)
		if err != nil {
			return WalletRegistrationCompletionResult{}, err
		}
		if found {
			return WalletRegistrationCompletionResult{
				Wallet:         replayed.Wallet,
				OwnershipProof: replayed.OwnershipProof,
				Replay:         true,
			}, nil
		}
	}
	now := s.clock.Now()
	if err := attempt.CanCompleteAt(now); err != nil {
		if errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired) {
			if expireErr := s.repository.ExpireWalletRegistrationAttempt(
				ctx, userID, attemptID, now,
			); expireErr != nil {
				return WalletRegistrationCompletionResult{}, expireErr
			}
		}
		return WalletRegistrationCompletionResult{}, err
	}
	if err := enforceRateLimit(
		ctx, s.limiter,
		walletCompleteRateLimitRules(
			string(userID), attempt.Address, input.Origin, input.Source,
		),
		now,
	); err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	if !equalDigest(attempt.NonceHash, strings.TrimSpace(input.Nonce)) {
		return WalletRegistrationCompletionResult{},
			s.rejectRegistrationSignature(ctx, attempt, now)
	}
	if !validWalletSignature(attempt, input.Signature) {
		return WalletRegistrationCompletionResult{},
			s.rejectRegistrationSignature(ctx, attempt, now)
	}

	existingWallet, found, err := s.repository.FindWalletByAccount(
		ctx, attempt.UserID, attempt.ChainID, attempt.AddressKey,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	walletID := existingWallet.ID
	if !found {
		walletID = accountdomain.WalletID(s.ids.NewID())
	}
	wallet, err := accountdomain.NewRegisteredWallet(
		walletID,
		attempt.UserID,
		attempt.Address,
		attempt.ChainID,
		now,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	if found {
		wallet.CreatedAt = existingWallet.CreatedAt
		if existingWallet.RegistrationStatus == accountdomain.WalletRegistered {
			wallet.RegisteredAt = existingWallet.RegisteredAt
		}
	}
	proofID := accountdomain.WalletOwnershipProofID(s.ids.NewID())
	proof, err := accountdomain.NewWalletOwnershipProof(
		proofID,
		wallet.ID,
		attempt,
		"0x"+hex.EncodeToString(attempt.MessageHash),
		now,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	wallet.CurrentOwnershipProofID = &proofID
	completed, replay, err := s.repository.CompleteWalletRegistrationAttempt(
		ctx,
		attempt,
		wallet,
		proof,
		strings.TrimSpace(input.ClientOperationID),
		completionRequestHash,
		now,
	)
	if err != nil {
		return WalletRegistrationCompletionResult{}, err
	}
	return WalletRegistrationCompletionResult{
		Wallet:         completed.Wallet,
		OwnershipProof: completed.OwnershipProof,
		Replay:         replay,
	}, nil
}

func (s *WalletVerificationService) rejectRegistrationSignature(
	ctx context.Context,
	attempt accountdomain.WalletRegistrationAttempt,
	now time.Time,
) error {
	if err := s.repository.RecordWalletRegistrationFailure(
		ctx, attempt.UserID, attempt.ID, now,
	); err != nil {
		return err
	}
	return accountdomain.ErrWalletSignatureInvalid
}

func (s *WalletVerificationService) GetPaymentIdentity(
	ctx context.Context,
	rawUserID, rawWalletID, rawProofID string,
) (
	accountdomain.Wallet,
	accountdomain.WalletOwnershipProof,
	error,
) {
	userID := accountdomain.UserID(strings.TrimSpace(rawUserID))
	walletID := accountdomain.WalletID(strings.TrimSpace(rawWalletID))
	proofID := accountdomain.WalletOwnershipProofID(strings.TrimSpace(rawProofID))
	if userID == "" || walletID == "" || proofID == "" {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	wallet, proof, err := s.repository.LockPaymentIdentity(
		ctx, userID, walletID, proofID,
	)
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{}, err
	}
	if !wallet.IsRegistered() ||
		wallet.CurrentOwnershipProofID == nil ||
		*wallet.CurrentOwnershipProofID != proofID {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	if proof.UserID != wallet.UserID ||
		proof.WalletID != wallet.ID ||
		proof.Address != wallet.Address ||
		proof.AccountID != wallet.AccountID ||
		proof.ChainID != wallet.ChainID ||
		!equalBytes(proof.AddressKey, wallet.AddressKey) {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	if !proof.ValidAt(s.clock.Now()) {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofExpired
	}
	return wallet, proof, nil
}

func walletRegistrationAttemptView(
	record WalletRegistrationAttemptRecord,
) WalletRegistrationAttemptView {
	return WalletRegistrationAttemptView{
		ID:          record.Attempt.ID,
		Address:     record.Attempt.Address,
		AccountID:   record.Attempt.AccountID,
		ChainID:     record.Attempt.ChainID,
		Status:      record.Attempt.Status,
		Message:     record.Attempt.Message,
		MessageHash: "0x" + hex.EncodeToString(record.Attempt.MessageHash),
		Nonce:       record.Nonce,
		ExpiresAt:   record.Attempt.ExpiresAt,
	}
}

func walletRegistrationMessage(
	origin, address, chainID, nonce, requestID string,
	issuedAt, expiresAt time.Time,
) string {
	domain := origin
	if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
		domain = parsed.Host
	}
	numericChainID := strings.TrimPrefix(chainID, "eip155:")
	return fmt.Sprintf(
		"%s wants you to sign in with your Ethereum account:\n%s\n\nRegister this wallet for Vitlane TEST flows.\n\nURI: %s\nVersion: 1\nChain ID: %s\nNonce: %s\nIssued At: %s\nExpiration Time: %s\nRequest ID: %s",
		domain,
		address,
		origin,
		numericChainID,
		nonce,
		issuedAt.UTC().Format(time.RFC3339),
		expiresAt.UTC().Format(time.RFC3339),
		requestID,
	)
}

func validWalletSignature(
	attempt accountdomain.WalletRegistrationAttempt,
	encoded string,
) bool {
	signature, err := hexutil.Decode(strings.TrimSpace(encoded))
	if err != nil || len(signature) != crypto.SignatureLength {
		return false
	}
	switch signature[64] {
	case 27, 28:
		signature[64] -= 27
	case 0, 1:
	default:
		return false
	}
	publicKey, err := crypto.SigToPub(
		accounts.TextHash([]byte(attempt.Message)),
		signature,
	)
	if err != nil {
		return false
	}
	recovered := crypto.PubkeyToAddress(*publicKey)
	return equalBytes(recovered.Bytes(), attempt.AddressKey)
}

func equalDigest(expected []byte, plaintext string) bool {
	sum := sha256.Sum256([]byte(plaintext))
	return equalBytes(expected, sum[:])
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}

func digestStrings(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "0x" + hex.EncodeToString(hash.Sum(nil))
}

func validClientOperationID(value string) bool {
	length := len(strings.TrimSpace(value))
	return length >= 8 && length <= 200
}

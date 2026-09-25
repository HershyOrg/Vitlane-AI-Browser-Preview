package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

type walletClock struct{ now time.Time }

func (c walletClock) Now() time.Time { return c.now }

type walletIDs struct{ values []string }

func (i *walletIDs) NewID() string {
	value := i.values[0]
	i.values = i.values[1:]
	return value
}

type walletSecrets struct{ value []byte }

func (s walletSecrets) Generate(int) ([]byte, error) {
	return append([]byte(nil), s.value...), nil
}

type walletRateLimiter struct {
	calls [][]RateLimitRule
	deny  bool
}

func (l *walletRateLimiter) Acquire(
	_ context.Context,
	rules []RateLimitRule,
	_ time.Time,
) (RateLimitDecision, error) {
	l.calls = append(l.calls, append([]RateLimitRule(nil), rules...))
	if l.deny {
		return RateLimitDecision{
			Allowed: false, Policy: rules[0].Policy,
			RetryAfter: 23 * time.Second,
		}, nil
	}
	return RateLimitDecision{Allowed: true}, nil
}

type completionRecord struct {
	result      WalletRegistrationCompletion
	requestHash string
	attemptID   accountdomain.WalletRegistrationAttemptID
}

type walletRepository struct {
	attempts                 map[accountdomain.WalletRegistrationAttemptID]WalletRegistrationAttemptRecord
	attemptOps               map[string]accountdomain.WalletRegistrationAttemptID
	completionOps            map[string]completionRecord
	wallets                  map[accountdomain.WalletID]accountdomain.Wallet
	proofs                   map[accountdomain.WalletOwnershipProofID]accountdomain.WalletOwnershipProof
	hideNextCompletionReplay bool
}

func newWalletRepository() *walletRepository {
	return &walletRepository{
		attempts:      map[accountdomain.WalletRegistrationAttemptID]WalletRegistrationAttemptRecord{},
		attemptOps:    map[string]accountdomain.WalletRegistrationAttemptID{},
		completionOps: map[string]completionRecord{},
		wallets:       map[accountdomain.WalletID]accountdomain.Wallet{},
		proofs:        map[accountdomain.WalletOwnershipProofID]accountdomain.WalletOwnershipProof{},
	}
}

func (r *walletRepository) FindWalletRegistrationCreationReplay(
	_ context.Context,
	userID accountdomain.UserID,
	clientOperationID string,
	requestHash string,
	now time.Time,
) (WalletRegistrationAttemptRecord, bool, error) {
	existingID, ok := r.attemptOps[clientOperationID]
	if !ok {
		return WalletRegistrationAttemptRecord{}, false, nil
	}
	existing := r.attempts[existingID]
	if existing.Attempt.UserID != userID ||
		existing.Attempt.RequestHash != requestHash {
		return WalletRegistrationAttemptRecord{}, false,
			accountdomain.ErrWalletRegistrationOperationReused
	}
	if existing.Attempt.Status == accountdomain.WalletRegistrationAttemptPending &&
		!now.Before(existing.Attempt.ExpiresAt) {
		existing.Attempt.Status = accountdomain.WalletRegistrationAttemptExpired
		existing.Attempt.Message = ""
		existing.Nonce = ""
		existing.Attempt.SecretCleanedAt = &now
		existing.Attempt.UpdatedAt = now
		r.attempts[existingID] = existing
	}
	return existing, true, nil
}

func (r *walletRepository) CreateWalletRegistrationAttempt(
	_ context.Context,
	attempt accountdomain.WalletRegistrationAttempt,
	nonce string,
) (WalletRegistrationAttemptRecord, bool, error) {
	if existingID, ok := r.attemptOps[attempt.ClientOperationID]; ok {
		existing := r.attempts[existingID]
		if existing.Attempt.RequestHash != attempt.RequestHash {
			return WalletRegistrationAttemptRecord{}, false,
				accountdomain.ErrWalletRegistrationOperationReused
		}
		if existing.Attempt.Status ==
			accountdomain.WalletRegistrationAttemptPending &&
			!attempt.CreatedAt.Before(existing.Attempt.ExpiresAt) {
			existing.Attempt.Status =
				accountdomain.WalletRegistrationAttemptExpired
			existing.Attempt.UpdatedAt = attempt.CreatedAt
			r.attempts[existingID] = existing
		}
		return existing, true, nil
	}
	for id, existing := range r.attempts {
		if existing.Attempt.UserID == attempt.UserID &&
			existing.Attempt.ChainID == attempt.ChainID &&
			string(existing.Attempt.AddressKey) == string(attempt.AddressKey) &&
			existing.Attempt.Status == accountdomain.WalletRegistrationAttemptPending {
			superseded := existing
			superseded.Attempt.Status =
				accountdomain.WalletRegistrationAttemptSuperseded
			r.attempts[id] = superseded
		}
	}
	record := WalletRegistrationAttemptRecord{Attempt: attempt, Nonce: nonce}
	r.attempts[attempt.ID] = record
	r.attemptOps[attempt.ClientOperationID] = attempt.ID
	return record, false, nil
}

func (r *walletRepository) FindWalletRegistrationAttempt(
	_ context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
) (accountdomain.WalletRegistrationAttempt, error) {
	record, ok := r.attempts[attemptID]
	if !ok || record.Attempt.UserID != userID {
		return accountdomain.WalletRegistrationAttempt{},
			accountdomain.ErrWalletRegistrationAttemptMissing
	}
	return record.Attempt, nil
}

func (r *walletRepository) ExpireWalletRegistrationAttempt(
	_ context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	now time.Time,
) error {
	record, ok := r.attempts[attemptID]
	if !ok || record.Attempt.UserID != userID {
		return accountdomain.ErrWalletRegistrationAttemptMissing
	}
	if record.Attempt.Status == accountdomain.WalletRegistrationAttemptExpired {
		return nil
	}
	if record.Attempt.Status != accountdomain.WalletRegistrationAttemptPending ||
		now.Before(record.Attempt.ExpiresAt) {
		return accountdomain.ErrWalletRegistrationAttemptClosed
	}
	record.Attempt.Status = accountdomain.WalletRegistrationAttemptExpired
	record.Attempt.UpdatedAt = now
	r.attempts[attemptID] = record
	return nil
}

func (r *walletRepository) FindWalletRegistrationCompletionReplay(
	_ context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	operationID, requestHash string,
) (WalletRegistrationCompletion, bool, error) {
	if r.hideNextCompletionReplay {
		r.hideNextCompletionReplay = false
		return WalletRegistrationCompletion{}, false, nil
	}
	existing, ok := r.completionOps[operationID]
	if !ok {
		return WalletRegistrationCompletion{}, false, nil
	}
	if existing.result.Wallet.UserID != userID ||
		existing.attemptID != attemptID ||
		existing.requestHash != requestHash {
		return WalletRegistrationCompletion{}, false,
			accountdomain.ErrWalletRegistrationOperationReused
	}
	return existing.result, true, nil
}

func (r *walletRepository) RecordWalletRegistrationFailure(
	_ context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	now time.Time,
) error {
	record, ok := r.attempts[attemptID]
	if !ok || record.Attempt.UserID != userID {
		return accountdomain.ErrWalletRegistrationAttemptMissing
	}
	record.Attempt.FailureCount++
	record.Attempt.UpdatedAt = now
	r.attempts[attemptID] = record
	return nil
}

func (r *walletRepository) FindWalletByAccount(
	_ context.Context,
	userID accountdomain.UserID,
	chainID string,
	addressKey []byte,
) (accountdomain.Wallet, bool, error) {
	for _, wallet := range r.wallets {
		if wallet.UserID == userID &&
			wallet.ChainID == chainID &&
			string(wallet.AddressKey) == string(addressKey) {
			return wallet, true, nil
		}
	}
	return accountdomain.Wallet{}, false, nil
}

func (r *walletRepository) FindWallet(
	_ context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
) (accountdomain.Wallet, error) {
	wallet, ok := r.wallets[walletID]
	if !ok || wallet.UserID != userID {
		return accountdomain.Wallet{}, accountdomain.ErrWalletNotRegistered
	}
	return wallet, nil
}

func (r *walletRepository) FindWalletOwnershipProof(
	_ context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (accountdomain.WalletOwnershipProof, error) {
	proof, ok := r.proofs[proofID]
	if !ok || proof.UserID != userID || proof.WalletID != walletID {
		return accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofMissing
	}
	return proof, nil
}

func (r *walletRepository) LockPaymentIdentity(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (
	accountdomain.Wallet,
	accountdomain.WalletOwnershipProof,
	error,
) {
	wallet, err := r.FindWallet(ctx, userID, walletID)
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{}, err
	}
	proof, err := r.FindWalletOwnershipProof(
		ctx, userID, walletID, proofID,
	)
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	return wallet, proof, nil
}

func (r *walletRepository) CompleteWalletRegistrationAttempt(
	_ context.Context,
	expected accountdomain.WalletRegistrationAttempt,
	wallet accountdomain.Wallet,
	proof accountdomain.WalletOwnershipProof,
	operationID, requestHash string,
	now time.Time,
) (WalletRegistrationCompletion, bool, error) {
	if existing, ok := r.completionOps[operationID]; ok {
		return existing.result, true, nil
	}
	record, ok := r.attempts[expected.ID]
	if !ok || record.Attempt.Status !=
		accountdomain.WalletRegistrationAttemptPending {
		return WalletRegistrationCompletion{}, false,
			accountdomain.ErrWalletRegistrationAttemptClosed
	}
	record.Attempt.Status = accountdomain.WalletRegistrationAttemptCompleted
	record.Attempt.CompletionOperationID = operationID
	record.Attempt.CompletionRequestHash = requestHash
	record.Attempt.WalletID = &wallet.ID
	record.Attempt.OwnershipProofID = &proof.ID
	record.Attempt.CompletedAt = &now
	record.Attempt.UpdatedAt = now
	r.attempts[expected.ID] = record
	r.wallets[wallet.ID] = wallet
	r.proofs[proof.ID] = proof
	result := WalletRegistrationCompletion{
		Wallet: wallet, OwnershipProof: proof,
	}
	r.completionOps[operationID] = completionRecord{
		result: result, requestHash: requestHash, attemptID: expected.ID,
	}
	return result, false, nil
}

func newWalletRegistrationService(
	now time.Time,
	repository *walletRepository,
	ids ...string,
) *WalletVerificationService {
	return NewWalletVerificationService(
		repository,
		walletSecrets{[]byte("fixed-test-nonce-with-32-bytes!!")},
		walletClock{now},
		&walletIDs{values: ids},
		"https://test.vitlane.example",
		"eip155:91342",
	)
}

func TestWalletRegistrationCreatesWalletOnlyAfterValidSignature(t *testing.T) {
	now := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	repository := newWalletRepository()
	service := newWalletRegistrationService(
		now, repository, "attempt-1", "wallet-1", "proof-1",
	)
	attempt, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.wallets) != 0 || attempt.Attempt.Nonce == "" {
		t.Fatalf("wallet was created before signature: %#v", repository.wallets)
	}
	if !strings.HasPrefix(
		attempt.Attempt.Message,
		"test.vitlane.example wants you to sign in with your Ethereum account:\n",
	) ||
		!strings.Contains(attempt.Attempt.Message, "\nChain ID: 91342\n") ||
		!strings.Contains(
			attempt.Attempt.Message,
			"\nURI: https://test.vitlane.example\n",
		) ||
		!strings.Contains(
			attempt.Attempt.Message,
			"\nRequest ID: attempt-1",
		) {
		t.Fatalf("registration message is not canonical ERC-4361: %q",
			attempt.Attempt.Message)
	}
	signature, err := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)), privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce: attempt.Attempt.Nonce, Signature: hexutil.Encode(signature),
			ClientOperationID: "complete-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Wallet.IsRegistered() ||
		completed.Wallet.CurrentOwnershipProofID == nil ||
		*completed.Wallet.CurrentOwnershipProofID != completed.OwnershipProof.ID {
		t.Fatalf("registration result mismatch: %#v", completed)
	}
	if completed.OwnershipProof.ValidUntil.Sub(
		completed.OwnershipProof.VerifiedAt,
	) != accountdomain.WalletOwnershipProofLifetime {
		t.Fatalf("proof is not exactly 24h: %#v", completed.OwnershipProof)
	}
	replayed, err := service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce: attempt.Attempt.Nonce, Signature: hexutil.Encode(signature),
			ClientOperationID: "complete-operation-1",
		},
	)
	if err != nil || !replayed.Replay ||
		replayed.Wallet.ID != completed.Wallet.ID ||
		replayed.OwnershipProof.ID != completed.OwnershipProof.ID {
		t.Fatalf("exact completion replay mismatch: %#v err=%v", replayed, err)
	}
	repository.hideNextCompletionReplay = true
	racedReplay, err := service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce: attempt.Attempt.Nonce, Signature: hexutil.Encode(signature),
			ClientOperationID: "complete-operation-1",
		},
	)
	if err != nil || !racedReplay.Replay ||
		racedReplay.Wallet.ID != completed.Wallet.ID ||
		racedReplay.OwnershipProof.ID != completed.OwnershipProof.ID {
		t.Fatalf(
			"completion committed between replay and attempt lookup did not converge: %#v err=%v",
			racedReplay, err,
		)
	}
	_, err = service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce:             attempt.Attempt.Nonce + "-changed",
			Signature:         hexutil.Encode(signature),
			ClientOperationID: "complete-operation-1",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletRegistrationOperationReused) {
		t.Fatalf("changed payload reused completion operation: %v", err)
	}
}

func TestWalletRateLimitPreservesExactCreateAndCompletionReplays(t *testing.T) {
	now := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	repository := newWalletRepository()
	service := newWalletRegistrationService(
		now, repository, "attempt-rate-1", "wallet-rate-1", "proof-rate-1",
	)
	limiter := &walletRateLimiter{}
	service.EnableRateLimiter(limiter)
	createInput := CreateRegistrationAttemptInput{
		UserID:  "user-1",
		Address: crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
		ChainID: "eip155:91342", ClientOperationID: "attempt-rate-operation",
		Origin: "https://test.vitlane.example", Source: "203.0.113.7",
	}
	attempt, err := service.CreateRegistrationAttempt(context.Background(), createInput)
	if err != nil {
		t.Fatal(err)
	}
	createReplay, err := service.CreateRegistrationAttempt(
		context.Background(), createInput,
	)
	if err != nil || !createReplay.Replay {
		t.Fatalf("create replay=%#v err=%v", createReplay, err)
	}
	if len(limiter.calls) != 1 {
		t.Fatalf("create replay consumed rate limit: calls=%d", len(limiter.calls))
	}
	signature, err := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)), privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	completeInput := CompleteRegistrationAttemptInput{
		UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
		Nonce: attempt.Attempt.Nonce, Signature: hexutil.Encode(signature),
		ClientOperationID: "complete-rate-operation",
		Origin:            "https://wallet-client.example", Source: "203.0.113.7",
	}
	completed, err := service.CompleteRegistrationAttempt(
		context.Background(), completeInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	completionReplay, err := service.CompleteRegistrationAttempt(
		context.Background(), completeInput,
	)
	if err != nil || !completionReplay.Replay ||
		completionReplay.Wallet.ID != completed.Wallet.ID {
		t.Fatalf("completion replay=%#v err=%v", completionReplay, err)
	}
	if len(limiter.calls) != 2 {
		t.Fatalf("completion replay consumed rate limit: calls=%d", len(limiter.calls))
	}
	if limiter.calls[0][0].Policy != RatePolicyWalletRegistrationCreate ||
		limiter.calls[1][0].Policy != RatePolicyWalletRegistrationComplete {
		t.Fatalf("unexpected Wallet rate policies: %#v", limiter.calls)
	}
	completionOriginFound := false
	for _, rule := range limiter.calls[1] {
		if rule.Dimension == "origin" &&
			rule.Subject == "https://wallet-client.example" {
			completionOriginFound = true
		}
	}
	if !completionOriginFound {
		t.Fatalf("completion did not use request origin: %#v", limiter.calls[1])
	}
}

func TestWalletRateLimitRejectsBeforeAttemptCreation(t *testing.T) {
	now := time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC)
	privateKey, _ := crypto.GenerateKey()
	repository := newWalletRepository()
	service := newWalletRegistrationService(now, repository, "unused-attempt")
	limiter := &walletRateLimiter{deny: true}
	service.EnableRateLimiter(limiter)
	_, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID:  "user-1",
			Address: crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
			ChainID: "eip155:91342", ClientOperationID: "denied-attempt-operation",
			Source: "203.0.113.8",
		},
	)
	if !errors.Is(err, ErrRateLimited) || RateLimitRetryAfter(err) != 23 {
		t.Fatalf("rate limit error=%v retry=%d", err, RateLimitRetryAfter(err))
	}
	if len(repository.attempts) != 0 {
		t.Fatalf("rate-limited request created attempts: %#v", repository.attempts)
	}
}

func TestInvalidSignatureDoesNotConsumeRegistrationAttempt(t *testing.T) {
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	ownerKey, _ := crypto.GenerateKey()
	otherKey, _ := crypto.GenerateKey()
	address := crypto.PubkeyToAddress(ownerKey.PublicKey).Hex()
	repository := newWalletRepository()
	service := newWalletRegistrationService(
		now, repository, "attempt-1", "wallet-1", "proof-1",
	)
	attempt, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	badSignature, _ := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)), otherKey,
	)
	_, err = service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce:             attempt.Attempt.Nonce,
			Signature:         hexutil.Encode(badSignature),
			ClientOperationID: "complete-operation-bad",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletSignatureInvalid) {
		t.Fatalf("invalid signature error=%v", err)
	}
	stored := repository.attempts[attempt.Attempt.ID].Attempt
	if stored.Status != accountdomain.WalletRegistrationAttemptPending ||
		stored.FailureCount != 1 || len(repository.wallets) != 0 {
		t.Fatalf("invalid signature consumed attempt: %#v", stored)
	}
	goodSignature, _ := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)), ownerKey,
	)
	if _, err := service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce:             attempt.Attempt.Nonce,
			Signature:         hexutil.Encode(goodSignature),
			ClientOperationID: "complete-operation-good",
		},
	); err != nil {
		t.Fatalf("valid retry failed: %v", err)
	}
}

func TestNewAttemptSupersedesLostPendingAttemptAndExactOperationReplays(t *testing.T) {
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	key, _ := crypto.GenerateKey()
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	repository := newWalletRepository()
	service := newWalletRegistrationService(
		now, repository, "attempt-1", "attempt-unused", "attempt-2",
	)
	first, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-1",
		},
	)
	if err != nil || !replayed.Replay ||
		replayed.Attempt.ID != first.Attempt.ID ||
		replayed.Attempt.Nonce != first.Attempt.Nonce {
		t.Fatalf("attempt replay mismatch: %#v err=%v", replayed, err)
	}
	second, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempt.ID == first.Attempt.ID ||
		repository.attempts[first.Attempt.ID].Attempt.Status !=
			accountdomain.WalletRegistrationAttemptSuperseded {
		t.Fatalf("lost attempt was not superseded: first=%#v second=%#v",
			repository.attempts[first.Attempt.ID], second)
	}
}

func TestExpiredAttemptIsPersistedBeforeCreateReplayOrCompletionReturns(t *testing.T) {
	now := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	key, _ := crypto.GenerateKey()
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	repository := newWalletRepository()
	createService := newWalletRegistrationService(
		now, repository, "attempt-1",
	)
	attempt, err := createService.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-expiry",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := attempt.Attempt.ExpiresAt
	expiredService := newWalletRegistrationService(
		expiredAt, repository, "attempt-unused",
	)
	_, err = expiredService.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-expiry",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired) {
		t.Fatalf("expired create replay error=%v", err)
	}
	stored := repository.attempts[attempt.Attempt.ID].Attempt
	if stored.Status != accountdomain.WalletRegistrationAttemptExpired ||
		!stored.UpdatedAt.Equal(expiredAt) {
		t.Fatalf("create replay did not persist EXPIRED: %#v", stored)
	}

	secondService := newWalletRegistrationService(
		now, repository, "attempt-2",
	)
	second, err := secondService.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-complete-expiry",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	signature, _ := crypto.Sign(
		accounts.TextHash([]byte(second.Attempt.Message)), key,
	)
	completeExpiredService := newWalletRegistrationService(
		second.Attempt.ExpiresAt, repository, "wallet-unused", "proof-unused",
	)
	_, err = completeExpiredService.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(second.Attempt.ID),
			Nonce: second.Attempt.Nonce, Signature: hexutil.Encode(signature),
			ClientOperationID: "complete-operation-expiry",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired) {
		t.Fatalf("expired completion error=%v", err)
	}
	stored = repository.attempts[second.Attempt.ID].Attempt
	if stored.Status != accountdomain.WalletRegistrationAttemptExpired ||
		!stored.UpdatedAt.Equal(second.Attempt.ExpiresAt) {
		t.Fatalf("completion did not persist EXPIRED: %#v", stored)
	}
}

func TestRegistrationReusesCanonicalWalletIDForReauthentication(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	key, _ := crypto.GenerateKey()
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	repository := newWalletRepository()
	createdAt := now.Add(-48 * time.Hour)
	existing, err := accountdomain.NewRegisteredWallet(
		"wallet-existing", "user-1", address, "eip155:91342", createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	deregisteredAt := now.Add(-time.Hour)
	existing.RegistrationStatus = accountdomain.WalletDeregistered
	existing.DeregisteredAt = &deregisteredAt
	existing.UpdatedAt = deregisteredAt
	repository.wallets[existing.ID] = existing
	service := newWalletRegistrationService(
		now, repository, "attempt-1", "proof-2",
	)
	attempt, err := service.CreateRegistrationAttempt(
		context.Background(),
		CreateRegistrationAttemptInput{
			UserID: "user-1", Address: address, ChainID: "eip155:91342",
			ClientOperationID: "attempt-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	signature, _ := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)), key,
	)
	completed, err := service.CompleteRegistrationAttempt(
		context.Background(),
		CompleteRegistrationAttemptInput{
			UserID: "user-1", AttemptID: string(attempt.Attempt.ID),
			Nonce:             attempt.Attempt.Nonce,
			Signature:         hexutil.Encode(signature),
			ClientOperationID: "complete-operation-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Wallet.ID != existing.ID ||
		completed.OwnershipProof.WalletID != existing.ID ||
		!completed.Wallet.CreatedAt.Equal(createdAt) {
		t.Fatalf("canonical wallet identity changed: %#v", completed)
	}
}

func TestPaymentIdentityRequiresExactCurrentValidProof(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository := newWalletRepository()
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	service := &WalletVerificationService{
		repository: repository,
		clock:      walletClock{now: now.Add(time.Minute)},
	}
	foundWallet, foundProof, err := service.GetPaymentIdentity(
		context.Background(), "user-1", "wallet-1", "proof-1",
	)
	if err != nil || foundWallet.ID != wallet.ID || foundProof.ID != proof.ID {
		t.Fatalf("exact current proof was rejected: wallet=%#v proof=%#v err=%v",
			foundWallet, foundProof, err)
	}
	if _, _, err := service.GetPaymentIdentity(
		context.Background(), "user-1", "wallet-1", "proof-other",
	); !errors.Is(err, accountdomain.ErrWalletOwnershipProofInvalid) {
		t.Fatalf("non-current proof must be rejected: %v", err)
	}
	service.clock = walletClock{now: proof.ValidUntil}
	if _, _, err := service.GetPaymentIdentity(
		context.Background(), "user-1", "wallet-1", "proof-1",
	); !errors.Is(err, accountdomain.ErrWalletOwnershipProofExpired) {
		t.Fatalf("proof validity must be half-open: %v", err)
	}
}

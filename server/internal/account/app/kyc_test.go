package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type kycRepository struct {
	wallets                   map[accountdomain.WalletID]accountdomain.Wallet
	proofs                    map[accountdomain.WalletOwnershipProofID]accountdomain.WalletOwnershipProof
	cases                     map[string]accountdomain.KYCVerificationCase
	operations                map[string]accountdomain.KYCProviderOperation
	aliases                   map[string]string
	aliasHashes               map[string]string
	credentials               map[accountdomain.KYCCredentialID]accountdomain.KYCCredential
	observations              map[accountdomain.KYCEvidenceObservationID]accountdomain.KYCEvidenceObservation
	completeStartContextError error
}

func newKYCRepository() *kycRepository {
	return &kycRepository{
		wallets:      map[accountdomain.WalletID]accountdomain.Wallet{},
		proofs:       map[accountdomain.WalletOwnershipProofID]accountdomain.WalletOwnershipProof{},
		cases:        map[string]accountdomain.KYCVerificationCase{},
		operations:   map[string]accountdomain.KYCProviderOperation{},
		aliases:      map[string]string{},
		aliasHashes:  map[string]string{},
		credentials:  map[accountdomain.KYCCredentialID]accountdomain.KYCCredential{},
		observations: map[accountdomain.KYCEvidenceObservationID]accountdomain.KYCEvidenceObservation{},
	}
}

func operationMapKey(
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID string,
) string {
	return string(userID) + ":" + string(kind) + ":" + clientOperationID
}

func (r *kycRepository) FindWallet(
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

func (r *kycRepository) FindWalletOwnershipProof(
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

func (r *kycRepository) FindKYCVerificationCase(
	_ context.Context,
	userID accountdomain.UserID,
	caseID string,
) (accountdomain.KYCVerificationCase, error) {
	verification, ok := r.cases[caseID]
	if !ok || verification.UserID != userID {
		return accountdomain.KYCVerificationCase{},
			accountdomain.ErrKYCVerificationMissing
	}
	return verification, nil
}

func (r *kycRepository) FindKYCProviderOperation(
	_ context.Context,
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID, requestHash string,
) (KYCOperationRecord, bool, error) {
	key := operationMapKey(userID, kind, clientOperationID)
	operation, ok := r.operations[key]
	aliasResolved := false
	if !ok {
		if canonicalKey, aliased := r.aliases[key]; aliased {
			if r.aliasHashes[key] != requestHash {
				return KYCOperationRecord{}, false,
					accountdomain.ErrKYCIdempotencyReused
			}
			operation, ok = r.operations[canonicalKey]
			aliasResolved = ok
		}
	}
	if !ok {
		return KYCOperationRecord{}, false, nil
	}
	if !aliasResolved && operation.RequestHash != requestHash {
		return KYCOperationRecord{}, false,
			accountdomain.ErrKYCIdempotencyReused
	}
	return r.operationRecord(operation), true, nil
}

func (r *kycRepository) ReserveKYCStart(
	_ context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
) (KYCOperationRecord, bool, error) {
	key := operationMapKey(
		operation.UserID, operation.Kind, operation.ClientOperationID,
	)
	if existing, ok := r.operations[key]; ok {
		return r.operationRecord(existing),
			existing.State == accountdomain.KYCOperationSucceeded, nil
	}
	for _, activeCase := range r.cases {
		if activeCase.UserID != verification.UserID ||
			activeCase.WalletID != verification.WalletID ||
			(activeCase.State != accountdomain.KYCStateCreated &&
				activeCase.State != accountdomain.KYCStatePendingProvider) {
			continue
		}
		for _, existing := range r.operations {
			if existing.CaseID == activeCase.ID &&
				existing.Kind == accountdomain.KYCOperationStart {
				canonicalKey := operationMapKey(
					existing.UserID,
					existing.Kind,
					existing.ClientOperationID,
				)
				r.aliases[key] = canonicalKey
				r.aliasHashes[key] = operation.RequestHash
				return r.operationRecord(existing), true, nil
			}
		}
		return KYCOperationRecord{}, false,
			accountdomain.ErrKYCVerificationState
	}
	r.cases[verification.ID] = verification
	r.operations[key] = operation
	return r.operationRecord(operation), false, nil
}

func (r *kycRepository) CompleteKYCStart(
	ctx context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
	_ time.Time,
) (KYCOperationRecord, bool, error) {
	r.completeStartContextError = ctx.Err()
	key := operationMapKey(
		operation.UserID, operation.Kind, operation.ClientOperationID,
	)
	if existing := r.operations[key]; existing.State ==
		accountdomain.KYCOperationSucceeded {
		return r.operationRecord(existing), true, nil
	}
	r.cases[verification.ID] = verification
	r.operations[key] = operation
	return r.operationRecord(operation), false, nil
}

func (r *kycRepository) ReserveKYCCheck(
	_ context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
) (KYCOperationRecord, bool, error) {
	key := operationMapKey(
		operation.UserID, operation.Kind, operation.ClientOperationID,
	)
	if existing, ok := r.operations[key]; ok {
		return r.operationRecord(existing),
			existing.State == accountdomain.KYCOperationSucceeded, nil
	}
	for _, existing := range r.operations {
		if existing.CaseID == verification.ID &&
			existing.Kind == accountdomain.KYCOperationCheck &&
			(existing.State == accountdomain.KYCOperationReserved ||
				existing.State == accountdomain.KYCOperationUnavailable) {
			canonicalKey := operationMapKey(
				existing.UserID,
				existing.Kind,
				existing.ClientOperationID,
			)
			r.aliases[key] = canonicalKey
			r.aliasHashes[key] = operation.RequestHash
			return r.operationRecord(existing), true, nil
		}
	}
	r.operations[key] = operation
	return r.operationRecord(operation), false, nil
}

func (r *kycRepository) CompleteKYCCheck(
	_ context.Context,
	verification accountdomain.KYCVerificationCase,
	credential *accountdomain.KYCCredential,
	observation *accountdomain.KYCEvidenceObservation,
	operation accountdomain.KYCProviderOperation,
	_ time.Time,
) (KYCOperationRecord, bool, error) {
	key := operationMapKey(
		operation.UserID, operation.Kind, operation.ClientOperationID,
	)
	if existing := r.operations[key]; existing.State ==
		accountdomain.KYCOperationSucceeded {
		return r.operationRecord(existing), true, nil
	}
	r.cases[verification.ID] = verification
	if credential != nil {
		r.credentials[credential.ID] = *credential
	}
	if observation != nil {
		r.observations[observation.ID] = *observation
	}
	r.operations[key] = operation
	return r.operationRecord(operation), false, nil
}

func (r *kycRepository) CompleteKYCProviderFailure(
	_ context.Context,
	operation accountdomain.KYCProviderOperation,
	now time.Time,
) (KYCOperationRecord, bool, error) {
	key := operationMapKey(
		operation.UserID, operation.Kind, operation.ClientOperationID,
	)
	if existing := r.operations[key]; existing.State ==
		accountdomain.KYCOperationSucceeded {
		return r.operationRecord(existing), true, nil
	}
	r.operations[key] = operation
	if operation.Kind == accountdomain.KYCOperationStart &&
		operation.State == accountdomain.KYCOperationFailed {
		verification := r.cases[operation.CaseID]
		next, err := verification.CancelBeforeProviderStarted(
			operation.FailureCode, now,
		)
		if err != nil {
			return KYCOperationRecord{}, false, err
		}
		r.cases[verification.ID] = next
	}
	return r.operationRecord(operation), false, nil
}

func (r *kycRepository) operationRecord(
	operation accountdomain.KYCProviderOperation,
) KYCOperationRecord {
	record := KYCOperationRecord{
		Case: r.cases[operation.CaseID], Operation: operation,
	}
	if operation.ResultCredentialID != nil {
		if credential, ok := r.credentials[*operation.ResultCredentialID]; ok {
			record.Credential = &credential
			for _, observation := range r.observations {
				if observation.CredentialID == credential.ID &&
					(record.Observation == nil ||
						observation.SourceVersion >
							record.Observation.SourceVersion) {
					next := observation
					record.Observation = &next
				}
			}
		}
	}
	return record
}

type kycProvider struct {
	startCalls   int
	checkCalls   int
	startKeys    []string
	checkKeys    []string
	startErrors  []error
	startResults []KYCProviderStartResult
	checkErrors  []error
	startHook    func()
}

func (p *kycProvider) Start(
	_ context.Context,
	request KYCProviderStartRequest,
) (KYCProviderStartResult, error) {
	p.startCalls++
	p.startKeys = append(p.startKeys, request.ProviderRequestKey)
	if p.startHook != nil {
		p.startHook()
	}
	if len(p.startErrors) > 0 {
		err := p.startErrors[0]
		p.startErrors = p.startErrors[1:]
		if err != nil {
			return KYCProviderStartResult{}, err
		}
	}
	if len(p.startResults) > 0 {
		result := p.startResults[0]
		p.startResults = p.startResults[1:]
		return result, nil
	}
	return KYCProviderStartResult{
		ProviderCaseRef: "mock-dojang:" + request.CaseID,
		State:           accountdomain.KYCStatePendingProvider,
	}, nil
}

func TestKYCStartPersistsProviderResultAfterCallerCancellation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	caller, cancelCaller := context.WithCancel(context.Background())
	provider := &kycProvider{startHook: cancelCaller}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now},
		&walletIDs{values: []string{"case-1", "start-operation-1"}},
	)
	lifecycle, stopLifecycle := context.WithCancel(context.Background())
	defer stopLifecycle()
	service.EnableFinalizer(runtimepolicy.NewFinalizer(lifecycle, time.Second))

	result, err := service.Start(caller, StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-cancelled-caller",
	})
	if err != nil || result.Case.State != accountdomain.KYCStatePendingProvider {
		t.Fatalf("provider result was not finalized: result=%#v err=%v", result, err)
	}
	if repository.completeStartContextError != nil {
		t.Fatalf("finalization inherited caller cancellation: %v", repository.completeStartContextError)
	}
}

func (p *kycProvider) Check(
	_ context.Context,
	request KYCProviderCheckRequest,
) (KYCProviderCheckResult, error) {
	p.checkCalls++
	p.checkKeys = append(p.checkKeys, request.ProviderRequestKey)
	if len(p.checkErrors) > 0 {
		err := p.checkErrors[0]
		p.checkErrors = p.checkErrors[1:]
		if err != nil {
			return KYCProviderCheckResult{}, err
		}
	}
	return KYCProviderCheckResult{
		State: accountdomain.KYCStateVerified,
		Evidence: &KYCCredentialEvidence{
			IssuerRef:         "vitlane.mock-dojang.v2",
			SchemaRef:         "mock-kyc-test-product.v2",
			EvidenceHash:      "0xevidence",
			IssuedAt:          request.Now,
			ValidUntil:        request.Now.Add(24 * time.Hour),
			ObservationStatus: accountdomain.KYCEvidenceValid,
			SourceVersion:     1,
			ObservedAt:        request.Now,
			RecheckAfter:      request.Now.Add(24 * time.Hour),
		},
	}, nil
}

func registeredWalletAndProof(
	t *testing.T,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
	address string,
	now time.Time,
) (accountdomain.Wallet, accountdomain.WalletOwnershipProof) {
	t.Helper()
	attempt, err := accountdomain.NewWalletRegistrationAttempt(
		accountdomain.WalletRegistrationAttemptID("attempt-"+walletID),
		userID,
		address,
		"eip155:91342",
		"https://test.vitlane.example",
		"message",
		[]byte("message-hash"),
		[]byte("nonce-hash"),
		"attempt-operation-"+string(walletID),
		"0xrequest",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := accountdomain.NewRegisteredWallet(
		walletID, userID, address, "eip155:91342", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := accountdomain.NewWalletOwnershipProof(
		proofID, wallet.ID, attempt, "0xmessage", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	wallet.CurrentOwnershipProofID = &proofID
	return wallet, proof
}

func TestKYCStartRequiresCurrentFreshOwnershipProof(t *testing.T) {
	now := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	provider := &kycProvider{}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now},
		&walletIDs{values: []string{"case-1", "start-operation-1"}},
	)
	started, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-operation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Case.State != accountdomain.KYCStatePendingProvider ||
		started.Case.ProviderVersion !=
			accountdomain.MockDojangProviderVersion ||
		started.Case.ExternalEffect != accountdomain.KYCEffectSimulated ||
		provider.startCalls != 1 {
		t.Fatalf("start result mismatch: %#v calls=%d",
			started, provider.startCalls)
	}

	staleProvider := &kycProvider{}
	staleService := NewKYCService(
		repository, staleProvider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now.Add(10*time.Minute + time.Nanosecond)},
		&walletIDs{},
	)
	_, err = staleService.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-operation-2",
	})
	if !errors.Is(err, accountdomain.ErrWalletOwnershipProofNotFresh) ||
		staleProvider.startCalls != 0 {
		t.Fatalf("stale proof started provider: err=%v calls=%d",
			err, staleProvider.startCalls)
	}
}

func TestInvalidProviderStartTerminatesCaseAndAllowsFreshOperation(t *testing.T) {
	now := time.Date(2026, 7, 29, 5, 30, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	provider := &kycProvider{
		startResults: []KYCProviderStartResult{{
			State: accountdomain.KYCStateVerified,
		}},
	}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now},
		&walletIDs{values: []string{
			"case-invalid", "start-operation-invalid",
			"case-retry", "start-operation-retry",
		}},
	)
	_, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-invalid-result",
	})
	if !errors.Is(err, accountdomain.ErrKYCVerificationInvalid) {
		t.Fatalf("invalid provider result error=%v", err)
	}
	failedCase := repository.cases["case-invalid"]
	if failedCase.State != accountdomain.KYCStateCancelled ||
		failedCase.FailureCode != "INVALID_PROVIDER_RESULT" ||
		failedCase.CompletedAt == nil {
		t.Fatalf("invalid provider start did not terminate case: %#v", failedCase)
	}
	started, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-after-invalid",
	})
	if err != nil ||
		started.Case.ID != "case-retry" ||
		started.Case.State != accountdomain.KYCStatePendingProvider {
		t.Fatalf("fresh operation could not recover: %#v err=%v", started, err)
	}
	if provider.startCalls != 2 {
		t.Fatalf("provider start calls=%d want=2", provider.startCalls)
	}
}

func TestMockKYCLifecyclePersistsCredentialObservationAndReplays(t *testing.T) {
	now := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	provider := &kycProvider{}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now},
		&walletIDs{values: []string{
			"case-1", "start-operation-1", "check-operation-1",
			"credential-1", "observation-1",
		}},
	)
	started, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-operation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.Check(context.Background(), CheckKYCInput{
		UserID: "user-1", CaseID: started.Case.ID,
		ClientOperationID: "kyc-check-operation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Case.State != accountdomain.KYCStateVerified ||
		completed.Credential == nil ||
		completed.Observation == nil ||
		completed.Credential.Level !=
			accountdomain.KYCCredentialMockDojangVerified ||
		completed.Observation.Status != accountdomain.KYCEvidenceValid {
		t.Fatalf("completed KYC records mismatch: %#v", completed)
	}
	replayed, err := service.Check(context.Background(), CheckKYCInput{
		UserID: "user-1", CaseID: started.Case.ID,
		ClientOperationID: "kyc-check-operation-1",
	})
	if err != nil || !replayed.Replay ||
		replayed.Credential == nil ||
		replayed.Credential.ID != completed.Credential.ID ||
		provider.checkCalls != 1 {
		t.Fatalf("check replay mismatch: %#v err=%v calls=%d",
			replayed, err, provider.checkCalls)
	}
}

func TestReservedKYCStartResumesWithStableProviderRequestKey(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	clientOperationID := "kyc-start-operation-1"
	requestHash := digestStrings(
		"user-1", "wallet-1", "proof-1", clientOperationID,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", "user-1", "wallet-1", "proof-1",
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := accountdomain.NewKYCProviderOperation(
		"start-operation-1", verification,
		accountdomain.KYCOperationStart, clientOperationID,
		requestHash, "stable-provider-request-key", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReserveKYCStart(
		context.Background(), verification, operation,
	); err != nil {
		t.Fatal(err)
	}
	provider := &kycProvider{}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now}, &walletIDs{values: []string{
			"discarded-case", "discarded-operation",
		}},
	)
	result, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "replacement-browser-operation",
	})
	if err != nil || result.Case.State !=
		accountdomain.KYCStatePendingProvider ||
		!result.Replay ||
		len(provider.startKeys) != 1 ||
		provider.startKeys[0] != "stable-provider-request-key" {
		t.Fatalf("reserved start recovery mismatch: %#v err=%v keys=%v",
			result, err, provider.startKeys)
	}
	replayed, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "replacement-browser-operation",
	})
	if err != nil || !replayed.Replay || provider.startCalls != 1 {
		t.Fatalf(
			"aliased start operation was not durable: %#v err=%v calls=%d",
			replayed, err, provider.startCalls,
		)
	}
}

func TestKYCProviderUnavailableRetriesReservedOperationWithoutChangingKey(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 7, 15, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	providerUnavailable := errors.New("provider temporarily unavailable")
	provider := &kycProvider{
		startErrors: []error{providerUnavailable},
		checkErrors: []error{providerUnavailable},
	}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now}, &walletIDs{values: []string{
			"case-1", "start-operation-1", "check-operation-1",
			"credential-1", "observation-1",
		}},
	)
	startInput := StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-retry-operation",
	}
	if _, err := service.Start(
		context.Background(), startInput,
	); !errors.Is(err, accountdomain.ErrKYCProviderUnavailable) {
		t.Fatalf("first provider start error=%v, want unavailable", err)
	}
	startOperationKey := operationMapKey(
		"user-1", accountdomain.KYCOperationStart,
		startInput.ClientOperationID,
	)
	if operation := repository.operations[startOperationKey]; operation.State != accountdomain.KYCOperationUnavailable ||
		!operation.Retryable ||
		operation.FailureCode != "PROVIDER_UNAVAILABLE" {
		t.Fatalf("provider start failure was not durable: %#v", operation)
	}
	started, err := service.Start(context.Background(), startInput)
	if err != nil || !started.Replay ||
		len(provider.startKeys) != 2 ||
		provider.startKeys[0] != provider.startKeys[1] {
		t.Fatalf(
			"provider start recovery mismatch: result=%#v err=%v keys=%v",
			started, err, provider.startKeys,
		)
	}

	checkInput := CheckKYCInput{
		UserID:            "user-1",
		CaseID:            started.Case.ID,
		ClientOperationID: "kyc-check-retry-operation",
	}
	if _, err := service.Check(
		context.Background(), checkInput,
	); !errors.Is(err, accountdomain.ErrKYCProviderUnavailable) {
		t.Fatalf("first provider check error=%v, want unavailable", err)
	}
	checkOperationKey := operationMapKey(
		"user-1", accountdomain.KYCOperationCheck,
		checkInput.ClientOperationID,
	)
	if operation := repository.operations[checkOperationKey]; operation.State != accountdomain.KYCOperationUnavailable ||
		!operation.Retryable ||
		operation.FailureCode != "PROVIDER_UNAVAILABLE" {
		t.Fatalf("provider check failure was not durable: %#v", operation)
	}
	checked, err := service.Check(context.Background(), checkInput)
	if err != nil || !checked.Replay ||
		checked.Case.State != accountdomain.KYCStateVerified ||
		len(provider.checkKeys) != 2 ||
		provider.checkKeys[0] != provider.checkKeys[1] {
		t.Fatalf(
			"provider check recovery mismatch: result=%#v err=%v keys=%v",
			checked, err, provider.checkKeys,
		)
	}
}

func TestKYCUnavailableCheckPastEvidenceWindowFailsClosedAndNewKeyRecovers(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	providerUnavailable := errors.New("provider temporarily unavailable")
	provider := &kycProvider{checkErrors: []error{providerUnavailable}}
	ids := &walletIDs{values: []string{
		"case-1", "start-operation-1",
		"stale-check-operation", "fresh-check-operation",
		"credential-1", "observation-1",
	}}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now}, ids,
	)
	started, err := service.Start(context.Background(), StartKYCInput{
		UserID: "user-1", WalletID: "wallet-1",
		OwnershipProofID:  "proof-1",
		ClientOperationID: "kyc-start-long-recovery",
	})
	if err != nil {
		t.Fatal(err)
	}
	staleInput := CheckKYCInput{
		UserID:            "user-1",
		CaseID:            started.Case.ID,
		ClientOperationID: "kyc-check-long-recovery",
	}
	if _, err := service.Check(
		context.Background(), staleInput,
	); !errors.Is(err, accountdomain.ErrKYCProviderUnavailable) {
		t.Fatalf("first check error=%v, want unavailable", err)
	}

	laterService := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now.Add(25 * time.Hour)}, ids,
	)
	if _, err := laterService.Check(
		context.Background(), staleInput,
	); !errors.Is(err, accountdomain.ErrKYCObservationInvalid) {
		t.Fatalf("expired recovered evidence error=%v, want invalid", err)
	}
	staleOperationKey := operationMapKey(
		"user-1", accountdomain.KYCOperationCheck,
		staleInput.ClientOperationID,
	)
	if operation := repository.operations[staleOperationKey]; operation.State != accountdomain.KYCOperationFailed ||
		operation.Retryable ||
		operation.FailureCode != "INVALID_PROVIDER_EVIDENCE" {
		t.Fatalf("stale evidence operation was not closed: %#v", operation)
	}
	if _, err := laterService.Check(
		context.Background(), staleInput,
	); !errors.Is(err, accountdomain.ErrKYCIdempotencyReused) {
		t.Fatalf("failed operation replay error=%v, want reused", err)
	}

	recovered, err := laterService.Check(
		context.Background(),
		CheckKYCInput{
			UserID:            "user-1",
			CaseID:            started.Case.ID,
			ClientOperationID: "kyc-check-fresh-recovery",
		},
	)
	if err != nil ||
		recovered.Case.State != accountdomain.KYCStateVerified ||
		recovered.Credential == nil ||
		recovered.Observation == nil {
		t.Fatalf("new check key did not recover: %#v err=%v", recovered, err)
	}
}

func TestReservedKYCCheckReusesOneProviderOperation(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 30, 0, 0, time.UTC)
	repository := newKYCRepository()
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	repository.wallets[wallet.ID] = wallet
	repository.proofs[proof.ID] = proof
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", "user-1", "wallet-1", "proof-1",
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ProviderStarted(
		"mock-dojang:case-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repository.cases[verification.ID] = verification
	requestHash := digestStrings(
		"user-1", "case-1", "first-check-operation",
	)
	operation, err := accountdomain.NewKYCProviderOperation(
		"check-operation-1", verification,
		accountdomain.KYCOperationCheck, "first-check-operation",
		requestHash, "stable-check-provider-key", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReserveKYCCheck(
		context.Background(), verification, operation,
	); err != nil {
		t.Fatal(err)
	}
	provider := &kycProvider{}
	service := NewKYCService(
		repository, provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		walletClock{now}, &walletIDs{values: []string{
			"discarded-check-operation", "credential-1", "observation-1",
		}},
	)
	result, err := service.Check(context.Background(), CheckKYCInput{
		UserID:            "user-1",
		CaseID:            verification.ID,
		ClientOperationID: "replacement-check-operation",
	})
	if err != nil || result.Case.State != accountdomain.KYCStateVerified ||
		!result.Replay ||
		len(provider.checkKeys) != 1 ||
		provider.checkKeys[0] != "stable-check-provider-key" {
		t.Fatalf(
			"reserved check recovery mismatch: %#v err=%v keys=%v",
			result, err, provider.checkKeys,
		)
	}
	replayed, err := service.Check(context.Background(), CheckKYCInput{
		UserID:            "user-1",
		CaseID:            verification.ID,
		ClientOperationID: "replacement-check-operation",
	})
	if err != nil || !replayed.Replay || provider.checkCalls != 1 {
		t.Fatalf(
			"aliased check operation was not durable: %#v err=%v calls=%d",
			replayed, err, provider.checkCalls,
		)
	}
}

func TestCreatedKYCProjectionOffersStartRecoveryInsteadOfCheck(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 45, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil,
		nil,
		[]accountdomain.KYCVerificationCase{verification},
		nil,
		now.Add(KYCStartOwnershipProofMaximumAge+time.Second),
	)
	if len(projected) != 1 ||
		!projected[0].Actions.CanStartKYC ||
		projected[0].Actions.CanCheckKYC ||
		projected[0].KYC.NextAction.Kind !=
			accountdomain.WalletNextActionStartKYC {
		t.Fatalf("CREATED KYC recovery action mismatch: %#v", projected)
	}
}

func TestKYCProjectionExposesRetryableProviderUnavailable(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 47, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := accountdomain.NewKYCProviderOperation(
		"operation-1", verification, accountdomain.KYCOperationStart,
		"provider-unavailable-start", "request-hash",
		"provider-request-key", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err = operation.ProviderUnavailable(
		"PROVIDER_UNAVAILABLE", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil, nil,
		[]accountdomain.KYCVerificationCase{verification},
		[]accountdomain.KYCProviderOperation{operation},
		now.Add(time.Second),
	)
	kyc := projected[0].KYC
	if kyc.FailureCode != "PROVIDER_UNAVAILABLE" ||
		kyc.Retryable == nil || !*kyc.Retryable ||
		kyc.NextAction.Kind != accountdomain.WalletNextActionStartKYC ||
		!projected[0].Actions.CanStartKYC ||
		projected[0].Actions.CanCheckKYC {
		t.Fatalf("retryable provider failure projection mismatch: %#v", projected)
	}
}

func TestKYCProjectionBlocksNonretryableProviderFailure(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 48, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ProviderStarted(
		"mock-dojang:case-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := accountdomain.NewKYCProviderOperation(
		"operation-1", verification, accountdomain.KYCOperationCheck,
		"invalid-provider-check", "request-hash",
		"provider-request-key", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err = operation.Fail(
		"INVALID_PROVIDER_RESULT", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil, nil,
		[]accountdomain.KYCVerificationCase{verification},
		[]accountdomain.KYCProviderOperation{operation},
		now.Add(time.Second),
	)
	kyc := projected[0].KYC
	if kyc.FailureCode != "INVALID_PROVIDER_RESULT" ||
		kyc.Retryable == nil || *kyc.Retryable ||
		kyc.NextAction.Kind != accountdomain.WalletNextActionContactSupport ||
		projected[0].Actions.CanStartKYC ||
		projected[0].Actions.CanCheckKYC {
		t.Fatalf("nonretryable provider failure projection mismatch: %#v", projected)
	}
}

func TestKYCProjectionPreservesRetryableTerminalCaseReason(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 49, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ProviderStarted(
		"mock-dojang:case-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ApplyCheck(
		accountdomain.KYCStateRejected, nil,
		"MOCK_DOJANG_REJECTED", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil, nil,
		[]accountdomain.KYCVerificationCase{verification},
		nil,
		now.Add(time.Second),
	)
	kyc := projected[0].KYC
	if kyc.Eligibility != KYCEligibilityRejected ||
		kyc.FailureCode != "MOCK_DOJANG_REJECTED" ||
		kyc.Retryable == nil || !*kyc.Retryable ||
		kyc.NextAction.Kind != accountdomain.WalletNextActionStartKYC ||
		!projected[0].Actions.CanStartKYC {
		t.Fatalf("terminal KYC reason projection mismatch: %#v", projected)
	}
}

func TestKYCProjectionAllowsNewCaseAfterDeregisteredWalletIsRegisteredAgain(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 7, 49, 30, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-2",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-before-reregistration", wallet.UserID, wallet.ID,
		"proof-before-reregistration",
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := accountdomain.NewKYCProviderOperation(
		"operation-before-reregistration", verification,
		accountdomain.KYCOperationStart,
		"wallet-deregistered-start", "request-hash",
		"provider-request-key", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.CancelBeforeProviderStarted(
		"WALLET_DEREGISTERED", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err = operation.Fail(
		"WALLET_DEREGISTERED", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil, nil,
		[]accountdomain.KYCVerificationCase{verification},
		[]accountdomain.KYCProviderOperation{operation},
		now.Add(time.Second),
	)
	kyc := projected[0].KYC
	if kyc.FailureCode != "WALLET_DEREGISTERED" ||
		kyc.Retryable == nil || !*kyc.Retryable ||
		kyc.NextAction.Kind != accountdomain.WalletNextActionStartKYC ||
		!projected[0].Actions.CanStartKYC {
		t.Fatalf("re-registered Wallet KYC recovery mismatch: %#v", projected)
	}
}

func TestDefaultWalletProjectionDoesNotOfferSetDefault(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 50, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	wallet.IsDefault = true
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		nil, nil, nil, nil, now,
	)
	if len(projected) != 1 ||
		projected[0].Actions.CanSetDefault ||
		!projected[0].Actions.CanDeregister {
		t.Fatalf("default Wallet actions mismatch: %#v", projected)
	}
}

func TestWalletProjectionJoinsOwnershipAndLatestKYCEvidence(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ProviderStarted(
		"mock-dojang:case-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := accountdomain.NewKYCCredential(
		"credential-1", verification, wallet,
		"vitlane.mock-dojang.v2", "mock-kyc-test-product.v2",
		"0xevidence", now, now.Add(24*time.Hour), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	credentialID := credential.ID
	verification, err = verification.ApplyCheck(
		accountdomain.KYCStateVerified, &credentialID, "", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := accountdomain.NewKYCEvidenceObservation(
		"observation-1", credential, accountdomain.KYCEvidenceValid,
		"0xevidence", 1, now, now.Add(24*time.Hour),
		now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		[]accountdomain.KYCCredential{credential},
		[]accountdomain.KYCEvidenceObservation{observation},
		[]accountdomain.KYCVerificationCase{verification},
		nil,
		now,
	)
	if len(projected) != 1 ||
		projected[0].Ownership.Status != accountdomain.WalletOwnershipValid ||
		projected[0].KYC.Eligibility != KYCEligibilityValid ||
		!projected[0].KYC.ActionEligible ||
		projected[0].KYC.Credential == nil ||
		projected[0].KYC.Observation == nil ||
		projected[0].KYC.NextAction.Kind !=
			accountdomain.WalletNextActionNone ||
		projected[0].KYC.Disclosure != MockDojangDisclosure ||
		projected[0].Actions.CanStartKYC ||
		projected[0].Actions.CanCheckKYC {
		t.Fatalf("wallet projection mismatch: %#v", projected)
	}
	deregisteredAt := now.Add(time.Minute)
	wallet.RegistrationStatus = accountdomain.WalletDeregistered
	wallet.CurrentOwnershipProofID = nil
	wallet.DeregisteredAt = &deregisteredAt
	projected = projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		[]accountdomain.KYCCredential{credential},
		[]accountdomain.KYCEvidenceObservation{observation},
		[]accountdomain.KYCVerificationCase{verification},
		nil,
		now,
	)
	if projected[0].KYC.Eligibility != KYCEligibilityValid ||
		projected[0].KYC.ActionEligible ||
		projected[0].Actions.CanStartKYC ||
		projected[0].Actions.CanCheckKYC {
		t.Fatalf(
			"historical KYC evidence became current deregistered-wallet authority: %#v",
			projected[0],
		)
	}
}

func TestWalletProjectionSeparatesRecheckRequiredFromExpired(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	wallet, proof := registeredWalletAndProof(
		t, "user-1", "wallet-1", "proof-1",
		"0x1111111111111111111111111111111111111111", now,
	)
	verification, err := accountdomain.NewKYCVerificationCase(
		"case-1", wallet.UserID, wallet.ID, proof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	verification, err = verification.ProviderStarted(
		"mock-dojang:case-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := accountdomain.NewKYCCredential(
		"credential-1", verification, wallet,
		"vitlane.mock-dojang.v2", "mock-kyc-test-product.v2",
		"0xevidence", now, now.Add(24*time.Hour), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	credentialID := credential.ID
	verification, err = verification.ApplyCheck(
		accountdomain.KYCStateVerified, &credentialID, "", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := accountdomain.NewKYCEvidenceObservation(
		"observation-1", credential, accountdomain.KYCEvidenceValid,
		"0xevidence", 1, now, now.Add(24*time.Hour),
		now.Add(30*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectWallets(
		[]accountdomain.Wallet{wallet},
		[]accountdomain.WalletOwnershipProof{proof},
		[]accountdomain.KYCCredential{credential},
		[]accountdomain.KYCEvidenceObservation{observation},
		[]accountdomain.KYCVerificationCase{verification},
		nil,
		now.Add(time.Hour),
	)
	if projected[0].KYC.Eligibility != KYCEligibilityRecheck ||
		projected[0].KYC.NextAction.Kind != accountdomain.WalletNextActionRecheck {
		t.Fatalf("KYC freshness must require recheck without expiring evidence: %#v",
			projected[0].KYC)
	}
}

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

const KYCStartOwnershipProofMaximumAge = 10 * time.Minute

// IdentityAssuranceEvidence is kept only as the read-only live Dojang adapter
// contract. It is not persisted by the Wallet/KYC redesign.
type IdentityAssuranceEvidence struct {
	Level        accountdomain.AssuranceLevel
	IssuerRef    string
	SchemaRef    string
	EvidenceHash string
	VerifiedAt   time.Time
	ExpiresAt    time.Time
}

type IdentityAssuranceProvider interface {
	Verify(context.Context, string, time.Time) (IdentityAssuranceEvidence, error)
}

type KYCProviderStartRequest struct {
	CaseID             string
	UserID             accountdomain.UserID
	WalletID           accountdomain.WalletID
	OwnershipProofID   accountdomain.WalletOwnershipProofID
	Address            string
	ChainID            string
	ProviderRequestKey string
	Now                time.Time
}

type KYCProviderStartResult struct {
	ProviderCaseRef string
	State           accountdomain.KYCVerificationState
	FailureCode     string
}

type KYCProviderCheckRequest struct {
	ProviderCaseRef    string
	Address            string
	ProviderRequestKey string
	Now                time.Time
}

type KYCCredentialEvidence struct {
	IssuerRef         string
	SchemaRef         string
	EvidenceHash      string
	IssuedAt          time.Time
	ValidUntil        time.Time
	ObservationStatus accountdomain.KYCEvidenceStatus
	SourceVersion     int64
	ObservedAt        time.Time
	RecheckAfter      time.Time
}

type KYCProviderCheckResult struct {
	State       accountdomain.KYCVerificationState
	Evidence    *KYCCredentialEvidence
	FailureCode string
}

type KYCProvider interface {
	Start(context.Context, KYCProviderStartRequest) (KYCProviderStartResult, error)
	Check(context.Context, KYCProviderCheckRequest) (KYCProviderCheckResult, error)
}

type KYCOperationRecord struct {
	Case        accountdomain.KYCVerificationCase
	Operation   accountdomain.KYCProviderOperation
	Credential  *accountdomain.KYCCredential
	Observation *accountdomain.KYCEvidenceObservation
}

type KYCRepository interface {
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
	FindKYCVerificationCase(
		context.Context,
		accountdomain.UserID,
		string,
	) (accountdomain.KYCVerificationCase, error)
	FindKYCProviderOperation(
		context.Context,
		accountdomain.UserID,
		accountdomain.KYCProviderOperationKind,
		string,
		string,
	) (KYCOperationRecord, bool, error)
	ReserveKYCStart(
		context.Context,
		accountdomain.KYCVerificationCase,
		accountdomain.KYCProviderOperation,
	) (KYCOperationRecord, bool, error)
	CompleteKYCStart(
		context.Context,
		accountdomain.KYCVerificationCase,
		accountdomain.KYCProviderOperation,
		time.Time,
	) (KYCOperationRecord, bool, error)
	ReserveKYCCheck(
		context.Context,
		accountdomain.KYCVerificationCase,
		accountdomain.KYCProviderOperation,
	) (KYCOperationRecord, bool, error)
	CompleteKYCCheck(
		context.Context,
		accountdomain.KYCVerificationCase,
		*accountdomain.KYCCredential,
		*accountdomain.KYCEvidenceObservation,
		accountdomain.KYCProviderOperation,
		time.Time,
	) (KYCOperationRecord, bool, error)
	CompleteKYCProviderFailure(
		context.Context,
		accountdomain.KYCProviderOperation,
		time.Time,
	) (KYCOperationRecord, bool, error)
}

type KYCService struct {
	repository     KYCRepository
	provider       KYCProvider
	providerKind   accountdomain.KYCProviderKind
	externalEffect accountdomain.KYCExternalEffect
	clock          sharedapp.Clock
	ids            sharedapp.IDGenerator
	finalizer      *runtimepolicy.Finalizer
}

func (s *KYCService) EnableFinalizer(finalizer runtimepolicy.Finalizer) {
	s.finalizer = &finalizer
}

func NewKYCService(
	repository KYCRepository,
	provider KYCProvider,
	providerKind accountdomain.KYCProviderKind,
	externalEffect accountdomain.KYCExternalEffect,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *KYCService {
	return &KYCService{
		repository:     repository,
		provider:       provider,
		providerKind:   providerKind,
		externalEffect: externalEffect,
		clock:          clock,
		ids:            ids,
	}
}

type StartKYCInput struct {
	UserID            string
	WalletID          string
	OwnershipProofID  string
	ClientOperationID string
}

type KYCStartResult struct {
	Case   accountdomain.KYCVerificationCase `json:"kycCase"`
	Replay bool                              `json:"replay"`
}

func (s *KYCService) Start(
	ctx context.Context,
	input StartKYCInput,
) (KYCStartResult, error) {
	if !s.mockProviderEnabled() ||
		!validClientOperationID(input.ClientOperationID) {
		return KYCStartResult{}, accountdomain.ErrKYCVerificationInvalid
	}
	userID := accountdomain.UserID(input.UserID)
	walletID := accountdomain.WalletID(input.WalletID)
	proofID := accountdomain.WalletOwnershipProofID(input.OwnershipProofID)
	clientOperationID := strings.TrimSpace(input.ClientOperationID)
	requestHash := digestStrings(
		string(userID), string(walletID), string(proofID), clientOperationID,
	)
	record, found, err := s.repository.FindKYCProviderOperation(
		ctx,
		userID,
		accountdomain.KYCOperationStart,
		clientOperationID,
		requestHash,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	if found {
		switch record.Operation.State {
		case accountdomain.KYCOperationSucceeded:
			return KYCStartResult{Case: record.Case, Replay: true}, nil
		case accountdomain.KYCOperationReserved:
			return s.resumeKYCStart(ctx, record, true)
		case accountdomain.KYCOperationUnavailable:
			if !record.Operation.Retryable {
				return KYCStartResult{},
					accountdomain.ErrKYCIdempotencyReused
			}
			return s.resumeKYCStart(ctx, record, true)
		default:
			return KYCStartResult{}, accountdomain.ErrKYCIdempotencyReused
		}
	}

	wallet, proof, err := s.requireFreshOwnershipProof(
		ctx, userID, walletID, proofID,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	now := s.clock.Now()
	verification, err := accountdomain.NewKYCVerificationCase(
		s.ids.NewID(),
		wallet.UserID,
		wallet.ID,
		proof.ID,
		s.providerKind,
		s.externalEffect,
		now,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	operationID := accountdomain.KYCProviderOperationID(s.ids.NewID())
	operation, err := accountdomain.NewKYCProviderOperation(
		operationID,
		verification,
		accountdomain.KYCOperationStart,
		clientOperationID,
		requestHash,
		"kyc:start:"+string(operationID),
		now,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	record, replay, err := s.repository.ReserveKYCStart(
		ctx, verification, operation,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	if replay && record.Operation.State == accountdomain.KYCOperationSucceeded {
		return KYCStartResult{Case: record.Case, Replay: true}, nil
	}
	return s.resumeKYCStart(ctx, record, replay)
}

func (s *KYCService) resumeKYCStart(
	ctx context.Context,
	record KYCOperationRecord,
	replay bool,
) (KYCStartResult, error) {
	wallet, err := s.repository.FindWallet(
		ctx, record.Case.UserID, record.Case.WalletID,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	now := s.clock.Now()
	providerResult, err := s.provider.Start(ctx, KYCProviderStartRequest{
		CaseID:             record.Case.ID,
		UserID:             record.Case.UserID,
		WalletID:           record.Case.WalletID,
		OwnershipProofID:   record.Case.StartedWithOwnershipProofID,
		Address:            wallet.Address,
		ChainID:            wallet.ChainID,
		ProviderRequestKey: record.Operation.ProviderRequestKey,
		Now:                record.Operation.CreatedAt,
	})
	if err != nil {
		if failureErr := s.recordProviderUnavailable(
			ctx, record.Operation, now,
		); failureErr != nil {
			return KYCStartResult{}, failureErr
		}
		return KYCStartResult{}, fmt.Errorf(
			"%w: %w", accountdomain.ErrKYCProviderUnavailable, err,
		)
	}
	if providerResult.State != accountdomain.KYCStatePendingProvider ||
		strings.TrimSpace(providerResult.ProviderCaseRef) == "" ||
		strings.TrimSpace(providerResult.FailureCode) != "" {
		if failureErr := s.recordProviderFailure(
			ctx, record.Operation, "INVALID_PROVIDER_RESULT", now,
		); failureErr != nil {
			return KYCStartResult{}, failureErr
		}
		return KYCStartResult{}, accountdomain.ErrKYCVerificationInvalid
	}
	nextCase, err := record.Case.ProviderStarted(
		providerResult.ProviderCaseRef, now,
	)
	if err != nil {
		return KYCStartResult{}, err
	}
	nextOperation, err := record.Operation.Succeed(nextCase, nil, now)
	if err != nil {
		return KYCStartResult{}, err
	}
	finalizeContext, cancel := s.finalizationContext(ctx)
	defer cancel()
	completed, completeReplay, err := s.repository.CompleteKYCStart(
		finalizeContext, nextCase, nextOperation, now,
	)
	if err != nil {
		return KYCStartResult{}, s.persistenceError(err)
	}
	return KYCStartResult{
		Case: completed.Case, Replay: replay || completeReplay,
	}, nil
}

type CheckKYCInput struct {
	UserID            string
	CaseID            string
	ClientOperationID string
}

type KYCCheckResult struct {
	Case        accountdomain.KYCVerificationCase     `json:"kycCase"`
	Credential  *accountdomain.KYCCredential          `json:"credential,omitempty"`
	Observation *accountdomain.KYCEvidenceObservation `json:"observation,omitempty"`
	Replay      bool                                  `json:"replay"`
}

func (s *KYCService) Check(
	ctx context.Context,
	input CheckKYCInput,
) (KYCCheckResult, error) {
	if !s.mockProviderEnabled() ||
		!validClientOperationID(input.ClientOperationID) {
		return KYCCheckResult{}, accountdomain.ErrKYCVerificationInvalid
	}
	userID := accountdomain.UserID(input.UserID)
	clientOperationID := strings.TrimSpace(input.ClientOperationID)
	requestHash := digestStrings(
		string(userID), strings.TrimSpace(input.CaseID), clientOperationID,
	)
	record, found, err := s.repository.FindKYCProviderOperation(
		ctx,
		userID,
		accountdomain.KYCOperationCheck,
		clientOperationID,
		requestHash,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	if found {
		switch record.Operation.State {
		case accountdomain.KYCOperationSucceeded:
			return kycCheckResult(record, true), nil
		case accountdomain.KYCOperationReserved:
			return s.resumeKYCCheck(ctx, record, true)
		case accountdomain.KYCOperationUnavailable:
			if !record.Operation.Retryable {
				return KYCCheckResult{},
					accountdomain.ErrKYCIdempotencyReused
			}
			return s.resumeKYCCheck(ctx, record, true)
		default:
			return KYCCheckResult{}, accountdomain.ErrKYCIdempotencyReused
		}
	}

	verification, err := s.repository.FindKYCVerificationCase(
		ctx, userID, input.CaseID,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	if !verification.CanCheck() {
		return KYCCheckResult{}, accountdomain.ErrKYCVerificationState
	}
	now := s.clock.Now()
	operationID := accountdomain.KYCProviderOperationID(s.ids.NewID())
	operation, err := accountdomain.NewKYCProviderOperation(
		operationID,
		verification,
		accountdomain.KYCOperationCheck,
		clientOperationID,
		requestHash,
		"kyc:check:"+string(operationID),
		now,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	record, replay, err := s.repository.ReserveKYCCheck(
		ctx, verification, operation,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	if replay && record.Operation.State == accountdomain.KYCOperationSucceeded {
		return kycCheckResult(record, true), nil
	}
	return s.resumeKYCCheck(ctx, record, replay)
}

func (s *KYCService) resumeKYCCheck(
	ctx context.Context,
	record KYCOperationRecord,
	replay bool,
) (KYCCheckResult, error) {
	if !record.Case.CanCheck() ||
		record.Case.ProviderCaseRef == nil {
		return KYCCheckResult{}, accountdomain.ErrKYCVerificationState
	}
	wallet, err := s.repository.FindWallet(
		ctx, record.Case.UserID, record.Case.WalletID,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	now := s.clock.Now()
	providerResult, err := s.provider.Check(ctx, KYCProviderCheckRequest{
		ProviderCaseRef:    *record.Case.ProviderCaseRef,
		Address:            wallet.Address,
		ProviderRequestKey: record.Operation.ProviderRequestKey,
		Now:                record.Operation.CreatedAt,
	})
	if err != nil {
		if failureErr := s.recordProviderUnavailable(
			ctx, record.Operation, now,
		); failureErr != nil {
			return KYCCheckResult{}, failureErr
		}
		return KYCCheckResult{}, fmt.Errorf(
			"%w: %w", accountdomain.ErrKYCProviderUnavailable, err,
		)
	}

	var credential *accountdomain.KYCCredential
	var observation *accountdomain.KYCEvidenceObservation
	var credentialID *accountdomain.KYCCredentialID
	switch providerResult.State {
	case accountdomain.KYCStatePendingProvider:
		if providerResult.Evidence != nil ||
			strings.TrimSpace(providerResult.FailureCode) != "" {
			if failureErr := s.recordProviderFailure(
				ctx, record.Operation, "INVALID_PROVIDER_RESULT", now,
			); failureErr != nil {
				return KYCCheckResult{}, failureErr
			}
			return KYCCheckResult{}, accountdomain.ErrKYCVerificationInvalid
		}
	case accountdomain.KYCStateVerified:
		if providerResult.Evidence == nil {
			if failureErr := s.recordProviderFailure(
				ctx, record.Operation, "MISSING_PROVIDER_EVIDENCE", now,
			); failureErr != nil {
				return KYCCheckResult{}, failureErr
			}
			return KYCCheckResult{}, accountdomain.ErrKYCCredentialInvalid
		}
		persistableCredential, persistableObservation, err :=
			s.newKYCEvidenceRecords(
				record.Case, wallet, *providerResult.Evidence, now,
			)
		if err != nil {
			if failureErr := s.recordProviderFailure(
				ctx, record.Operation, "INVALID_PROVIDER_EVIDENCE", now,
			); failureErr != nil {
				return KYCCheckResult{}, failureErr
			}
			return KYCCheckResult{}, err
		}
		credential = &persistableCredential
		observation = &persistableObservation
		id := persistableCredential.ID
		credentialID = &id
	case accountdomain.KYCStateRejected, accountdomain.KYCStateExpired:
		if providerResult.Evidence != nil ||
			strings.TrimSpace(providerResult.FailureCode) == "" {
			if failureErr := s.recordProviderFailure(
				ctx, record.Operation, "INVALID_PROVIDER_RESULT", now,
			); failureErr != nil {
				return KYCCheckResult{}, failureErr
			}
			return KYCCheckResult{}, accountdomain.ErrKYCVerificationInvalid
		}
	default:
		if failureErr := s.recordProviderFailure(
			ctx, record.Operation, "INVALID_PROVIDER_RESULT", now,
		); failureErr != nil {
			return KYCCheckResult{}, failureErr
		}
		return KYCCheckResult{}, accountdomain.ErrKYCVerificationInvalid
	}
	nextCase, err := record.Case.ApplyCheck(
		providerResult.State,
		credentialID,
		providerResult.FailureCode,
		now,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	nextOperation, err := record.Operation.Succeed(
		nextCase, credentialID, now,
	)
	if err != nil {
		return KYCCheckResult{}, err
	}
	finalizeContext, cancel := s.finalizationContext(ctx)
	defer cancel()
	completed, completeReplay, err := s.repository.CompleteKYCCheck(
		finalizeContext,
		nextCase,
		credential,
		observation,
		nextOperation,
		now,
	)
	if err != nil {
		return KYCCheckResult{}, s.persistenceError(err)
	}
	return kycCheckResult(completed, replay || completeReplay), nil
}

func (s *KYCService) recordProviderUnavailable(
	ctx context.Context,
	operation accountdomain.KYCProviderOperation,
	now time.Time,
) error {
	ctx, cancel := s.finalizationContext(ctx)
	defer cancel()
	unavailable, err := operation.ProviderUnavailable(
		"PROVIDER_UNAVAILABLE", now,
	)
	if err != nil {
		return err
	}
	_, _, err = s.repository.CompleteKYCProviderFailure(
		ctx, unavailable, now,
	)
	return err
}

func (s *KYCService) recordProviderFailure(
	ctx context.Context,
	operation accountdomain.KYCProviderOperation,
	failureCode string,
	now time.Time,
) error {
	ctx, cancel := s.finalizationContext(ctx)
	defer cancel()
	failed, err := operation.Fail(failureCode, now)
	if err != nil {
		return err
	}
	_, _, err = s.repository.CompleteKYCProviderFailure(
		ctx, failed, now,
	)
	return err
}

func (s *KYCService) finalizationContext(
	caller context.Context,
) (context.Context, context.CancelFunc) {
	if s.finalizer != nil {
		return s.finalizer.Context()
	}
	return runtimepolicy.WithTimeout(caller, 3*time.Second)
}

func (s *KYCService) persistenceError(err error) error {
	if s.externalEffect == accountdomain.KYCEffectSimulated {
		return err
	}
	return fault.Wrap(
		err, fault.ExternalEffectUnknown, "KYC_PROVIDER_EFFECT_UNKNOWN", false,
	)
}

func (s *KYCService) requireFreshOwnershipProof(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (
	accountdomain.Wallet,
	accountdomain.WalletOwnershipProof,
	error,
) {
	wallet, err := s.repository.FindWallet(ctx, userID, walletID)
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{}, err
	}
	if !wallet.IsRegistered() {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletNotRegistered
	}
	if proofID == "" ||
		wallet.CurrentOwnershipProofID == nil ||
		*wallet.CurrentOwnershipProofID != proofID {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	proof, err := s.repository.FindWalletOwnershipProof(
		ctx, userID, walletID, proofID,
	)
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{}, err
	}
	now := s.clock.Now()
	if proof.UserID != wallet.UserID ||
		proof.WalletID != wallet.ID ||
		proof.ID != proofID ||
		!proof.ValidAt(now) {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofExpired
	}
	if !proof.FreshAt(now, KYCStartOwnershipProofMaximumAge) {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofNotFresh
	}
	return wallet, proof, nil
}

func (s *KYCService) mockProviderEnabled() bool {
	return s.provider != nil &&
		s.providerKind == accountdomain.KYCProviderMockDojang &&
		s.externalEffect == accountdomain.KYCEffectSimulated
}

func (s *KYCService) newKYCEvidenceRecords(
	verification accountdomain.KYCVerificationCase,
	wallet accountdomain.Wallet,
	evidence KYCCredentialEvidence,
	now time.Time,
) (
	accountdomain.KYCCredential,
	accountdomain.KYCEvidenceObservation,
	error,
) {
	if evidence.IssuedAt.After(now) ||
		!evidence.ValidUntil.After(now) ||
		!evidence.ValidUntil.After(evidence.IssuedAt) ||
		evidence.ObservationStatus != accountdomain.KYCEvidenceValid ||
		evidence.SourceVersion <= 0 ||
		evidence.ObservedAt.After(now) ||
		evidence.RecheckAfter.Before(evidence.ObservedAt) {
		return accountdomain.KYCCredential{},
			accountdomain.KYCEvidenceObservation{},
			accountdomain.ErrKYCObservationInvalid
	}
	credential, err := accountdomain.NewKYCCredential(
		accountdomain.KYCCredentialID(s.ids.NewID()),
		verification,
		wallet,
		evidence.IssuerRef,
		evidence.SchemaRef,
		evidence.EvidenceHash,
		evidence.IssuedAt,
		evidence.ValidUntil,
		now,
	)
	if err != nil {
		return accountdomain.KYCCredential{},
			accountdomain.KYCEvidenceObservation{}, err
	}
	observation, err := accountdomain.NewKYCEvidenceObservation(
		accountdomain.KYCEvidenceObservationID(s.ids.NewID()),
		credential,
		evidence.ObservationStatus,
		evidence.EvidenceHash,
		evidence.SourceVersion,
		evidence.ObservedAt,
		evidence.ValidUntil,
		evidence.RecheckAfter,
	)
	if err != nil {
		return accountdomain.KYCCredential{},
			accountdomain.KYCEvidenceObservation{}, err
	}
	return credential, observation, nil
}

func kycCheckResult(
	record KYCOperationRecord,
	replay bool,
) KYCCheckResult {
	return KYCCheckResult{
		Case: record.Case, Credential: record.Credential,
		Observation: record.Observation, Replay: replay,
	}
}

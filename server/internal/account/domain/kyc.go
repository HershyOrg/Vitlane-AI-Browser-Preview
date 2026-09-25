package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrKYCVerificationInvalid = errors.New("KYC_VERIFICATION_INVALID")
	ErrKYCVerificationState   = errors.New("KYC_VERIFICATION_STATE_INVALID")
	ErrKYCVerificationMissing = errors.New("KYC_VERIFICATION_NOT_FOUND")
	ErrKYCIdempotencyReused   = errors.New("KYC_IDEMPOTENCY_KEY_REUSED")
	ErrKYCProviderUnavailable = errors.New("KYC_PROVIDER_UNAVAILABLE")
	ErrKYCCredentialInvalid   = errors.New("KYC_CREDENTIAL_INVALID")
	ErrKYCObservationInvalid  = errors.New("KYC_EVIDENCE_OBSERVATION_INVALID")
)

type KYCProviderKind string
type KYCExternalEffect string
type KYCVerificationState string
type KYCProviderOperationID string
type KYCProviderOperationKind string
type KYCProviderOperationState string
type KYCCredentialID string
type KYCCredentialLevel string
type KYCEvidenceObservationID string
type KYCEvidenceStatus string

const (
	KYCProviderDojang     KYCProviderKind = "DOJANG"
	KYCProviderMockDojang KYCProviderKind = "MOCK_DOJANG"

	KYCEffectLive      KYCExternalEffect = "LIVE"
	KYCEffectSimulated KYCExternalEffect = "SIMULATED"

	KYCStateCreated         KYCVerificationState = "CREATED"
	KYCStatePendingProvider KYCVerificationState = "PENDING_PROVIDER"
	KYCStateVerified        KYCVerificationState = "VERIFIED"
	KYCStateRejected        KYCVerificationState = "REJECTED"
	KYCStateExpired         KYCVerificationState = "EXPIRED"
	KYCStateCancelled       KYCVerificationState = "CANCELLED"

	KYCCredentialMockDojangVerified KYCCredentialLevel = "MOCK_DOJANG_VERIFIED"

	KYCEvidenceValid   KYCEvidenceStatus = "VALID"
	KYCEvidenceExpired KYCEvidenceStatus = "EXPIRED"
	KYCEvidenceRevoked KYCEvidenceStatus = "REVOKED"

	KYCOperationStart  KYCProviderOperationKind = "START"
	KYCOperationCheck  KYCProviderOperationKind = "CHECK"
	KYCOperationCancel KYCProviderOperationKind = "CANCEL"

	KYCOperationReserved    KYCProviderOperationState = "RESERVED"
	KYCOperationSucceeded   KYCProviderOperationState = "SUCCEEDED"
	KYCOperationUnavailable KYCProviderOperationState = "UNAVAILABLE"
	KYCOperationFailed      KYCProviderOperationState = "FAILED"

	MockDojangProviderVersion = "vitlane.mock-dojang.v2"
)

type KYCVerificationCase struct {
	ID                          string                 `json:"id"`
	UserID                      UserID                 `json:"userId"`
	WalletID                    WalletID               `json:"walletId"`
	StartedWithOwnershipProofID WalletOwnershipProofID `json:"startedWithOwnershipProofId"`
	RequestedLevel              string                 `json:"requestedLevel"`
	ProviderKind                KYCProviderKind        `json:"providerKind"`
	ProviderVersion             string                 `json:"providerVersion"`
	ExternalEffect              KYCExternalEffect      `json:"externalEffect"`
	State                       KYCVerificationState   `json:"state"`
	ProviderCaseRef             *string                `json:"providerCaseRef,omitempty"`
	CredentialID                *KYCCredentialID       `json:"credentialId,omitempty"`
	FailureCode                 string                 `json:"failureCode,omitempty"`
	CreatedAt                   time.Time              `json:"createdAt"`
	UpdatedAt                   time.Time              `json:"updatedAt"`
	CompletedAt                 *time.Time             `json:"completedAt,omitempty"`
}

func NewKYCVerificationCase(
	id string,
	userID UserID,
	walletID WalletID,
	ownershipProofID WalletOwnershipProofID,
	providerKind KYCProviderKind,
	externalEffect KYCExternalEffect,
	now time.Time,
) (KYCVerificationCase, error) {
	if strings.TrimSpace(id) == "" ||
		userID == "" ||
		walletID == "" ||
		ownershipProofID == "" ||
		providerKind != KYCProviderMockDojang ||
		externalEffect != KYCEffectSimulated {
		return KYCVerificationCase{}, ErrKYCVerificationInvalid
	}
	return KYCVerificationCase{
		ID:                          strings.TrimSpace(id),
		UserID:                      userID,
		WalletID:                    walletID,
		StartedWithOwnershipProofID: ownershipProofID,
		RequestedLevel:              "ADVANCED_TEST_KYC",
		ProviderKind:                providerKind,
		ProviderVersion:             MockDojangProviderVersion,
		ExternalEffect:              externalEffect,
		State:                       KYCStateCreated,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}, nil
}

func (c KYCVerificationCase) CanCheck() bool {
	return c.State == KYCStatePendingProvider
}

func (c KYCVerificationCase) ProviderStarted(
	providerCaseRef string,
	now time.Time,
) (KYCVerificationCase, error) {
	if c.State != KYCStateCreated ||
		c.ProviderCaseRef != nil ||
		strings.TrimSpace(providerCaseRef) == "" {
		return KYCVerificationCase{}, ErrKYCVerificationState
	}
	next := c
	ref := strings.TrimSpace(providerCaseRef)
	next.ProviderCaseRef = &ref
	next.State = KYCStatePendingProvider
	next.UpdatedAt = now
	return next, nil
}

func (c KYCVerificationCase) CancelBeforeProviderStarted(
	failureCode string,
	now time.Time,
) (KYCVerificationCase, error) {
	if c.State != KYCStateCreated ||
		c.ProviderCaseRef != nil ||
		strings.TrimSpace(failureCode) == "" {
		return KYCVerificationCase{}, ErrKYCVerificationState
	}
	next := c
	next.State = KYCStateCancelled
	next.FailureCode = strings.TrimSpace(failureCode)
	next.UpdatedAt = now
	completedAt := now
	next.CompletedAt = &completedAt
	return next, nil
}

func (c KYCVerificationCase) ApplyCheck(
	state KYCVerificationState,
	credentialID *KYCCredentialID,
	failureCode string,
	now time.Time,
) (KYCVerificationCase, error) {
	if !c.CanCheck() {
		return KYCVerificationCase{}, ErrKYCVerificationState
	}
	switch state {
	case KYCStatePendingProvider:
		if credentialID != nil || strings.TrimSpace(failureCode) != "" {
			return KYCVerificationCase{}, ErrKYCVerificationInvalid
		}
	case KYCStateVerified:
		if credentialID == nil || *credentialID == "" ||
			strings.TrimSpace(failureCode) != "" {
			return KYCVerificationCase{}, ErrKYCVerificationInvalid
		}
	case KYCStateRejected, KYCStateExpired:
		if credentialID != nil || strings.TrimSpace(failureCode) == "" {
			return KYCVerificationCase{}, ErrKYCVerificationInvalid
		}
	default:
		return KYCVerificationCase{}, ErrKYCVerificationInvalid
	}
	next := c
	next.State = state
	next.CredentialID = credentialID
	next.FailureCode = strings.TrimSpace(failureCode)
	next.UpdatedAt = now
	if state != KYCStatePendingProvider {
		completedAt := now
		next.CompletedAt = &completedAt
	}
	return next, nil
}

type KYCProviderOperation struct {
	ID                 KYCProviderOperationID    `json:"id"`
	UserID             UserID                    `json:"userId"`
	WalletID           WalletID                  `json:"walletId"`
	CaseID             string                    `json:"caseId"`
	Kind               KYCProviderOperationKind  `json:"kind"`
	ClientOperationID  string                    `json:"clientOperationId"`
	RequestHash        string                    `json:"-"`
	ProviderRequestKey string                    `json:"providerRequestKey"`
	State              KYCProviderOperationState `json:"state"`
	ProviderCaseRef    *string                   `json:"providerCaseRef,omitempty"`
	ResultCaseState    *KYCVerificationState     `json:"resultCaseState,omitempty"`
	ResultCredentialID *KYCCredentialID          `json:"resultCredentialId,omitempty"`
	FailureCode        string                    `json:"failureCode,omitempty"`
	Retryable          bool                      `json:"retryable"`
	CreatedAt          time.Time                 `json:"createdAt"`
	UpdatedAt          time.Time                 `json:"updatedAt"`
	CompletedAt        *time.Time                `json:"completedAt,omitempty"`
}

func NewKYCProviderOperation(
	id KYCProviderOperationID,
	verification KYCVerificationCase,
	kind KYCProviderOperationKind,
	clientOperationID, requestHash, providerRequestKey string,
	now time.Time,
) (KYCProviderOperation, error) {
	if id == "" ||
		verification.ID == "" ||
		(kind != KYCOperationStart &&
			kind != KYCOperationCheck &&
			kind != KYCOperationCancel) ||
		!validOperationID(clientOperationID) ||
		strings.TrimSpace(requestHash) == "" ||
		strings.TrimSpace(providerRequestKey) == "" {
		return KYCProviderOperation{}, ErrKYCVerificationInvalid
	}
	return KYCProviderOperation{
		ID:                 id,
		UserID:             verification.UserID,
		WalletID:           verification.WalletID,
		CaseID:             verification.ID,
		Kind:               kind,
		ClientOperationID:  strings.TrimSpace(clientOperationID),
		RequestHash:        strings.TrimSpace(requestHash),
		ProviderRequestKey: strings.TrimSpace(providerRequestKey),
		State:              KYCOperationReserved,
		Retryable:          true,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

func (o KYCProviderOperation) Succeed(
	verification KYCVerificationCase,
	credentialID *KYCCredentialID,
	now time.Time,
) (KYCProviderOperation, error) {
	if (o.State != KYCOperationReserved &&
		(o.State != KYCOperationUnavailable || !o.Retryable)) ||
		o.UserID != verification.UserID ||
		o.WalletID != verification.WalletID ||
		o.CaseID != verification.ID ||
		verification.State == KYCStateCreated ||
		(verification.State == KYCStateVerified &&
			(credentialID == nil || *credentialID == "")) ||
		(verification.State != KYCStateVerified && credentialID != nil) {
		return KYCProviderOperation{}, ErrKYCVerificationState
	}
	next := o
	next.State = KYCOperationSucceeded
	next.ProviderCaseRef = verification.ProviderCaseRef
	state := verification.State
	next.ResultCaseState = &state
	next.ResultCredentialID = credentialID
	next.FailureCode = ""
	next.Retryable = false
	next.UpdatedAt = now
	completedAt := now
	next.CompletedAt = &completedAt
	return next, nil
}

func (o KYCProviderOperation) ProviderUnavailable(
	failureCode string,
	now time.Time,
) (KYCProviderOperation, error) {
	return o.providerFailure(
		KYCOperationUnavailable, failureCode, true, now,
	)
}

func (o KYCProviderOperation) Fail(
	failureCode string,
	now time.Time,
) (KYCProviderOperation, error) {
	return o.providerFailure(
		KYCOperationFailed, failureCode, false, now,
	)
}

func (o KYCProviderOperation) providerFailure(
	state KYCProviderOperationState,
	failureCode string,
	retryable bool,
	now time.Time,
) (KYCProviderOperation, error) {
	if (o.State != KYCOperationReserved &&
		o.State != KYCOperationUnavailable) ||
		(state != KYCOperationUnavailable &&
			state != KYCOperationFailed) ||
		strings.TrimSpace(failureCode) == "" {
		return KYCProviderOperation{}, ErrKYCVerificationState
	}
	next := o
	next.State = state
	next.ProviderCaseRef = nil
	next.ResultCaseState = nil
	next.ResultCredentialID = nil
	next.FailureCode = strings.TrimSpace(failureCode)
	next.Retryable = retryable
	next.UpdatedAt = now
	completedAt := now
	next.CompletedAt = &completedAt
	return next, nil
}

type KYCCredential struct {
	ID               KYCCredentialID    `json:"id"`
	UserID           UserID             `json:"userId"`
	WalletID         WalletID           `json:"walletId"`
	CaseID           string             `json:"caseId"`
	Level            KYCCredentialLevel `json:"level"`
	ProviderKind     KYCProviderKind    `json:"providerKind"`
	ProviderVersion  string             `json:"providerVersion"`
	ExternalEffect   KYCExternalEffect  `json:"externalEffect"`
	SubjectAccountID string             `json:"subjectAccountId"`
	IssuerRef        string             `json:"issuerRef"`
	SchemaRef        string             `json:"schemaRef"`
	EvidenceHash     string             `json:"evidenceHash"`
	IssuedAt         time.Time          `json:"issuedAt"`
	ValidUntil       time.Time          `json:"validUntil"`
	CreatedAt        time.Time          `json:"createdAt"`
}

func NewKYCCredential(
	id KYCCredentialID,
	verification KYCVerificationCase,
	wallet Wallet,
	issuerRef, schemaRef, evidenceHash string,
	issuedAt, validUntil, now time.Time,
) (KYCCredential, error) {
	if id == "" ||
		verification.ID == "" ||
		verification.UserID != wallet.UserID ||
		verification.WalletID != wallet.ID ||
		verification.ProviderKind != KYCProviderMockDojang ||
		verification.ProviderVersion != MockDojangProviderVersion ||
		verification.ExternalEffect != KYCEffectSimulated ||
		strings.TrimSpace(wallet.AccountID) == "" ||
		strings.TrimSpace(issuerRef) == "" ||
		strings.TrimSpace(schemaRef) == "" ||
		strings.TrimSpace(evidenceHash) == "" ||
		validUntil.Before(issuedAt) ||
		validUntil.Equal(issuedAt) {
		return KYCCredential{}, ErrKYCCredentialInvalid
	}
	return KYCCredential{
		ID:               id,
		UserID:           wallet.UserID,
		WalletID:         wallet.ID,
		CaseID:           verification.ID,
		Level:            KYCCredentialMockDojangVerified,
		ProviderKind:     verification.ProviderKind,
		ProviderVersion:  verification.ProviderVersion,
		ExternalEffect:   verification.ExternalEffect,
		SubjectAccountID: wallet.AccountID,
		IssuerRef:        strings.TrimSpace(issuerRef),
		SchemaRef:        strings.TrimSpace(schemaRef),
		EvidenceHash:     strings.TrimSpace(evidenceHash),
		IssuedAt:         issuedAt,
		ValidUntil:       validUntil,
		CreatedAt:        now,
	}, nil
}

type KYCEvidenceObservation struct {
	ID            KYCEvidenceObservationID `json:"id"`
	CredentialID  KYCCredentialID          `json:"credentialId"`
	UserID        UserID                   `json:"userId"`
	WalletID      WalletID                 `json:"walletId"`
	Status        KYCEvidenceStatus        `json:"status"`
	EvidenceHash  string                   `json:"evidenceHash"`
	SourceVersion int64                    `json:"sourceVersion"`
	ObservedAt    time.Time                `json:"observedAt"`
	ValidUntil    time.Time                `json:"validUntil"`
	RecheckAfter  time.Time                `json:"recheckAfter"`
}

func NewKYCEvidenceObservation(
	id KYCEvidenceObservationID,
	credential KYCCredential,
	status KYCEvidenceStatus,
	evidenceHash string,
	sourceVersion int64,
	observedAt, validUntil, recheckAfter time.Time,
) (KYCEvidenceObservation, error) {
	if id == "" ||
		credential.ID == "" ||
		(status != KYCEvidenceValid &&
			status != KYCEvidenceExpired &&
			status != KYCEvidenceRevoked) ||
		strings.TrimSpace(evidenceHash) == "" ||
		sourceVersion <= 0 ||
		validUntil.Before(observedAt) ||
		recheckAfter.Before(observedAt) {
		return KYCEvidenceObservation{}, ErrKYCObservationInvalid
	}
	return KYCEvidenceObservation{
		ID:            id,
		CredentialID:  credential.ID,
		UserID:        credential.UserID,
		WalletID:      credential.WalletID,
		Status:        status,
		EvidenceHash:  strings.TrimSpace(evidenceHash),
		SourceVersion: sourceVersion,
		ObservedAt:    observedAt,
		ValidUntil:    validUntil,
		RecheckAfter:  recheckAfter,
	}, nil
}

func (o KYCEvidenceObservation) ValidAt(now time.Time) bool {
	return o.Status == KYCEvidenceValid &&
		!now.Before(o.ObservedAt) &&
		now.Before(o.ValidUntil) &&
		now.Before(o.RecheckAfter)
}

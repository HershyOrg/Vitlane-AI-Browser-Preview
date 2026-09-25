package app

import (
	"context"
	"errors"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type Repository interface {
	CreateUser(ctx context.Context, user accountdomain.User) error
	// ListUsersByID는 표시용 최소 신원 조회다 — 다른 제품(Support 운영자
	// 화면 등)이 users 테이블을 직접 읽는 대신 이 포트를 쓴다(ADR-0059).
	ListUsersByID(ctx context.Context, ids []accountdomain.UserID) ([]accountdomain.User, error)
	ListWallets(context.Context, accountdomain.UserID) ([]accountdomain.Wallet, error)
	ListWalletOwnershipProofs(
		context.Context,
		accountdomain.UserID,
	) ([]accountdomain.WalletOwnershipProof, error)
	DeregisterWallet(context.Context, accountdomain.UserID, accountdomain.WalletID, time.Time) error
	EnsureTestBuyerProfile(context.Context, accountdomain.BuyerProfile) (accountdomain.BuyerProfile, error)
	ListBuyerProfiles(context.Context, accountdomain.UserID) ([]accountdomain.BuyerProfile, error)
	FindDefaultBuyerProfile(context.Context, accountdomain.UserID) (accountdomain.BuyerProfile, error)
	ListKYCCredentials(
		context.Context,
		accountdomain.UserID,
	) ([]accountdomain.KYCCredential, error)
	ListKYCEvidenceObservations(
		context.Context,
		accountdomain.UserID,
	) ([]accountdomain.KYCEvidenceObservation, error)
	ListKYCVerificationCases(context.Context, accountdomain.UserID) ([]accountdomain.KYCVerificationCase, error)
	ListLatestKYCProviderOperations(
		context.Context,
		accountdomain.UserID,
	) ([]accountdomain.KYCProviderOperation, error)
	UpsertPolicyAcceptance(context.Context, accountdomain.UserID, accountdomain.PolicyAcceptance) error
	ListPolicyAcceptances(context.Context, accountdomain.UserID) ([]accountdomain.PolicyAcceptance, error)
}

type Service struct {
	repository Repository
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

const (
	TestSettlementPolicyID      = "PHASE5_TEST_SETTLEMENT"
	TestSettlementPolicyVersion = "2026-07-24"
)

type AccountOverview struct {
	Wallets           []WalletProjection               `json:"wallets"`
	BuyerProfiles     []accountdomain.BuyerProfile     `json:"buyerProfiles"`
	PolicyAcceptances []accountdomain.PolicyAcceptance `json:"policyAcceptances"`
	AssurancePolicy   map[string]string                `json:"assurancePolicy"`
}

type WalletProjection struct {
	Wallet    accountdomain.Wallet                    `json:"wallet"`
	Ownership accountdomain.WalletOwnershipProjection `json:"ownership"`
	KYC       WalletKYCProjection                     `json:"kyc"`
	Actions   WalletActionsProjection                 `json:"actions"`
}

type KYCEligibility string

const (
	KYCEligibilityNone     KYCEligibility = "NONE"
	KYCEligibilityPending  KYCEligibility = "PENDING"
	KYCEligibilityValid    KYCEligibility = "VALID"
	KYCEligibilityRecheck  KYCEligibility = "RECHECK_REQUIRED"
	KYCEligibilityExpired  KYCEligibility = "EXPIRED"
	KYCEligibilityRevoked  KYCEligibility = "REVOKED"
	KYCEligibilityRejected KYCEligibility = "REJECTED"

	MockDojangDisclosure = "MockDojang KYC는 TEST 전용이며 externalEffect=SIMULATED입니다. 실제 외부 신원 확인이 아닙니다."
)

type WalletKYCProjection struct {
	Eligibility    KYCEligibility                        `json:"eligibility"`
	ActionEligible bool                                  `json:"actionEligible"`
	ProviderKind   accountdomain.KYCProviderKind         `json:"providerKind"`
	ExternalEffect accountdomain.KYCExternalEffect       `json:"externalEffect"`
	Disclosure     string                                `json:"disclosure"`
	ActiveCase     *accountdomain.KYCVerificationCase    `json:"activeCase,omitempty"`
	Credential     *accountdomain.KYCCredential          `json:"credential,omitempty"`
	Observation    *accountdomain.KYCEvidenceObservation `json:"observation,omitempty"`
	FailureCode    string                                `json:"failureCode,omitempty"`
	Retryable      *bool                                 `json:"retryable,omitempty"`
	NextAction     accountdomain.WalletNextAction        `json:"nextAction"`
}

type WalletActionsProjection struct {
	CanSetDefault     bool `json:"canSetDefault"`
	CanDeregister     bool `json:"canDeregister"`
	CanReauthenticate bool `json:"canReauthenticate"`
	CanStartKYC       bool `json:"canStartKYC"`
	CanCheckKYC       bool `json:"canCheckKYC"`
}

func (s *Service) GetOverview(ctx context.Context, userID string) (AccountOverview, error) {
	wallets, err := s.repository.ListWallets(ctx, accountdomain.UserID(userID))
	if err != nil {
		return AccountOverview{}, err
	}
	proofs, err := s.repository.ListWalletOwnershipProofs(
		ctx, accountdomain.UserID(userID),
	)
	if err != nil {
		return AccountOverview{}, err
	}
	profiles, err := s.repository.ListBuyerProfiles(ctx, accountdomain.UserID(userID))
	if err != nil {
		return AccountOverview{}, err
	}
	credentials, err := s.repository.ListKYCCredentials(
		ctx, accountdomain.UserID(userID),
	)
	if err != nil {
		return AccountOverview{}, err
	}
	observations, err := s.repository.ListKYCEvidenceObservations(
		ctx, accountdomain.UserID(userID),
	)
	if err != nil {
		return AccountOverview{}, err
	}
	acceptances, err := s.repository.ListPolicyAcceptances(ctx, accountdomain.UserID(userID))
	if err != nil {
		return AccountOverview{}, err
	}
	kycVerifications, err := s.repository.ListKYCVerificationCases(
		ctx, accountdomain.UserID(userID),
	)
	if err != nil {
		return AccountOverview{}, err
	}
	kycOperations, err := s.repository.ListLatestKYCProviderOperations(
		ctx, accountdomain.UserID(userID),
	)
	if err != nil {
		return AccountOverview{}, err
	}
	walletProjections := projectWallets(
		wallets, proofs, credentials, observations,
		kycVerifications, kycOperations, s.clock.Now(),
	)
	return AccountOverview{
		Wallets: walletProjections, BuyerProfiles: profiles,
		PolicyAcceptances: acceptances,
		AssurancePolicy: map[string]string{
			"PLAN_RESEARCH":       "LOGIN",
			"WALLET_REGISTRATION": "CURRENT_WALLET_OWNERSHIP_PROOF",
			"TEST_LOW_VALUE":      "CURRENT_WALLET_OWNERSHIP_PROOF",
			"TEST_ADVANCED":       "CURRENT_MOCK_DOJANG_KYC_CREDENTIAL",
			"REAL_VALUE_PAYMENT":  "NOT_AVAILABLE",
		},
	}, nil
}

func (s *Service) GetDefaultBuyerProfile(
	ctx context.Context,
	userID string,
) (accountdomain.BuyerProfile, error) {
	profile, err := s.repository.FindDefaultBuyerProfile(ctx, accountdomain.UserID(userID))
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, accountdomain.ErrBuyerProfileInvalid) {
		return accountdomain.BuyerProfile{}, err
	}
	next, err := accountdomain.NewTestBuyerProfile(
		accountdomain.BuyerProfileID(s.ids.NewID()), accountdomain.UserID(userID), s.clock.Now(),
	)
	if err != nil {
		return accountdomain.BuyerProfile{}, err
	}
	return s.repository.EnsureTestBuyerProfile(ctx, next)
}

func (s *Service) DeregisterWallet(ctx context.Context, userID, walletID string) error {
	return s.repository.DeregisterWallet(
		ctx, accountdomain.UserID(userID), accountdomain.WalletID(walletID), s.clock.Now(),
	)
}

func (s *Service) AcceptTestSettlementPolicy(
	ctx context.Context,
	userID, policyVersion string,
) (accountdomain.PolicyAcceptance, error) {
	if policyVersion != TestSettlementPolicyVersion {
		return accountdomain.PolicyAcceptance{}, accountdomain.ErrAccountStateInvalid
	}
	acceptance := accountdomain.PolicyAcceptance{
		PolicyID: TestSettlementPolicyID, PolicyVersion: policyVersion,
		AcceptedAt: s.clock.Now(),
	}
	if err := s.repository.UpsertPolicyAcceptance(
		ctx, accountdomain.UserID(userID), acceptance,
	); err != nil {
		return accountdomain.PolicyAcceptance{}, err
	}
	return acceptance, nil
}

func NewService(repository Repository, clock sharedapp.Clock, ids sharedapp.IDGenerator) *Service {
	return &Service{repository: repository, clock: clock, ids: ids}
}

// ListUsersByID는 표시용 최소 신원(email·displayName)이다. 탈퇴(tombstone)
// 계정은 빈 email로 나간다 — 표시는 소비 화면이 정한다.
func (s *Service) ListUsersByID(
	ctx context.Context,
	ids []accountdomain.UserID,
) ([]accountdomain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return s.repository.ListUsersByID(ctx, ids)
}

func (s *Service) CreateDevelopmentUser(ctx context.Context) (accountdomain.User, error) {
	user := accountdomain.NewUser(accountdomain.UserID(s.ids.NewID()), s.clock.Now())
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return accountdomain.User{}, err
	}
	return user, nil
}

func projectWallets(
	wallets []accountdomain.Wallet,
	proofs []accountdomain.WalletOwnershipProof,
	credentials []accountdomain.KYCCredential,
	observations []accountdomain.KYCEvidenceObservation,
	cases []accountdomain.KYCVerificationCase,
	operations []accountdomain.KYCProviderOperation,
	now time.Time,
) []WalletProjection {
	proofsByID := make(map[accountdomain.WalletOwnershipProofID]accountdomain.WalletOwnershipProof)
	for _, proof := range proofs {
		proofsByID[proof.ID] = proof
	}
	credentialsByID := make(map[accountdomain.KYCCredentialID]accountdomain.KYCCredential)
	for _, credential := range credentials {
		credentialsByID[credential.ID] = credential
	}
	latestObservations := make(
		map[accountdomain.KYCCredentialID]accountdomain.KYCEvidenceObservation,
	)
	for _, observation := range observations {
		latest, ok := latestObservations[observation.CredentialID]
		if !ok || observation.SourceVersion > latest.SourceVersion {
			latestObservations[observation.CredentialID] = observation
		}
	}
	latestCases := make(map[accountdomain.WalletID]accountdomain.KYCVerificationCase)
	for _, verification := range cases {
		latest, ok := latestCases[verification.WalletID]
		if !ok || verification.UpdatedAt.After(latest.UpdatedAt) {
			latestCases[verification.WalletID] = verification
		}
	}
	latestOperations := make(
		map[string]accountdomain.KYCProviderOperation,
	)
	for _, operation := range operations {
		latest, ok := latestOperations[operation.CaseID]
		if !ok || kycOperationIsNewer(operation, latest) {
			latestOperations[operation.CaseID] = operation
		}
	}
	result := make([]WalletProjection, 0, len(wallets))
	for _, wallet := range wallets {
		var currentProof *accountdomain.WalletOwnershipProof
		if wallet.CurrentOwnershipProofID != nil {
			if proof, ok := proofsByID[*wallet.CurrentOwnershipProofID]; ok {
				currentProof = &proof
			}
		}
		ownership := accountdomain.ProjectWalletOwnership(wallet, currentProof, now)
		registered := wallet.IsRegistered()
		verification := latestCases[wallet.ID]
		kyc := projectWalletKYC(
			verification, credentialsByID, latestObservations, now,
		)
		kyc = projectKYCFailure(
			kyc, verification, latestOperations[verification.ID], registered,
		)
		activeCaseState := accountdomain.KYCVerificationState("")
		if kyc.ActiveCase != nil {
			activeCaseState = kyc.ActiveCase.State
		}
		currentProofIsFresh := currentProof != nil &&
			currentProof.FreshAt(now, KYCStartOwnershipProofMaximumAge)
		kyc.ActionEligible = registered &&
			ownership.Status == accountdomain.WalletOwnershipValid &&
			kyc.Eligibility == KYCEligibilityValid
		actionBlocked := kyc.NextAction.Kind ==
			accountdomain.WalletNextActionContactSupport
		result = append(result, WalletProjection{
			Wallet: wallet, Ownership: ownership, KYC: kyc,
			Actions: WalletActionsProjection{
				CanSetDefault:     false,
				CanDeregister:     registered,
				CanReauthenticate: registered && !currentProofIsFresh,
				CanStartKYC: registered && !actionBlocked &&
					(activeCaseState == accountdomain.KYCStateCreated ||
						(currentProofIsFresh &&
							kyc.Eligibility != KYCEligibilityPending &&
							kyc.Eligibility != KYCEligibilityValid)),
				CanCheckKYC: !actionBlocked && activeCaseState ==
					accountdomain.KYCStatePendingProvider,
			},
		})
	}
	return result
}

func projectWalletKYC(
	verification accountdomain.KYCVerificationCase,
	credentials map[accountdomain.KYCCredentialID]accountdomain.KYCCredential,
	observations map[accountdomain.KYCCredentialID]accountdomain.KYCEvidenceObservation,
	now time.Time,
) WalletKYCProjection {
	projection := WalletKYCProjection{
		Eligibility:    KYCEligibilityNone,
		ProviderKind:   accountdomain.KYCProviderMockDojang,
		ExternalEffect: accountdomain.KYCEffectSimulated,
		Disclosure:     MockDojangDisclosure,
		NextAction: accountdomain.WalletNextAction{
			Kind: accountdomain.WalletNextActionStartKYC,
		},
	}
	if verification.ID == "" {
		return projection
	}
	switch verification.State {
	case accountdomain.KYCStateCreated:
		projection.Eligibility = KYCEligibilityPending
		active := verification
		projection.ActiveCase = &active
		projection.NextAction = accountdomain.WalletNextAction{
			Kind: accountdomain.WalletNextActionStartKYC,
		}
	case accountdomain.KYCStatePendingProvider:
		projection.Eligibility = KYCEligibilityPending
		active := verification
		projection.ActiveCase = &active
		projection.NextAction = accountdomain.WalletNextAction{
			Kind: accountdomain.WalletNextActionCheckResult,
		}
	case accountdomain.KYCStateVerified:
		if verification.CredentialID == nil {
			projection.Eligibility = KYCEligibilityRevoked
			return projection
		}
		credential, ok := credentials[*verification.CredentialID]
		if !ok {
			projection.Eligibility = KYCEligibilityRevoked
			return projection
		}
		credentialSnapshot := credential
		projection.Credential = &credentialSnapshot
		observation, ok := observations[credential.ID]
		if !ok {
			projection.Eligibility = KYCEligibilityRevoked
			return projection
		}
		observationSnapshot := observation
		projection.Observation = &observationSnapshot
		switch observation.Status {
		case accountdomain.KYCEvidenceRevoked:
			projection.Eligibility = KYCEligibilityRevoked
			return projection
		case accountdomain.KYCEvidenceExpired:
			projection.Eligibility = KYCEligibilityExpired
			return projection
		}
		if !credential.ValidUntil.After(now) ||
			!observation.ValidUntil.After(now) {
			projection.Eligibility = KYCEligibilityExpired
			projection.NextAction = accountdomain.WalletNextAction{
				Kind: accountdomain.WalletNextActionRecheck,
			}
			return projection
		}
		if !observation.RecheckAfter.After(now) {
			projection.Eligibility = KYCEligibilityRecheck
			projection.NextAction = accountdomain.WalletNextAction{
				Kind: accountdomain.WalletNextActionRecheck,
			}
			return projection
		}
		projection.Eligibility = KYCEligibilityValid
		projection.NextAction = accountdomain.WalletNextAction{
			Kind: accountdomain.WalletNextActionNone,
		}
	case accountdomain.KYCStateRejected:
		projection.Eligibility = KYCEligibilityRejected
	case accountdomain.KYCStateExpired:
		projection.Eligibility = KYCEligibilityExpired
	case accountdomain.KYCStateCancelled:
		projection.Eligibility = KYCEligibilityRevoked
	default:
		projection.Eligibility = KYCEligibilityRevoked
	}
	return projection
}

func kycOperationIsNewer(
	candidate accountdomain.KYCProviderOperation,
	current accountdomain.KYCProviderOperation,
) bool {
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return string(candidate.ID) > string(current.ID)
}

func projectKYCFailure(
	projection WalletKYCProjection,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
	walletRegistered bool,
) WalletKYCProjection {
	if verification.ID == "" {
		return projection
	}
	if operation.CaseID == verification.ID &&
		(operation.State == accountdomain.KYCOperationUnavailable ||
			operation.State == accountdomain.KYCOperationFailed) {
		projection.FailureCode = operation.FailureCode
		retryable := operation.Retryable
		projection.Retryable = &retryable
		switch {
		case operation.State == accountdomain.KYCOperationUnavailable &&
			operation.Kind == accountdomain.KYCOperationStart:
			projection.NextAction = accountdomain.WalletNextAction{
				Kind:  accountdomain.WalletNextActionStartKYC,
				Label: "신원 확인 다시 시도",
			}
		case operation.State == accountdomain.KYCOperationUnavailable &&
			operation.Kind == accountdomain.KYCOperationCheck:
			projection.NextAction = accountdomain.WalletNextAction{
				Kind:  accountdomain.WalletNextActionCheckResult,
				Label: "결과 다시 확인",
			}
		case operation.FailureCode == "WALLET_DEREGISTERED":
			if walletRegistered {
				retryable = true
				projection.Retryable = &retryable
				projection.NextAction = accountdomain.WalletNextAction{
					Kind:  accountdomain.WalletNextActionStartKYC,
					Label: "신원 확인 다시 시작",
				}
			} else {
				projection.NextAction = accountdomain.WalletNextAction{
					Kind:  accountdomain.WalletNextActionRegister,
					Label: "지갑 다시 등록",
				}
			}
		default:
			projection.NextAction = accountdomain.WalletNextAction{
				Kind:  accountdomain.WalletNextActionContactSupport,
				Label: "KYC 지원 문의",
			}
		}
		return projection
	}
	if verification.FailureCode == "" {
		return projection
	}
	projection.FailureCode = verification.FailureCode
	retryable := verification.State == accountdomain.KYCStateRejected ||
		verification.State == accountdomain.KYCStateExpired
	projection.Retryable = &retryable
	switch {
	case retryable:
		projection.NextAction = accountdomain.WalletNextAction{
			Kind:  accountdomain.WalletNextActionStartKYC,
			Label: "신원 확인 다시 시작",
		}
	case verification.FailureCode == "WALLET_DEREGISTERED":
		if walletRegistered {
			retryable = true
			projection.Retryable = &retryable
			projection.NextAction = accountdomain.WalletNextAction{
				Kind:  accountdomain.WalletNextActionStartKYC,
				Label: "신원 확인 다시 시작",
			}
		} else {
			projection.NextAction = accountdomain.WalletNextAction{
				Kind:  accountdomain.WalletNextActionRegister,
				Label: "지갑 다시 등록",
			}
		}
	default:
		projection.NextAction = accountdomain.WalletNextAction{
			Kind:  accountdomain.WalletNextActionContactSupport,
			Label: "KYC 지원 문의",
		}
	}
	return projection
}

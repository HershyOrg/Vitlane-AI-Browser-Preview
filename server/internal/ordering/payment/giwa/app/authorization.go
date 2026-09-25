package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	commonmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

const walletOwnershipOnlyPolicy = "WALLET_OWNERSHIP_ONLY"

type AuthorizationContext struct {
	AgencyOrderID          string
	AgencyOrderHash        string
	ConsentAgencyOrderHash string
	AgencyOrderStatus      string
	InstructionHash        string
	ConsentInstructionHash string
	InstructionExpiresAt   time.Time
	ConsentAmount          string
	InstructionAmount      string
	ConsentCurrency        string
	InstructionCurrency    string
	// AmountBaseUnits는 payer가 지불하는 총액(passThrough + fee)이다.
	AmountBaseUnits                   string
	PassThroughBaseUnits              string
	FeeBaseUnits                      string
	TokenAddress                      string
	SettlementAddress                 string
	ChainID                           uint64
	MerchantID                        string
	MerchantRegistryVersion           uint64
	FeeBps                            uint16
	FeeRecipient                      string
	PrincipalRecipient                string
	CurrentMerchantPrincipalRecipient string
	CurrentMerchantRegistryVersion    uint64
	CurrentMerchantActive             bool
	ConsentHash                       string
	PayerWalletID                     string
	WalletOwnershipProofID            string
	PayerAccountID                    string
	PayerAddress                      string
	PayerChainID                      string
	OwnershipProofMethod              string
	OwnershipMessageHash              string
	OwnershipVerifiedAt               time.Time
	OwnershipValidUntil               time.Time
	TestPolicyID                      string
	TestPolicyVersion                 string
}

type Repository interface {
	GetAuthorizationContext(context.Context, string, string) (AuthorizationContext, error)
	GetAuthorization(context.Context, string, string) (settlementdomain.AuthorizationRecord, error)
	CreateAuthorization(
		context.Context, settlementdomain.AuthorizationRecord, settlementdomain.Payment,
	) error
	RecordSubmittedTransaction(context.Context, string, string, string, time.Time) error
	RecordWalletTransaction(context.Context, string, string, string, string, time.Time) error
	GetRefundIntent(context.Context, string, string, time.Time) (settlementdomain.RefundIntent, error)
	RecordRefundTransaction(context.Context, string, string, string, time.Time) error
	GetPayment(context.Context, string, string) (settlementdomain.Payment, error)
}

type DigestSigner interface {
	Address() string
	SignDigest([]byte) ([]byte, error)
}

type Service struct {
	repository       Repository
	signer           DigestSigner
	clock            sharedapp.Clock
	ids              sharedapp.IDGenerator
	config           settlementdomain.SettlementConfig
	agencyOrders     AgencyOrderAuthorizationRepository
	agencyIdentities AgencyOrderIdentityReader
	instructions     paymentapp.InstructionGate
	transactor       sharedapp.Transactor
}

type AgencyOrderIdentity struct {
	WalletID             string
	OwnershipProofID     string
	PayerAccountID       string
	PayerAddress         string
	PayerChainID         string
	OwnershipProofMethod string
	OwnershipMessageHash string
	OwnershipVerifiedAt  time.Time
	OwnershipValidUntil  time.Time
}

type AgencyOrderIdentityReader interface {
	ResolveAgencyOrderIdentity(context.Context, string, string, string) (AgencyOrderIdentity, error)
}

type AgencyOrderAuthorizationRepository interface {
	CreateAgencyOrderConsent(context.Context, string, string, AgencyOrderIdentity, settlementdomain.SettlementConfig, time.Time) error
}

func NewService(
	repository Repository,
	signer DigestSigner,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	config settlementdomain.SettlementConfig,
) *Service {
	return &Service{repository: repository, signer: signer, clock: clock, ids: ids, config: config}
}

func (s *Service) EnableAgencyOrders(repository AgencyOrderAuthorizationRepository, identities AgencyOrderIdentityReader) {
	s.agencyOrders = repository
	s.agencyIdentities = identities
}

// EnableInstructionGate installs Payment-owned durable confirmation. Signing
// remains unavailable while AgencyOrder has not confirmed instruction ownership.
func (s *Service) EnableInstructionGate(
	instructions paymentapp.InstructionGate,
	transactor sharedapp.Transactor,
) {
	s.instructions = instructions
	s.transactor = transactor
}

func (s *Service) Config() settlementdomain.SettlementConfig {
	return s.config
}

func (s *Service) AuthorizeAgencyOrder(ctx context.Context, userID, agencyOrderID, walletID, ownershipProofID string) (settlementdomain.AuthorizationRecord, error) {
	if s.agencyOrders == nil || s.agencyIdentities == nil {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	identity, err := s.agencyIdentities.ResolveAgencyOrderIdentity(ctx, userID, walletID, ownershipProofID)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	if err := s.agencyOrders.CreateAgencyOrderConsent(ctx, userID, agencyOrderID, identity, s.config, s.clock.Now()); err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	return s.Authorize(ctx, userID, agencyOrderID)
}

func (s *Service) Authorize(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.AuthorizationRecord, error) {
	existing, err := s.repository.GetAuthorization(ctx, userID, agencyOrderID)
	if err == nil {
		// An existing record is the immutable result of an already completed
		// approval command. Return it even after its pay deadline so an exact
		// idempotent retry converges. The expired authorization still cannot be
		// paid; issuing a usable replacement requires a new quote and approval.
		if err := validateSealedAuthorizationRecord(existing); err != nil {
			return settlementdomain.AuthorizationRecord{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, settlementdomain.ErrAuthorizationNotFound) {
		return settlementdomain.AuthorizationRecord{}, err
	}
	contextValue, err := s.repository.GetAuthorizationContext(ctx, userID, agencyOrderID)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	if !instructionMatchesActiveSettlement(contextValue, s.config) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInstructionStale
	}
	now := s.clock.Now()
	if contextValue.AgencyOrderStatus != "USER_APPROVED" ||
		contextValue.AgencyOrderHash != contextValue.ConsentAgencyOrderHash ||
		contextValue.InstructionHash != contextValue.ConsentInstructionHash ||
		contextValue.ConsentAmount != contextValue.InstructionAmount ||
		contextValue.ConsentCurrency != contextValue.InstructionCurrency ||
		contextValue.PayerWalletID == "" ||
		contextValue.WalletOwnershipProofID == "" ||
		contextValue.PayerAccountID == "" ||
		contextValue.PayerAddress == "" ||
		contextValue.PayerChainID != "eip155:"+strconv.FormatUint(contextValue.ChainID, 10) ||
		contextValue.PayerAccountID != contextValue.PayerChainID+":"+contextValue.PayerAddress ||
		contextValue.OwnershipProofMethod != "EIP191_PERSONAL_SIGN" ||
		contextValue.OwnershipMessageHash == "" ||
		contextValue.OwnershipVerifiedAt.After(now) ||
		contextValue.TestPolicyID != "PHASE5_TEST_SETTLEMENT" ||
		contextValue.TestPolicyVersion != "2026-07-24" ||
		!now.Before(contextValue.InstructionExpiresAt) ||
		!now.Before(contextValue.OwnershipValidUntil) ||
		contextValue.OwnershipValidUntil.Before(contextValue.InstructionExpiresAt) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	if !common.IsHexAddress(s.signer.Address()) ||
		!common.IsHexAddress(contextValue.PayerAddress) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	if s.instructions == nil || s.transactor == nil {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	claim := procmsg.InstructionClaim{AgencyOrderID: agencyOrderID, Rail: "GIWA", ReferenceID: contextValue.ConsentHash, SnapshotHash: contextValue.AgencyOrderHash, ValidAt: now}
	if err := s.instructions.ConfirmInstruction(ctx, claim); err != nil {
		if errors.Is(err, procmsg.ErrInstructionPending) {
			return settlementdomain.AuthorizationRecord{}, err
		}
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrPaymentStateInvalid
	}
	merchantID := crypto.Keccak256Hash([]byte(contextValue.MerchantID)).Hex()
	ownershipProofPolicy := crypto.Keccak256Hash(
		[]byte(walletOwnershipOnlyPolicy),
	).Hex()
	orderHash := crypto.Keccak256Hash([]byte(
		contextValue.AgencyOrderHash + ":" + contextValue.InstructionHash + ":" + contextValue.ConsentHash,
	)).Hex()
	// v2 (ADR-0050): 서명은 {passThrough, fee} 정확값을 고정하고, 소비자에게
	// 표시·승인된 총액과 정확히 합산 일치해야 한다.
	if err := validateAuthorizationAmounts(contextValue); err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	nonceHash := crypto.Keccak256Hash([]byte(s.ids.NewID()))
	nonce := new(big.Int).SetBytes(nonceHash.Bytes())
	payDeadline := contextValue.InstructionExpiresAt
	refundAfter := payDeadline.Add(time.Hour)
	authorization := settlementdomain.PaymentAuthorization{
		Payer: strings.ToLower(contextValue.PayerAddress), Token: strings.ToLower(contextValue.TokenAddress),
		PassThroughAmount: contextValue.PassThroughBaseUnits,
		FeeAmount:         contextValue.FeeBaseUnits, OrderHash: orderHash,
		MerchantID: merchantID, MerchantRegistryVersion: contextValue.MerchantRegistryVersion,
		FeeBps: contextValue.FeeBps, FeeRecipient: strings.ToLower(contextValue.FeeRecipient),
		PrincipalRecipient: strings.ToLower(contextValue.PrincipalRecipient),
		AssuranceLevel:     ownershipProofPolicy, Nonce: nonce.String(),
		PayDeadline: uint64(payDeadline.Unix()), RefundAfter: uint64(refundAfter.Unix()),
	}
	if err := authorization.Validate(now); err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	domain := settlementdomain.AuthorizationDomain{
		Name: "Vitlane Settlement", Version: "2",
		ChainID:           contextValue.ChainID,
		VerifyingContract: strings.ToLower(contextValue.SettlementAddress),
	}
	typedData := paymentTypedData(domain, authorization)
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, fmt.Errorf("hash typed data: %w", err)
	}
	record := settlementdomain.AuthorizationRecord{
		ID:            s.ids.NewID(),
		AgencyOrderID: agencyOrderID,
		Authorization: authorization,
		Domain:        domain,
		Signer:        s.signer.Address(),
		TypedDataHash: common.BytesToHash(digest).Hex(),
		CreatedAt:     now,
	}
	record, err = s.signRecord(record)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	payment := settlementdomain.Payment{
		ID:            s.ids.NewID(),
		AgencyOrderID: agencyOrderID,
		OrderHash:     orderHash,
		ChainID:       contextValue.ChainID, Settlement: strings.ToLower(contextValue.SettlementAddress),
		Payer: strings.ToLower(contextValue.PayerAddress), AmountBaseUnits: contextValue.AmountBaseUnits,
		State: settlementdomain.PaymentAuthorized, CreatedAt: now, UpdatedAt: now,
	}
	if s.instructions == nil || s.transactor == nil {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	if err := s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if createErr := s.repository.CreateAuthorization(tx, record, payment); createErr != nil {
			return createErr
		}

		return nil
	}); err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	return record, nil
}

// validateAuthorizationAmounts는 AgencyOrder payment instruction 총액이 정확히
// passThrough + fee로 분해되는지 확인한다. 불일치는 표시 금액과 서명 금액이
// 어긋난다는 뜻이므로 fail-closed 한다.
func validateAuthorizationAmounts(contextValue AuthorizationContext) error {
	passThrough, passOK := new(big.Int).SetString(contextValue.PassThroughBaseUnits, 10)
	fee, feeOK := new(big.Int).SetString(contextValue.FeeBaseUnits, 10)
	total, totalOK := new(big.Int).SetString(contextValue.AmountBaseUnits, 10)
	if !passOK || !feeOK || !totalOK || passThrough.Sign() <= 0 || fee.Sign() < 0 {
		return settlementdomain.ErrAuthorizationInvalid
	}
	if new(big.Int).Add(passThrough, fee).Cmp(total) != 0 {
		return settlementdomain.ErrAuthorizationInvalid
	}
	return nil
}

func instructionMatchesActiveSettlement(
	contextValue AuthorizationContext,
	config settlementdomain.SettlementConfig,
) bool {
	return contextValue.ChainID == config.ChainID &&
		strings.EqualFold(contextValue.TokenAddress, config.TokenAddress) &&
		strings.EqualFold(contextValue.SettlementAddress, config.SettlementAddress) &&
		contextValue.FeeBps == config.FeeBps &&
		strings.EqualFold(contextValue.FeeRecipient, config.FeeRecipient) &&
		contextValue.CurrentMerchantActive &&
		contextValue.MerchantRegistryVersion == contextValue.CurrentMerchantRegistryVersion &&
		strings.EqualFold(
			contextValue.PrincipalRecipient,
			contextValue.CurrentMerchantPrincipalRecipient,
		)
}

func (s *Service) signRecord(
	record settlementdomain.AuthorizationRecord,
) (settlementdomain.AuthorizationRecord, error) {
	typedData := paymentTypedData(record.Domain, record.Authorization)
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, fmt.Errorf("hash typed data: %w", err)
	}
	if !strings.EqualFold(common.BytesToHash(digest).Hex(), record.TypedDataHash) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	if !strings.EqualFold(record.Signer, s.signer.Address()) {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	signature, err := s.signer.SignDigest(digest)
	if err != nil {
		return settlementdomain.AuthorizationRecord{}, fmt.Errorf("sign typed data: %w", err)
	}
	if len(signature) != crypto.SignatureLength {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationInvalid
	}
	if signature[64] < 27 {
		signature[64] += 27
	}
	record.Signature = hexutil.Encode(signature)
	if err := validateSealedAuthorizationRecord(record); err != nil {
		return settlementdomain.AuthorizationRecord{}, err
	}
	return record, nil
}

func validateSealedAuthorizationRecord(
	record settlementdomain.AuthorizationRecord,
) error {
	if record.ID == "" || record.AgencyOrderID == "" ||
		record.Domain.Name != "Vitlane Settlement" ||
		record.Domain.Version != "2" ||
		record.Domain.ChainID == 0 ||
		!common.IsHexAddress(record.Domain.VerifyingContract) ||
		!common.IsHexAddress(record.Signer) ||
		record.TypedDataHash == "" ||
		record.Signature == "" {
		return settlementdomain.ErrAuthorizationInvalid
	}
	if err := record.Authorization.Validate(record.CreatedAt); err != nil {
		return err
	}
	typedData := paymentTypedData(record.Domain, record.Authorization)
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return fmt.Errorf("hash stored typed data: %w", err)
	}
	if !strings.EqualFold(
		common.BytesToHash(digest).Hex(),
		record.TypedDataHash,
	) {
		return settlementdomain.ErrAuthorizationInvalid
	}
	signature, err := hexutil.Decode(record.Signature)
	if err != nil || len(signature) != crypto.SignatureLength {
		return settlementdomain.ErrAuthorizationInvalid
	}
	recoverySignature := append([]byte(nil), signature...)
	if recoverySignature[64] >= 27 {
		recoverySignature[64] -= 27
	}
	if recoverySignature[64] > 1 {
		return settlementdomain.ErrAuthorizationInvalid
	}
	publicKey, err := crypto.SigToPub(digest, recoverySignature)
	if err != nil || !strings.EqualFold(
		crypto.PubkeyToAddress(*publicKey).Hex(),
		record.Signer,
	) {
		return settlementdomain.ErrAuthorizationInvalid
	}
	return nil
}

func (s *Service) RecordPayTransaction(
	ctx context.Context,
	userID, agencyOrderID, txHash string,
) error {
	decoded, err := hexutil.Decode(txHash)
	if err != nil || len(decoded) != common.HashLength {
		return settlementdomain.ErrTransactionInvalid
	}
	return s.repository.RecordSubmittedTransaction(
		ctx, userID, agencyOrderID, strings.ToLower(txHash), s.clock.Now(),
	)
}

func (s *Service) RecordWalletTransaction(
	ctx context.Context,
	userID, agencyOrderID, purpose, txHash string,
) error {
	purpose = strings.ToUpper(strings.TrimSpace(purpose))
	if purpose != "CLAIM" && purpose != "APPROVE" {
		return settlementdomain.ErrTransactionInvalid
	}
	decoded, err := hexutil.Decode(txHash)
	if err != nil || len(decoded) != common.HashLength {
		return settlementdomain.ErrTransactionInvalid
	}
	return s.repository.RecordWalletTransaction(
		ctx, userID, agencyOrderID, purpose, strings.ToLower(txHash), s.clock.Now(),
	)
}

func (s *Service) CreateRefundIntent(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.RefundIntent, error) {
	return s.repository.GetRefundIntent(ctx, userID, agencyOrderID, s.clock.Now())
}

func (s *Service) RecordRefundTransaction(
	ctx context.Context,
	userID, agencyOrderID, txHash string,
) error {
	decoded, err := hexutil.Decode(txHash)
	if err != nil || len(decoded) != common.HashLength {
		return settlementdomain.ErrTransactionInvalid
	}
	return s.repository.RecordRefundTransaction(
		ctx, userID, agencyOrderID, strings.ToLower(txHash), s.clock.Now(),
	)
}

func (s *Service) GetPayment(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.Payment, error) {
	return s.repository.GetPayment(ctx, userID, agencyOrderID)
}

func (s *Service) GetAuthorization(
	ctx context.Context,
	userID, agencyOrderID string,
) (settlementdomain.AuthorizationRecord, error) {
	return s.repository.GetAuthorization(ctx, userID, agencyOrderID)
}

func paymentTypedData(
	domain settlementdomain.AuthorizationDomain,
	a settlementdomain.PaymentAuthorization,
) apitypes.TypedData {
	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"}, {Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"},
			},
			"PaymentAuthorization": {
				{Name: "payer", Type: "address"}, {Name: "token", Type: "address"},
				{Name: "passThroughAmount", Type: "uint256"},
				{Name: "feeAmount", Type: "uint256"}, {Name: "orderHash", Type: "bytes32"},
				{Name: "merchantId", Type: "bytes32"},
				{Name: "merchantRegistryVersion", Type: "uint64"},
				{Name: "feeBps", Type: "uint16"}, {Name: "feeRecipient", Type: "address"},
				{Name: "principalRecipient", Type: "address"},
				{Name: "assuranceLevel", Type: "bytes32"}, {Name: "nonce", Type: "uint256"},
				{Name: "payDeadline", Type: "uint64"}, {Name: "refundAfter", Type: "uint64"},
			},
		},
		PrimaryType: "PaymentAuthorization",
		Domain: apitypes.TypedDataDomain{
			Name: domain.Name, Version: domain.Version,
			ChainId: (*commonmath.HexOrDecimal256)(
				new(big.Int).SetUint64(domain.ChainID),
			),
			VerifyingContract: strings.ToLower(domain.VerifyingContract),
		},
		Message: apitypes.TypedDataMessage{
			"payer": a.Payer, "token": a.Token,
			"passThroughAmount": a.PassThroughAmount, "feeAmount": a.FeeAmount,
			"orderHash": a.OrderHash, "merchantId": a.MerchantID,
			"merchantRegistryVersion": fmt.Sprintf("%d", a.MerchantRegistryVersion),
			"feeBps":                  fmt.Sprintf("%d", a.FeeBps), "feeRecipient": a.FeeRecipient,
			"principalRecipient": a.PrincipalRecipient, "assuranceLevel": a.AssuranceLevel,
			"nonce": a.Nonce, "payDeadline": fmt.Sprintf("%d", a.PayDeadline),
			"refundAfter": fmt.Sprintf("%d", a.RefundAfter),
		},
	}
}

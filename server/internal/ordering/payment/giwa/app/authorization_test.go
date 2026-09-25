package app

import (
	"context"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

type authorizationClock struct{ now time.Time }

func (c authorizationClock) Now() time.Time { return c.now }

type authorizationIDs struct{ values []string }

func (i *authorizationIDs) NewID() string {
	value := i.values[0]
	i.values = i.values[1:]
	return value
}

type authorizationTransactor struct{}

func (authorizationTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type authorizationRepositoryTransactor struct {
	repository *authorizationRepository
}

func (t authorizationRepositoryTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	record := t.repository.record
	payment := t.repository.payment
	if err := fn(ctx); err != nil {
		t.repository.record = record
		t.repository.payment = payment
		return err
	}
	return nil
}

type authorizationInstructionGate struct {
	calls int
	err   error
}

func (c *authorizationInstructionGate) ConfirmInstruction(context.Context, procmsg.InstructionClaim) error {
	c.calls++
	return c.err
}

func enableInstructionGate(service *Service) *authorizationInstructionGate {
	consumer := &authorizationInstructionGate{}
	service.EnableInstructionGate(consumer, authorizationTransactor{})
	return consumer
}

type authorizationSigner struct {
	privateKey []byte
	address    string
}

func (s authorizationSigner) Address() string { return s.address }

func (s authorizationSigner) SignDigest(digest []byte) ([]byte, error) {
	key, err := crypto.ToECDSA(s.privateKey)
	if err != nil {
		return nil, err
	}
	return crypto.Sign(digest, key)
}

type authorizationRepository struct {
	context      AuthorizationContext
	contextCalls int
	record       settlementdomain.AuthorizationRecord
	payment      settlementdomain.Payment
}

type agencyAuthorizationRepository struct {
	userID, orderID string
	identity        AgencyOrderIdentity
	called          bool
}

func (r *agencyAuthorizationRepository) CreateAgencyOrderConsent(_ context.Context, userID, orderID string, identity AgencyOrderIdentity, _ settlementdomain.SettlementConfig, _ time.Time) error {
	r.userID, r.orderID, r.identity, r.called = userID, orderID, identity, true
	return nil
}

type agencyIdentityReader struct{ identity AgencyOrderIdentity }

func (r agencyIdentityReader) ResolveAgencyOrderIdentity(context.Context, string, string, string) (AgencyOrderIdentity, error) {
	return r.identity, nil
}

func (r *authorizationRepository) GetAuthorizationContext(context.Context, string, string) (AuthorizationContext, error) {
	r.contextCalls++
	return r.context, nil
}

func (r *authorizationRepository) GetAuthorization(context.Context, string, string) (settlementdomain.AuthorizationRecord, error) {
	if r.record.ID == "" {
		return settlementdomain.AuthorizationRecord{}, settlementdomain.ErrAuthorizationNotFound
	}
	return r.record, nil
}

func (r *authorizationRepository) CreateAuthorization(_ context.Context, record settlementdomain.AuthorizationRecord, payment settlementdomain.Payment) error {
	r.record, r.payment = record, payment
	return nil
}

func (*authorizationRepository) RecordSubmittedTransaction(context.Context, string, string, string, time.Time) error {
	return nil
}

func (*authorizationRepository) RecordWalletTransaction(context.Context, string, string, string, string, time.Time) error {
	return nil
}

func (*authorizationRepository) GetRefundIntent(context.Context, string, string, time.Time) (settlementdomain.RefundIntent, error) {
	return settlementdomain.RefundIntent{}, nil
}

func (*authorizationRepository) RecordRefundTransaction(context.Context, string, string, string, time.Time) error {
	return nil
}

func (r *authorizationRepository) GetPayment(context.Context, string, string) (settlementdomain.Payment, error) {
	return r.payment, nil
}

func TestGetAuthorizationReturnsStoredRecordForResume(t *testing.T) {
	repository := &authorizationRepository{record: settlementdomain.AuthorizationRecord{
		ID:            "authorization-1",
		AgencyOrderID: "order-1",
	}}
	service := NewService(
		repository, nil, authorizationClock{}, &authorizationIDs{},
		settlementdomain.SettlementConfig{},
	)

	record, err := service.GetAuthorization(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "authorization-1" || record.AgencyOrderID != "order-1" {
		t.Fatalf("record=%+v", record)
	}
}

func validAuthorizationContext(now time.Time) AuthorizationContext {
	return AuthorizationContext{
		AgencyOrderID: "agency-order-1", AgencyOrderHash: "sha256:agency-order",
		ConsentAgencyOrderHash: "sha256:agency-order", AgencyOrderStatus: "USER_APPROVED",
		InstructionHash: "0xquote", ConsentInstructionHash: "0xquote",
		InstructionExpiresAt: now.Add(10 * time.Minute),
		ConsentAmount:        "50.5", InstructionAmount: "50.5",
		ConsentCurrency: "USD", InstructionCurrency: "USD", AmountBaseUnits: "50500000",
		PassThroughBaseUnits: "50000000", FeeBaseUnits: "500000",
		TokenAddress:      "0x1111111111111111111111111111111111111111",
		SettlementAddress: "0x2222222222222222222222222222222222222222", ChainID: 91342,
		MerchantID: "AMAZON_US", MerchantRegistryVersion: 1, FeeBps: 100,
		FeeRecipient:                      "0x3333333333333333333333333333333333333333",
		PrincipalRecipient:                "0x4444444444444444444444444444444444444444",
		CurrentMerchantPrincipalRecipient: "0x4444444444444444444444444444444444444444",
		CurrentMerchantRegistryVersion:    1,
		CurrentMerchantActive:             true,
		ConsentHash:                       "0xapproval",
		PayerWalletID:                     "wallet-1", WalletOwnershipProofID: "proof-1",
		PayerAddress:         "0x5555555555555555555555555555555555555555",
		PayerChainID:         "eip155:91342",
		PayerAccountID:       "eip155:91342:0x5555555555555555555555555555555555555555",
		OwnershipProofMethod: "EIP191_PERSONAL_SIGN",
		OwnershipMessageHash: "0xproof-message",
		OwnershipVerifiedAt:  now.Add(-time.Minute), OwnershipValidUntil: now.Add(time.Hour),
		TestPolicyID: "PHASE5_TEST_SETTLEMENT", TestPolicyVersion: "2026-07-24",
	}
}

func matchingSettlementConfig(
	contextValue AuthorizationContext,
) settlementdomain.SettlementConfig {
	return settlementdomain.SettlementConfig{
		ChainID:           contextValue.ChainID,
		TokenAddress:      contextValue.TokenAddress,
		SettlementAddress: contextValue.SettlementAddress,
		FeeBps:            contextValue.FeeBps,
		FeeRecipient:      contextValue.FeeRecipient,
	}
}

func TestAuthorizeSealsAndReusesEIP712Authorization(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	repository := &authorizationRepository{context: validAuthorizationContext(now)}
	ids := &authorizationIDs{values: []string{"nonce-source", "authorization-1", "payment-1"}}
	clock := &authorizationClock{now: now}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, clock, ids, matchingSettlementConfig(repository.context))
	consumer := enableInstructionGate(service)

	first, err := service.Authorize(context.Background(), "user-1", "agency-order-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Authorization.PassThroughAmount != "50000000" ||
		first.Authorization.FeeAmount != "500000" || first.Authorization.FeeBps != 100 {
		t.Fatalf("authorization lost exact payment terms: %#v", first.Authorization)
	}
	if total, totalErr := first.Authorization.TotalAmount(); totalErr != nil || total != "50500000" {
		t.Fatalf("authorization total=%s err=%v", total, totalErr)
	}
	if first.AgencyOrderID != "agency-order-1" {
		t.Fatalf("authorization lost AgencyOrder identity: %#v", first)
	}
	if consumer.calls != 1 {
		t.Fatalf("instruction consume calls=%d want=1", consumer.calls)
	}
	expectedPolicy := crypto.Keccak256Hash(
		[]byte("WALLET_OWNERSHIP_ONLY"),
	).Hex()
	if first.Authorization.AssuranceLevel != expectedPolicy {
		t.Fatalf(
			"authorization policy hash=%s want=%s",
			first.Authorization.AssuranceLevel, expectedPolicy,
		)
	}
	if first.Authorization.RefundAfter != first.Authorization.PayDeadline+3600 {
		t.Fatalf("unexpected refund window: %#v", first.Authorization)
	}
	if len(first.Signature) != 132 || first.TypedDataHash == "" {
		t.Fatalf("missing EIP-712 proof: %#v", first)
	}
	if first.Domain.ChainID != 91342 ||
		first.Domain.VerifyingContract != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("authorization did not seal its EIP-712 domain: %#v", first.Domain)
	}
	clock.now = now.Add(11 * time.Minute)
	rotatedKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	rotatedService := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(rotatedKey),
		address:    crypto.PubkeyToAddress(rotatedKey.PublicKey).Hex(),
	}, clock, &authorizationIDs{}, settlementdomain.SettlementConfig{
		ChainID: 1, SettlementAddress: "0x9999999999999999999999999999999999999999",
	})
	repository.context.CurrentMerchantActive = false
	second, err := rotatedService.Authorize(
		context.Background(), "user-1", "agency-order-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, first) || !reflect.DeepEqual(repository.record, first) ||
		repository.payment.State != settlementdomain.PaymentAuthorized ||
		repository.contextCalls != 1 ||
		second.Authorization.PayDeadline >= uint64(clock.now.Unix()) {
		t.Fatalf(
			"expired authorization retry after signer/config rotation was not exact:\nfirst=%#v\nsecond=%#v",
			first, second,
		)
	}
}

func TestAuthorizeRollsBackWhenInstructionCannotBeConsumed(t *testing.T) {
	now := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	repository := &authorizationRepository{context: validAuthorizationContext(now)}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{
		values: []string{"nonce-source", "authorization-1", "payment-1"},
	}, matchingSettlementConfig(repository.context))
	consumer := &authorizationInstructionGate{err: errors.New("instruction consumed")}
	service.EnableInstructionGate(
		consumer, authorizationRepositoryTransactor{repository: repository},
	)

	_, err = service.Authorize(context.Background(), "user-1", "agency-order-1")
	if !errors.Is(err, settlementdomain.ErrPaymentStateInvalid) {
		t.Fatalf("Authorize error=%v want=%v", err, settlementdomain.ErrPaymentStateInvalid)
	}
	if consumer.calls != 1 {
		t.Fatalf("instruction consume calls=%d want=1", consumer.calls)
	}
	if repository.record.ID != "" || repository.payment.ID != "" {
		t.Fatalf("authorization escaped rollback: record=%#v payment=%#v",
			repository.record, repository.payment)
	}
}

func TestAuthorizeAgencyOrderCreatesConsentAndKeepsCanonicalIdentity(t *testing.T) {
	now := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	contextValue := validAuthorizationContext(now)
	contextValue.AgencyOrderID = "agency-order-1"
	contextValue.AgencyOrderHash = "sha256:agency-order"
	contextValue.ConsentAgencyOrderHash = contextValue.AgencyOrderHash
	contextValue.InstructionHash = contextValue.AgencyOrderHash
	contextValue.ConsentInstructionHash = contextValue.AgencyOrderHash
	contextValue.ConsentHash = contextValue.AgencyOrderHash
	repository := &authorizationRepository{context: contextValue}
	identity := AgencyOrderIdentity{
		WalletID: "wallet-1", OwnershipProofID: "proof-1",
		PayerAddress: contextValue.PayerAddress, PayerChainID: contextValue.PayerChainID,
	}
	agencyRepository := &agencyAuthorizationRepository{}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{
		values: []string{"nonce-source", "authorization-1", "payment-1"},
	}, matchingSettlementConfig(contextValue))
	service.EnableAgencyOrders(agencyRepository, agencyIdentityReader{identity: identity})
	enableInstructionGate(service)

	record, err := service.AuthorizeAgencyOrder(
		context.Background(), "user-1", "agency-order-1", "wallet-1", "proof-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !agencyRepository.called || agencyRepository.userID != "user-1" ||
		agencyRepository.orderID != "agency-order-1" || agencyRepository.identity.WalletID != "wallet-1" {
		t.Fatalf("AgencyOrder consent was not sealed first: %#v", agencyRepository)
	}
	if record.AgencyOrderID != "agency-order-1" {
		t.Fatalf("AgencyOrder authorization exposed the wrong source: %#v", record)
	}
	if repository.payment.AgencyOrderID != "agency-order-1" {
		t.Fatalf("AgencyOrder payment lost source identity: %#v", repository.payment)
	}
}

func TestRecordTransactionsRejectsNonHash(t *testing.T) {
	service := NewService(&authorizationRepository{}, nil, authorizationClock{}, &authorizationIDs{}, settlementdomain.SettlementConfig{})
	if !errors.Is(service.RecordPayTransaction(context.Background(), "u", "p", "0x12"), settlementdomain.ErrTransactionInvalid) {
		t.Fatal("invalid pay hash was accepted")
	}
	if !errors.Is(service.RecordRefundTransaction(context.Background(), "u", "p", "wrong"), settlementdomain.ErrTransactionInvalid) {
		t.Fatal("invalid refund hash was accepted")
	}
}

func TestAuthorizeRejectsAmountSplitMismatch(t *testing.T) {
	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	// 총액 ≠ passThrough + fee 이면 표시 금액과 서명 금액이 어긋난다 — fail-closed.
	contextValue := validAuthorizationContext(now)
	contextValue.FeeBaseUnits = "400000"
	repository := &authorizationRepository{context: contextValue}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{
		values: []string{"nonce-source", "authorization-1", "payment-1"},
	}, matchingSettlementConfig(contextValue))
	if _, err := service.Authorize(context.Background(), "user-1", "agency-order-1"); !errors.Is(
		err, settlementdomain.ErrAuthorizationInvalid,
	) {
		t.Fatalf("amount split mismatch must block authorization, got %v", err)
	}
}

func TestAuthorizeRejectsExpiredOwnershipProof(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	authorizationContext := validAuthorizationContext(now)
	authorizationContext.OwnershipValidUntil = now
	repository := &authorizationRepository{context: authorizationContext}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{}, matchingSettlementConfig(authorizationContext))
	_, err = service.Authorize(context.Background(), "user-1", "agency-order-1")
	if !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("expired ownership proof must block authorization, got %v", err)
	}
}

func TestAuthorizeRejectsOwnershipProofThatDoesNotCoverPayDeadline(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	authorizationContext := validAuthorizationContext(now)
	authorizationContext.OwnershipValidUntil = authorizationContext.InstructionExpiresAt.Add(-time.Second)
	repository := &authorizationRepository{context: authorizationContext}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{}, matchingSettlementConfig(authorizationContext))
	_, err = service.Authorize(context.Background(), "user-1", "agency-order-1")
	if !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("proof shorter than pay deadline must block authorization, got %v", err)
	}
}

func TestAuthorizeRejectsExpiredInstructionForFirstIssuance(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	authorizationContext := validAuthorizationContext(now)
	authorizationContext.InstructionExpiresAt = now
	repository := &authorizationRepository{context: authorizationContext}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{}, matchingSettlementConfig(authorizationContext))
	_, err = service.Authorize(context.Background(), "user-1", "agency-order-1")
	if !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("expired instruction must block first authorization issuance, got %v", err)
	}
}

func TestAuthorizeAcceptsCaseInsensitiveSettlementAndMerchantAddresses(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	contextValue := validAuthorizationContext(now)
	contextValue.TokenAddress = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	contextValue.SettlementAddress = "0xabcdefabcdefabcdefabcdefabcdefabcdefabce"
	contextValue.FeeRecipient = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcf"
	contextValue.PrincipalRecipient = "0xabcdefabcdefabcdefabcdefabcdefabcdefabc0"
	contextValue.CurrentMerchantPrincipalRecipient = uppercaseAddress(
		contextValue.PrincipalRecipient,
	)
	config := matchingSettlementConfig(contextValue)
	config.TokenAddress = uppercaseAddress(config.TokenAddress)
	config.SettlementAddress = uppercaseAddress(config.SettlementAddress)
	config.FeeRecipient = uppercaseAddress(config.FeeRecipient)
	repository := &authorizationRepository{context: contextValue}
	service := NewService(repository, authorizationSigner{
		privateKey: crypto.FromECDSA(key),
		address:    crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}, authorizationClock{now}, &authorizationIDs{
		values: []string{"nonce-source", "authorization-1", "payment-1"},
	}, config)
	enableInstructionGate(service)

	record, err := service.Authorize(context.Background(), "user-1", "agency-order-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Authorization.Token != strings.ToLower(contextValue.TokenAddress) ||
		record.Authorization.FeeRecipient != strings.ToLower(contextValue.FeeRecipient) ||
		record.Authorization.PrincipalRecipient != strings.ToLower(contextValue.PrincipalRecipient) ||
		record.Domain.VerifyingContract != strings.ToLower(contextValue.SettlementAddress) {
		t.Fatalf("authorization addresses were not normalized: %#v", record)
	}
}

func TestAuthorizeRejectsStaleInstructionBeforeFirstIssuance(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*AuthorizationContext, *settlementdomain.SettlementConfig)
	}{
		{
			name: "chain",
			mutate: func(_ *AuthorizationContext, config *settlementdomain.SettlementConfig) {
				config.ChainID++
			},
		},
		{
			name: "token",
			mutate: func(_ *AuthorizationContext, config *settlementdomain.SettlementConfig) {
				config.TokenAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "settlement",
			mutate: func(_ *AuthorizationContext, config *settlementdomain.SettlementConfig) {
				config.SettlementAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "fee bps",
			mutate: func(_ *AuthorizationContext, config *settlementdomain.SettlementConfig) {
				config.FeeBps++
			},
		},
		{
			name: "fee recipient",
			mutate: func(_ *AuthorizationContext, config *settlementdomain.SettlementConfig) {
				config.FeeRecipient = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "merchant principal",
			mutate: func(contextValue *AuthorizationContext, _ *settlementdomain.SettlementConfig) {
				contextValue.CurrentMerchantPrincipalRecipient =
					"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "merchant registry version",
			mutate: func(contextValue *AuthorizationContext, _ *settlementdomain.SettlementConfig) {
				contextValue.CurrentMerchantRegistryVersion++
			},
		},
		{
			name: "inactive merchant",
			mutate: func(contextValue *AuthorizationContext, _ *settlementdomain.SettlementConfig) {
				contextValue.CurrentMerchantActive = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contextValue := validAuthorizationContext(now)
			config := matchingSettlementConfig(contextValue)
			test.mutate(&contextValue, &config)
			repository := &authorizationRepository{context: contextValue}
			service := NewService(repository, authorizationSigner{
				privateKey: crypto.FromECDSA(key),
				address:    crypto.PubkeyToAddress(key.PublicKey).Hex(),
			}, authorizationClock{now}, &authorizationIDs{
				values: []string{"nonce-source", "authorization-1", "payment-1"},
			}, config)

			_, err := service.Authorize(context.Background(), "user-1", "agency-order-1")
			if !errors.Is(err, settlementdomain.ErrAuthorizationInstructionStale) {
				t.Fatalf("error=%v want=%v", err, settlementdomain.ErrAuthorizationInstructionStale)
			}
			if repository.record.ID != "" || repository.payment.ID != "" {
				t.Fatalf(
					"stale instruction created records: authorization=%#v payment=%#v",
					repository.record, repository.payment,
				)
			}
		})
	}
}

func uppercaseAddress(value string) string {
	return value[:2] + strings.ToUpper(value[2:])
}

func TestPendingInstructionCannotCreateGIWASignatureOrNonce(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	facts := validAuthorizationContext(now)
	repo := &authorizationRepository{context: facts}
	ids := &authorizationIDs{values: []string{"nonce", "authorization", "payment"}}
	service := NewService(repo, authorizationSigner{privateKey: crypto.FromECDSA(key), address: crypto.PubkeyToAddress(key.PublicKey).Hex()}, authorizationClock{now}, ids, matchingSettlementConfig(facts))
	gate := enableInstructionGate(service)
	gate.err = procmsg.ErrInstructionPending
	record, err := service.Authorize(context.Background(), "user-1", "agency-order-1")
	if !errors.Is(err, procmsg.ErrInstructionPending) || record.ID != "" || record.Signature != "" || repo.record.ID != "" || repo.payment.ID != "" || len(ids.values) != 3 {
		t.Fatalf("pending confirmation created authorization/payment/nonce: err=%v record=%+v ids=%v", err, record, ids.values)
	}
	gate.err = nil
	if _, err := service.Authorize(context.Background(), "user-1", "agency-order-1"); err != nil {
		t.Fatal(err)
	}
	if repo.record.ID == "" || repo.record.Signature == "" || gate.calls != 2 {
		t.Fatal("confirmed instruction did not resume existing approval")
	}
}

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountinfra "github.com/vitlane/vitlane/server/internal/account/infra"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	settlementhttp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/iface/http"
	settlementaccount "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/infra/account"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// Exercise the SQL consent gate used before Service.Authorize. The app's sealed
// signature replay tests alone cannot catch first-issuance checks in this gate.
func TestAgencyOrderConsentReplayAfterInstructionExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := seedGIWACompensationGraph(t, ctx, 1)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	clock := &testfixture.Clock{Time: f.now}
	ids := sharedapp.UUIDGenerator{}
	wallets := accountapp.NewWalletVerificationService(accountpostgres.NewRepository(f.database), accountinfra.CryptoSecretGenerator{}, clock, ids, "https://test.vitlane.example", "eip155:91342")
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := wallets.CreateRegistrationAttempt(ctx, accountapp.CreateRegistrationAttemptInput{UserID: giwaTestUserID, Address: crypto.PubkeyToAddress(key.PublicKey).Hex(), ChainID: "eip155:91342", ClientOperationID: ids.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(attempt.Attempt.Message)), key)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := wallets.CompleteRegistrationAttempt(ctx, accountapp.CompleteRegistrationAttemptInput{UserID: giwaTestUserID, AttemptID: string(attempt.Attempt.ID), Nonce: attempt.Attempt.Nonce, Signature: hexutil.Encode(signature), ClientOperationID: ids.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	identity := settlementapp.AgencyOrderIdentity{WalletID: string(registered.Wallet.ID), OwnershipProofID: string(registered.OwnershipProof.ID), PayerAddress: registered.Wallet.Address, PayerChainID: registered.Wallet.ChainID, OwnershipValidUntil: registered.OwnershipProof.ValidUntil}
	config := settlementdomain.SettlementConfig{ChainCAIP2: "eip155:91342", ChainID: 91342, TokenAddress: "0x1111111111111111111111111111111111111111", SettlementAddress: giwaTestSettlementAddr, FeeRecipient: "0x2222222222222222222222222222222222222222", FeeBps: 100}
	r := NewRepository(f.database)
	exec(`INSERT INTO merchant_registry_entries(merchant_id,display_name,domain_suffixes,country,currency,fulfillment_mode,payment_enabled,principal_recipient,registry_version,active,updated_at)
		VALUES($1,'TEST',ARRAY['test.example'],'US','USD','MANUAL_MERCHANT_ORDER',true,$2,1,true,$3)`, settlementdomain.GenericWebUSDSettlementPathID, config.FeeRecipient, f.now)
	exec(`INSERT INTO user_policy_acceptances(user_id,policy_id,policy_version,accepted_at) VALUES($1,'PHASE5_TEST_SETTLEMENT','2026-07-24',$2)`, giwaTestUserID, f.now)
	exec(`INSERT INTO agency_order_payment_instructions(id,agency_order_id,user_id,agency_order_snapshot_hash,amount_minor,currency,rail,asset,provider_environment,economic_effect,merchant_execution_mode,execution_profile_hash,state,payload,expires_at,created_at)
		VALUES(gen_random_uuid(),$1,$2,'order-hash',1465,'USD','GIWA','TVITUSD','TESTNET','NO_REAL_VALUE','SIMULATED_NO_EFFECT',$3,'PENDING','{}',$4::timestamptz+interval '20 minutes',$4)`, giwaTestOrderID, giwaTestUserID, giwaTestProfileHash, f.now)
	late := f.now.Add(21 * time.Minute)
	if err := r.CreateAgencyOrderConsent(ctx, giwaTestUserID, giwaTestOrderID, identity, config, late); !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("expired first consent: %v", err)
	}
	if err := r.CreateAgencyOrderConsent(ctx, giwaTestUserID, giwaTestOrderID, identity, config, f.now); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateAgencyOrderConsent(ctx, giwaTestUserID, giwaTestOrderID, identity, config, late); !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("consent without sealed authorization bypassed expiry: %v", err)
	}
	exec(`UPDATE agency_order_payment_instructions SET state='CONSUMED' WHERE agency_order_id=$1`, giwaTestOrderID)
	exec(`INSERT INTO settlement_authorizations(id,agency_order_id,order_hash,payer,nonce,pay_deadline,refund_after,signer_address,typed_data_hash,authorization_payload,created_at)
		VALUES(gen_random_uuid(),$1,$2,$3,1,$4::timestamptz+interval '10 minutes',$4::timestamptz+interval '70 minutes',$3,'sealed-hash','{"signature":"sealed-test-value"}',$4)`, giwaTestOrderID, giwaTestOrderHash, identity.PayerAddress, f.now)
	readSnapshot := func() string {
		t.Helper()
		var result string
		if err := f.database.DB.QueryRowContext(ctx, `SELECT jsonb_build_object('consent',to_jsonb(c),'authorization',to_jsonb(a),'payment',to_jsonb(p),'instruction',to_jsonb(i))::text FROM agency_order_payment_consents c JOIN settlement_authorizations a USING(agency_order_id) JOIN settlement_payments p USING(agency_order_id) JOIN agency_order_payment_instructions i USING(agency_order_id) WHERE c.agency_order_id=$1`, giwaTestOrderID).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := readSnapshot()
	// Registry/config rotation must not turn a sealed result into a new issue.
	exec(`UPDATE merchant_registry_entries SET active=false,registry_version=2 WHERE merchant_id=$1`, settlementdomain.GenericWebUSDSettlementPathID)
	rotated := config
	rotated.ChainID = 1
	rotated.TokenAddress = "0x3333333333333333333333333333333333333333"
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.CreateAgencyOrderConsent(ctx, giwaTestUserID, giwaTestOrderID, identity, rotated, late)
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("sealed replay after expiry: %v", err)
		}
	}
	for _, field := range []string{"wallet", "proof", "user"} {
		changed, user := identity, giwaTestUserID
		switch field {
		case "wallet":
			changed.WalletID = ids.NewID()
		case "proof":
			changed.OwnershipProofID = ids.NewID()
		case "user":
			user = ids.NewID()
		}
		if err := r.CreateAgencyOrderConsent(ctx, user, giwaTestOrderID, changed, config, late); !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
			t.Fatalf("changed %s was not rejected: %v", field, err)
		}
	}
	if after := readSnapshot(); before != after {
		t.Fatal("replay changed consent, authorization, payment, or instruction")
	}
	service := settlementapp.NewService(r, nil, clock, ids, config)
	service.EnableAgencyOrders(r, settlementaccount.NewAgencyOrderIdentityAdapter(wallets))
	handler := settlementhttp.NewHandler(service)
	for _, expired := range []bool{false, true} {
		proofID := ids.NewID()
		if expired {
			clock.Time = f.now.Add(25 * time.Hour)
			proofID = identity.OwnershipProofID
		}
		body, err := json.Marshal(map[string]string{"walletId": identity.WalletID, "ownershipProofId": proofID})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agencyOrder/"+giwaTestOrderID+"/settlement-authorizations", strings.NewReader(string(body)))
		request.SetPathValue("agencyOrderId", giwaTestOrderID)
		request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), giwaTestUserID))
		recorder := httptest.NewRecorder()
		handler.AuthorizeAgencyOrder(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "SETTLEMENT_WALLET_OWNERSHIP_REQUIRED") {
			t.Fatalf("expired=%v status=%d response=%s", expired, recorder.Code, recorder.Body.String())
		}
	}
	if before != readSnapshot() {
		t.Fatal("rejected Wallet proof changed sealed payment data")
	}
	// A damaged sealed graph fails closed instead of reconstructing consent.
	exec(`DELETE FROM agency_order_payment_consents WHERE agency_order_id=$1`, giwaTestOrderID)
	if err := r.CreateAgencyOrderConsent(ctx, giwaTestUserID, giwaTestOrderID, identity, config, late); !errors.Is(err, settlementdomain.ErrAuthorizationInvalid) {
		t.Fatalf("missing sealed consent accepted: %v", err)
	}
}

package dojang

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

func TestLiveGiwaDojangContracts(t *testing.T) {
	rpcURL := os.Getenv("DOJANG_TEST_RPC_URL")
	if rpcURL == "" {
		t.Skip("DOJANG_TEST_RPC_URL is not set")
	}
	provider, err := DialProvider(context.Background(), rpcURL, Config{
		Level:           accountdomain.AssuranceDojangVerifiedAddress,
		ScrollAddress:   "0xd5077b67dcb56caC8b270C7788FC3E6ee03F17B9",
		EASAddress:      "0x4200000000000000000000000000000000000021",
		AttesterID:      "0xd99b42e778498aa3c9c1f6a012359130252780511687a35982e8e52735453034",
		AttesterAddress: "0x4097bF3Cb731AEB3E501b910B33B2aF9Fa68E388",
		SchemaUID:       "0x072d75e18b2be4f89a13a7147240477481c4b526d5795802acba59046b426e08",
	}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	_, err = provider.Verify(
		context.Background(), "0x0000000000000000000000000000000000000000", time.Now(),
	)
	if !errors.Is(err, accountdomain.ErrAssuranceInvalid) {
		t.Fatalf("unattested zero address must fail closed, got %v", err)
	}
}

func TestLiveGiwaDojangTestFaucetAttestation(t *testing.T) {
	rpcURL := os.Getenv("DOJANG_TEST_RPC_URL")
	wallet := os.Getenv("DOJANG_TEST_ATTESTED_WALLET")
	if rpcURL == "" || wallet == "" {
		t.Skip("DOJANG_TEST_RPC_URL and DOJANG_TEST_ATTESTED_WALLET are not set")
	}
	provider, err := DialProvider(context.Background(), rpcURL, Config{
		Level:           accountdomain.AssuranceDojangTestFaucet,
		ScrollAddress:   "0xd5077b67dcb56caC8b270C7788FC3E6ee03F17B9",
		EASAddress:      "0x4200000000000000000000000000000000000021",
		AttesterID:      "0xaa92f8c143657dde575de430aecaea6ca91f2e6072339b16932d426895d8d678",
		AttesterAddress: "0x63CCe2b569A7bC35895ee24306c1512fefc06121",
		SchemaUID:       "0x072d75e18b2be4f89a13a7147240477481c4b526d5795802acba59046b426e08",
	}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	evidence, err := provider.Verify(context.Background(), wallet, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Level != accountdomain.AssuranceDojangTestFaucet ||
		evidence.EvidenceHash == "" || !evidence.ExpiresAt.After(time.Now()) {
		t.Fatalf("unexpected live test-faucet evidence: %#v", evidence)
	}
}

func TestValidateVerifiedAddressAttestation(t *testing.T) {
	config := Config{
		Level:           accountdomain.AssuranceDojangVerifiedAddress,
		ScrollAddress:   "0xd5077b67dcb56caC8b270C7788FC3E6ee03F17B9",
		EASAddress:      "0x4200000000000000000000000000000000000021",
		AttesterID:      "0xd99b42e778498aa3c9c1f6a012359130252780511687a35982e8e52735453034",
		AttesterAddress: "0x4097bF3Cb731AEB3E501b910B33B2aF9Fa68E388",
		SchemaUID:       "0x072d75e18b2be4f89a13a7147240477481c4b526d5795802acba59046b426e08",
	}
	now := time.Date(2026, 7, 23, 4, 0, 0, 0, time.UTC)
	uid := common.HexToHash("0x1234")
	boolType, err := abi.NewType("bool", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := (abi.Arguments{{Type: boolType}}).Pack(true)
	if err != nil {
		t.Fatal(err)
	}
	attestation := easAttestation{
		UID: uid, Schema: common.HexToHash(config.SchemaUID),
		Time:           uint64(now.Add(-time.Hour).Unix()),
		ExpirationTime: uint64(now.Add(time.Hour).Unix()),
		Recipient:      common.HexToAddress("0xa0Ee7A142d267C1f36714E4a8F75612F20a79720"),
		Attester:       common.HexToAddress(config.AttesterAddress), Data: data,
	}
	evidence, err := validateAttestation(
		config, common.HexToHash(config.SchemaUID), uid, attestation.Recipient,
		attestation, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Level != accountdomain.AssuranceDojangVerifiedAddress ||
		evidence.EvidenceHash == "" || !evidence.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}

	attestation.RevocationTime = uint64(now.Unix())
	if _, err := validateAttestation(
		config, common.HexToHash(config.SchemaUID), uid, attestation.Recipient,
		attestation, now,
	); !errors.Is(err, accountdomain.ErrAssuranceInvalid) {
		t.Fatalf("revoked attestation must fail, got %v", err)
	}
}

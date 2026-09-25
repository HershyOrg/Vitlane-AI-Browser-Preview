package mockdojang

import (
	"context"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

func TestProviderUsesPendingThenVerifiedSimulatedLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	provider := Provider{}
	started, err := provider.Start(
		context.Background(),
		accountapp.KYCProviderStartRequest{
			CaseID:             "case-1",
			UserID:             "user-1",
			WalletID:           "wallet-1",
			OwnershipProofID:   "proof-1",
			Address:            "0x1111111111111111111111111111111111111111",
			ChainID:            "eip155:91342",
			ProviderRequestKey: "provider-start-1",
			Now:                now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.State != accountdomain.KYCStatePendingProvider ||
		started.ProviderCaseRef != "mock-dojang:case-1" {
		t.Fatalf("start result mismatch: %#v", started)
	}
	checked, err := provider.Check(
		context.Background(),
		accountapp.KYCProviderCheckRequest{
			ProviderCaseRef:    started.ProviderCaseRef,
			Address:            "0x1111111111111111111111111111111111111111",
			ProviderRequestKey: "provider-check-1",
			Now:                now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if checked.State != accountdomain.KYCStateVerified ||
		checked.Evidence == nil ||
		checked.Evidence.ObservationStatus != accountdomain.KYCEvidenceValid ||
		checked.Evidence.SourceVersion != 1 ||
		checked.Evidence.EvidenceHash == "" ||
		checked.Evidence.ValidUntil.Sub(checked.Evidence.IssuedAt) !=
			24*time.Hour {
		t.Fatalf("check result mismatch: %#v", checked)
	}
}

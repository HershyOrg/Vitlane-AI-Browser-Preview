package account

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

type rejectedIdentityRepository struct {
	accountapp.WalletVerificationRepository
	err error
}

func (r rejectedIdentityRepository) LockPaymentIdentity(context.Context, accountdomain.UserID, accountdomain.WalletID, accountdomain.WalletOwnershipProofID) (accountdomain.Wallet, accountdomain.WalletOwnershipProof, error) {
	return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{}, r.err
}

func TestAgencyOrderIdentityClassifiesOwnershipRejection(t *testing.T) {
	unavailable := errors.New("database unavailable")
	for _, tc := range []struct {
		name        string
		cause, want error
	}{
		{"invalid", accountdomain.ErrWalletOwnershipProofInvalid, settlementdomain.ErrWalletOwnershipRequired},
		{"expired", accountdomain.ErrWalletOwnershipProofExpired, settlementdomain.ErrWalletOwnershipRequired},
		{"missing", accountdomain.ErrWalletOwnershipProofMissing, settlementdomain.ErrWalletOwnershipRequired},
		{"internal", unavailable, unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := rejectedIdentityRepository{err: fmt.Errorf("lookup: %w", tc.cause)}
			service := accountapp.NewWalletVerificationService(repository, nil, nil, nil, "https://test.example", "eip155:91342")
			identity, err := NewAgencyOrderIdentityAdapter(service).ResolveAgencyOrderIdentity(context.Background(), "user", "wallet", "proof")
			if !errors.Is(err, tc.want) || identity.WalletID != "" {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
		})
	}
}

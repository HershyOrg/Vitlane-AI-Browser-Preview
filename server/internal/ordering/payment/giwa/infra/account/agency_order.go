package account

import (
	"context"
	"errors"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

type AgencyOrderIdentityAdapter struct {
	service *accountapp.WalletVerificationService
}

func NewAgencyOrderIdentityAdapter(service *accountapp.WalletVerificationService) *AgencyOrderIdentityAdapter {
	return &AgencyOrderIdentityAdapter{service: service}
}

func (a *AgencyOrderIdentityAdapter) ResolveAgencyOrderIdentity(ctx context.Context, userID, walletID, proofID string) (settlementapp.AgencyOrderIdentity, error) {
	wallet, proof, err := a.service.GetPaymentIdentity(ctx, userID, walletID, proofID)
	if err != nil {
		if errors.Is(err, accountdomain.ErrWalletOwnershipProofInvalid) ||
			errors.Is(err, accountdomain.ErrWalletOwnershipProofExpired) ||
			errors.Is(err, accountdomain.ErrWalletOwnershipProofMissing) {
			return settlementapp.AgencyOrderIdentity{}, settlementdomain.ErrWalletOwnershipRequired
		}
		return settlementapp.AgencyOrderIdentity{}, err
	}
	return settlementapp.AgencyOrderIdentity{
		WalletID: string(wallet.ID), OwnershipProofID: string(proof.ID),
		PayerAccountID: wallet.AccountID, PayerAddress: wallet.Address, PayerChainID: wallet.ChainID,
		OwnershipProofMethod: proof.Method, OwnershipMessageHash: proof.MessageHash,
		OwnershipVerifiedAt: proof.VerifiedAt, OwnershipValidUntil: proof.ValidUntil,
	}, nil
}

package mockdojang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

const (
	issuerRef = "vitlane.mock-dojang.v2"
	schemaRef = "mock-kyc-test-product.v2"
)

type Provider struct{}

func (Provider) Start(
	_ context.Context,
	request accountapp.KYCProviderStartRequest,
) (accountapp.KYCProviderStartResult, error) {
	if strings.TrimSpace(request.CaseID) == "" ||
		request.UserID == "" ||
		request.WalletID == "" ||
		request.OwnershipProofID == "" ||
		strings.TrimSpace(request.Address) == "" ||
		strings.TrimSpace(request.ProviderRequestKey) == "" {
		return accountapp.KYCProviderStartResult{},
			accountdomain.ErrKYCVerificationInvalid
	}
	return accountapp.KYCProviderStartResult{
		ProviderCaseRef: "mock-dojang:" + request.CaseID,
		State:           accountdomain.KYCStatePendingProvider,
	}, nil
}

func (Provider) Check(
	_ context.Context,
	request accountapp.KYCProviderCheckRequest,
) (accountapp.KYCProviderCheckResult, error) {
	if !strings.HasPrefix(
		strings.TrimSpace(request.ProviderCaseRef),
		"mock-dojang:",
	) ||
		strings.TrimSpace(request.Address) == "" ||
		strings.TrimSpace(request.ProviderRequestKey) == "" {
		return accountapp.KYCProviderCheckResult{},
			accountdomain.ErrKYCVerificationInvalid
	}
	hash := sha256.Sum256([]byte(
		strings.ToLower(request.Address) + ":" +
			request.ProviderCaseRef + ":" +
			request.ProviderRequestKey + ":" + issuerRef,
	))
	return accountapp.KYCProviderCheckResult{
		State: accountdomain.KYCStateVerified,
		Evidence: &accountapp.KYCCredentialEvidence{
			IssuerRef:         issuerRef,
			SchemaRef:         schemaRef,
			EvidenceHash:      "0x" + hex.EncodeToString(hash[:]),
			IssuedAt:          request.Now,
			ValidUntil:        request.Now.Add(24 * time.Hour),
			ObservationStatus: accountdomain.KYCEvidenceValid,
			SourceVersion:     1,
			ObservedAt:        request.Now,
			RecheckAfter:      request.Now.Add(24 * time.Hour),
		},
	}, nil
}

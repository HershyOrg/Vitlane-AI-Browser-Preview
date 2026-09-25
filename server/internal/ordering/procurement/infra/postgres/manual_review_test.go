package postgres

import (
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

func TestSameManualDecisionAttemptBindsAllOperatorEvidence(t *testing.T) {
	observedAt := time.Date(2026, 8, 27, 12, 0, 0, 123456789, time.UTC)
	record := domain.DecisionRecord{
		TaskID: "task-1", DecidedByUserID: "operator-1",
		Decision:        domain.DecisionWithinAuthorization,
		PublicRationale: "Within the approved customer conditions.",
		InternalNote:    "accounting note", ObservedCondition: "Variant and price matched",
		EvidenceSource: domain.EvidenceMerchantPage, EvidenceHash: "0123456789abcdef",
		ObservedAt: observedAt.Round(time.Microsecond),
	}
	same := func(internalNote string, source domain.EvidenceSource, at time.Time) bool {
		return sameManualDecisionAttempt(
			record, "task-1", "operator-1", domain.DecisionWithinAuthorization,
			"Within the approved customer conditions.", internalNote,
			"Variant and price matched", source, "0123456789abcdef", at,
		)
	}
	if !same("accounting note", domain.EvidenceMerchantPage, observedAt) {
		t.Fatal("identical decision attempt should replay")
	}
	if same("changed note", domain.EvidenceMerchantPage, observedAt) {
		t.Fatal("changed private note must conflict")
	}
	if same("accounting note", domain.EvidenceMerchantPolicy, observedAt) {
		t.Fatal("changed evidence source must conflict")
	}
	if same("accounting note", domain.EvidenceMerchantPage, observedAt.Add(time.Second)) {
		t.Fatal("changed observation time must conflict")
	}
}

func TestSameCustomerRequestAttemptBindsOptionsAndPublicContext(t *testing.T) {
	request := domain.CustomerRequest{
		RequestedByUserID: "operator-1", Kind: domain.RequestInformation,
		Prompt: "Choose a color", ResponseType: domain.ResponseSingleChoice,
		ResponseOptions: []string{"Navy", "Black"}, PublicContext: "The original color is unavailable.",
	}
	same := func(options []string, publicContext string) bool {
		return sameCustomerRequestAttempt(
			request, "operator-1", domain.RequestInformation, "Choose a color",
			domain.ResponseSingleChoice, options, publicContext,
		)
	}
	if !same([]string{"Navy", "Black"}, "The original color is unavailable.") {
		t.Fatal("identical customer request should replay")
	}
	if same([]string{"Navy", "Red"}, "The original color is unavailable.") {
		t.Fatal("changed response options must conflict")
	}
	if same([]string{"Navy", "Black"}, "Different customer context") {
		t.Fatal("changed public context must conflict")
	}
}

func TestAuthorizationAllowsManualProcurementRequiresCurrentKindAndExactProfile(t *testing.T) {
	const (
		sandboxProfile = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
		liveProfile    = "0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a"
	)
	tests := []struct {
		name, kind, authorizationProfile, orderProfile string
		want                                           bool
	}{
		{
			name: "v1 live", kind: string(domain.AuthorizationManualOperatorPurchase),
			authorizationProfile: liveProfile, orderProfile: liveProfile, want: true,
		},
		{
			name: "v1 sandbox", kind: string(domain.AuthorizationManualOperatorPurchase),
			authorizationProfile: sandboxProfile, orderProfile: sandboxProfile,
			want: true,
		},
		{
			name: "profile mismatch", kind: string(domain.AuthorizationManualOperatorPurchase),
			authorizationProfile: sandboxProfile, orderProfile: liveProfile, want: false,
		},
		{
			name: "empty profile", kind: string(domain.AuthorizationManualOperatorPurchase),
			authorizationProfile: "", orderProfile: "", want: false,
		},
		{
			name: "unknown kind", kind: "OBSOLETE_KIND",
			authorizationProfile: sandboxProfile, orderProfile: sandboxProfile,
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := authorizationAllowsManualProcurement(
				test.kind, test.authorizationProfile, test.orderProfile,
			)
			if got != test.want {
				t.Fatalf("authorizationAllowsManualProcurement()=%t want=%t", got, test.want)
			}
		})
	}
}

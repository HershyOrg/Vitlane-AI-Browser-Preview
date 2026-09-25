package domain

import "testing"

func TestOrderExecutionProfileCanonicalTuplesAndHashes(t *testing.T) {
	cases := []struct {
		rail PaymentRail
		hash string
	}{
		{PaymentRailPayPalSandbox, "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"},
		{PaymentRailTVITUSD, "0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732"},
		{PaymentRailPayPalLive, "0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a"},
	}
	for _, test := range cases {
		profile, err := ExecutionProfileForRail(test.rail)
		if err != nil {
			t.Fatalf("profile %s: %v", test.rail, err)
		}
		if err := profile.Validate(); err != nil {
			t.Fatalf("validate %s: %v", test.rail, err)
		}
		hash, err := profile.Hash()
		if err != nil {
			t.Fatalf("hash %s: %v", test.rail, err)
		}
		if hash != test.hash {
			t.Fatalf("hash %s=%s want=%s", test.rail, hash, test.hash)
		}
		selection := profile.LegacyPaymentSelection()
		if !profile.MatchesSelection(selection) {
			t.Fatalf("profile %s does not match compatibility selection %#v", test.rail, selection)
		}
	}
}

func TestOrderExecutionProfileRejectsMixedMode(t *testing.T) {
	mixed, err := ExecutionProfileForRail(PaymentRailPayPalSandbox)
	if err != nil {
		t.Fatal(err)
	}
	mixed.EconomicEffect = EconomicEffectRealMoney
	if err := mixed.Validate(); err == nil {
		t.Fatal("Sandbox real-money tuple must be rejected")
	}
	mixed, err = ExecutionProfileForRail(PaymentRailPayPalLive)
	if err != nil {
		t.Fatal(err)
	}
	mixed.MerchantExecutionMode = MerchantExecutionSimulatedNoEffect
	if err := mixed.Validate(); err == nil {
		t.Fatal("PayPal Live simulated merchant tuple must be rejected")
	}
}

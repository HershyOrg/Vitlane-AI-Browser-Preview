package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionProfileMigrationCarriesExactImmutableHandoffs(t *testing.T) {
	up, err := os.ReadFile("../../../../../migrations/000082_order_execution_profiles.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../../../migrations/000082_order_execution_profiles.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	upSQL := string(up)
	downSQL := string(down)
	for _, required := range []string{
		"provider_environment='SANDBOX' AND asset='USD'",
		"provider_environment='TESTNET' AND asset='TVITUSD'",
		"provider_environment='LIVE' AND asset='USD'",
		"economic_effect='REAL_MONEY'",
		"merchant_execution_mode='LIVE_MERCHANT_EFFECT'",
		"trg_agency_order_execution_profile_immutable",
		"agency_order_payment_instructions_order_profile_fk",
		"payment_customer_payments_order_profile_fk",
		"payment_funds_receipts_payment_profile_fk",
		"procurement_manifests_order_profile_fk",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("000082 up migration missing %q", required)
		}
	}
	for _, hash := range []string{
		"0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665",
		"0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732",
		"0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a",
	} {
		if strings.Count(upSQL, hash) < 3 {
			t.Fatalf("canonical profile hash %s is not propagated through handoffs", hash)
		}
	}
	for _, boundary := range []struct {
		name, dropConstraint, normalize string
	}{
		{
			name: "customer payment",
			dropConstraint: "ALTER TABLE payment_customer_payments\n" +
				"    DROP CONSTRAINT payment_customer_payments_provider_environment_check;",
			normalize: "UPDATE payment_customer_payments\n" +
				"SET provider_environment='TESTNET'",
		},
		{
			name: "funds receipt",
			dropConstraint: "ALTER TABLE payment_funds_receipts\n" +
				"    DROP CONSTRAINT payment_funds_receipts_provider_environment_check;",
			normalize: "UPDATE payment_funds_receipts\n" +
				"SET provider_environment='TESTNET'",
		},
	} {
		dropIndex := strings.Index(upSQL, boundary.dropConstraint)
		normalizeIndex := strings.Index(upSQL, boundary.normalize)
		if dropIndex < 0 || normalizeIndex < 0 || dropIndex > normalizeIndex {
			t.Fatalf(
				"000082 must drop the legacy %s environment check before TESTNET normalization",
				boundary.name,
			)
		}
	}
	for _, required := range []string{
		"SELECT 1 FROM agency_orders",
		"SELECT 1 FROM agency_order_payment_instructions",
		"SELECT 1 FROM payment_customer_payments",
		"SELECT 1 FROM payment_funds_receipts",
		"SELECT 1 FROM agency_order_receipts",
		"cannot remove execution profiles while Live or real-money facts exist",
		"USING ERRCODE = '23000'",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("000082 down migration missing Live fact rollback guard %q", required)
		}
	}
	if !strings.Contains(downSQL, "DROP TRIGGER trg_agency_order_execution_profile_immutable") ||
		!strings.Contains(downSQL, "DROP COLUMN execution_profile_hash") {
		t.Fatal("000082 down migration does not remove the immutable profile boundary")
	}
}

package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestCatalogAgencyOrderMigrationKeepsOneImmutableSettlementSource(t *testing.T) {
	t.Parallel()

	up := readAgencyOrderMigration(t, "000058_phase8_agency_order_step2.up.sql")
	down := readAgencyOrderMigration(t, "000058_phase8_agency_order_step2.down.sql")

	for _, table := range []string{
		"agency_order_sheet_sessions",
		"agency_order_provider_capabilities",
		"agency_orders",
		"agency_order_payment_instructions",
		"agency_order_payment_consents",
		"agency_order_outbox",
		"agency_order_audits",
	} {
		if !strings.Contains(up, "CREATE TABLE "+table) {
			t.Fatalf("AgencyOrder migration misses %s", table)
		}
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table) {
			t.Fatalf("AgencyOrder rollback misses %s", table)
		}
	}

	for _, required := range []string{
		"source_cart_snapshot_hash TEXT NOT NULL",
		"snapshot_hash TEXT NOT NULL UNIQUE",
		"status TEXT NOT NULL CHECK (status = 'ISSUED')",
		"rail TEXT NOT NULL CHECK (rail = 'GIWA')",
		"asset TEXT NOT NULL CHECK (asset = 'TVITUSD')",
		"settlement_authorizations_one_source CHECK",
		"settlement_payments_one_source CHECK",
		"(purchase_id IS NOT NULL)::integer + (agency_order_id IS NOT NULL)::integer = 1",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("AgencyOrder immutable settlement contract misses %q", required)
		}
	}

	for _, required := range []string{
		"DELETE FROM settlement_payments WHERE agency_order_id IS NOT NULL",
		"DELETE FROM settlement_authorizations WHERE agency_order_id IS NOT NULL",
		"ALTER TABLE settlement_payments ALTER COLUMN purchase_id SET NOT NULL",
		"ALTER TABLE settlement_authorizations ALTER COLUMN purchase_id SET NOT NULL",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("AgencyOrder rollback cannot safely restore Purchase-only settlement: %q", required)
		}
	}
	if strings.Index(down, "DELETE FROM settlement_payments") > strings.Index(down, "ALTER TABLE settlement_payments DROP COLUMN") {
		t.Fatal("AgencyOrder payment rows must be removed before dropping their source column")
	}
}

func TestAgencyOrderCleanupMigrationIsDurableAndReversible(t *testing.T) {
	t.Parallel()

	up := readAgencyOrderMigration(t, "000060_agency_order_sheet_cleanup.up.sql")
	down := readAgencyOrderMigration(t, "000060_agency_order_sheet_cleanup.down.sql")
	for _, required := range []string{
		"'BUYER_CONTEXT'",
		"cleanup_state TEXT NOT NULL DEFAULT 'NOT_REQUIRED'",
		"cleanup_attempt_count INTEGER NOT NULL DEFAULT 0",
		"cleanup_available_at TIMESTAMPTZ",
		"cleanup_lease_until TIMESTAMPTZ",
		"idx_agency_order_sheet_cleanup_jobs",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("AgencyOrder cleanup migration misses %q", required)
		}
	}
	for _, required := range []string{
		"DELETE FROM agency_order_provider_capabilities",
		"DROP COLUMN IF EXISTS cleanup_state",
		"CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT'))",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("AgencyOrder cleanup rollback misses %q", required)
		}
	}
}

func TestAgencyOrderContinueURLCapabilityMigrationIsEncryptedAndReversible(t *testing.T) {
	t.Parallel()

	up := readAgencyOrderMigration(t, "000061_agency_order_continue_url_capability.up.sql")
	down := readAgencyOrderMigration(t, "000061_agency_order_continue_url_capability.down.sql")
	if !strings.Contains(up, "'UCP_CONTINUE_URL'") ||
		!strings.Contains(up, "agency_order_provider_capabilities_capability_kind_check") {
		t.Fatal("continue URL capability migration does not extend the encrypted vault kind")
	}
	for _, required := range []string{
		"DELETE FROM agency_order_provider_capabilities",
		"WHERE capability_kind='UCP_CONTINUE_URL'",
		"CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT','BUYER_CONTEXT'))",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("continue URL capability rollback misses %q", required)
		}
	}
}

func TestOrderSheetShippingInputMigrationSeparatesAccountDefaultsFromOrderSnapshots(t *testing.T) {
	t.Parallel()

	up := readAgencyOrderMigration(t, "000062_order_sheet_shipping_input.up.sql")
	down := readAgencyOrderMigration(t, "000062_order_sheet_shipping_input.down.sql")
	for _, required := range []string{
		"ALTER COLUMN source_profile_id DROP NOT NULL",
		"source_kind TEXT NOT NULL DEFAULT 'ACCOUNT_PROFILE'",
		"shipping_snapshots_source_kind_check",
		"source_kind = 'ORDER_SHEET_INPUT' AND source_profile_id IS NULL",
		"idx_shipping_snapshots_order_sheet_input",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("OrderSheet shipping input migration misses %q", required)
		}
	}
	for _, required := range []string{
		"cannot remove OrderSheet shipping input while direct snapshots exist",
		"DROP COLUMN source_kind",
		"ALTER COLUMN source_profile_id SET NOT NULL",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("OrderSheet shipping input rollback misses %q", required)
		}
	}
}

func readAgencyOrderMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("../../../../migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

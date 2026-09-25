package postgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	catalogLikedVariantsMigration    = "000054_phase8_liked_variants.up.sql"
	catalogResearchMigration         = "000055_phase8_research_activation.up.sql"
	catalogProductIdentityMigration  = "000056_phase8_candidate_product_identity.up.sql"
	researchTerminalFailureMigration = "000057_research_terminal_failure.up.sql"
	agencyOrderMigration             = "000058_phase8_agency_order_step2.up.sql"
	agencyOrderRuntimeMigration      = "000059_agency_order_runtime_cutover.up.sql"
	agencyOrderCleanupMigration      = "000060_agency_order_sheet_cleanup.up.sql"
	agencyOrderContinueURLMigration  = "000061_agency_order_continue_url_capability.up.sql"
	orderSheetShippingInputMigration = "000062_order_sheet_shipping_input.up.sql"
	paymentPayPalSandboxMigration    = "000063_payment_paypal_sandbox.up.sql"
	refundSlicesPartialMigration     = "000064_refund_slices_partial.up.sql"
	settlementPartialRefundMigration = "000065_settlement_partial_refund_commands.up.sql"
	agencyOrderProcessStageMigration = "000066_agency_order_process_stage.up.sql"
	procurementContextMigration      = "000067_procurement_context.up.sql"
	procurementLiveContractMigration = "000068_procurement_live_contract.up.sql"
	logisticsCoreMigration           = "000069_logistics_core.up.sql"
	resolutionDelayRuleMigration     = "000070_resolution_delay_rule.up.sql"
	sandboxLiveEquivalenceMigration  = "000071_sandbox_live_equivalence.up.sql"
)

// TestCatalogMigrationsUpgradeRollbackReapply exercises the production
// migration runner against a PostgreSQL 16 database created only for this
// test. It proves that Phase 8 can be rolled back in reverse order and then
// reapplied without leaving schema_migrations or its foreign keys stale.
func TestCatalogMigrationsUpgradeRollbackReapply(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	adminDatabase, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := isolatedCutoverDatabaseName(t)
	if _, err := adminDatabase.DB.ExecContext(
		ctx,
		"CREATE DATABASE "+quotePostgresIdentifier(databaseName)+" TEMPLATE template0",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated Phase 8 migration database: %v", err)
	}

	var isolatedDatabase *Database
	t.Cleanup(func() {
		if isolatedDatabase != nil {
			if err := isolatedDatabase.Close(); err != nil {
				t.Errorf("close isolated Phase 8 migration database: %v", err)
			}
		}
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(), 15*time.Second,
		)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated Phase 8 migration database: %v", err)
		}
		if err := adminDatabase.Close(); err != nil {
			t.Errorf("close Phase 8 migration admin database: %v", err)
		}
	})

	isolatedDatabase, err = Open(
		ctx, databaseURLWithName(t, databaseURL, databaseName),
	)
	if err != nil {
		t.Fatalf("open isolated Phase 8 migration database: %v", err)
	}
	assertPostgres16(t, ctx, isolatedDatabase.DB)

	migrationDirectory := migrationTestDirectory(t)
	// 000072 legacy hard cutover는 비가역(down이 예외를 던진다)이라 roundtrip
	// 대상에서 제외한다 — 전용 populated-DB test가 000072를 검증한다.
	reversibleMigrationDirectory := migrationDirectoryThrough(
		t, migrationDirectory, sandboxLiveEquivalenceMigration,
	)
	if err := isolatedDatabase.Migrate(ctx, reversibleMigrationDirectory); err != nil {
		t.Fatalf("upgrade isolated database through Phase 8 migrations: %v", err)
	}
	var upgradedMigrationCount int
	if err := isolatedDatabase.DB.QueryRowContext(
		ctx, `SELECT count(*) FROM schema_migrations`,
	).Scan(&upgradedMigrationCount); err != nil {
		t.Fatalf("count upgraded migrations: %v", err)
	}
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, true, true)
	assertCatalogSchema(t, ctx, isolatedDatabase.DB, true)
	assertCatalogProviderIdentityIndex(t, ctx, isolatedDatabase.DB, true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, researchTerminalFailureMigration, true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "research_rounds", "failure_reason_code", true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "research_rounds", "failure_retryable", true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_orders", true)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "settlement_authorizations", "agency_order_id", true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_sheet_sessions", "cleanup_state", true,
	)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, orderSheetShippingInputMigration, true,
	)
	assertColumnExists(t, ctx, isolatedDatabase.DB, "shipping_snapshots", "source_kind", true)
	assertColumnNullable(t, ctx, isolatedDatabase.DB, "shipping_snapshots", "source_profile_id", true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, refundSlicesPartialMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_refund_slices", true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, settlementPartialRefundMigration, true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "settlement_command_outbox", "refund_key", true,
	)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, agencyOrderProcessStageMigration, true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_processes", "terminal_reason", true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "payment_customer_payments", "terminal_state", false,
	)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, procurementContextMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_orders", true)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_customer_notices", true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, procurementLiveContractMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_payments", true)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_cancellations", true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, logisticsCoreMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_expected_units", true)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_shipments", true)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, resolutionDelayRuleMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_delivery_resolutions", true)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_returns", true)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_refund_requests", "reason_code", true,
	)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, sandboxLiveEquivalenceMigration, true,
	)
	assertMerchantOrderStateVocabulary(t, ctx, isolatedDatabase.DB, false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000071_sandbox_live_equivalence.down.sql",
		sandboxLiveEquivalenceMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)
	assertMerchantOrderStateVocabulary(t, ctx, isolatedDatabase.DB, true)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000070_resolution_delay_rule.down.sql",
		resolutionDelayRuleMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-2,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_delivery_resolutions", false)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_refund_requests", "reason_code", false,
	)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000069_logistics_core.down.sql",
		logisticsCoreMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-3,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_expected_units", false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_shipments", false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000068_procurement_live_contract.down.sql",
		procurementLiveContractMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-4,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_payments", false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_cancellations", false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000067_procurement_context.down.sql",
		procurementContextMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-5,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_orders", false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_customer_notices", false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000066_agency_order_process_stage.down.sql",
		agencyOrderProcessStageMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-6,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_processes", "terminal_reason", false,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "payment_customer_payments", "terminal_state", true,
	)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000065_settlement_partial_refund_commands.down.sql",
		settlementPartialRefundMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-7,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "settlement_command_outbox", "refund_key", false,
	)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000064_refund_slices_partial.down.sql",
		refundSlicesPartialMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-8,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_refund_slices", false)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, paymentPayPalSandboxMigration, true,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "payment_customer_payments", true)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000063_payment_paypal_sandbox.down.sql",
		paymentPayPalSandboxMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-9,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "payment_customer_payments", false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000062_order_sheet_shipping_input.down.sql",
		orderSheetShippingInputMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-10,
	)
	assertColumnExists(t, ctx, isolatedDatabase.DB, "shipping_snapshots", "source_kind", false)
	assertColumnNullable(t, ctx, isolatedDatabase.DB, "shipping_snapshots", "source_profile_id", false)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000061_agency_order_continue_url_capability.down.sql",
		agencyOrderContinueURLMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-11,
	)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000060_agency_order_sheet_cleanup.down.sql",
		agencyOrderCleanupMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-12,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_sheet_sessions", "cleanup_state", false,
	)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000059_agency_order_runtime_cutover.down.sql",
		agencyOrderRuntimeMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-13,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_processes", false)

	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000058_phase8_agency_order_step2.down.sql",
		agencyOrderMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-14,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_orders", false)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "settlement_authorizations", "agency_order_id", false,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, agencyOrderMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-13,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_orders", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, agencyOrderRuntimeMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-12,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_processes", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, agencyOrderCleanupMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-11,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_sheet_sessions", "cleanup_state", true,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, agencyOrderContinueURLMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-10,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, orderSheetShippingInputMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-9,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, paymentPayPalSandboxMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-8,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "payment_customer_payments", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, refundSlicesPartialMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-7,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_refund_slices", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, settlementPartialRefundMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-6,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "settlement_command_outbox", "refund_key", true,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, agencyOrderProcessStageMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-5,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "agency_order_processes", "terminal_reason", true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "payment_customer_payments", "terminal_state", false,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, procurementContextMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-4,
	)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_orders", true)
	assertTableExists(t, ctx, isolatedDatabase.DB, "agency_order_customer_notices", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, procurementLiveContractMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, upgradedMigrationCount-3)
	assertTableExists(t, ctx, isolatedDatabase.DB, "merchant_payments", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, logisticsCoreMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, upgradedMigrationCount-2)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_expected_units", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, resolutionDelayRuleMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1)
	assertTableExists(t, ctx, isolatedDatabase.DB, "logistics_returns", true)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, sandboxLiveEquivalenceMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, upgradedMigrationCount)
	assertMerchantOrderStateVocabulary(t, ctx, isolatedDatabase.DB, false)

	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000057_research_terminal_failure.down.sql",
		researchTerminalFailureMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB, "research_rounds", "failure_reason_code", false,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		researchTerminalFailureMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount,
	)

	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000056_phase8_candidate_product_identity.down.sql",
		catalogProductIdentityMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, true, false)
	assertCatalogProviderIdentityIndex(t, ctx, isolatedDatabase.DB, false)

	seedCatalogLocatorIdentityDuplicates(t, ctx, isolatedDatabase.DB)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		catalogProductIdentityMigration,
	)
	assertCatalogProductIdentityCutover(t, ctx, isolatedDatabase.DB)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000056_phase8_candidate_product_identity.down.sql",
		catalogProductIdentityMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)
	assertCatalogSharedLocatorRollback(t, ctx, isolatedDatabase.DB)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		catalogProductIdentityMigration,
	)
	assertCatalogSharedLocatorReapply(t, ctx, isolatedDatabase.DB)
	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000056_phase8_candidate_product_identity.down.sql",
		catalogProductIdentityMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)

	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000055_phase8_research_activation.down.sql",
		catalogResearchMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-2,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, false, false)
	assertCatalogResearchTables(t, ctx, isolatedDatabase.DB, false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "phase8_liked_variants", true)
	assertCatalogForeignKey(
		t, ctx, isolatedDatabase.DB,
		"phase8_liked_variants", "phase8_liked_variants_curation_fkey",
		"curations", true,
	)
	assertCatalogForeignKey(
		t, ctx, isolatedDatabase.DB,
		"phase8_liked_variants", "phase8_liked_variants_candidate_fkey",
		"phase8_research_candidates", false,
	)

	applyCatalogDownMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		"000054_phase8_liked_variants.down.sql",
		catalogLikedVariantsMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-3,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, false, false, false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "phase8_liked_variants", false)
	assertCatalogResearchTables(t, ctx, isolatedDatabase.DB, false)

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		catalogLikedVariantsMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-2,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, false, false)
	assertTableExists(t, ctx, isolatedDatabase.DB, "phase8_liked_variants", true)
	assertCatalogForeignKey(
		t, ctx, isolatedDatabase.DB,
		"phase8_liked_variants", "phase8_liked_variants_curation_fkey",
		"curations", true,
	)

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		catalogResearchMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount-1,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, true, false)

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory,
		catalogProductIdentityMigration,
	)
	assertRecordedMigrationCount(
		t, ctx, isolatedDatabase.DB, upgradedMigrationCount,
	)
	assertCatalogMigrationRecords(t, ctx, isolatedDatabase.DB, true, true, true)
	assertCatalogSchema(t, ctx, isolatedDatabase.DB, true)
	assertCatalogProviderIdentityIndex(t, ctx, isolatedDatabase.DB, true)
}

func migrationDirectoryThrough(t *testing.T, sourceDirectory, lastUpMigration string) string {
	t.Helper()
	targetDirectory := t.TempDir()
	entries, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatalf("read migration directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") || name > lastUpMigration {
			continue
		}
		body, err := os.ReadFile(filepath.Join(sourceDirectory, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(targetDirectory, name), body, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", name, err)
		}
	}
	return targetDirectory
}

func applyCatalogDownMigration(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
	downName string,
	recordedUpName string,
) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, downName))
	if err != nil {
		t.Fatalf("read Phase 8 down migration %s: %v", downName, err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin Phase 8 rollback %s: %v", downName, err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("apply Phase 8 down migration %s: %v", downName, err)
	}
	if _, err := transaction.ExecContext(
		ctx, `DELETE FROM schema_migrations WHERE version=$1`, recordedUpName,
	); err != nil {
		t.Fatalf("unrecord Phase 8 migration %s: %v", recordedUpName, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit Phase 8 rollback %s: %v", downName, err)
	}
}

func assertCatalogMigrationRecords(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	likedVariants bool,
	research bool,
	productIdentity bool,
) {
	t.Helper()
	assertMigrationRecorded(
		t, ctx, database, catalogLikedVariantsMigration, likedVariants,
	)
	assertMigrationRecorded(
		t, ctx, database, catalogResearchMigration, research,
	)
	assertMigrationRecorded(
		t, ctx, database, catalogProductIdentityMigration, productIdentity,
	)
}

func assertCatalogProviderIdentityIndex(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	expected bool,
) {
	t.Helper()
	var exists bool
	if err := database.QueryRowContext(ctx, `
		SELECT to_regclass(
			'public.phase8_research_candidates_provider_product_identity_idx'
		) IS NOT NULL
	`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != expected {
		t.Fatalf("Phase 8 provider product identity index exists=%t want=%t", exists, expected)
	}
}

func seedCatalogLocatorIdentityDuplicates(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Now().UTC()
	statements := []string{
		`INSERT INTO users(id,status,created_at,updated_at)
		 VALUES ('97000000-0000-4000-8000-000000000001','ACTIVE',$1,$1)`,
		`INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,created_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000002',
			'97000000-0000-4000-8000-000000000001',
			'identity cutover','SINGLE','EXPERIMENT',100,'USD','US','',$1
		 )`,
		`INSERT INTO curations(
			id,shopping_plan_id,user_id,phase,version,created_at,updated_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000002',
			'97000000-0000-4000-8000-000000000001','CURATING',1,$1,$1
		 )`,
		`INSERT INTO plan_targets(
			id,curation_id,user_id,plan_id,title,normalized_intent,category,
			allocated_amount,allocated_currency,country,city,url_mode,
			order_index,version,created_at,updated_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000004',
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000002',
			'identity cutover','identity cutover','test',100,'USD','US','','NONE',
			0,1,$1,$1
		 )`,
		`INSERT INTO phase8_research_pools(
			user_id,curation_id,plan_target_id,version,expand_ordinal,
			latest_mode,created_at,updated_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000004',1,0,'REPLACE',$1,$1
		 )`,
		`INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,variant_id,seller_domain,visible,display_order,
			first_seen_at,last_seen_at
		 ) VALUES
			('97000000-0000-4000-8000-000000000001',
			 '97000000-0000-4000-8000-000000000003',
			 '97000000-0000-4000-8000-000000000004',
			 'legacy-first','gid://shopify/Product/1','SHOPIFY_LIVE','url:old',
			 'PRODUCT_URL','https://old.example/products/1',NULL,NULL,false,4,$1,$1),
			('97000000-0000-4000-8000-000000000001',
			 '97000000-0000-4000-8000-000000000003',
			 '97000000-0000-4000-8000-000000000004',
			 'legacy-second','gid://shopify/Product/1','SHOPIFY_LIVE','variant:new',
			 'MERCHANT_VARIANT',NULL,'variant-1','merchant.example',true,1,
			 $1::timestamptz + interval '1 second',
			 $1::timestamptz + interval '2 seconds'),
			('97000000-0000-4000-8000-000000000001',
			 '97000000-0000-4000-8000-000000000003',
			 '97000000-0000-4000-8000-000000000004',
			 'shared-locator-first','gid://shopify/Product/2','SHOPIFY_LIVE',
			 'url:https://shared.example/products/one','PRODUCT_URL',
			 'https://shared.example/products/one',NULL,NULL,true,2,$1,$1),
			('97000000-0000-4000-8000-000000000001',
			 '97000000-0000-4000-8000-000000000003',
			 '97000000-0000-4000-8000-000000000004',
			 'shared-locator-second','gid://shopify/Product/3','SHOPIFY_LIVE',
			 'rollback-seed:shared-locator-second','PRODUCT_URL',
			 'https://shared.example/products/one',NULL,NULL,true,3,
			 $1::timestamptz + interval '1 second',
			 $1::timestamptz + interval '1 second')`,
		`INSERT INTO phase8_candidate_configurations(
			user_id,curation_id,plan_target_id,candidate_id,variant_id,
			selected_options,observed_at,updated_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000004',
			'legacy-second','variant-1','[]',$1,$1
		 )`,
		`INSERT INTO phase8_variant_interactions(
			user_id,curation_id,plan_target_id,candidate_id,variant_id,
			pinned,sentiment,updated_at
		 ) VALUES
			(
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000004',
			'legacy-first','variant-1',true,'LIKE',$1
			),(
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',
			'97000000-0000-4000-8000-000000000004',
			'legacy-second','variant-1',false,'DISLIKE',
			$1::timestamptz + interval '2 seconds'
		 )`,
		`INSERT INTO phase8_liked_variants(
			user_id,curation_id,candidate_id,variant_id,product_title,
			variant_title,product_url,merchant,price_minor,currency,
			target_title,updated_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',
			'legacy-first','variant-1','Product','Variant',
			'https://merchant.example/products/1','Merchant',100,'USD','Target',$1
		 )`,
		`INSERT INTO phase8_cart_views(user_id,curation_id,version,created_at,updated_at)
		 VALUES (
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003',1,$1,$1
		 )`,
		`INSERT INTO phase8_cart_items(
			user_id,curation_id,cart_item_id,plan_target_id,candidate_id,
			product_title_snapshot,product_url,merchant_name_snapshot,
			seller_domain,intent_point_snapshot,variant_id,
			variant_title_snapshot,selected_options,preview_price_minor,
			preview_currency,quantity,observed_at,added_at
		 ) VALUES (
			'97000000-0000-4000-8000-000000000001',
			'97000000-0000-4000-8000-000000000003','cart-item-1',
			'97000000-0000-4000-8000-000000000004','legacy-second',
			'Product','https://merchant.example/products/1','Merchant',
			'merchant.example','Intent','variant-1','Variant','[]',100,'USD',1,$1,$1
		 )`,
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement, now); err != nil {
			t.Fatalf("seed Phase 8 identity cutover: %v", err)
		}
	}
}

func assertCatalogProductIdentityCutover(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	const userID = "97000000-0000-4000-8000-000000000001"
	const curationID = "97000000-0000-4000-8000-000000000003"
	var count int
	var candidateID, identityKey, locatorKind string
	var visible bool
	var displayOrder int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*), min(candidate_id), min(identity_key),
		       min(locator_kind), bool_or(visible), min(display_order)
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2
		  AND provider_product_id='gid://shopify/Product/1'
	`, userID, curationID).Scan(
		&count, &candidateID, &identityKey, &locatorKind, &visible, &displayOrder,
	); err != nil {
		t.Fatal(err)
	}
	if count != 1 || candidateID != "legacy-first" ||
		identityKey != "shopify-product:gid://shopify/Product/1" ||
		locatorKind != "MERCHANT_VARIANT" ||
		!visible || displayOrder != 1 {
		t.Fatalf("canonical Candidate count=%d id=%q identity=%q locator=%q visible=%t order=%d",
			count, candidateID, identityKey, locatorKind, visible, displayOrder)
	}
	for _, table := range []string{
		"phase8_candidate_configurations", "phase8_variant_interactions",
		"phase8_cart_items",
	} {
		query := `SELECT count(*) FROM ` + quotePostgresIdentifier(table) +
			` WHERE user_id=$1 AND curation_id=$2 AND candidate_id='legacy-first'`
		if err := database.QueryRowContext(ctx, query, userID, curationID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s canonical child rows=%d", table, count)
		}
	}
	var sentiment string
	if err := database.QueryRowContext(ctx, `
		SELECT sentiment
		FROM phase8_variant_interactions
		WHERE user_id=$1 AND curation_id=$2
		  AND candidate_id='legacy-first' AND variant_id='variant-1'
	`, userID, curationID).Scan(&sentiment); err != nil {
		t.Fatal(err)
	}
	if sentiment != "DISLIKE" {
		t.Fatalf("canonical duplicate interaction sentiment=%q", sentiment)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT count(*)
		FROM phase8_liked_variants
		WHERE user_id=$1 AND curation_id=$2
		  AND candidate_id='legacy-first' AND variant_id='variant-1'
	`, userID, curationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("DISLIKE canonical interaction retained liked rows=%d", count)
	}
}

func assertCatalogSharedLocatorRollback(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	const userID = "97000000-0000-4000-8000-000000000001"
	const curationID = "97000000-0000-4000-8000-000000000003"
	var count, distinctIdentities, exactLocatorKeys, rollbackAliases int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*), count(DISTINCT identity_key),
		       count(*) FILTER (
		           WHERE identity_key='url:https://shared.example/products/one'
		       ),
		       count(*) FILTER (WHERE identity_key LIKE 'rollback-candidate:%')
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2
		  AND provider_product_id IN (
		      'gid://shopify/Product/2', 'gid://shopify/Product/3'
		  )
	`, userID, curationID).Scan(
		&count, &distinctIdentities, &exactLocatorKeys, &rollbackAliases,
	); err != nil {
		t.Fatal(err)
	}
	if count != 2 || distinctIdentities != 2 || exactLocatorKeys != 1 || rollbackAliases != 1 {
		t.Fatalf("shared-locator rollback count=%d distinct=%d exact=%d aliases=%d",
			count, distinctIdentities, exactLocatorKeys, rollbackAliases)
	}
}

func assertCatalogSharedLocatorReapply(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	const userID = "97000000-0000-4000-8000-000000000001"
	const curationID = "97000000-0000-4000-8000-000000000003"
	var count, distinctProducts, canonicalKeys int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*), count(DISTINCT provider_product_id),
		       count(*) FILTER (
		           WHERE identity_key='shopify-product:' || provider_product_id
		       )
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2
		  AND provider_product_id IN (
		      'gid://shopify/Product/2', 'gid://shopify/Product/3'
		  )
	`, userID, curationID).Scan(&count, &distinctProducts, &canonicalKeys); err != nil {
		t.Fatal(err)
	}
	if count != 2 || distinctProducts != 2 || canonicalKeys != 2 {
		t.Fatalf("shared-locator reapply count=%d products=%d canonical=%d",
			count, distinctProducts, canonicalKeys)
	}
}

func assertCatalogSchema(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	expected bool,
) {
	t.Helper()
	assertTableExists(t, ctx, database, "phase8_liked_variants", expected)
	assertCatalogResearchTables(t, ctx, database, expected)
	for _, foreignKey := range []struct {
		table      string
		constraint string
		references string
	}{
		{"phase8_liked_variants", "phase8_liked_variants_curation_fkey", "curations"},
		{"phase8_research_pools", "phase8_research_pools_curation_fkey", "curations"},
		{"phase8_research_pools", "phase8_research_pools_target_fkey", "plan_targets"},
		{"phase8_research_pool_commands", "phase8_research_pool_commands_target_fkey", "plan_targets"},
		{"phase8_research_candidates", "phase8_research_candidates_pool_fkey", "phase8_research_pools"},
		{"phase8_liked_variants", "phase8_liked_variants_candidate_fkey", "phase8_research_candidates"},
		{"phase8_candidate_configurations", "phase8_candidate_configurations_candidate_fkey", "phase8_research_candidates"},
		{"phase8_variant_interactions", "phase8_variant_interactions_candidate_fkey", "phase8_research_candidates"},
		{"phase8_cart_views", "phase8_cart_views_curation_fkey", "curations"},
		{"phase8_cart_items", "phase8_cart_items_view_fkey", "phase8_cart_views"},
		{"phase8_cart_items", "phase8_cart_items_candidate_fkey", "phase8_research_candidates"},
	} {
		assertCatalogForeignKey(
			t, ctx, database, foreignKey.table, foreignKey.constraint,
			foreignKey.references, expected,
		)
	}
}

func assertCatalogResearchTables(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	expected bool,
) {
	t.Helper()
	for _, table := range []string{
		"phase8_research_pools",
		"phase8_research_pool_commands",
		"phase8_research_candidates",
		"phase8_candidate_configurations",
		"phase8_variant_interactions",
		"phase8_cart_views",
		"phase8_cart_items",
	} {
		assertTableExists(t, ctx, database, table, expected)
	}
}

func assertCatalogForeignKey(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
	constraint string,
	references string,
	expected bool,
) {
	t.Helper()
	var actual bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM pg_constraint constraint_record
			JOIN pg_class table_record
			  ON table_record.oid=constraint_record.conrelid
			JOIN pg_namespace table_namespace
			  ON table_namespace.oid=table_record.relnamespace
			JOIN pg_class referenced_record
			  ON referenced_record.oid=constraint_record.confrelid
			JOIN pg_namespace referenced_namespace
			  ON referenced_namespace.oid=referenced_record.relnamespace
			WHERE constraint_record.contype='f'
			  AND constraint_record.conname=$1
			  AND table_namespace.nspname='public'
			  AND table_record.relname=$2
			  AND referenced_namespace.nspname='public'
			  AND referenced_record.relname=$3
		)
	`, constraint, table, references).Scan(&actual); err != nil {
		t.Fatalf(
			"check foreign key %s on %s -> %s: %v",
			constraint, table, references, err,
		)
	}
	if actual != expected {
		t.Fatalf(
			"foreign key %s on %s -> %s exists=%t, want %t",
			constraint, table, references, actual, expected,
		)
	}
}

// assertMerchantOrderStateVocabulary는 ADR-0053 어휘 축소를 검사한다:
// wantSimulated=false면 state CHECK에 'SIMULATED'가 없어야 한다.
func assertMerchantOrderStateVocabulary(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	wantSimulated bool,
) {
	t.Helper()
	var definition string
	if err := database.QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname='merchant_orders_state_check'
	`).Scan(&definition); err != nil {
		t.Fatalf("read merchant_orders state check: %v", err)
	}
	if strings.Contains(definition, "SIMULATED") != wantSimulated {
		t.Fatalf("merchant_orders state vocabulary mismatch (wantSimulated=%v): %s",
			wantSimulated, definition)
	}
}

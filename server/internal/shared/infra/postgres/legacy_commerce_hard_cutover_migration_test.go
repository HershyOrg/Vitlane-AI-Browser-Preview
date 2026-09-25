package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLegacyCommerceHardCutoverIsDestructiveAndAgencyOrderOnly(t *testing.T) {
	body, err := os.ReadFile("../../../../migrations/000072_legacy_commerce_hard_cutover.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(body)
	for _, required := range []string{
		"DELETE FROM settlement_payments WHERE purchase_id IS NOT NULL",
		"DROP COLUMN purchase_id",
		"ALTER COLUMN agency_order_id SET NOT NULL",
		"DROP TABLE IF EXISTS purchases CASCADE",
		"DROP TABLE IF EXISTS fulfillment_executions CASCADE",
		"DROP TABLE IF EXISTS candidate_interactions CASCADE",
		"DELETE FROM curation_actions",
		"effect_kind IN ('NONE', 'INTELLIGENCE')",
		"RENAME COLUMN purchase_path TO orderability",
		"vitlane.orderability.v1",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("hard cutover migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{"legacy_archive", "preflight", "drain", "reconcile legacy"} {
		if strings.Contains(strings.ToLower(sql), forbidden) {
			t.Fatalf("hard cutover migration retained forbidden compatibility step %q", forbidden)
		}
	}
}

func TestLegacyCommerceHardCutoverAppliesToPostgres16(t *testing.T) {
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
		ctx, "CREATE DATABASE "+quotePostgresIdentifier(databaseName)+" TEMPLATE template0",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated legacy commerce database: %v", err)
	}

	var isolated *Database
	t.Cleanup(func() {
		if isolated != nil {
			if err := isolated.Close(); err != nil {
				t.Errorf("close isolated legacy commerce database: %v", err)
			}
		}
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated legacy commerce database: %v", err)
		}
		if err := adminDatabase.Close(); err != nil {
			t.Errorf("close legacy commerce admin database: %v", err)
		}
	})

	isolated, err = Open(ctx, databaseURLWithName(t, databaseURL, databaseName))
	if err != nil {
		t.Fatalf("open isolated legacy commerce database: %v", err)
	}
	assertPostgres16(t, ctx, isolated.DB)
	if err := isolated.Migrate(ctx, migrationTestDirectory(t)); err != nil {
		t.Fatalf("apply migrations through destructive hard cutover: %v", err)
	}

	for _, table := range []string{
		"purchases", "checkout_quotes", "user_approvals",
		"fulfillment_executions", "fulfillment_requests", "receipts",
		"candidate_interactions", "pii_access_audit_events",
	} {
		assertTableExists(t, ctx, isolated.DB, table, false)
	}
	for _, table := range []string{
		"agency_orders", "settlement_authorizations", "settlement_payments",
		"agency_order_pii_access_audits",
	} {
		assertTableExists(t, ctx, isolated.DB, table, true)
	}
	for _, table := range []string{"settlement_authorizations", "settlement_payments"} {
		assertColumnExists(t, ctx, isolated.DB, table, "purchase_id", false)
		assertColumnExists(t, ctx, isolated.DB, table, "agency_order_id", true)
		assertColumnNullable(t, ctx, isolated.DB, table, "agency_order_id", false)
	}
	assertColumnExists(t, ctx, isolated.DB, "candidates", "purchase_support", false)
	assertColumnExists(t, ctx, isolated.DB, "candidates", "purchase_path", false)
	assertColumnExists(t, ctx, isolated.DB, "candidates", "order_support", true)
	assertColumnExists(t, ctx, isolated.DB, "candidates", "orderability", true)
	assertMigrationRecorded(
		t, ctx, isolated.DB, "000072_legacy_commerce_hard_cutover.up.sql", true,
	)
}

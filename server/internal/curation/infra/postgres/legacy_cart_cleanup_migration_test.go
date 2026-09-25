package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCartCleanupMigrationContract(t *testing.T) {
	t.Parallel()

	up := readLegacyCartCleanupMigration(
		t,
		"000037_intent_curation_legacy_cart_cleanup.up.sql",
	)
	down := readLegacyCartCleanupMigration(
		t,
		"000037_intent_curation_legacy_cart_cleanup.down.sql",
	)

	for _, fragment := range []string{
		"INTENT_CURATION_FINANCIAL_RESPONSIBILITY_ACTIVE",
		"'AUTHORIZED', 'AWAITING_ALLOWANCE', 'PAYMENT_SUBMITTED'",
		"WHERE state NOT IN ('FINALIZED', 'FAILED')",
		"WHERE state <> 'FINALIZED'",
		"WHERE status = 'REFUND_PENDING'",
		"ERRCODE = '55000'",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing financial gate %q", fragment)
		}
	}

	fragments := []string{
		"DROP TRIGGER IF EXISTS shopping_plans_create_cart ON shopping_plans",
		"DROP FUNCTION IF EXISTS vitlane_create_plan_cart()",
		"DROP TABLE IF EXISTS shopping_cart_commands",
		"DROP TABLE IF EXISTS shopping_cart_items",
		"DROP TABLE IF EXISTS shopping_carts",
	}
	previous := -1
	for _, fragment := range fragments {
		index := strings.Index(up, fragment)
		if index < 0 {
			t.Errorf("up migration is missing %q", fragment)
			continue
		}
		if index <= previous {
			t.Errorf("up migration fragment %q is out of dependency order", fragment)
		}
		previous = index
	}

	for _, forbidden := range []string{
		"DROP TABLE IF EXISTS curation_selections",
		"DROP TABLE IF EXISTS curation_selection_commands",
		"DROP TABLE IF EXISTS purchase_origin_requests",
		"DROP TABLE IF EXISTS shopping_sessions",
		"CREATE TABLE shopping_carts",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration changes a canonical or unrelated model: %q", forbidden)
		}
	}

	for _, fragment := range []string{
		"is irreversible",
		"restore the approved pre-cutover database backup",
		"ERRCODE='55000'",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing restore-only contract %q", fragment)
		}
	}
	if strings.Contains(down, "CREATE TABLE") ||
		strings.Contains(down, "CREATE TRIGGER") {
		t.Error("down migration must not fabricate empty legacy Cart state")
	}
}

func readLegacyCartCleanupMigration(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "..", "migrations", name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(content)
}

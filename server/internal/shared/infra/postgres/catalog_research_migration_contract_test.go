package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestCatalogResearchActivationPersistsOnlyStableCatalogRefsAndUserDrafts(t *testing.T) {
	t.Parallel()

	activation := readCatalogMigration(t, "000055_phase8_research_activation.up.sql")
	down := readCatalogMigration(t, "000055_phase8_research_activation.down.sql")
	for _, table := range []string{
		"phase8_research_pools", "phase8_research_pool_commands",
		"phase8_research_candidates", "phase8_candidate_configurations",
		"phase8_variant_interactions", "phase8_cart_views", "phase8_cart_items",
	} {
		if !strings.Contains(activation, "CREATE TABLE "+table) {
			t.Fatalf("activation migration misses %s", table)
		}
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table) {
			t.Fatalf("activation down migration misses %s", table)
		}
	}
	commandBlock := catalogCreateTableBlock(t, activation, "phase8_research_pool_commands")
	for _, required := range []string{
		"PRIMARY KEY (user_id, idempotency_key)", "request_hash TEXT NOT NULL",
		"expected_pool_version BIGINT NOT NULL",
		"status TEXT NOT NULL CHECK (status IN ('RUNNING', 'COMPLETED'))",
		"fencing_token TEXT NOT NULL", "lease_expires_at TIMESTAMPTZ NOT NULL",
		"phase8_research_pool_commands_lease_window_check",
		"phase8_research_pool_commands_result_shape_check",
	} {
		if !strings.Contains(commandBlock, required) {
			t.Fatalf("CandidatePool command contract misses %q", required)
		}
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX phase8_research_pool_commands_one_running_target_idx",
		"WHERE status='RUNNING'", "CREATE TRIGGER phase8_research_candidates_capacity_guard",
		"CONSTRAINT='phase8_research_candidates_capacity_guard'",
	} {
		if !strings.Contains(activation, required) {
			t.Fatalf("CandidatePool concurrency/cap contract misses %q", required)
		}
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS phase8_research_candidates_capacity_guard",
		"DROP FUNCTION IF EXISTS phase8_guard_candidate_capacity()",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("CandidatePool capacity guard is not reversible: %q", required)
		}
	}
	candidateBlock := catalogCreateTableBlock(t, activation, "phase8_research_candidates")
	for _, forbidden := range []string{
		"description", "price", "availability", "media", "image_url",
	} {
		if strings.Contains(strings.ToLower(candidateBlock), forbidden) {
			t.Fatalf("Shopify display fact persisted in Candidate pool: %s", forbidden)
		}
	}
	for _, required := range []string{
		"locator_kind TEXT NOT NULL", "product_url TEXT", "variant_id TEXT",
		"source_kind TEXT NOT NULL CHECK (source_kind='SHOPIFY_LIVE')",
		"seller_domain TEXT", "intent_point_snapshot TEXT NOT NULL",
		"feature_lines_snapshot JSONB NOT NULL", "specification_lines_snapshot JSONB NOT NULL",
		"UNIQUE (user_id, curation_id, candidate_id)",
		"UNIQUE (user_id, curation_id, plan_target_id, identity_key)",
	} {
		if !strings.Contains(candidateBlock, required) {
			t.Fatalf("stable Candidate contract misses %q", required)
		}
	}
	if strings.Contains(candidateBlock, "'LEGACY'") {
		t.Fatal("Phase 8 Candidate authority must not admit legacy Candidate rows")
	}
	for _, required := range []string{
		"CONSTRAINT phase8_liked_variants_candidate_fkey",
		"FOREIGN KEY (user_id, curation_id, candidate_id)",
		"REFERENCES phase8_research_candidates(user_id, curation_id, candidate_id)",
	} {
		if !strings.Contains(activation, required) {
			t.Fatalf("atomic liked Variant binding misses %q", required)
		}
	}
	if !strings.Contains(down, "DROP CONSTRAINT IF EXISTS phase8_liked_variants_candidate_fkey") {
		t.Fatal("atomic liked Variant binding is not reversible")
	}
	cartBlock := catalogCreateTableBlock(t, activation, "phase8_cart_items")
	for _, required := range []string{
		"product_title_snapshot TEXT NOT NULL", "variant_id TEXT NOT NULL",
		"preview_price_minor BIGINT NOT NULL", "observed_at TIMESTAMPTZ NOT NULL",
		"UNIQUE (user_id, curation_id, candidate_id, variant_id)",
	} {
		if !strings.Contains(cartBlock, required) {
			t.Fatalf("fallible CartView snapshot contract misses %q", required)
		}
	}
	for _, forbidden := range []string{"offer_hash", "offer_expiry", "resolved_offer", "choice_token"} {
		if strings.Contains(strings.ToLower(cartBlock), forbidden) {
			t.Fatalf("CartView incorrectly became an exact-offer store: %s", forbidden)
		}
	}
}

func TestCatalogCandidateProductIdentityMigrationIsAdditiveAndReversible(t *testing.T) {
	t.Parallel()

	up := readCatalogMigration(t, "000056_phase8_candidate_product_identity.up.sql")
	down := readCatalogMigration(t, "000056_phase8_candidate_product_identity.down.sql")
	for _, required := range []string{
		"CREATE FUNCTION phase8_shopify_product_identity_key",
		"first_value(candidate.candidate_id)",
		"phase8_candidate_configurations_v56",
		"phase8_variant_interactions_v56",
		"interaction.candidate_id AS source_candidate_id",
		"phase8_liked_variants_v56",
		"interaction.source_candidate_id=liked.candidate_id",
		"interaction.sentiment='LIKE'",
		"phase8_cart_items_v56",
		"SET provider_product_id=btrim(provider_product_id)",
		"identity_key=phase8_shopify_product_identity_key(provider_product_id)",
		"CREATE UNIQUE INDEX phase8_research_candidates_provider_product_identity_idx",
		"candidate.provider_product_id=NEW.provider_product_id",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("product identity migration misses %q", required)
		}
	}
	for _, forbidden := range []string{
		"DROP TABLE phase8_research_candidates",
		"TRUNCATE phase8_research_candidates",
	} {
		if strings.Contains(up, forbidden) {
			t.Fatalf("product identity migration uses destructive shortcut %q", forbidden)
		}
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS phase8_research_candidates_provider_product_identity_idx",
		"row_number() OVER",
		"'rollback-candidate:' || candidate.candidate_id",
		"candidate.identity_key=NEW.identity_key",
		"DROP FUNCTION IF EXISTS phase8_shopify_product_identity_key(TEXT)",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("product identity rollback misses %q", required)
		}
	}
}

func catalogCreateTableBlock(t *testing.T, migration, table string) string {
	t.Helper()
	startMarker := "CREATE TABLE " + table + " ("
	start := strings.Index(migration, startMarker)
	if start < 0 {
		t.Fatalf("table not found: %s", table)
	}
	rest := migration[start+len(startMarker):]
	end := strings.Index(rest, "\n);\n")
	if end < 0 {
		t.Fatalf("table block not terminated: %s", table)
	}
	return rest[:end]
}

func readCatalogMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("../../../../migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestCatalogLikedVariantsMigrationIsUserScopedAndRecoverable(t *testing.T) {
	upBytes, err := os.ReadFile("../../../../migrations/000054_phase8_liked_variants.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downBytes, err := os.ReadFile("../../../../migrations/000054_phase8_liked_variants.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)
	for _, required := range []string{
		"CREATE TABLE phase8_liked_variants",
		"CONSTRAINT phase8_liked_variants_curation_fkey",
		"FOREIGN KEY (user_id, curation_id)",
		"REFERENCES curations(user_id, id) ON DELETE CASCADE",
		"PRIMARY KEY (user_id, candidate_id, variant_id)",
		"price_minor BIGINT NOT NULL CHECK (price_minor >= 0)",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("liked Variant migration misses %q", required)
		}
	}
	if !strings.Contains(string(downBytes), "DROP TABLE IF EXISTS phase8_liked_variants") {
		t.Fatal("liked Variant down migration is missing")
	}
}

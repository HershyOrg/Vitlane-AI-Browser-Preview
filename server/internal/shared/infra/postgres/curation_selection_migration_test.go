package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCurationSelectionExpandMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t,
		directory,
		"000034_curation_selection_expand.up.sql",
	)
	down := readMigrationContract(
		t,
		directory,
		"000034_curation_selection_expand.down.sql",
	)

	for _, fragment := range []string{
		"CREATE TABLE curation_selections",
		"FOREIGN KEY (user_id, curation_id)\n" +
			"        REFERENCES curations(user_id, id)",
		"FOREIGN KEY (user_id, plan_target_id, curation_id)\n" +
			"        REFERENCES plan_targets(user_id, id, curation_id)",
		"FOREIGN KEY (user_id, shopping_session_id, plan_target_id)\n" +
			"        REFERENCES shopping_sessions(user_id, id, plan_target_id)",
		"FOREIGN KEY (\n" +
			"            candidate_configuration_id, user_id, shopping_session_id,\n" +
			"            candidate_id, candidate_configuration_hash\n" +
			"        ) REFERENCES candidate_configurations(",
		"quantity BIGINT NOT NULL CHECK (quantity BETWEEN 1 AND 99)",
		"CONSTRAINT curation_selections_immutable_lineage_unique",
		"CREATE TRIGGER curation_selection_immutable",
		"OLD.removed_at IS NOT NULL",
		"NEW.version <> OLD.version + 1",
		"NEW.removed_by_user_id IS DISTINCT FROM OLD.user_id",
		"CREATE TABLE curation_selection_commands",
		"command_kind IN ('CREATE', 'UPDATE', 'REMOVE')",
		"request_hash ~ '^[0-9a-f]{64}$'",
		"response_snapshot->>'id'=selection_id::text",
		"response_snapshot->>'curationId'=curation_id::text",
		"UNIQUE (user_id, client_command_id)",
		"CREATE TRIGGER curation_selection_command_immutable",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}

	for _, forbidden := range []string{
		"REFERENCES curations(plan_id)",
		"SET id=plan_id",
		`"DERIVE"`,
		"DROP TABLE IF EXISTS shopping_carts",
		"DROP TABLE IF EXISTS shopping_cart_items",
		"ALTER TABLE purchases",
		"CREATE TABLE purchase_",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration contains forbidden coupling %q", forbidden)
		}
	}

	assertSelectionHashSQLContract(t, up)

	for _, fragment := range []string{
		"DROP TRIGGER IF EXISTS curation_selection_command_immutable",
		"DROP FUNCTION IF EXISTS reject_curation_selection_command_mutation()",
		"DROP TABLE IF EXISTS curation_selection_commands",
		"DROP TRIGGER IF EXISTS curation_selection_immutable",
		"DROP FUNCTION IF EXISTS enforce_curation_selection_immutability()",
		"DROP TABLE IF EXISTS curation_selections",
		"DROP FUNCTION IF EXISTS curation_selection_snapshot_hash_v1(\n" +
			"    UUID, UUID, UUID, UUID, UUID, UUID, TEXT, BIGINT, BIGINT\n" +
			")",
		"candidate_configurations_selection_lineage_unique",
		"candidate_configurations_selection_identity_unique",
		"shopping_sessions_user_target_fkey",
		"shopping_sessions_user_id_id_target_unique",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}

	for _, forbidden := range []string{
		"shopping_carts",
		"shopping_cart_items",
		"ALTER TABLE purchases",
	} {
		if strings.Contains(down, forbidden) {
			t.Errorf("down migration changes an out-of-scope table %q", forbidden)
		}
	}
}

func assertSelectionHashSQLContract(t *testing.T, up string) {
	t.Helper()

	const declaration = "CREATE FUNCTION curation_selection_snapshot_hash_v1("
	start := strings.Index(up, declaration)
	if start < 0 {
		t.Fatal("selection snapshot hash function is missing")
	}
	remainder := up[start:]
	end := strings.Index(remainder, "\n$$;")
	if end < 0 {
		t.Fatal("selection snapshot hash function body is incomplete")
	}
	body := remainder[:end]

	for _, fragment := range []string{
		"RETURNS TEXT",
		"IMMUTABLE",
		"STRICT",
		"PARALLEL SAFE",
		"'sha256:' || encode(",
		"sha256(",
		"convert_to(",
		`'{"schema":"CurationSelectionSnapshotHash.v1","snapshot":{'`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("snapshot hash function is missing %q", fragment)
		}
	}

	assertOrderedSelectionHashFields(
		t,
		body,
		[]string{
			`"candidateConfigurationHash"`,
			`"candidateConfigurationId"`,
			`"candidateId"`,
			`"curationId"`,
			`"quantity"`,
			`"selectionId"`,
			`"shoppingSessionId"`,
			`"targetId"`,
			`"version"`,
		},
	)

	const canonicalPreimage = `{"schema":"CurationSelectionSnapshotHash.v1","snapshot":{"candidateConfigurationHash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","candidateConfigurationId":"66666666-6666-4666-8666-666666666666","candidateId":"99999999-9999-4999-8999-999999999999","curationId":"22222222-2222-4222-8222-222222222222","quantity":2,"selectionId":"44444444-4444-4444-8444-444444444444","shoppingSessionId":"88888888-8888-4888-8888-888888888888","targetId":"77777777-7777-4777-8777-777777777777","version":3}}`
	sum := sha256.Sum256([]byte(canonicalPreimage))
	got := "sha256:" + hex.EncodeToString(sum[:])
	const want = "sha256:e2654daccb61c0e3379167dd8104bce4b2ff6134be8e2e3a675b21c98d0fea2b"
	if got != want {
		t.Fatalf("golden snapshot hash=%q want=%q", got, want)
	}
}

func assertOrderedSelectionHashFields(
	t *testing.T,
	body string,
	fields []string,
) {
	t.Helper()
	previous := -1
	for _, field := range fields {
		index := strings.Index(body, field)
		if index < 0 {
			t.Errorf("snapshot hash function is missing field %s", field)
			continue
		}
		if index <= previous {
			t.Errorf("snapshot hash field %s is out of canonical order", field)
		}
		previous = index
	}
}

package postgres

import (
	"strings"
	"testing"
)

func TestIntentCurationStateActionExpandMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000033_intent_curation_state_action_expand.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000033_intent_curation_state_action_expand.down.sql",
	)

	for _, fragment := range []string{
		"SET\n    id=gen_random_uuid()",
		"CHECK (phase IN ('PLANNING', 'CURATING'))",
		"CREATE TABLE curation_actions",
		"phase_at_request IN ('HAVING_INTENT', 'PLANNING', 'CURATING')",
		"effect_kind IN ('NONE', 'AGENT_WORK', 'PURCHASE_THREAD')",
		"expected_curation_version BIGINT NOT NULL",
		"CHECK (octet_length(request_hash) = 32)",
		"ADD COLUMN curation_action_id UUID",
		"REFERENCES curation_actions(id, actor_user_id)",
		"status IN (\n            'REQUESTED', 'MATERIALIZING', 'COMPLETED'",
		// curation_runs already has foreign keys, so the status backfill above
		// queues row trigger events and every later ALTER TABLE on that table
		// fails with SQLSTATE 55006. Only a database that actually has rows to
		// update hits it, so this contract keeps the flush in place.
		"SET CONSTRAINTS ALL IMMEDIATE;",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}

	for _, forbidden := range []string{
		"SET id=plan_id",
		"ADD COLUMN action_status",
		"CREATE UNIQUE INDEX curation_actions_one_active",
		"REFERENCES curation_actions(id, actor_user_id)\n        ON DELETE CASCADE",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration contains forbidden coupling %q", forbidden)
		}
	}

	for _, fragment := range []string{
		"DROP TABLE IF EXISTS curation_actions",
		"DROP COLUMN IF EXISTS curation_action_id",
		"DROP COLUMN IF EXISTS phase",
		"DROP COLUMN IF EXISTS shopping_plan_id",
		"DROP COLUMN IF EXISTS id",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
}

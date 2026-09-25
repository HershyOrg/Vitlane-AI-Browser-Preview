package postgres

import (
	"strings"
	"testing"
)

func TestShoppingPlanImmutableCutoverMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t,
		directory,
		"000036_intent_curation_shopping_plan_immutable_cutover.up.sql",
	)
	down := readMigrationContract(
		t,
		directory,
		"000036_intent_curation_shopping_plan_immutable_cutover.down.sql",
	)

	for _, fragment := range []string{
		"DROP INDEX IF EXISTS idx_shopping_plans_user_updated_at",
		"DROP COLUMN IF EXISTS status",
		"DROP COLUMN IF EXISTS current_planning_proposal_id",
		"DROP COLUMN IF EXISTS version",
		"DROP COLUMN IF EXISTS updated_at",
		"DROP COLUMN IF EXISTS planning_context_version",
		"DROP COLUMN IF EXISTS planning_context_hash",
		"DROP CONSTRAINT IF EXISTS plan_targets_plan_id_order_index_key",
		"CREATE UNIQUE INDEX idx_plan_targets_active_plan_order_unique",
		"WHERE removed_at IS NULL",
		"CREATE TRIGGER shopping_plans_reject_update",
		"BEFORE UPDATE ON shopping_plans",
		"shopping_plans are immutable after creation",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing immutable contract fragment %q", fragment)
		}
	}

	for _, fragment := range []string{
		"DROP TRIGGER IF EXISTS shopping_plans_reject_update",
		"DROP INDEX IF EXISTS idx_plan_targets_active_plan_order_unique",
		"ADD CONSTRAINT plan_targets_plan_id_order_index_key",
		"ADD COLUMN planning_context_version",
		"ADD COLUMN current_planning_proposal_id",
		"ADD COLUMN status",
		"ADD COLUMN version",
		"ADD COLUMN updated_at",
		"CREATE INDEX idx_shopping_plans_user_updated_at",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
}

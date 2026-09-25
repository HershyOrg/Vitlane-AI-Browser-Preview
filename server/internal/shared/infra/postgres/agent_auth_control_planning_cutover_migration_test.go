package postgres

import (
	"strings"
	"testing"
)

func TestAgentAuthControlPlanningCutoverMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000026_agent_auth_control_planning_cutover.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000026_agent_auth_control_planning_cutover.down.sql",
	)

	for _, fragment := range []string{
		"agent_work_orders_planning_identity_key",
		"ADD COLUMN agent_work_order_id UUID",
		"planning_proposals_agent_work_order_fkey",
		"planning_proposals_agent_control_provenance_check",
		"planning_proposals_work_order_idempotency_idx",
		"num_nonnulls(agent_grant_id, agent_work_order_id) = 1",
		"FOREIGN KEY (agent_work_order_id, task_id, user_id)",
		"REFERENCES agent_work_orders(id, planning_task_id, user_id)",
		"DEFERRABLE INITIALLY DEFERRED",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("Planning cutover up migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"DROP TABLE agent_grants",
		"DROP TABLE agent_connections",
		"TRUNCATE",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("Planning cutover unexpectedly destroys data with %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"DROP INDEX IF EXISTS planning_proposals_work_order_idempotency_idx",
		"DROP COLUMN IF EXISTS agent_work_order_id",
		"DROP CONSTRAINT IF EXISTS agent_work_orders_planning_identity_key",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("Planning cutover down migration is missing %q", fragment)
		}
	}
}

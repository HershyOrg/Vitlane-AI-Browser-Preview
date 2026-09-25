package postgres

import (
	"strings"
	"testing"
)

func TestAgentAuthControlResearchCutoverMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000025_agent_auth_control_research_cutover.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000025_agent_auth_control_research_cutover.down.sql",
	)

	for _, fragment := range []string{
		"agent_work_orders_research_identity_key",
		"ADD COLUMN agent_work_order_id UUID",
		"research_submissions_agent_work_order_fkey",
		"research_submissions_agent_control_provenance_check",
		"research_submissions_work_order_idempotency_idx",
		"research_catalog_observations_agent_work_order_fkey",
		"research_catalog_observations_agent_control_provenance_check",
		"research_catalog_observations_work_order_access_idx",
		"num_nonnulls(agent_grant_id, agent_work_order_id) = 1",
		"DEFERRABLE INITIALLY DEFERRED",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("Research cutover up migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"DROP TABLE agent_grants",
		"DROP TABLE agent_connections",
		"TRUNCATE",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("Research cutover unexpectedly destroys data with %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"DROP INDEX IF EXISTS research_catalog_observations_work_order_access_idx",
		"DROP INDEX IF EXISTS research_submissions_work_order_idempotency_idx",
		"DROP COLUMN IF EXISTS agent_work_order_id",
		"DROP CONSTRAINT IF EXISTS agent_work_orders_research_identity_key",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("Research cutover down migration is missing %q", fragment)
		}
	}
}

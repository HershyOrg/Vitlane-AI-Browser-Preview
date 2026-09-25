package postgres

import (
	"strings"
	"testing"
)

func TestAgentConnectionRevokeCommandMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000028_agent_connection_revoke_command.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000028_agent_connection_revoke_command.down.sql",
	)

	for _, fragment := range []string{
		"CREATE TABLE agent_connection_revoke_commands",
		"PRIMARY KEY (user_id, idempotency_key)",
		"expected_version BIGINT NOT NULL CHECK (expected_version > 0)",
		"result_version IS NULL OR result_version = expected_version + 1",
		"FOREIGN KEY (connection_id, user_id)",
		"REFERENCES agent_connections(id, user_id) ON DELETE NO ACTION",
		"DEFERRABLE INITIALLY DEFERRED",
		"agent_connection_revoke_commands_completion_check",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}
	if !strings.Contains(
		down, "DROP TABLE IF EXISTS agent_connection_revoke_commands",
	) {
		t.Error("down migration must remove the revoke command table")
	}
}

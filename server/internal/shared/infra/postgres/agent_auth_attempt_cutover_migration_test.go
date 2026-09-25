package postgres

import (
	"strings"
	"testing"
)

func TestAgentAuthAttemptCutoverMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000027_agent_auth_attempt_cutover.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000027_agent_auth_attempt_cutover.down.sql",
	)

	for _, fragment := range []string{
		"CREATE TABLE agent_auth_attempts",
		"'REQUESTED', 'USER_AUTHENTICATING', 'CONSENT_REQUIRED'",
		"'CODE_ISSUED', 'TOKEN_EXCHANGED'",
		"state_hash BYTEA NOT NULL",
		"request_fingerprint BYTEA NOT NULL",
		"DROP TABLE oauth_authorization_codes",
		"auth_attempt_id UUID NOT NULL UNIQUE",
		"invalidated_at TIMESTAMPTZ",
		"source_auth_attempt_id UUID",
		"confirmation_deadline_at TIMESTAMPTZ",
		"agent_credential_families_id_connection_key",
		"DEFERRABLE INITIALLY DEFERRED",
		"CREATE TABLE agent_auth_attempt_cancel_requests",
		"PRIMARY KEY (user_id, idempotency_key)",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"state TEXT",
		"raw_state",
		"code_verifier",
		"connection_id UUID NOT NULL\n        REFERENCES agent_connections",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration persists forbidden legacy material %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"DROP TABLE IF EXISTS agent_auth_attempt_cancel_requests",
		"DROP COLUMN IF EXISTS source_auth_attempt_id",
		"DROP TABLE agent_auth_attempts",
		"connection_id UUID NOT NULL",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
}

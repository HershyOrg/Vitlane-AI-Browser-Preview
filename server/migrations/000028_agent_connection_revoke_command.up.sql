CREATE TABLE agent_connection_revoke_commands (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (
        btrim(idempotency_key) <> ''
        AND octet_length(idempotency_key) <= 200
    ),
    connection_id UUID NOT NULL,
    expected_version BIGINT NOT NULL CHECK (expected_version > 0),
    result_version BIGINT CHECK (
        result_version IS NULL OR result_version = expected_version + 1
    ),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, idempotency_key),
    CONSTRAINT agent_connection_revoke_commands_connection_fkey
        FOREIGN KEY (connection_id, user_id)
        REFERENCES agent_connections(id, user_id) ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT agent_connection_revoke_commands_completion_check CHECK (
        (result_version IS NULL AND completed_at IS NULL)
        OR
        (result_version IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE INDEX agent_connection_revoke_commands_connection_idx
    ON agent_connection_revoke_commands(connection_id, created_at DESC);

DROP TABLE IF EXISTS agent_auth_attempt_cancel_requests;

ALTER TABLE agent_auth_attempts
    DROP CONSTRAINT IF EXISTS agent_auth_attempts_family_fkey,
    DROP CONSTRAINT IF EXISTS agent_auth_attempts_connection_fkey;

ALTER TABLE agent_credential_families
    DROP CONSTRAINT IF EXISTS agent_credential_families_id_connection_key;

ALTER TABLE agent_connections
    DROP CONSTRAINT IF EXISTS agent_connections_confirmation_deadline_check,
    DROP CONSTRAINT IF EXISTS agent_connections_source_auth_attempt_key,
    DROP CONSTRAINT IF EXISTS agent_connections_source_auth_attempt_fkey,
    DROP COLUMN IF EXISTS confirmation_deadline_at,
    DROP COLUMN IF EXISTS source_auth_attempt_id;

DROP TABLE oauth_authorization_codes;

CREATE TABLE oauth_authorization_codes (
    code_hash BYTEA PRIMARY KEY,
    connection_id UUID NOT NULL
        REFERENCES agent_connections(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL
        REFERENCES agent_oauth_clients(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    resource TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    pkce_challenge TEXT NOT NULL,
    credential_family_id UUID,
    credential_epoch BIGINT,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT oauth_authorization_codes_credential_link_check CHECK (
        (
            credential_family_id IS NULL
            AND credential_epoch IS NULL
        )
        OR
        (
            credential_family_id IS NOT NULL
            AND credential_epoch IS NOT NULL
            AND credential_epoch > 0
        )
    ),
    CONSTRAINT oauth_authorization_codes_credential_family_fkey
        FOREIGN KEY (credential_family_id, connection_id, credential_epoch)
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX oauth_authorization_codes_credential_family_idx
    ON oauth_authorization_codes(
        credential_family_id, credential_epoch, expires_at
    )
    WHERE credential_family_id IS NOT NULL AND consumed_at IS NULL;

DROP TABLE agent_auth_attempts;

-- Hard cutover from consent-created Connections to explicit OAuth attempts.
-- Authorization codes are five-minute credentials, so in-flight legacy codes
-- are intentionally invalidated rather than migrated.

CREATE TABLE agent_auth_attempts (
    id UUID PRIMARY KEY,
    oauth_client_id UUID NOT NULL
        REFERENCES agent_oauth_clients(id) ON DELETE RESTRICT,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    requested_scopes TEXT[] NOT NULL,
    resource TEXT NOT NULL CHECK (btrim(resource) <> ''),
    issuer TEXT NOT NULL CHECK (btrim(issuer) <> ''),
    state_hash BYTEA NOT NULL CHECK (octet_length(state_hash) = 32),
    request_fingerprint BYTEA NOT NULL
        CHECK (octet_length(request_fingerprint) = 32),
    status TEXT NOT NULL CHECK (
        status IN (
            'REQUESTED', 'USER_AUTHENTICATING', 'CONSENT_REQUIRED',
            'CODE_ISSUED', 'TOKEN_EXCHANGED', 'DENIED', 'CANCELLED',
            'FAILED', 'EXPIRED', 'SUPERSEDED'
        )
    ),
    terminal_reason_code TEXT,
    retry_of_attempt_id UUID
        REFERENCES agent_auth_attempts(id) ON DELETE SET NULL,
    superseded_by_attempt_id UUID
        REFERENCES agent_auth_attempts(id) ON DELETE SET NULL,
    connection_id UUID,
    credential_family_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_auth_attempts_timeline_check CHECK (
        expires_at > created_at
        AND updated_at >= created_at
        AND (completed_at IS NULL OR completed_at >= created_at)
    ),
    CONSTRAINT agent_auth_attempts_state_check CHECK (
        (
            status IN ('REQUESTED', 'USER_AUTHENTICATING')
            AND user_id IS NULL
            AND connection_id IS NULL
            AND credential_family_id IS NULL
            AND completed_at IS NULL
            AND terminal_reason_code IS NULL
        )
        OR
        (
            status IN ('CONSENT_REQUIRED', 'CODE_ISSUED')
            AND user_id IS NOT NULL
            AND connection_id IS NULL
            AND credential_family_id IS NULL
            AND completed_at IS NULL
            AND terminal_reason_code IS NULL
        )
        OR
        (
            status = 'TOKEN_EXCHANGED'
            AND user_id IS NOT NULL
            AND connection_id IS NOT NULL
            AND credential_family_id IS NOT NULL
            AND completed_at IS NOT NULL
            AND terminal_reason_code IS NULL
        )
        OR
        (
            status IN (
                'DENIED', 'CANCELLED', 'FAILED', 'EXPIRED', 'SUPERSEDED'
            )
            AND completed_at IS NOT NULL
            AND terminal_reason_code IS NOT NULL
        )
    ),
    CONSTRAINT agent_auth_attempts_retry_self_check CHECK (
        retry_of_attempt_id IS NULL OR retry_of_attempt_id <> id
    ),
    CONSTRAINT agent_auth_attempts_superseded_self_check CHECK (
        superseded_by_attempt_id IS NULL OR superseded_by_attempt_id <> id
    ),
    CONSTRAINT agent_auth_attempts_credential_pair_check CHECK (
        (connection_id IS NULL AND credential_family_id IS NULL)
        OR
        (connection_id IS NOT NULL AND credential_family_id IS NOT NULL)
    ),
    CONSTRAINT agent_auth_attempts_request_identity_key UNIQUE (
        oauth_client_id, state_hash
    ),
    CONSTRAINT agent_auth_attempts_id_user_key UNIQUE (id, user_id)
);

CREATE INDEX agent_auth_attempts_user_updated_idx
    ON agent_auth_attempts(user_id, updated_at DESC)
    WHERE user_id IS NOT NULL;

CREATE INDEX agent_auth_attempts_deadline_idx
    ON agent_auth_attempts(expires_at)
    WHERE status IN (
        'REQUESTED', 'USER_AUTHENTICATING', 'CONSENT_REQUIRED', 'CODE_ISSUED'
    );

DROP TABLE oauth_authorization_codes;

CREATE TABLE oauth_authorization_codes (
    code_hash BYTEA PRIMARY KEY,
    auth_attempt_id UUID NOT NULL UNIQUE
        REFERENCES agent_auth_attempts(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL
        REFERENCES agent_oauth_clients(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    resource TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    pkce_challenge TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    invalidation_reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT oauth_authorization_codes_terminal_check CHECK (
        NOT (consumed_at IS NOT NULL AND invalidated_at IS NOT NULL)
        AND (
            (invalidated_at IS NULL AND invalidation_reason_code IS NULL)
            OR
            (invalidated_at IS NOT NULL AND invalidation_reason_code IS NOT NULL)
        )
    )
);

CREATE INDEX oauth_authorization_codes_exchangeable_idx
    ON oauth_authorization_codes(expires_at)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

ALTER TABLE agent_connections
    ADD COLUMN source_auth_attempt_id UUID,
    ADD COLUMN confirmation_deadline_at TIMESTAMPTZ,
    ADD CONSTRAINT agent_connections_source_auth_attempt_fkey
        FOREIGN KEY (source_auth_attempt_id)
        REFERENCES agent_auth_attempts(id) ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT agent_connections_source_auth_attempt_key
        UNIQUE (source_auth_attempt_id),
    ADD CONSTRAINT agent_connections_confirmation_deadline_check CHECK (
        confirmation_deadline_at IS NULL
        OR confirmation_deadline_at > authorized_at
    );

ALTER TABLE agent_credential_families
    ADD CONSTRAINT agent_credential_families_id_connection_key
        UNIQUE (id, connection_id);

ALTER TABLE agent_auth_attempts
    ADD CONSTRAINT agent_auth_attempts_connection_fkey
        FOREIGN KEY (connection_id, user_id)
        REFERENCES agent_connections(id, user_id) ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT agent_auth_attempts_family_fkey
        FOREIGN KEY (credential_family_id, connection_id)
        REFERENCES agent_credential_families(id, connection_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE agent_auth_attempt_cancel_requests (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (
        btrim(idempotency_key) <> ''
        AND octet_length(idempotency_key) <= 200
    ),
    auth_attempt_id UUID NOT NULL,
    expected_version BIGINT NOT NULL CHECK (expected_version > 0),
    result_version BIGINT CHECK (
        result_version IS NULL OR result_version > 0
    ),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, idempotency_key),
    CONSTRAINT agent_auth_attempt_cancel_requests_attempt_fkey
        FOREIGN KEY (auth_attempt_id, user_id)
        REFERENCES agent_auth_attempts(id, user_id) ON DELETE CASCADE,
    CONSTRAINT agent_auth_attempt_cancel_requests_completion_check CHECK (
        (result_version IS NULL AND completed_at IS NULL)
        OR
        (result_version IS NOT NULL AND completed_at IS NOT NULL)
    )
);

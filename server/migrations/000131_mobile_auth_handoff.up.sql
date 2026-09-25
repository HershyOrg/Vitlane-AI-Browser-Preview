CREATE TABLE mobile_auth_handoffs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    verifier_challenge BYTEA NOT NULL CHECK (octet_length(verifier_challenge) = 32),
    session_expires_at TIMESTAMPTZ NOT NULL,
    authenticated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at),
    CHECK (session_expires_at >= expires_at)
);

CREATE INDEX idx_mobile_auth_handoffs_cleanup
    ON mobile_auth_handoffs(expires_at, id);

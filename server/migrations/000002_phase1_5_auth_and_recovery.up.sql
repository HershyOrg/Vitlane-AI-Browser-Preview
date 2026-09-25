ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS external_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('GOOGLE')),
    provider_subject TEXT NOT NULL,
    email_snapshot TEXT NOT NULL,
    email_verified BOOLEAN NOT NULL,
    display_name_snapshot TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider, provider_subject)
);

CREATE TABLE IF NOT EXISTS oauth_login_attempts (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('GOOGLE')),
    state_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(state_hash) = 32),
    browser_binding_hash BYTEA NOT NULL CHECK (octet_length(browser_binding_hash) = 32),
    nonce_hash BYTEA NOT NULL CHECK (octet_length(nonce_hash) = 32),
    pkce_verifier TEXT NOT NULL,
    return_path TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS plan_creation_requests (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    shopping_plan_id UUID REFERENCES shopping_plans(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_active_lookup
    ON auth_sessions(token_hash)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_login_attempts_expires_at
    ON oauth_login_attempts(expires_at);
CREATE INDEX IF NOT EXISTS idx_shopping_plans_user_updated_at
    ON shopping_plans(user_id, updated_at DESC);

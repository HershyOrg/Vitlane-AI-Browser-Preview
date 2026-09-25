CREATE TABLE agent_oauth_clients (
    id UUID PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE,
    registration_method TEXT NOT NULL
        CHECK (registration_method IN ('PREDEFINED', 'DCR', 'CIMD')),
    client_name TEXT NOT NULL,
    redirect_uris TEXT[] NOT NULL,
    token_endpoint_auth_method TEXT NOT NULL
        CHECK (token_endpoint_auth_method = 'none'),
    client_profile TEXT NOT NULL DEFAULT 'GENERIC_MCP',
    metadata_document_url TEXT,
    metadata_hash TEXT,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE agent_connections (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL REFERENCES agent_oauth_clients(id) ON DELETE RESTRICT,
    label TEXT NOT NULL,
    authorized_scopes TEXT[] NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE', 'REVOKED')),
    authorized_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, oauth_client_id)
);

CREATE TABLE oauth_authorization_codes (
    code_hash BYTEA PRIMARY KEY,
    connection_id UUID NOT NULL REFERENCES agent_connections(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL REFERENCES agent_oauth_clients(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    resource TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    pkce_challenge TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE oauth_access_tokens (
    token_hash BYTEA PRIMARY KEY,
    connection_id UUID NOT NULL REFERENCES agent_connections(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL REFERENCES agent_oauth_clients(id) ON DELETE CASCADE,
    resource TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);

CREATE TABLE oauth_refresh_tokens (
    token_hash BYTEA PRIMARY KEY,
    token_family_id UUID NOT NULL,
    connection_id UUID NOT NULL REFERENCES agent_connections(id) ON DELETE CASCADE,
    oauth_client_id UUID NOT NULL REFERENCES agent_oauth_clients(id) ON DELETE CASCADE,
    resource TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);

CREATE INDEX oauth_refresh_tokens_family_idx
    ON oauth_refresh_tokens(token_family_id);

CREATE TABLE agent_grants (
    id UUID PRIMARY KEY,
    connection_id UUID NOT NULL REFERENCES agent_connections(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('PLANNING', 'RESEARCH')),
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    planning_task_id UUID REFERENCES planning_tasks(id) ON DELETE RESTRICT,
    scopes TEXT[] NOT NULL,
    activation_ref TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    CHECK (
        (kind = 'PLANNING' AND planning_task_id IS NOT NULL)
        OR (kind = 'RESEARCH' AND planning_task_id IS NULL)
    )
);

CREATE UNIQUE INDEX agent_grants_active_planning_task_idx
    ON agent_grants(planning_task_id)
    WHERE kind = 'PLANNING' AND revoked_at IS NULL;

CREATE TABLE agent_grant_sessions (
    agent_grant_id UUID NOT NULL REFERENCES agent_grants(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    released_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agent_grant_id, shopping_session_id)
);

CREATE UNIQUE INDEX agent_grant_sessions_active_session_idx
    ON agent_grant_sessions(shopping_session_id)
    WHERE released_at IS NULL;

CREATE TABLE agent_grant_requests (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL,
    agent_grant_id UUID NOT NULL REFERENCES agent_grants(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, idempotency_key)
);

ALTER TABLE planning_tasks
    ADD COLUMN current_agent_grant_id UUID REFERENCES agent_grants(id) ON DELETE SET NULL,
    ADD COLUMN first_discovered_at TIMESTAMPTZ,
    ADD COLUMN first_context_read_at TIMESTAMPTZ,
    ADD COLUMN last_agent_activity_at TIMESTAMPTZ;

ALTER TABLE research_rounds
    ADD COLUMN current_agent_grant_id UUID REFERENCES agent_grants(id) ON DELETE SET NULL,
    ADD COLUMN first_discovered_at TIMESTAMPTZ,
    ADD COLUMN first_context_read_at TIMESTAMPTZ,
    ADD COLUMN last_agent_activity_at TIMESTAMPTZ;

ALTER TABLE planning_proposals
    ALTER COLUMN agent_capability_id DROP NOT NULL,
    ADD COLUMN agent_grant_id UUID REFERENCES agent_grants(id) ON DELETE RESTRICT,
    ADD CONSTRAINT planning_proposal_agent_provenance_check
        CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL));

CREATE UNIQUE INDEX planning_proposals_grant_idempotency_idx
    ON planning_proposals(task_id, agent_grant_id, client_proposal_id)
    WHERE agent_grant_id IS NOT NULL;

ALTER TABLE research_submissions
    ALTER COLUMN agent_capability_id DROP NOT NULL,
    ADD COLUMN agent_grant_id UUID REFERENCES agent_grants(id) ON DELETE RESTRICT,
    ADD CONSTRAINT research_submission_agent_provenance_check
        CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL));

CREATE UNIQUE INDEX research_submissions_grant_idempotency_idx
    ON research_submissions(research_round_id, agent_grant_id, client_submission_id)
    WHERE agent_grant_id IS NOT NULL;

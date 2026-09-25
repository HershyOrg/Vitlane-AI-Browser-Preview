ALTER TABLE shopping_plans
    DROP CONSTRAINT IF EXISTS shopping_plans_plan_mode_check;

UPDATE shopping_plans
SET plan_mode = CASE
    WHEN plan_mode = 'SINGLE_PRODUCT' THEN 'SINGLE'
    WHEN plan_mode = 'MULTI_PRODUCT' THEN 'AUTO'
    ELSE plan_mode
END;

ALTER TABLE shopping_plans
    ADD CONSTRAINT shopping_plans_plan_mode_check
        CHECK (plan_mode IN ('SINGLE', 'AUTO')),
    ADD COLUMN IF NOT EXISTS planning_context_version BIGINT NOT NULL DEFAULT 1
        CHECK (planning_context_version > 0),
    ADD COLUMN IF NOT EXISTS planning_context_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE plan_targets
    ADD COLUMN IF NOT EXISTS target_hash_schema TEXT NOT NULL
        DEFAULT 'vitlane.plan-target.v1';

CREATE TABLE IF NOT EXISTS planning_tasks (
    id UUID PRIMARY KEY,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    context_version BIGINT NOT NULL CHECK (context_version > 0),
    context_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('REQUESTED', 'COMPLETED', 'CANCELLED')),
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_planning_tasks_one_requested_per_plan
    ON planning_tasks(plan_id)
    WHERE status = 'REQUESTED';
CREATE INDEX IF NOT EXISTS idx_planning_tasks_user_status
    ON planning_tasks(user_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_capabilities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('PLANNING')),
    label TEXT NOT NULL DEFAULT '',
    scopes TEXT[] NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_capabilities_user_plan
    ON agent_capabilities(user_id, plan_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_capabilities_active_token
    ON agent_capabilities(token_hash)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS planning_proposals (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES planning_tasks(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_capability_id UUID NOT NULL REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    client_proposal_id UUID NOT NULL,
    context_version BIGINT NOT NULL CHECK (context_version > 0),
    context_hash TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    proposal_hash TEXT NOT NULL,
    payload JSONB NOT NULL,
    validation_status TEXT NOT NULL CHECK (validation_status IN ('ACCEPTED', 'REJECTED')),
    validation_reason_codes TEXT[] NOT NULL DEFAULT '{}',
    submitted_at TIMESTAMPTZ NOT NULL,
    UNIQUE (task_id, agent_capability_id, client_proposal_id)
);

CREATE INDEX IF NOT EXISTS idx_planning_proposals_plan_submitted
    ON planning_proposals(plan_id, submitted_at DESC);

ALTER TABLE shopping_plans
    ADD COLUMN IF NOT EXISTS current_planning_proposal_id UUID
        REFERENCES planning_proposals(id) ON DELETE SET NULL;

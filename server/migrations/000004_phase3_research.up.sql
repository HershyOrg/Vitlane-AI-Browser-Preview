ALTER TABLE agent_capabilities
    DROP CONSTRAINT IF EXISTS agent_capabilities_kind_check;

ALTER TABLE agent_capabilities
    ADD CONSTRAINT agent_capabilities_kind_check
        CHECK (kind IN ('PLANNING', 'RESEARCH'));

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check;

ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY', 'RESEARCHING', 'REVIEWING'));

CREATE TABLE agent_capability_sessions (
    capability_id UUID NOT NULL REFERENCES agent_capabilities(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (capability_id, shopping_session_id)
);

CREATE INDEX idx_agent_capability_sessions_session
    ON agent_capability_sessions(shopping_session_id);

CREATE TABLE research_rounds (
    id UUID PRIMARY KEY,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    round_number INTEGER NOT NULL CHECK (round_number > 0),
    context_schema TEXT NOT NULL,
    context_version BIGINT NOT NULL CHECK (context_version > 0),
    context_hash TEXT NOT NULL,
    context_snapshot JSONB NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('REQUESTED', 'RESULTS_READY', 'NO_RESULTS', 'CANCELLED', 'SUPERSEDED')
    ),
    result_submission_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (shopping_session_id, round_number)
);

CREATE UNIQUE INDEX idx_research_rounds_one_requested_per_session
    ON research_rounds(shopping_session_id)
    WHERE status = 'REQUESTED';
CREATE INDEX idx_research_rounds_user_status
    ON research_rounds(user_id, status, created_at DESC);

ALTER TABLE shopping_sessions
    ADD COLUMN current_research_round_id UUID
        REFERENCES research_rounds(id) ON DELETE SET NULL;

CREATE TABLE research_submissions (
    id UUID PRIMARY KEY,
    research_round_id UUID NOT NULL REFERENCES research_rounds(id) ON DELETE CASCADE,
    agent_capability_id UUID NOT NULL REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    client_submission_id TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    context_version BIGINT NOT NULL,
    context_hash TEXT NOT NULL,
    submission_hash TEXT NOT NULL,
    payload JSONB NOT NULL,
    outcome TEXT NOT NULL,
    validation_status TEXT NOT NULL CHECK (validation_status IN ('VALID', 'REJECTED')),
    validation_reason_codes TEXT[] NOT NULL DEFAULT '{}',
    submitted_at TIMESTAMPTZ NOT NULL,
    UNIQUE (research_round_id, agent_capability_id, client_submission_id)
);

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_result_submission_fkey
        FOREIGN KEY (result_submission_id)
        REFERENCES research_submissions(id)
        ON DELETE SET NULL;

CREATE TABLE candidates (
    id UUID PRIMARY KEY,
    research_submission_id UUID NOT NULL REFERENCES research_submissions(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    product_url TEXT NOT NULL,
    merchant_domain TEXT NOT NULL,
    category TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    price_amount NUMERIC(36, 18) NOT NULL CHECK (price_amount > 0),
    price_currency CHAR(3) NOT NULL,
    variant_options JSONB NOT NULL DEFAULT '[]'::jsonb,
    evidence JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    purchase_support TEXT NOT NULL DEFAULT 'UNKNOWN'
        CHECK (purchase_support IN ('UNKNOWN', 'SUPPORTED', 'UNSUPPORTED')),
    eligibility JSONB NOT NULL,
    candidate_hash_schema TEXT NOT NULL,
    candidate_hash TEXT NOT NULL,
    order_index INTEGER NOT NULL CHECK (order_index >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (research_submission_id, candidate_hash),
    UNIQUE (research_submission_id, order_index)
);

CREATE INDEX idx_candidates_session_created
    ON candidates(shopping_session_id, created_at DESC);

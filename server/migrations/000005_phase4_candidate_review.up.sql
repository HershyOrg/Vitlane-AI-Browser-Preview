ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check;

ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY', 'RESEARCHING', 'REVIEWING', 'SELECTED'));

ALTER TABLE candidates
    ADD CONSTRAINT candidates_id_session_unique
        UNIQUE (id, shopping_session_id);

ALTER TABLE shopping_sessions
    ADD COLUMN selected_candidate_id UUID;

ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_selected_candidate_fkey
        FOREIGN KEY (selected_candidate_id, id)
        REFERENCES candidates(id, shopping_session_id)
        ON DELETE RESTRICT;

CREATE TABLE candidate_decisions (
    id UUID PRIMARY KEY,
    event_sequence BIGSERIAL UNIQUE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('PIN', 'REJECT', 'SELECT', 'UNDO')),
    feedback TEXT NOT NULL DEFAULT '',
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reverses_decision_id UUID REFERENCES candidate_decisions(id) ON DELETE RESTRICT,
    client_command_id UUID NOT NULL,
    command_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id)
        ON DELETE RESTRICT,
    CHECK (
        (decision = 'UNDO' AND reverses_decision_id IS NOT NULL)
        OR (decision <> 'UNDO' AND reverses_decision_id IS NULL)
    ),
    UNIQUE (user_id, client_command_id, decision)
);

CREATE UNIQUE INDEX idx_candidate_decisions_one_reversal
    ON candidate_decisions(reverses_decision_id)
    WHERE reverses_decision_id IS NOT NULL;

CREATE INDEX idx_candidate_decisions_session_sequence
    ON candidate_decisions(shopping_session_id, event_sequence);

CREATE TABLE research_feedback (
    id UUID PRIMARY KEY,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    previous_round_id UUID NOT NULL REFERENCES research_rounds(id) ON DELETE RESTRICT,
    next_round_id UUID NOT NULL UNIQUE REFERENCES research_rounds(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feedback TEXT NOT NULL DEFAULT '',
    decision_snapshot JSONB NOT NULL,
    schema_version TEXT NOT NULL,
    feedback_version BIGINT NOT NULL CHECK (feedback_version > 0),
    feedback_hash TEXT NOT NULL,
    previous_round_status TEXT NOT NULL
        CHECK (previous_round_status IN ('RESULTS_READY', 'NO_RESULTS')),
    status TEXT NOT NULL CHECK (status IN ('ACTIVE', 'CANCELLED')),
    client_request_id UUID NOT NULL,
    request_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    cancelled_at TIMESTAMPTZ,
    UNIQUE (user_id, client_request_id)
);

CREATE UNIQUE INDEX idx_research_feedback_active_previous
    ON research_feedback(previous_round_id)
    WHERE status = 'ACTIVE';

CREATE INDEX idx_research_feedback_session_created
    ON research_feedback(shopping_session_id, created_at DESC);

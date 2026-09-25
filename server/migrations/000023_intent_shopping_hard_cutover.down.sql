-- The preview hard-cutover data deletion is intentionally irreversible.
-- Remove post-cutover shopping data before restoring the legacy NOT NULL
-- configuration constraint; this is destructive and does not recover old data.
TRUNCATE TABLE shopping_plans RESTART IDENTITY CASCADE;

DROP INDEX IF EXISTS idx_plan_targets_active_order;

ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_removal_audit_check,
    ADD COLUMN removed_from_plan_id UUID
        REFERENCES shopping_plans(id) ON DELETE CASCADE,
    ALTER COLUMN plan_id DROP NOT NULL,
    ADD CONSTRAINT plan_targets_removal_membership_check CHECK (
        (
            plan_id IS NOT NULL
            AND removed_from_plan_id IS NULL
            AND removed_at IS NULL
            AND removed_by_user_id IS NULL
        )
        OR
        (
            plan_id IS NULL
            AND removed_from_plan_id IS NOT NULL
            AND removed_at IS NOT NULL
            AND removed_by_user_id IS NOT NULL
        )
    );

CREATE UNIQUE INDEX idx_plan_targets_active_order
    ON plan_targets(plan_id, order_index)
    WHERE plan_id IS NOT NULL;

CREATE INDEX idx_plan_targets_removed_from_plan
    ON plan_targets(removed_from_plan_id, removed_at, id)
    WHERE removed_from_plan_id IS NOT NULL;

CREATE FUNCTION require_active_plan_target_for_session_write()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM 1
    FROM plan_targets target
    WHERE target.id=NEW.plan_target_id
      AND target.plan_id IS NOT NULL
      AND target.removed_at IS NULL
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'SHOPPING_SESSION_TARGET_NOT_ACTIVE'
            USING
                ERRCODE='23514',
                CONSTRAINT='shopping_sessions_active_target_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER shopping_sessions_active_target_guard
BEFORE INSERT OR UPDATE ON shopping_sessions
FOR EACH ROW
EXECUTE FUNCTION require_active_plan_target_for_session_write();

CREATE FUNCTION require_active_plan_target_for_purchase_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM 1
    FROM shopping_sessions s
    JOIN plan_targets target ON target.id=s.plan_target_id
    WHERE s.id=NEW.shopping_session_id
      AND s.user_id=NEW.user_id
      AND target.plan_id IS NOT NULL
      AND target.removed_at IS NULL
    FOR SHARE OF target;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'PURCHASE_TARGET_NOT_ACTIVE'
            USING
                ERRCODE='23514',
                CONSTRAINT='purchases_active_target_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER purchases_active_target_guard
BEFORE INSERT ON purchases
FOR EACH ROW
EXECUTE FUNCTION require_active_plan_target_for_purchase_insert();

DROP TABLE IF EXISTS shopping_cart_commands;
DROP TABLE IF EXISTS shopping_cart_items;
DROP TRIGGER IF EXISTS shopping_plans_create_cart ON shopping_plans;
DROP FUNCTION IF EXISTS vitlane_create_plan_cart();
DROP TABLE IF EXISTS shopping_carts;
DROP TABLE IF EXISTS candidate_interaction_events;
DROP TABLE IF EXISTS candidate_interactions;

ALTER TABLE research_feedback
    RENAME COLUMN interaction_snapshot TO decision_snapshot;

ALTER TABLE research_submissions
    DROP CONSTRAINT IF EXISTS research_submissions_current_schema_check;

ALTER TABLE candidate_configurations
    DROP CONSTRAINT IF EXISTS candidate_configurations_candidate_user_configuration_unique;

ALTER TABLE purchase_intent_snapshots
    DROP CONSTRAINT IF EXISTS purchase_intent_snapshot_current_check;

ALTER TABLE purchases
    DROP CONSTRAINT IF EXISTS purchases_candidate_snapshot_current_check,
    ALTER COLUMN buyer_profile_id DROP NOT NULL,
    ALTER COLUMN buyer_profile_version DROP NOT NULL,
    ALTER COLUMN buyer_profile_snapshot_hash DROP NOT NULL,
    ALTER COLUMN shipping_snapshot_id DROP NOT NULL,
    ALTER COLUMN shipping_profile_id DROP NOT NULL,
    ALTER COLUMN shipping_profile_version DROP NOT NULL,
    ALTER COLUMN shipping_country DROP NOT NULL,
    ALTER COLUMN shipping_masked_summary DROP NOT NULL,
    ALTER COLUMN shipping_snapshot_hmac DROP NOT NULL;

ALTER TABLE candidates
    DROP CONSTRAINT IF EXISTS candidates_purchase_path_current_check,
    DROP CONSTRAINT IF EXISTS candidates_variant_discovery_current_check,
    DROP CONSTRAINT IF EXISTS candidates_current_hash_schema_check,
    ADD COLUMN variant_options JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check,
    ADD COLUMN selected_candidate_id UUID,
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY', 'RESEARCHING', 'REVIEWING', 'SELECTED')),
    ADD CONSTRAINT shopping_sessions_selected_candidate_fkey
        FOREIGN KEY (selected_candidate_id, id)
        REFERENCES candidates(id, shopping_session_id)
        ON DELETE RESTRICT;

CREATE TABLE candidate_decisions (
    id UUID PRIMARY KEY,
    event_sequence BIGSERIAL UNIQUE,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL,
    decision TEXT NOT NULL
        CHECK (decision IN ('PIN', 'REJECT', 'SELECT', 'UNDO')),
    feedback TEXT NOT NULL DEFAULT '',
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reverses_decision_id UUID
        REFERENCES candidate_decisions(id) ON DELETE RESTRICT,
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
    UNIQUE (user_id, client_command_id, decision, candidate_id)
);

CREATE UNIQUE INDEX idx_candidate_decisions_one_reversal
    ON candidate_decisions(reverses_decision_id)
    WHERE reverses_decision_id IS NOT NULL;

CREATE INDEX idx_candidate_decisions_session_sequence
    ON candidate_decisions(shopping_session_id, event_sequence);

ALTER TABLE candidate_configurations
    ADD COLUMN select_decision_id UUID NOT NULL UNIQUE
        REFERENCES candidate_decisions(id) ON DELETE RESTRICT,
    ADD CONSTRAINT candidate_configurations_candidate_hash_decision_unique
        UNIQUE (candidate_id, configuration_hash, select_decision_id);

CREATE TABLE plan_purchase_item_decisions (
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision TEXT NOT NULL CHECK (decision IN ('PURCHASE', 'SKIP')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (plan_id, shopping_session_id)
);

CREATE INDEX idx_plan_purchase_item_decisions_user
    ON plan_purchase_item_decisions(user_id, updated_at DESC);

CREATE TABLE plan_purchase_closures (
    plan_id UUID PRIMARY KEY REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK (reason IN ('ALL_PURCHASED', 'USER_STOPPED')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_plan_purchase_closures_user
    ON plan_purchase_closures(user_id, created_at DESC);

-- Restore an empty preview bearer-capability schema for a version-22 rollback.
-- The hard cutover cannot recover deleted bearer credentials or shopping data.
CREATE TABLE agent_capabilities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    kind TEXT NOT NULL
        CONSTRAINT agent_capabilities_kind_check
        CHECK (kind IN ('PLANNING', 'RESEARCH')),
    label TEXT NOT NULL DEFAULT '',
    scopes TEXT[] NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_capabilities_user_plan
    ON agent_capabilities(user_id, plan_id, created_at DESC);

CREATE INDEX idx_agent_capabilities_active_token
    ON agent_capabilities(token_hash)
    WHERE revoked_at IS NULL;

CREATE TABLE agent_capability_sessions (
    capability_id UUID NOT NULL
        REFERENCES agent_capabilities(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (capability_id, shopping_session_id)
);

CREATE INDEX idx_agent_capability_sessions_session
    ON agent_capability_sessions(shopping_session_id);

ALTER TABLE planning_proposals
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_capability_id UUID
        REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    ADD CONSTRAINT planning_proposal_agent_provenance_check
        CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL)),
    ADD CONSTRAINT planning_proposals_capability_idempotency_unique
        UNIQUE (task_id, agent_capability_id, client_proposal_id);

ALTER TABLE research_submissions
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_capability_id UUID
        REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    ADD CONSTRAINT research_submission_agent_provenance_check
        CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL)),
    ADD CONSTRAINT research_submissions_capability_idempotency_unique
        UNIQUE (research_round_id, agent_capability_id, client_submission_id);

DROP INDEX IF EXISTS research_catalog_observations_round_access_idx;

ALTER TABLE research_catalog_observations
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_capability_id UUID
        REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    ADD CONSTRAINT research_catalog_observations_agent_provenance_check
        CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL));

CREATE INDEX research_catalog_observations_round_access_idx
    ON research_catalog_observations(
        research_round_id, user_id, agent_capability_id, agent_grant_id, expires_at
    );

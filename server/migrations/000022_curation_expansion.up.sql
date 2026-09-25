CREATE TABLE curations (
    plan_id UUID PRIMARY KEY REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'OPEN'
        CHECK (status IN ('OPEN', 'CLOSED')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ
);

INSERT INTO curations(plan_id, user_id, status, version, created_at, updated_at)
SELECT id, user_id, 'OPEN', 1, created_at, updated_at
FROM shopping_plans
ON CONFLICT (plan_id) DO NOTHING;

CREATE TABLE curation_runs (
    id UUID PRIMARY KEY,
    curation_plan_id UUID NOT NULL REFERENCES curations(plan_id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('INITIAL', 'EXPANSION')),
    instruction TEXT NOT NULL,
    planning_task_id UUID NOT NULL UNIQUE
        REFERENCES planning_tasks(id) ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    status TEXT NOT NULL CHECK (
        status IN (
            'REQUESTED', 'MATERIALIZING', 'RESEARCH_READY',
            'RESEARCHING', 'COMPLETED', 'FAILED', 'CANCELLED'
        )
    ),
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (user_id, idempotency_key)
);

CREATE INDEX idx_curation_runs_plan_created
    ON curation_runs(curation_plan_id, created_at DESC);

CREATE UNIQUE INDEX idx_curation_runs_one_open_expansion
    ON curation_runs(curation_plan_id)
    WHERE kind = 'EXPANSION'
      AND status IN (
          'REQUESTED', 'MATERIALIZING', 'RESEARCH_READY', 'RESEARCHING'
      );

ALTER TABLE plan_targets
    ADD COLUMN created_by_curation_run_id UUID
        REFERENCES curation_runs(id) ON DELETE RESTRICT,
    ADD COLUMN removed_from_plan_id UUID
        REFERENCES shopping_plans(id) ON DELETE CASCADE,
    ADD COLUMN removed_at TIMESTAMPTZ,
    ADD COLUMN removed_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_plan_id_order_index_key,
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

CREATE INDEX idx_plan_targets_created_by_curation_run
    ON plan_targets(created_by_curation_run_id)
    WHERE created_by_curation_run_id IS NOT NULL;

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

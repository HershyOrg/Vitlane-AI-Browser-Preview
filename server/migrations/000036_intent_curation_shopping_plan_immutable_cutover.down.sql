DROP TRIGGER IF EXISTS shopping_plans_reject_update ON shopping_plans;
DROP FUNCTION IF EXISTS reject_shopping_plan_update();

DROP INDEX IF EXISTS idx_plan_targets_active_plan_order_unique;

ALTER TABLE plan_targets
    ADD CONSTRAINT plan_targets_plan_id_order_index_key
        UNIQUE (plan_id, order_index);

ALTER TABLE shopping_plans
    ADD COLUMN planning_context_version BIGINT NOT NULL DEFAULT 1
        CHECK (planning_context_version > 0),
    ADD COLUMN planning_context_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN current_planning_proposal_id UUID
        REFERENCES planning_proposals(id) ON DELETE SET NULL,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'PLANNING',
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    ADD COLUMN updated_at TIMESTAMPTZ;

UPDATE shopping_plans AS plan
SET
    status=CASE
        WHEN curation.phase='CURATING' THEN 'ACTIVE'
        WHEN EXISTS (
            SELECT 1
            FROM plan_targets AS target
            WHERE target.plan_id=plan.id
              AND target.removed_at IS NULL
        ) THEN 'CONFIRMED'
        ELSE 'PLANNING'
    END,
    updated_at=COALESCE(curation.updated_at, plan.created_at)
FROM curations AS curation
WHERE curation.shopping_plan_id=plan.id;

UPDATE shopping_plans
SET updated_at=created_at
WHERE updated_at IS NULL;

ALTER TABLE shopping_plans
    ALTER COLUMN updated_at SET NOT NULL;

CREATE INDEX idx_shopping_plans_user_updated_at
    ON shopping_plans(user_id, updated_at DESC);

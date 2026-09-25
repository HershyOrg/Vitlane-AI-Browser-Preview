-- ADR-0026 hard cutover.
--
-- ShoppingPlan is the immutable source snapshot created by INTENT_NEXT_STEP.
-- User-journey phase/version belongs to Curation; Planning execution belongs
-- to PlanningTask/CurationRun; Target/Session own their respective lifecycles.
DROP INDEX IF EXISTS idx_shopping_plans_user_updated_at;

ALTER TABLE shopping_plans
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS current_planning_proposal_id,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS planning_context_version,
    DROP COLUMN IF EXISTS planning_context_hash;

-- Removed Targets keep immutable history and are never renumbered. Active
-- ordering therefore needs a partial uniqueness boundary rather than the
-- Phase 1 all-history UNIQUE(plan_id, order_index) constraint.
ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_plan_id_order_index_key;

CREATE UNIQUE INDEX idx_plan_targets_active_plan_order_unique
    ON plan_targets(plan_id, order_index)
    WHERE removed_at IS NULL;

CREATE OR REPLACE FUNCTION reject_shopping_plan_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'shopping_plans are immutable after creation'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER shopping_plans_reject_update
BEFORE UPDATE ON shopping_plans
FOR EACH ROW
EXECUTE FUNCTION reject_shopping_plan_update();

ALTER TABLE shopping_plans
    DROP COLUMN IF EXISTS current_planning_proposal_id;

DROP TABLE IF EXISTS planning_proposals;
DROP TABLE IF EXISTS agent_capabilities;
DROP TABLE IF EXISTS planning_tasks;

ALTER TABLE plan_targets
    DROP COLUMN IF EXISTS target_hash_schema;

ALTER TABLE shopping_plans
    DROP COLUMN IF EXISTS planning_context_hash,
    DROP COLUMN IF EXISTS planning_context_version,
    DROP CONSTRAINT IF EXISTS shopping_plans_plan_mode_check;

UPDATE shopping_plans
SET plan_mode = CASE
    WHEN plan_mode = 'SINGLE' THEN 'SINGLE_PRODUCT'
    WHEN plan_mode = 'AUTO' THEN 'MULTI_PRODUCT'
    ELSE plan_mode
END;

ALTER TABLE shopping_plans
    ADD CONSTRAINT shopping_plans_plan_mode_check
        CHECK (plan_mode IN ('SINGLE_PRODUCT', 'MULTI_PRODUCT'));

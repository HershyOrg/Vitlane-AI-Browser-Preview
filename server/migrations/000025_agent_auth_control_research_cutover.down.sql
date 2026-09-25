DROP INDEX IF EXISTS research_catalog_observations_work_order_access_idx;

ALTER TABLE research_catalog_observations
    DROP CONSTRAINT IF EXISTS research_catalog_observations_agent_control_provenance_check,
    DROP CONSTRAINT IF EXISTS research_catalog_observations_agent_work_order_fkey,
    DROP COLUMN IF EXISTS agent_work_order_id;

DROP INDEX IF EXISTS research_submissions_work_order_idempotency_idx;

ALTER TABLE research_submissions
    DROP CONSTRAINT IF EXISTS research_submissions_agent_control_provenance_check,
    DROP CONSTRAINT IF EXISTS research_submissions_agent_work_order_fkey,
    DROP COLUMN IF EXISTS agent_work_order_id;

ALTER TABLE agent_work_orders
    DROP CONSTRAINT IF EXISTS agent_work_orders_research_identity_key;

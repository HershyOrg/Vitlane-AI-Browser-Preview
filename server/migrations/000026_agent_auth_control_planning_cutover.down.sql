DROP INDEX IF EXISTS planning_proposals_work_order_idempotency_idx;

ALTER TABLE planning_proposals
    DROP CONSTRAINT IF EXISTS planning_proposals_agent_control_provenance_check,
    DROP CONSTRAINT IF EXISTS planning_proposals_agent_work_order_fkey,
    DROP COLUMN IF EXISTS agent_work_order_id;

ALTER TABLE agent_work_orders
    DROP CONSTRAINT IF EXISTS agent_work_orders_planning_identity_key;

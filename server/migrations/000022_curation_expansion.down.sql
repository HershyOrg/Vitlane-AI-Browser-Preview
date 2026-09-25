DROP TRIGGER IF EXISTS purchases_active_target_guard ON purchases;
DROP FUNCTION IF EXISTS require_active_plan_target_for_purchase_insert();
DROP TRIGGER IF EXISTS shopping_sessions_active_target_guard ON shopping_sessions;
DROP FUNCTION IF EXISTS require_active_plan_target_for_session_write();

DROP INDEX IF EXISTS idx_plan_targets_created_by_curation_run;
DROP INDEX IF EXISTS idx_plan_targets_removed_from_plan;
DROP INDEX IF EXISTS idx_plan_targets_active_order;
DROP INDEX IF EXISTS idx_curation_runs_one_open_expansion;

ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_removal_membership_check;

WITH active_max AS (
    SELECT plan_id, COALESCE(MAX(order_index), -1) AS max_order_index
    FROM plan_targets
    WHERE plan_id IS NOT NULL
    GROUP BY plan_id
),
removed_order AS (
    SELECT
        target.id,
        target.removed_from_plan_id AS restored_plan_id,
        COALESCE(active.max_order_index, -1)
            + ROW_NUMBER() OVER (
                PARTITION BY target.removed_from_plan_id
                ORDER BY target.removed_at, target.id
            ) AS restored_order_index
    FROM plan_targets target
    LEFT JOIN active_max active
        ON active.plan_id=target.removed_from_plan_id
    WHERE target.plan_id IS NULL
      AND target.removed_from_plan_id IS NOT NULL
)
UPDATE plan_targets AS target
SET
    plan_id = removed_order.restored_plan_id,
    order_index = removed_order.restored_order_index
FROM removed_order
WHERE target.id = removed_order.id;

ALTER TABLE plan_targets
    ALTER COLUMN plan_id SET NOT NULL,
    DROP COLUMN IF EXISTS removed_by_user_id,
    DROP COLUMN IF EXISTS removed_at,
    DROP COLUMN IF EXISTS removed_from_plan_id,
    DROP COLUMN IF EXISTS created_by_curation_run_id;

ALTER TABLE plan_targets
    ADD CONSTRAINT plan_targets_plan_id_order_index_key
        UNIQUE (plan_id, order_index);

DROP TABLE IF EXISTS curation_runs;
DROP TABLE IF EXISTS curations;

ALTER TABLE shopping_plans
    DROP CONSTRAINT IF EXISTS
        shopping_plans_research_scope_snapshot_object_check,
    DROP COLUMN IF EXISTS research_scope_snapshot;

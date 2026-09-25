-- Intent settings are part of the immutable ShoppingPlan input snapshot.
-- Existing preview rows predate this guarantee and therefore remain NULL;
-- every new plan creation writes the complete validated ResearchScope.
ALTER TABLE shopping_plans
    ADD COLUMN research_scope_snapshot JSONB;

ALTER TABLE shopping_plans
    ADD CONSTRAINT shopping_plans_research_scope_snapshot_object_check
        CHECK (
            research_scope_snapshot IS NULL
            OR jsonb_typeof(research_scope_snapshot) = 'object'
        );

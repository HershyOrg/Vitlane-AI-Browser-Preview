-- ADR-0026 Curation identity hard cutover.
--
-- The expand migration introduced the canonical Curation identity while
-- retaining ShoppingPlan-derived aliases for a rolling application cutover.
-- Every runtime consumer now uses curations.id / curation_runs.curation_id, so
-- the aliases and the purchase-completion lifecycle can be removed.

DROP INDEX IF EXISTS idx_curation_runs_one_open_expansion;
DROP INDEX IF EXISTS idx_curation_runs_plan_created;

ALTER TABLE curation_runs
    DROP CONSTRAINT IF EXISTS curation_runs_curation_plan_id_fkey,
    DROP COLUMN IF EXISTS curation_plan_id;

CREATE INDEX idx_curation_runs_curation_created
    ON curation_runs(curation_id, created_at DESC);

CREATE UNIQUE INDEX idx_curation_runs_one_open_expansion
    ON curation_runs(curation_id)
    WHERE kind='EXPANSION'
      AND status IN ('REQUESTED', 'MATERIALIZING');

-- plan_id was the original primary key. Preserve the independent immutable
-- shopping_plan_id relation, but make the Curation ID the aggregate identity.
ALTER TABLE curations
    DROP CONSTRAINT IF EXISTS curations_pkey,
    DROP CONSTRAINT IF EXISTS curations_id_unique,
    ADD CONSTRAINT curations_pkey PRIMARY KEY (id);

ALTER TABLE curations
    DROP COLUMN IF EXISTS plan_id,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS closed_at;

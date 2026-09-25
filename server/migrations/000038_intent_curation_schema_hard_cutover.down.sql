-- Restore the expand-period aliases so migration 000032 can be rolled back.
-- Curation archive is the only canonical terminal marker available after the
-- hard cutover, so it is mapped to the historical CLOSED projection.

ALTER TABLE curations
    ADD COLUMN plan_id UUID,
    ADD COLUMN status TEXT DEFAULT 'OPEN',
    ADD COLUMN closed_at TIMESTAMPTZ;

UPDATE curations
SET
    plan_id=shopping_plan_id,
    status=CASE WHEN archived_at IS NULL THEN 'OPEN' ELSE 'CLOSED' END,
    closed_at=archived_at;

ALTER TABLE curations
    ALTER COLUMN plan_id SET NOT NULL,
    ALTER COLUMN status SET NOT NULL,
    ADD CONSTRAINT curations_plan_id_fkey
        FOREIGN KEY (plan_id) REFERENCES shopping_plans(id) ON DELETE CASCADE,
    ADD CONSTRAINT curations_status_check
        CHECK (status IN ('OPEN', 'CLOSED'));

ALTER TABLE curations
    ADD CONSTRAINT curations_id_unique UNIQUE (id);

ALTER TABLE curations
    DROP CONSTRAINT IF EXISTS curations_pkey,
    ADD CONSTRAINT curations_pkey PRIMARY KEY (plan_id);

DROP INDEX IF EXISTS idx_curation_runs_one_open_expansion;
DROP INDEX IF EXISTS idx_curation_runs_curation_created;

ALTER TABLE curation_runs
    ADD COLUMN curation_plan_id UUID;

UPDATE curation_runs AS run
SET curation_plan_id=curation.shopping_plan_id
FROM curations AS curation
WHERE curation.id=run.curation_id
  AND curation.user_id=run.user_id;

ALTER TABLE curation_runs
    ALTER COLUMN curation_plan_id SET NOT NULL,
    ADD CONSTRAINT curation_runs_curation_plan_id_fkey
        FOREIGN KEY (curation_plan_id)
        REFERENCES curations(plan_id) ON DELETE CASCADE;

CREATE INDEX idx_curation_runs_plan_created
    ON curation_runs(curation_plan_id, created_at DESC);

CREATE UNIQUE INDEX idx_curation_runs_one_open_expansion
    ON curation_runs(curation_plan_id)
    WHERE kind='EXPANSION'
      AND status IN ('REQUESTED', 'MATERIALIZING');

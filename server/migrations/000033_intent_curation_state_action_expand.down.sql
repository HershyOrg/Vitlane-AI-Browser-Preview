DROP INDEX IF EXISTS idx_agent_work_batches_curation_action;
ALTER TABLE agent_work_batches
    DROP CONSTRAINT IF EXISTS agent_work_batches_curation_action_fkey,
    DROP COLUMN IF EXISTS curation_action_id;

DROP INDEX IF EXISTS idx_curation_actions_timeline;
DROP TABLE IF EXISTS curation_actions;

DROP INDEX IF EXISTS idx_curation_runs_one_open_expansion;
ALTER TABLE curation_runs
    DROP CONSTRAINT IF EXISTS curation_runs_status_check,
    ADD CONSTRAINT curation_runs_status_check CHECK (
        status IN (
            'REQUESTED', 'MATERIALIZING', 'RESEARCH_READY',
            'RESEARCHING', 'COMPLETED', 'FAILED', 'CANCELLED'
        )
    );
CREATE UNIQUE INDEX idx_curation_runs_one_open_expansion
    ON curation_runs(curation_plan_id)
    WHERE kind='EXPANSION'
      AND status IN (
          'REQUESTED', 'MATERIALIZING', 'RESEARCH_READY', 'RESEARCHING'
      );

ALTER TABLE curation_runs
    DROP CONSTRAINT IF EXISTS curation_runs_user_curation_fkey,
    DROP COLUMN IF EXISTS curation_id;

UPDATE shopping_sessions
SET target_snapshot=target_snapshot - 'curationId';

DROP INDEX IF EXISTS idx_plan_targets_curation_order;
ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_user_id_curation_unique,
    DROP CONSTRAINT IF EXISTS plan_targets_user_id_unique,
    DROP CONSTRAINT IF EXISTS plan_targets_user_curation_fkey,
    DROP CONSTRAINT IF EXISTS plan_targets_user_fkey,
    DROP COLUMN IF EXISTS curation_id,
    DROP COLUMN IF EXISTS user_id;

ALTER TABLE curations
    DROP CONSTRAINT IF EXISTS curations_archive_audit_check,
    DROP CONSTRAINT IF EXISTS curations_phase_check,
    DROP CONSTRAINT IF EXISTS curations_shopping_plan_fkey,
    DROP CONSTRAINT IF EXISTS curations_user_shopping_plan_unique,
    DROP CONSTRAINT IF EXISTS curations_shopping_plan_unique,
    DROP CONSTRAINT IF EXISTS curations_user_id_id_unique,
    DROP CONSTRAINT IF EXISTS curations_id_user_unique,
    DROP CONSTRAINT IF EXISTS curations_id_unique,
    DROP COLUMN IF EXISTS archived_by_user_id,
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS phase,
    DROP COLUMN IF EXISTS shopping_plan_id,
    DROP COLUMN IF EXISTS id;

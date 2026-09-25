-- Structural rollback. Intelligence job rows are not preserved: this migration
-- only ever ran before the intelligence path carried production history.

ALTER TABLE managed_runner_reservations
    ADD CONSTRAINT managed_runner_reservations_attempt_id_fkey
        FOREIGN KEY (attempt_id)
        REFERENCES agent_work_attempts(id) ON DELETE SET NULL;

ALTER TABLE curation_actions
    DROP CONSTRAINT curation_actions_initiator_check,
    ADD CONSTRAINT curation_actions_initiator_check
        CHECK (initiator IN ('USER', 'MANAGED_RUNNER'));

DROP INDEX IF EXISTS research_catalog_observations_intelligence_access_idx;
ALTER TABLE research_catalog_observations
    DROP CONSTRAINT research_catalog_observations_provenance_check,
    DROP CONSTRAINT research_catalog_observations_intelligence_job_fkey,
    DROP COLUMN intelligence_job_id,
    ADD CONSTRAINT research_catalog_observations_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

DROP INDEX IF EXISTS research_submissions_intelligence_idempotency_idx;
ALTER TABLE research_submissions
    DROP CONSTRAINT research_submissions_provenance_check,
    DROP CONSTRAINT research_submissions_intelligence_job_fkey,
    DROP COLUMN intelligence_job_id,
    ADD CONSTRAINT research_submissions_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

DROP INDEX IF EXISTS planning_proposals_intelligence_idempotency_idx;
ALTER TABLE planning_proposals
    DROP CONSTRAINT planning_proposals_provenance_check,
    DROP CONSTRAINT planning_proposals_intelligence_job_fkey,
    DROP COLUMN intelligence_job_id,
    ADD CONSTRAINT planning_proposals_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

DROP TABLE IF EXISTS intelligence_steps;
DROP TABLE IF EXISTS intelligence_attempts;
DROP TABLE IF EXISTS intelligence_jobs;

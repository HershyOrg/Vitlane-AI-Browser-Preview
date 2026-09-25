-- ADR-0038 hard cutover: the external Agent runtime is removed.
--
-- Agent execution and control data is transient orchestration state with no
-- value once the runtime is gone, so it is deleted rather than converted to a
-- terminal status nobody will read. Product results are not in scope: plans,
-- curations, targets, tasks, proposals, rounds, submissions, observations,
-- candidates, selections and purchases are all preserved, and this migration
-- proves it by comparing counts and id hashes before and after.
--
-- This migration is not reversible. The down file recreates structure only.

-- 1. Audit the world before anything is touched.
CREATE TABLE agent_runtime_cutover_audits (
    id UUID PRIMARY KEY,
    executed_at TIMESTAMPTZ NOT NULL,
    deleted_counts JSONB NOT NULL,
    preserved_counts JSONB NOT NULL,
    preserved_hashes JSONB NOT NULL,
    closed_planning_tasks INTEGER NOT NULL,
    closed_research_rounds INTEGER NOT NULL,
    reopened_sessions INTEGER NOT NULL
);

CREATE TEMPORARY TABLE cutover_before ON COMMIT DROP AS
SELECT
    (SELECT jsonb_object_agg(name, total) FROM (
        SELECT 'agent_work_batches' AS name, count(*) AS total FROM agent_work_batches
        UNION ALL SELECT 'agent_work_orders', count(*) FROM agent_work_orders
        UNION ALL SELECT 'agent_work_attempts', count(*) FROM agent_work_attempts
        UNION ALL SELECT 'agent_delegations', count(*) FROM agent_delegations
        UNION ALL SELECT 'agent_command_requests', count(*) FROM agent_command_requests
        UNION ALL SELECT 'agent_connections', count(*) FROM agent_connections
        UNION ALL SELECT 'agent_grants', count(*) FROM agent_grants
        UNION ALL SELECT 'oauth_access_tokens', count(*) FROM oauth_access_tokens
        UNION ALL SELECT 'oauth_refresh_tokens', count(*) FROM oauth_refresh_tokens
        UNION ALL SELECT 'managed_runner_steps', count(*) FROM managed_runner_steps
    ) AS deleted) AS deleted_counts,
    (SELECT jsonb_object_agg(name, total) FROM (
        SELECT 'shopping_plans' AS name, count(*) AS total FROM shopping_plans
        UNION ALL SELECT 'curations', count(*) FROM curations
        UNION ALL SELECT 'curation_actions', count(*) FROM curation_actions
        UNION ALL SELECT 'plan_targets', count(*) FROM plan_targets
        UNION ALL SELECT 'planning_tasks', count(*) FROM planning_tasks
        UNION ALL SELECT 'planning_proposals', count(*) FROM planning_proposals
        UNION ALL SELECT 'shopping_sessions', count(*) FROM shopping_sessions
        UNION ALL SELECT 'research_rounds', count(*) FROM research_rounds
        UNION ALL SELECT 'research_submissions', count(*) FROM research_submissions
        UNION ALL SELECT 'research_catalog_observations', count(*) FROM research_catalog_observations
        UNION ALL SELECT 'candidates', count(*) FROM candidates
        UNION ALL SELECT 'curation_selections', count(*) FROM curation_selections
        UNION ALL SELECT 'purchases', count(*) FROM purchases
        UNION ALL SELECT 'managed_runner_usage_daily', count(*) FROM managed_runner_usage_daily
        UNION ALL SELECT 'managed_runner_reservations', count(*) FROM managed_runner_reservations
        UNION ALL SELECT 'oauth_login_attempts', count(*) FROM oauth_login_attempts
    ) AS preserved) AS preserved_counts,
    (SELECT jsonb_object_agg(name, digest) FROM (
        SELECT 'planning_proposals' AS name,
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), '')) AS digest
        FROM planning_proposals
        UNION ALL SELECT 'research_submissions',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM research_submissions
        UNION ALL SELECT 'research_catalog_observations',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM research_catalog_observations
        UNION ALL SELECT 'candidates',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM candidates
        UNION ALL SELECT 'curation_selections',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM curation_selections
        UNION ALL SELECT 'purchases',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM purchases
        UNION ALL SELECT 'managed_runner_reservations',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM managed_runner_reservations
    ) AS hashes) AS preserved_hashes;

-- 2. Close the product work the retired runtime would have executed.
--
-- This is a product-owned transition, not a conversion of agent data: the task
-- and the round are Curation's and Research's own rows, and leaving them
-- REQUESTED forever would strand the Target with no way back for the user. The
-- transition mirrors the application's own cancel path.
CREATE TEMPORARY TABLE cutover_closed_tasks ON COMMIT DROP AS
WITH closed AS (
    UPDATE planning_tasks
    SET status = 'CANCELLED', completed_at = now(), updated_at = now()
    WHERE status = 'REQUESTED'
      AND plan_id IN (
          SELECT id FROM shopping_plans WHERE agent_mode = 'EXTERNAL'
      )
    RETURNING id
)
SELECT id FROM closed;

UPDATE curation_runs
SET status = 'CANCELLED', updated_at = now()
WHERE status IN ('REQUESTED', 'MATERIALIZING')
  AND planning_task_id IN (SELECT id FROM cutover_closed_tasks);

CREATE TEMPORARY TABLE cutover_closed_rounds ON COMMIT DROP AS
WITH closed AS (
    UPDATE research_rounds
    SET status = 'CANCELLED', completed_at = now()
    WHERE status = 'REQUESTED'
      AND shopping_session_id IN (
          SELECT s.id FROM shopping_sessions s
          JOIN plan_targets t ON t.id = s.plan_target_id
          JOIN shopping_plans p ON p.id = t.plan_id
          WHERE p.agent_mode = 'EXTERNAL'
      )
    RETURNING id, shopping_session_id
)
SELECT id, shopping_session_id FROM closed;

CREATE TEMPORARY TABLE cutover_reopened_sessions ON COMMIT DROP AS
WITH reopened AS (
    UPDATE shopping_sessions
    SET status = 'REVIEWING', current_research_round_id = NULL,
        version = version + 1, updated_at = now()
    WHERE status = 'RESEARCHING'
      AND id IN (SELECT shopping_session_id FROM cutover_closed_rounds)
    RETURNING id
)
SELECT id FROM reopened;

-- 3. Detach every inbound reference from a preserved table, one constraint at
--    a time. CASCADE is never used: a dependency this migration did not
--    anticipate must fail loudly instead of silently taking product data.
--    The legacy provenance id columns keep their values as historical record;
--    only the constraints go.
ALTER TABLE planning_proposals
    DROP CONSTRAINT planning_proposals_agent_grant_id_fkey,
    DROP CONSTRAINT planning_proposals_agent_work_order_fkey;
ALTER TABLE research_submissions
    DROP CONSTRAINT research_submissions_agent_grant_id_fkey,
    DROP CONSTRAINT research_submissions_agent_work_order_fkey;
ALTER TABLE research_catalog_observations
    DROP CONSTRAINT research_catalog_observations_agent_grant_id_fkey,
    DROP CONSTRAINT research_catalog_observations_agent_work_order_fkey;

-- current_agent_grant_id is agent bookkeeping rather than product history, so
-- the column goes with the constraint.
ALTER TABLE planning_tasks
    DROP CONSTRAINT planning_tasks_current_agent_grant_id_fkey,
    DROP COLUMN current_agent_grant_id;
ALTER TABLE research_rounds
    DROP CONSTRAINT research_rounds_current_agent_grant_id_fkey,
    DROP COLUMN current_agent_grant_id;

-- 4. Drop the retired runtime, children before parents.
--
-- managed_runner_steps belongs here because intelligence_steps replaced it;
-- managed_runner_usage_daily and managed_runner_reservations do not, because
-- they are the spend record and outlive the runtime that produced it.
DROP TABLE managed_runner_steps;
DROP TABLE agent_connection_acquisition_intents;
-- An order points at its active attempt and an attempt points back at its
-- order, so the pair can only be dropped together. Delegations reference the
-- order too, so they join the same statement.
DROP TABLE agent_work_attempts, agent_delegations, agent_work_orders;
-- A batch references the command request that authorized it, so the batch goes
-- first even though it reads like the larger aggregate.
DROP TABLE agent_work_batches;
DROP TABLE agent_command_requests;
DROP TABLE agent_grant_sessions;
DROP TABLE agent_grant_requests;
DROP TABLE agent_grants;
DROP TABLE agent_auth_attempt_cancel_requests;
DROP TABLE agent_connection_revoke_commands;
DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
-- Connections, credential families and auth attempts reference each other in a
-- cycle, so they can only be dropped together.
DROP TABLE agent_credential_families, agent_auth_attempts, agent_connections;
DROP TABLE agent_oauth_clients;

-- Two tables are deliberately absent from the list above.
--
-- oauth_login_attempts: despite the name it is the Google browser login's PKCE
-- state, not agent OAuth.
--
-- agent_auth_control_cutover_audits: it is the evidence record of the 000030
-- cutover, not part of the runtime this migration removes. The deploy script's
-- postflight reads it when a database starts below cutover level 3, and a past
-- cutover's evidence outlives the thing it was cutting over — the same reason
-- managed_runner_usage_daily stays.

-- 5. Rename the effect kind that named the retired runtime. The value marks a
-- user command that hands work to AI, which is now an IntelligenceJob.
ALTER TABLE curation_actions
    DROP CONSTRAINT curation_actions_effect_kind_check;
UPDATE curation_actions SET effect_kind='INTELLIGENCE' WHERE effect_kind='AGENT_WORK';
ALTER TABLE curation_actions
    ADD CONSTRAINT curation_actions_effect_kind_check CHECK (
        effect_kind IN ('NONE', 'INTELLIGENCE', 'PURCHASE_THREAD'));

-- 6. Record the audit and refuse to commit if a single preserved row moved.
DO $$
DECLARE
    before_counts JSONB;
    before_hashes JSONB;
    after_counts JSONB;
    after_hashes JSONB;
    deleted JSONB;
    table_name TEXT;
BEGIN
    SELECT deleted_counts, preserved_counts, preserved_hashes
    INTO deleted, before_counts, before_hashes FROM cutover_before;

    SELECT jsonb_object_agg(name, total) INTO after_counts FROM (
        SELECT 'shopping_plans' AS name, count(*) AS total FROM shopping_plans
        UNION ALL SELECT 'curations', count(*) FROM curations
        UNION ALL SELECT 'curation_actions', count(*) FROM curation_actions
        UNION ALL SELECT 'plan_targets', count(*) FROM plan_targets
        UNION ALL SELECT 'planning_tasks', count(*) FROM planning_tasks
        UNION ALL SELECT 'planning_proposals', count(*) FROM planning_proposals
        UNION ALL SELECT 'shopping_sessions', count(*) FROM shopping_sessions
        UNION ALL SELECT 'research_rounds', count(*) FROM research_rounds
        UNION ALL SELECT 'research_submissions', count(*) FROM research_submissions
        UNION ALL SELECT 'research_catalog_observations', count(*) FROM research_catalog_observations
        UNION ALL SELECT 'candidates', count(*) FROM candidates
        UNION ALL SELECT 'curation_selections', count(*) FROM curation_selections
        UNION ALL SELECT 'purchases', count(*) FROM purchases
        UNION ALL SELECT 'managed_runner_usage_daily', count(*) FROM managed_runner_usage_daily
        UNION ALL SELECT 'managed_runner_reservations', count(*) FROM managed_runner_reservations
        UNION ALL SELECT 'oauth_login_attempts', count(*) FROM oauth_login_attempts
    ) AS preserved;

    SELECT jsonb_object_agg(name, digest) INTO after_hashes FROM (
        SELECT 'planning_proposals' AS name,
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), '')) AS digest
        FROM planning_proposals
        UNION ALL SELECT 'research_submissions',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM research_submissions
        UNION ALL SELECT 'research_catalog_observations',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM research_catalog_observations
        UNION ALL SELECT 'candidates',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM candidates
        UNION ALL SELECT 'curation_selections',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM curation_selections
        UNION ALL SELECT 'purchases',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM purchases
        UNION ALL SELECT 'managed_runner_reservations',
               md5(coalesce(string_agg(id::text, ',' ORDER BY id), ''))
        FROM managed_runner_reservations
    ) AS hashes;

    FOR table_name IN SELECT jsonb_object_keys(before_counts) LOOP
        IF (before_counts->>table_name)::bigint
            <> (after_counts->>table_name)::bigint THEN
            RAISE EXCEPTION
                'agent runtime cutover changed preserved table %: % -> %',
                table_name, before_counts->>table_name,
                after_counts->>table_name;
        END IF;
    END LOOP;

    FOR table_name IN SELECT jsonb_object_keys(before_hashes) LOOP
        IF (before_hashes->>table_name)
            IS DISTINCT FROM (after_hashes->>table_name) THEN
            RAISE EXCEPTION
                'agent runtime cutover changed preserved rows in %', table_name;
        END IF;
    END LOOP;

    INSERT INTO agent_runtime_cutover_audits(
        id, executed_at, deleted_counts, preserved_counts, preserved_hashes,
        closed_planning_tasks, closed_research_rounds, reopened_sessions
    ) VALUES (
        gen_random_uuid(), now(), deleted, after_counts, after_hashes,
        (SELECT count(*) FROM cutover_closed_tasks),
        (SELECT count(*) FROM cutover_closed_rounds),
        (SELECT count(*) FROM cutover_reopened_sessions)
    );
END $$;

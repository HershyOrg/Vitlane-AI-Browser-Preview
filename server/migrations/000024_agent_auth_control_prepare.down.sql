-- Roll back only the additive ADR-0024 preparation.  Legacy AgentGrant and
-- OAuth rows were not rewritten by the up migration and remain intact.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM agent_connections
        GROUP BY user_id, oauth_client_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'AGENT_CONNECTION_GENERATIONS_REQUIRE_RECONCILIATION'
            USING
                ERRCODE = '23505',
                CONSTRAINT =
                    'agent_connections_user_id_oauth_client_id_key';
    END IF;
END;
$$;

ALTER TABLE agent_work_orders
    DROP CONSTRAINT IF EXISTS agent_work_orders_active_attempt_fkey;

DROP TABLE IF EXISTS agent_connection_acquisition_intents;
DROP TABLE IF EXISTS agent_work_attempts;
DROP TABLE IF EXISTS agent_delegations;
DROP TABLE IF EXISTS agent_work_orders;
DROP TABLE IF EXISTS agent_work_batches;

DROP TRIGGER IF EXISTS agent_command_requests_immutable_guard
    ON agent_command_requests;
DROP TRIGGER IF EXISTS agent_command_requests_completion_guard
    ON agent_command_requests;
DROP FUNCTION IF EXISTS vitlane_guard_agent_command_request_update();
DROP FUNCTION IF EXISTS vitlane_require_agent_command_request_completion();
DROP TABLE IF EXISTS agent_command_requests;

DROP INDEX IF EXISTS oauth_authorization_codes_credential_family_idx;
ALTER TABLE oauth_authorization_codes
    DROP CONSTRAINT IF EXISTS oauth_authorization_codes_credential_family_fkey,
    DROP CONSTRAINT IF EXISTS oauth_authorization_codes_credential_link_check,
    DROP COLUMN IF EXISTS credential_epoch,
    DROP COLUMN IF EXISTS credential_family_id;

DROP INDEX IF EXISTS oauth_refresh_tokens_family_generation_idx;
ALTER TABLE oauth_refresh_tokens
    DROP CONSTRAINT IF EXISTS oauth_refresh_tokens_credential_family_fkey,
    DROP CONSTRAINT IF EXISTS oauth_refresh_tokens_credential_link_check,
    DROP COLUMN IF EXISTS refresh_generation,
    DROP COLUMN IF EXISTS credential_epoch,
    DROP COLUMN IF EXISTS credential_family_id;

DROP INDEX IF EXISTS oauth_access_tokens_credential_family_idx;
ALTER TABLE oauth_access_tokens
    DROP CONSTRAINT IF EXISTS oauth_access_tokens_credential_family_fkey,
    DROP CONSTRAINT IF EXISTS oauth_access_tokens_credential_link_check,
    DROP COLUMN IF EXISTS credential_epoch,
    DROP COLUMN IF EXISTS credential_family_id;

ALTER TABLE agent_connections
    DROP CONSTRAINT IF EXISTS agent_connections_current_family_fkey;

DROP TABLE IF EXISTS agent_credential_families;

ALTER TABLE agent_connections
    DROP CONSTRAINT IF EXISTS agent_connections_user_generation_key,
    DROP CONSTRAINT IF EXISTS agent_connections_id_user_generation_key,
    DROP CONSTRAINT IF EXISTS agent_connections_id_user_key,
    DROP CONSTRAINT IF EXISTS agent_connections_version_check,
    DROP CONSTRAINT IF EXISTS agent_connections_managed_credential_check,
    DROP CONSTRAINT IF EXISTS agent_connections_current_family_check,
    DROP CONSTRAINT IF EXISTS agent_connections_credential_status_check,
    DROP CONSTRAINT IF EXISTS agent_connections_issuer_check,
    DROP CONSTRAINT IF EXISTS agent_connections_resource_check,
    DROP CONSTRAINT IF EXISTS agent_connections_generation_check,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS current_credential_epoch,
    DROP COLUMN IF EXISTS current_credential_family_id,
    DROP COLUMN IF EXISTS credential_status,
    DROP COLUMN IF EXISTS issuer,
    DROP COLUMN IF EXISTS resource,
    DROP COLUMN IF EXISTS connection_generation;

ALTER TABLE agent_connections
    ADD CONSTRAINT agent_connections_user_id_oauth_client_id_key
        UNIQUE (user_id, oauth_client_id);

ALTER TABLE research_submissions
    DROP CONSTRAINT IF EXISTS research_submissions_result_identity_key;

ALTER TABLE planning_proposals
    DROP CONSTRAINT IF EXISTS planning_proposals_result_identity_key;

ALTER TABLE research_rounds
    DROP CONSTRAINT IF EXISTS research_rounds_id_user_key;

ALTER TABLE planning_tasks
    DROP CONSTRAINT IF EXISTS planning_tasks_id_user_key;

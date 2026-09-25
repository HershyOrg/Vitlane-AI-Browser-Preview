DROP TABLE IF EXISTS managed_runner_reservations;
DROP TABLE IF EXISTS managed_runner_usage_daily;
DROP TABLE IF EXISTS managed_runner_steps;

DROP INDEX IF EXISTS agent_command_requests_managed_idempotency_idx;

ALTER TABLE agent_command_requests
    DROP CONSTRAINT IF EXISTS agent_command_requests_actor_kind_check,
    DROP CONSTRAINT IF EXISTS agent_command_requests_actor_check;

ALTER TABLE agent_command_requests
    ADD CONSTRAINT agent_command_requests_actor_kind_check CHECK (
        actor_kind IN ('WEB_USER', 'AGENT_PRINCIPAL')
    ),
    ADD CONSTRAINT agent_command_requests_actor_check CHECK (
        (
            actor_kind = 'WEB_USER'
            AND actor_connection_id IS NULL
            AND actor_credential_family_id IS NULL
            AND actor_credential_epoch IS NULL
        )
        OR
        (
            actor_kind = 'AGENT_PRINCIPAL'
            AND actor_auth_session_id IS NULL
            AND actor_connection_id IS NOT NULL
            AND actor_credential_family_id IS NOT NULL
            AND actor_credential_epoch IS NOT NULL
            AND actor_credential_epoch > 0
        )
    );

ALTER TABLE curation_actions
    DROP CONSTRAINT IF EXISTS curation_actions_initiator_check,
    DROP COLUMN IF EXISTS initiator;

ALTER TABLE agent_work_attempts
    DROP CONSTRAINT IF EXISTS agent_work_attempts_runner_id_check,
    DROP CONSTRAINT IF EXISTS agent_work_attempts_claimed_check,
    DROP CONSTRAINT IF EXISTS agent_work_attempts_offer_check;

ALTER TABLE agent_work_attempts
    DROP COLUMN IF EXISTS runner_id;

ALTER TABLE agent_work_attempts
    ADD CONSTRAINT agent_work_attempts_offer_check CHECK (
        status <> 'OFFERED'
        OR (
            claimed_connection_id IS NULL
            AND claim_handle_hash IS NULL
            AND claim_ack_deadline_at IS NULL
            AND activity_deadline_at IS NULL
            AND last_activity_kind IS NULL
            AND last_activity_at IS NULL
        )
    ),
    ADD CONSTRAINT agent_work_attempts_claimed_check CHECK (
        status NOT IN ('CLAIMED', 'RUNNING')
        OR (
            claimed_connection_id IS NOT NULL
            AND claim_handle_hash IS NOT NULL
            AND claim_ack_deadline_at IS NOT NULL
            AND last_activity_kind IS NOT NULL
            AND btrim(last_activity_kind) <> ''
            AND last_activity_at IS NOT NULL
        )
    );

ALTER TABLE agent_delegations
    DROP CONSTRAINT IF EXISTS agent_delegations_assignee_mode_check,
    DROP CONSTRAINT IF EXISTS agent_delegations_assignee_state_check;

ALTER TABLE agent_delegations
    ADD CONSTRAINT agent_delegations_assignee_mode_check CHECK (
        assignee_mode IN ('SPECIFIC_CONNECTION', 'CLAIMING_CONNECTION')
    ),
    ADD CONSTRAINT agent_delegations_assignee_state_check CHECK (
        (
            assignee_mode = 'CLAIMING_CONNECTION'
            AND status = 'PENDING_CLAIM'
            AND assignee_connection_id IS NULL
        )
        OR
        (
            assignee_mode = 'CLAIMING_CONNECTION'
            AND status = 'ACTIVE'
            AND assignee_connection_id IS NOT NULL
        )
        OR
        (
            assignee_mode = 'SPECIFIC_CONNECTION'
            AND assignee_connection_id IS NOT NULL
        )
        OR status IN ('REVOKED', 'EXPIRED')
    );

DROP INDEX IF EXISTS agent_work_batches_managed_status_idx;

ALTER TABLE agent_work_batches
    DROP CONSTRAINT IF EXISTS agent_work_batches_managed_activation_check,
    DROP CONSTRAINT IF EXISTS agent_work_batches_open_activation_check,
    DROP CONSTRAINT IF EXISTS agent_work_batches_agent_mode_check,
    DROP COLUMN IF EXISTS agent_mode;

ALTER TABLE agent_work_batches
    ADD CONSTRAINT agent_work_batches_open_activation_check CHECK (
        status NOT IN ('OPEN', 'PARTIAL')
        OR activation_ref_hash IS NOT NULL
    );

ALTER TABLE shopping_plans
    DROP CONSTRAINT IF EXISTS shopping_plans_model_key_check,
    DROP CONSTRAINT IF EXISTS shopping_plans_agent_mode_check,
    DROP COLUMN IF EXISTS model_key,
    DROP COLUMN IF EXISTS agent_mode;

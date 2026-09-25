-- ADR-0032 Managed Agent Runner.
--
-- The ManagedRunner is an in-process agent client. It reuses the ADR-0024
-- Batch/WorkOrder/Delegation/Attempt state machine and differs only in how it
-- authenticates: no OAuth connection, no activation ref deep link. Existing
-- rows keep the EXTERNAL behaviour, so this is an additive expand migration.

-- ShoppingPlan owns the immutable AgentMode and model choice snapshot.
ALTER TABLE shopping_plans
    ADD COLUMN agent_mode TEXT NOT NULL DEFAULT 'EXTERNAL',
    ADD COLUMN model_key TEXT;

ALTER TABLE shopping_plans
    ADD CONSTRAINT shopping_plans_agent_mode_check
        CHECK (agent_mode IN ('MANAGED', 'EXTERNAL')),
    ADD CONSTRAINT shopping_plans_model_key_check
        CHECK (
            (agent_mode = 'MANAGED') = (model_key IS NOT NULL)
            AND (model_key IS NULL OR btrim(model_key) <> '')
        );

-- The claim predicate needs the mode without joining back to the plan.
ALTER TABLE agent_work_batches
    ADD COLUMN agent_mode TEXT NOT NULL DEFAULT 'EXTERNAL';

ALTER TABLE agent_work_batches
    ADD CONSTRAINT agent_work_batches_agent_mode_check
        CHECK (agent_mode IN ('MANAGED', 'EXTERNAL'));

-- MANAGED batches never mint an activation ref because there is no deep link
-- to route. EXTERNAL keeps the original all-or-none requirement.
ALTER TABLE agent_work_batches
    DROP CONSTRAINT agent_work_batches_open_activation_check;

ALTER TABLE agent_work_batches
    ADD CONSTRAINT agent_work_batches_open_activation_check CHECK (
        agent_mode = 'MANAGED'
        OR status NOT IN ('OPEN', 'PARTIAL')
        OR activation_ref_hash IS NOT NULL
    ),
    ADD CONSTRAINT agent_work_batches_managed_activation_check CHECK (
        agent_mode <> 'MANAGED'
        OR (
            activation_ref_ciphertext IS NULL
            AND activation_ref_hash IS NULL
            AND secret_key_version IS NULL
            AND activation_expires_at IS NULL
        )
    );

CREATE INDEX agent_work_batches_managed_status_idx
    ON agent_work_batches(status, created_at)
    WHERE agent_mode = 'MANAGED';

-- MANAGED_RUNNER delegations are bound to the server itself, never to an
-- AgentConnection, so assignee_connection_id must stay NULL for them.
ALTER TABLE agent_delegations
    DROP CONSTRAINT agent_delegations_assignee_mode_check,
    DROP CONSTRAINT agent_delegations_assignee_state_check;

ALTER TABLE agent_delegations
    ADD CONSTRAINT agent_delegations_assignee_mode_check CHECK (
        assignee_mode IN (
            'SPECIFIC_CONNECTION', 'CLAIMING_CONNECTION', 'MANAGED_RUNNER'
        )
    ),
    ADD CONSTRAINT agent_delegations_assignee_state_check CHECK (
        (
            assignee_mode = 'MANAGED_RUNNER'
            AND assignee_connection_id IS NULL
        )
        OR
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

-- A claimed MANAGED attempt records the runner identity where an EXTERNAL
-- attempt records the bound connection. Exactly one of the two is present.
ALTER TABLE agent_work_attempts
    ADD COLUMN runner_id TEXT;

ALTER TABLE agent_work_attempts
    DROP CONSTRAINT agent_work_attempts_offer_check,
    DROP CONSTRAINT agent_work_attempts_claimed_check;

ALTER TABLE agent_work_attempts
    ADD CONSTRAINT agent_work_attempts_offer_check CHECK (
        status <> 'OFFERED'
        OR (
            claimed_connection_id IS NULL
            AND runner_id IS NULL
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
            (
                (claimed_connection_id IS NOT NULL AND runner_id IS NULL)
                OR (claimed_connection_id IS NULL AND runner_id IS NOT NULL)
            )
            AND claim_handle_hash IS NOT NULL
            AND claim_ack_deadline_at IS NOT NULL
            AND last_activity_kind IS NOT NULL
            AND btrim(last_activity_kind) <> ''
            AND last_activity_at IS NOT NULL
        )
    ),
    ADD CONSTRAINT agent_work_attempts_runner_id_check CHECK (
        runner_id IS NULL OR btrim(runner_id) <> ''
    );

-- The ManagedRunner issues commands on the user's behalf after their HTTP
-- request has already finished, so it has no AuthSession to record. Its
-- authority is the claimed MANAGED WorkOrder that reached the code, which is a
-- third kind of actor rather than a browser user with a missing session.
ALTER TABLE agent_command_requests
    DROP CONSTRAINT agent_command_requests_actor_kind_check,
    DROP CONSTRAINT agent_command_requests_actor_check;

ALTER TABLE agent_command_requests
    ADD CONSTRAINT agent_command_requests_actor_kind_check CHECK (
        actor_kind IN ('WEB_USER', 'AGENT_PRINCIPAL', 'MANAGED_RUNNER')
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
        OR
        (
            actor_kind = 'MANAGED_RUNNER'
            AND actor_auth_session_id IS NULL
            AND actor_connection_id IS NULL
            AND actor_credential_family_id IS NULL
            AND actor_credential_epoch IS NULL
        )
    );

CREATE UNIQUE INDEX agent_command_requests_managed_idempotency_idx
    ON agent_command_requests(actor_user_id, command, idempotency_key)
    WHERE actor_kind = 'MANAGED_RUNNER';

-- ADR-0032 section 8: MANAGED plans advance to CURATING without a second user
-- action. The audit envelope still exists; only the initiator differs.
ALTER TABLE curation_actions
    ADD COLUMN initiator TEXT NOT NULL DEFAULT 'USER';

ALTER TABLE curation_actions
    ADD CONSTRAINT curation_actions_initiator_check
        CHECK (initiator IN ('USER', 'MANAGED_RUNNER'));

-- Append-only observation of what the runner is doing. Safe labels and reason
-- codes only: no prompt text, no intent text, no model output, no PII.
CREATE TABLE managed_runner_steps (
    id UUID PRIMARY KEY,
    work_order_id UUID NOT NULL
        REFERENCES agent_work_orders(id) ON DELETE CASCADE,
    attempt_id UUID NOT NULL
        REFERENCES agent_work_attempts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    kind TEXT NOT NULL CHECK (
        kind IN (
            'INTERPRETING', 'SEARCHING_CATALOG', 'RANKING', 'SUBMITTING'
        )
    ),
    status TEXT NOT NULL CHECK (
        status IN ('RUNNING', 'SUCCEEDED', 'FAILED')
    ),
    reason_code TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT managed_runner_steps_completion_check CHECK (
        (status = 'RUNNING' AND completed_at IS NULL)
        OR (
            status IN ('SUCCEEDED', 'FAILED')
            AND completed_at IS NOT NULL
            AND completed_at >= started_at
        )
    ),
    CONSTRAINT managed_runner_steps_attempt_ordinal_key
        UNIQUE (attempt_id, ordinal)
);

CREATE INDEX managed_runner_steps_order_idx
    ON managed_runner_steps(work_order_id, attempt_id, ordinal);

-- Daily cost ledger. SERVER rows use a fixed scope_id so the same primary key
-- covers both scopes and both rows can be locked in one deterministic order.
CREATE TABLE managed_runner_usage_daily (
    usage_date DATE NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('SERVER', 'USER')),
    scope_id TEXT NOT NULL CHECK (btrim(scope_id) <> ''),
    reserved_micros BIGINT NOT NULL DEFAULT 0 CHECK (reserved_micros >= 0),
    settled_micros BIGINT NOT NULL DEFAULT 0 CHECK (settled_micros >= 0),
    request_count BIGINT NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (usage_date, scope, scope_id),
    CONSTRAINT managed_runner_usage_daily_server_scope_check CHECK (
        scope <> 'SERVER' OR scope_id = 'SERVER'
    )
);

-- One HELD row per model call. A crashed process leaves the reservation behind
-- and the reconciler releases it after expires_at instead of leaking budget.
CREATE TABLE managed_runner_reservations (
    id UUID PRIMARY KEY,
    usage_date DATE NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The attempt link is diagnostic ("which work spent this?"), not part of
    -- the reservation's identity. Budget accounting must not depend on it, so
    -- it is nullable and ON DELETE SET NULL rather than CASCADE: deleting an
    -- attempt must never silently erase spend the ledger already counted.
    attempt_id UUID
        REFERENCES agent_work_attempts(id) ON DELETE SET NULL,
    amount_micros BIGINT NOT NULL CHECK (amount_micros > 0),
    status TEXT NOT NULL CHECK (status IN ('HELD', 'SETTLED', 'RELEASED')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT managed_runner_reservations_completion_check CHECK (
        (status = 'HELD' AND completed_at IS NULL)
        OR (status IN ('SETTLED', 'RELEASED') AND completed_at IS NOT NULL)
    ),
    CONSTRAINT managed_runner_reservations_deadline_check
        CHECK (expires_at > created_at)
);

CREATE INDEX managed_runner_reservations_held_idx
    ON managed_runner_reservations(expires_at, id)
    WHERE status = 'HELD';

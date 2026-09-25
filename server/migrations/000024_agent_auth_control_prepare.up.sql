-- ADR-0024 additive preparation.
--
-- This migration deliberately keeps the Phase 4.5 AgentGrant and OAuth
-- representation alive. Existing application images may continue to insert
-- rows without the new credential-family columns while the AgentControl
-- repositories are introduced. The old one-Connection-per-client uniqueness is
-- released because a new authorization must allocate a distinct generation.
-- A later, separately verified cutover can make the nullable compatibility
-- columns mandatory and remove AgentGrant.

ALTER TABLE agent_connections
    DROP CONSTRAINT agent_connections_user_id_oauth_client_id_key;

ALTER TABLE agent_connections
    ADD COLUMN connection_generation BIGINT,
    ADD COLUMN resource TEXT,
    ADD COLUMN issuer TEXT,
    ADD COLUMN credential_status TEXT,
    ADD COLUMN current_credential_family_id UUID,
    ADD COLUMN current_credential_epoch BIGINT,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT agent_connections_generation_check
        CHECK (connection_generation IS NULL OR connection_generation > 0),
    ADD CONSTRAINT agent_connections_resource_check
        CHECK (resource IS NULL OR btrim(resource) <> ''),
    ADD CONSTRAINT agent_connections_issuer_check
        CHECK (issuer IS NULL OR btrim(issuer) <> ''),
    ADD CONSTRAINT agent_connections_credential_status_check
        CHECK (
            credential_status IS NULL
            OR credential_status IN (
                'UNCONFIRMED', 'HEALTHY', 'REAUTH_REQUIRED', 'REVOKED'
            )
        ),
    ADD CONSTRAINT agent_connections_current_family_check
        CHECK (
            (
                current_credential_family_id IS NULL
                AND current_credential_epoch IS NULL
            )
            OR
            (
                current_credential_family_id IS NOT NULL
                AND current_credential_epoch IS NOT NULL
                AND current_credential_epoch > 0
            )
        ),
    ADD CONSTRAINT agent_connections_managed_credential_check
        CHECK (
            credential_status IS NULL
            OR (
                connection_generation IS NOT NULL
                AND resource IS NOT NULL
                AND issuer IS NOT NULL
                AND (
                    credential_status NOT IN ('UNCONFIRMED', 'HEALTHY')
                    OR current_credential_family_id IS NOT NULL
                )
                AND (
                    credential_status <> 'REVOKED'
                    OR status = 'REVOKED'
                )
            )
        ),
    ADD CONSTRAINT agent_connections_version_check CHECK (version > 0),
    ADD CONSTRAINT agent_connections_id_user_key UNIQUE (id, user_id),
    ADD CONSTRAINT agent_connections_id_user_generation_key
        UNIQUE (id, user_id, connection_generation),
    ADD CONSTRAINT agent_connections_user_generation_key
        UNIQUE (user_id, connection_generation);

CREATE TABLE agent_credential_families (
    id UUID PRIMARY KEY,
    connection_id UUID NOT NULL
        REFERENCES agent_connections(id) ON DELETE CASCADE,
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    status TEXT NOT NULL CHECK (
        status IN (
            'UNCONFIRMED', 'ACTIVE', 'REVOKED', 'COMPROMISED', 'EXPIRED'
        )
    ),
    refresh_generation BIGINT NOT NULL DEFAULT 0
        CHECK (refresh_generation >= 0),
    confirmation_deadline_at TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    inactivity_expires_at TIMESTAMPTZ,
    absolute_expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT agent_credential_families_state_check CHECK (
        (
            status = 'UNCONFIRMED'
            AND confirmation_deadline_at IS NOT NULL
            AND confirmed_at IS NULL
            AND revoked_at IS NULL
        )
        OR
        (
            status = 'ACTIVE'
            AND confirmed_at IS NOT NULL
            AND revoked_at IS NULL
        )
        OR
        (
            status IN ('REVOKED', 'COMPROMISED', 'EXPIRED')
            AND revoked_at IS NOT NULL
        )
    ),
    CONSTRAINT agent_credential_families_deadline_check CHECK (
        (confirmation_deadline_at IS NULL OR confirmation_deadline_at > created_at)
        AND (confirmed_at IS NULL OR confirmed_at >= created_at)
        AND (inactivity_expires_at IS NULL OR inactivity_expires_at > created_at)
        AND (absolute_expires_at IS NULL OR absolute_expires_at > created_at)
        AND (
            inactivity_expires_at IS NULL
            OR absolute_expires_at IS NULL
            OR inactivity_expires_at <= absolute_expires_at
        )
        AND (revoked_at IS NULL OR revoked_at >= created_at)
        AND updated_at >= created_at
    ),
    CONSTRAINT agent_credential_families_connection_epoch_key
        UNIQUE (connection_id, epoch),
    CONSTRAINT agent_credential_families_identity_key
        UNIQUE (id, connection_id, epoch)
);

CREATE UNIQUE INDEX agent_credential_families_current_idx
    ON agent_credential_families(connection_id)
    WHERE status IN ('UNCONFIRMED', 'ACTIVE');

ALTER TABLE agent_connections
    ADD CONSTRAINT agent_connections_current_family_fkey
        FOREIGN KEY (
            current_credential_family_id,
            id,
            current_credential_epoch
        )
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE oauth_authorization_codes
    ADD COLUMN credential_family_id UUID,
    ADD COLUMN credential_epoch BIGINT,
    ADD CONSTRAINT oauth_authorization_codes_credential_link_check CHECK (
        (
            credential_family_id IS NULL
            AND credential_epoch IS NULL
        )
        OR
        (
            credential_family_id IS NOT NULL
            AND credential_epoch IS NOT NULL
            AND credential_epoch > 0
        )
    ),
    ADD CONSTRAINT oauth_authorization_codes_credential_family_fkey
        FOREIGN KEY (credential_family_id, connection_id, credential_epoch)
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX oauth_authorization_codes_credential_family_idx
    ON oauth_authorization_codes(
        credential_family_id, credential_epoch, expires_at
    )
    WHERE credential_family_id IS NOT NULL AND consumed_at IS NULL;

ALTER TABLE oauth_access_tokens
    ADD COLUMN credential_family_id UUID,
    ADD COLUMN credential_epoch BIGINT,
    ADD CONSTRAINT oauth_access_tokens_credential_link_check CHECK (
        (
            credential_family_id IS NULL
            AND credential_epoch IS NULL
        )
        OR
        (
            credential_family_id IS NOT NULL
            AND credential_epoch IS NOT NULL
            AND credential_epoch > 0
        )
    ),
    ADD CONSTRAINT oauth_access_tokens_credential_family_fkey
        FOREIGN KEY (credential_family_id, connection_id, credential_epoch)
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX oauth_access_tokens_credential_family_idx
    ON oauth_access_tokens(credential_family_id, credential_epoch, expires_at)
    WHERE credential_family_id IS NOT NULL AND revoked_at IS NULL;

ALTER TABLE oauth_refresh_tokens
    ADD COLUMN credential_family_id UUID,
    ADD COLUMN credential_epoch BIGINT,
    ADD COLUMN refresh_generation BIGINT,
    ADD CONSTRAINT oauth_refresh_tokens_credential_link_check CHECK (
        (
            credential_family_id IS NULL
            AND credential_epoch IS NULL
            AND refresh_generation IS NULL
        )
        OR
        (
            credential_family_id IS NOT NULL
            AND credential_epoch IS NOT NULL
            AND credential_epoch > 0
            AND refresh_generation IS NOT NULL
            AND refresh_generation > 0
            AND token_family_id = credential_family_id
        )
    ),
    ADD CONSTRAINT oauth_refresh_tokens_credential_family_fkey
        FOREIGN KEY (credential_family_id, connection_id, credential_epoch)
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX oauth_refresh_tokens_family_generation_idx
    ON oauth_refresh_tokens(credential_family_id, credential_epoch, refresh_generation)
    WHERE credential_family_id IS NOT NULL;

ALTER TABLE planning_tasks
    ADD CONSTRAINT planning_tasks_id_user_key UNIQUE (id, user_id);

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_id_user_key UNIQUE (id, user_id);

ALTER TABLE planning_proposals
    ADD CONSTRAINT planning_proposals_result_identity_key
        UNIQUE (id, task_id, user_id);

ALTER TABLE research_submissions
    ADD CONSTRAINT research_submissions_result_identity_key
        UNIQUE (id, research_round_id);

CREATE TABLE agent_command_requests (
    id UUID PRIMARY KEY,
    actor_kind TEXT NOT NULL
        CHECK (actor_kind IN ('WEB_USER', 'AGENT_PRINCIPAL')),
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Audit snapshot only. A deleted/expired AuthSession must not rewrite an
    -- immutable command actor identity; replay re-authenticates separately.
    actor_auth_session_id UUID,
    actor_connection_id UUID,
    actor_credential_family_id UUID,
    actor_credential_epoch BIGINT,
    command TEXT NOT NULL CHECK (
        btrim(command) <> '' AND octet_length(command) <= 200
    ),
    idempotency_key TEXT NOT NULL CHECK (
        btrim(idempotency_key) <> '' AND octet_length(idempotency_key) <= 200
    ),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    subject_kind TEXT,
    subject_id UUID,
    subject_epoch BIGINT,
    subject_generation BIGINT,
    response_status INTEGER,
    response_headers_snapshot JSONB,
    response_ciphertext BYTEA,
    response_hash BYTEA,
    response_key_version BIGINT,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_command_requests_actor_check CHECK (
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
    ),
    CONSTRAINT agent_command_requests_subject_check CHECK (
        (
            subject_kind IS NULL
            AND subject_id IS NULL
            AND subject_epoch IS NULL
            AND subject_generation IS NULL
        )
        OR
        (
            subject_kind IS NOT NULL
            AND btrim(subject_kind) <> ''
            AND subject_id IS NOT NULL
            AND (subject_epoch IS NULL OR subject_epoch > 0)
            AND (subject_generation IS NULL OR subject_generation > 0)
        )
    ),
    CONSTRAINT agent_command_requests_response_check CHECK (
        (
            response_status IS NULL
            AND response_headers_snapshot IS NULL
            AND response_ciphertext IS NULL
            AND response_hash IS NULL
            AND response_key_version IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            response_status IS NOT NULL
            AND response_status BETWEEN 100 AND 599
            AND response_headers_snapshot IS NOT NULL
            AND jsonb_typeof(response_headers_snapshot) = 'object'
            AND response_ciphertext IS NOT NULL
            AND octet_length(response_ciphertext) > 0
            AND response_hash IS NOT NULL
            AND octet_length(response_hash) = 32
            AND response_key_version IS NOT NULL
            AND response_key_version > 0
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
    ),
    CONSTRAINT agent_command_requests_id_user_key
        UNIQUE (id, actor_user_id),
    CONSTRAINT agent_command_requests_actor_connection_fkey
        FOREIGN KEY (actor_connection_id, actor_user_id)
        REFERENCES agent_connections(id, user_id)
        ON DELETE CASCADE,
    CONSTRAINT agent_command_requests_actor_family_fkey
        FOREIGN KEY (
            actor_credential_family_id,
            actor_connection_id,
            actor_credential_epoch
        )
        REFERENCES agent_credential_families(id, connection_id, epoch)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX agent_command_requests_web_idempotency_idx
    ON agent_command_requests(actor_user_id, command, idempotency_key)
    WHERE actor_kind = 'WEB_USER';

CREATE UNIQUE INDEX agent_command_requests_agent_idempotency_idx
    ON agent_command_requests(
        actor_user_id,
        actor_connection_id,
        actor_credential_family_id,
        actor_credential_epoch,
        command,
        idempotency_key
    )
    WHERE actor_kind = 'AGENT_PRINCIPAL';

CREATE INDEX agent_command_requests_incomplete_idx
    ON agent_command_requests(created_at, id)
    WHERE completed_at IS NULL;

CREATE FUNCTION vitlane_guard_agent_command_request_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.actor_kind,
        NEW.actor_user_id,
        NEW.actor_auth_session_id,
        NEW.actor_connection_id,
        NEW.actor_credential_family_id,
        NEW.actor_credential_epoch,
        NEW.command,
        NEW.idempotency_key,
        NEW.request_hash,
        NEW.subject_kind,
        NEW.subject_id,
        NEW.subject_epoch,
        NEW.subject_generation,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.actor_kind,
        OLD.actor_user_id,
        OLD.actor_auth_session_id,
        OLD.actor_connection_id,
        OLD.actor_credential_family_id,
        OLD.actor_credential_epoch,
        OLD.command,
        OLD.idempotency_key,
        OLD.request_hash,
        OLD.subject_kind,
        OLD.subject_id,
        OLD.subject_epoch,
        OLD.subject_generation,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'AGENT_COMMAND_REQUEST_IDENTITY_IMMUTABLE'
            USING
                ERRCODE = '23514',
                CONSTRAINT = 'agent_command_requests_identity_immutable';
    END IF;

    IF OLD.completed_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'AGENT_COMMAND_REQUEST_RESPONSE_IMMUTABLE'
            USING
                ERRCODE = '23514',
                CONSTRAINT = 'agent_command_requests_response_immutable';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_command_requests_immutable_guard
BEFORE UPDATE ON agent_command_requests
FOR EACH ROW
EXECUTE FUNCTION vitlane_guard_agent_command_request_update();

CREATE FUNCTION vitlane_require_agent_command_request_completion()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM agent_command_requests
        WHERE id = NEW.id
          AND completed_at IS NULL
    ) THEN
        RAISE EXCEPTION 'AGENT_COMMAND_REQUEST_RESPONSE_REQUIRED'
            USING
                ERRCODE = '23514',
                CONSTRAINT = 'agent_command_requests_response_required';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER agent_command_requests_completion_guard
AFTER INSERT OR UPDATE ON agent_command_requests
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION vitlane_require_agent_command_request_completion();

CREATE TABLE agent_work_batches (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_kind TEXT NOT NULL CHECK (work_kind IN ('PLANNING', 'RESEARCH')),
    authorized_by_action_id UUID NOT NULL,
    parent_batch_id UUID,
    derivation_policy_version TEXT,
    scope_snapshot_hash TEXT NOT NULL CHECK (
        scope_snapshot_hash ~ '^[0-9a-f]{64}$'
    ),
    status TEXT NOT NULL CHECK (
        status IN (
            'OPEN', 'PARTIAL', 'SUCCEEDED',
            'CANCELLED', 'FAILED_FINAL', 'EXPIRED'
        )
    ),
    activation_epoch BIGINT NOT NULL CHECK (activation_epoch > 0),
    activation_ref_ciphertext BYTEA,
    activation_ref_hash BYTEA,
    secret_key_version BIGINT,
    activation_expires_at TIMESTAMPTZ,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_work_batches_activation_secret_check CHECK (
        (
            activation_ref_ciphertext IS NULL
            AND activation_ref_hash IS NULL
            AND secret_key_version IS NULL
            AND activation_expires_at IS NULL
        )
        OR
        (
            activation_ref_ciphertext IS NOT NULL
            AND octet_length(activation_ref_ciphertext) > 0
            AND activation_ref_hash IS NOT NULL
            AND octet_length(activation_ref_hash) = 32
            AND secret_key_version IS NOT NULL
            AND secret_key_version > 0
            AND activation_expires_at IS NOT NULL
            AND activation_expires_at > created_at
        )
    ),
    CONSTRAINT agent_work_batches_open_activation_check CHECK (
        status NOT IN ('OPEN', 'PARTIAL')
        OR activation_ref_hash IS NOT NULL
    ),
    CONSTRAINT agent_work_batches_completion_check CHECK (
        (status = 'OPEN' AND completed_at IS NULL)
        OR (
            status = 'PARTIAL'
            AND (completed_at IS NULL OR completed_at >= created_at)
        )
        OR (
            status IN ('SUCCEEDED', 'CANCELLED', 'FAILED_FINAL', 'EXPIRED')
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
    ),
    CONSTRAINT agent_work_batches_timestamps_check
        CHECK (updated_at >= created_at),
    CONSTRAINT agent_work_batches_id_user_key UNIQUE (id, user_id),
    CONSTRAINT agent_work_batches_id_user_kind_key
        UNIQUE (id, user_id, work_kind),
    CONSTRAINT agent_work_batches_action_user_fkey
        FOREIGN KEY (authorized_by_action_id, user_id)
        REFERENCES agent_command_requests(id, actor_user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_batches_parent_user_fkey
        FOREIGN KEY (parent_batch_id, user_id)
        REFERENCES agent_work_batches(id, user_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT agent_work_batches_not_own_parent_check
        CHECK (parent_batch_id IS NULL OR parent_batch_id <> id)
);

CREATE UNIQUE INDEX agent_work_batches_activation_ref_idx
    ON agent_work_batches(activation_ref_hash)
    WHERE activation_ref_hash IS NOT NULL;

CREATE INDEX agent_work_batches_user_status_idx
    ON agent_work_batches(user_id, status, updated_at DESC);

CREATE TABLE agent_work_orders (
    id UUID PRIMARY KEY,
    batch_id UUID NOT NULL,
    user_id UUID NOT NULL,
    work_kind TEXT NOT NULL CHECK (work_kind IN ('PLANNING', 'RESEARCH')),
    planning_task_id UUID,
    research_round_id UUID,
    scope_snapshot_hash TEXT NOT NULL CHECK (
        scope_snapshot_hash ~ '^[0-9a-f]{64}$'
    ),
    status TEXT NOT NULL CHECK (
        status IN ('OPEN', 'SUCCEEDED', 'CANCELLED', 'FAILED_FINAL', 'EXPIRED')
    ),
    active_attempt_id UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    deadline_at TIMESTAMPTZ NOT NULL,
    planning_proposal_id UUID,
    research_submission_id UUID,
    terminal_reason_code TEXT,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_work_orders_target_check CHECK (
        (
            work_kind = 'PLANNING'
            AND planning_task_id IS NOT NULL
            AND research_round_id IS NULL
        )
        OR
        (
            work_kind = 'RESEARCH'
            AND planning_task_id IS NULL
            AND research_round_id IS NOT NULL
        )
    ),
    CONSTRAINT agent_work_orders_result_check CHECK (
        (
            status = 'SUCCEEDED'
            AND (
                (
                    work_kind = 'PLANNING'
                    AND planning_proposal_id IS NOT NULL
                    AND research_submission_id IS NULL
                )
                OR
                (
                    work_kind = 'RESEARCH'
                    AND planning_proposal_id IS NULL
                    AND research_submission_id IS NOT NULL
                )
            )
        )
        OR
        (
            status <> 'SUCCEEDED'
            AND planning_proposal_id IS NULL
            AND research_submission_id IS NULL
        )
    ),
    CONSTRAINT agent_work_orders_completion_check CHECK (
        (
            status = 'OPEN'
            AND completed_at IS NULL
            AND terminal_reason_code IS NULL
        )
        OR
        (
            status = 'SUCCEEDED'
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
        OR
        (
            status IN ('CANCELLED', 'FAILED_FINAL', 'EXPIRED')
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
            AND terminal_reason_code IS NOT NULL
            AND btrim(terminal_reason_code) <> ''
        )
    ),
    CONSTRAINT agent_work_orders_deadline_check
        CHECK (deadline_at > created_at AND updated_at >= created_at),
    CONSTRAINT agent_work_orders_batch_fkey
        FOREIGN KEY (batch_id, user_id, work_kind)
        REFERENCES agent_work_batches(id, user_id, work_kind)
        ON DELETE CASCADE,
    CONSTRAINT agent_work_orders_planning_task_fkey
        FOREIGN KEY (planning_task_id, user_id)
        REFERENCES planning_tasks(id, user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_orders_research_round_fkey
        FOREIGN KEY (research_round_id, user_id)
        REFERENCES research_rounds(id, user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_orders_planning_result_fkey
        FOREIGN KEY (planning_proposal_id, planning_task_id, user_id)
        REFERENCES planning_proposals(id, task_id, user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_orders_research_result_fkey
        FOREIGN KEY (research_submission_id, research_round_id)
        REFERENCES research_submissions(id, research_round_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_orders_id_user_key UNIQUE (id, user_id),
    CONSTRAINT agent_work_orders_id_batch_user_key
        UNIQUE (id, batch_id, user_id),
    CONSTRAINT agent_work_orders_id_scope_key
        UNIQUE (id, scope_snapshot_hash),
    CONSTRAINT agent_work_orders_batch_planning_target_key
        UNIQUE (batch_id, planning_task_id),
    CONSTRAINT agent_work_orders_batch_research_target_key
        UNIQUE (batch_id, research_round_id)
);

CREATE UNIQUE INDEX agent_work_orders_open_planning_target_idx
    ON agent_work_orders(planning_task_id)
    WHERE status = 'OPEN' AND planning_task_id IS NOT NULL;

CREATE UNIQUE INDEX agent_work_orders_open_research_target_idx
    ON agent_work_orders(research_round_id)
    WHERE status = 'OPEN' AND research_round_id IS NOT NULL;

CREATE INDEX agent_work_orders_batch_status_idx
    ON agent_work_orders(batch_id, status, id);

CREATE TABLE agent_delegations (
    id UUID PRIMARY KEY,
    work_order_id UUID NOT NULL,
    user_id UUID NOT NULL,
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) > 0),
    scope_snapshot_hash TEXT NOT NULL CHECK (
        scope_snapshot_hash ~ '^[0-9a-f]{64}$'
    ),
    assignee_mode TEXT NOT NULL CHECK (
        assignee_mode IN ('SPECIFIC_CONNECTION', 'CLAIMING_CONNECTION')
    ),
    assignee_connection_id UUID,
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    status TEXT NOT NULL CHECK (
        status IN ('PENDING_CLAIM', 'ACTIVE', 'REVOKED', 'EXPIRED')
    ),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT agent_delegations_assignee_state_check CHECK (
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
    ),
    CONSTRAINT agent_delegations_specific_assignee_check CHECK (
        assignee_mode <> 'SPECIFIC_CONNECTION'
        OR assignee_connection_id IS NOT NULL
    ),
    CONSTRAINT agent_delegations_terminal_check CHECK (
        (
            status IN ('PENDING_CLAIM', 'ACTIVE')
            AND revoked_at IS NULL
        )
        OR
        (
            status IN ('REVOKED', 'EXPIRED')
            AND revoked_at IS NOT NULL
            AND revoked_at >= created_at
        )
    ),
    CONSTRAINT agent_delegations_deadline_check CHECK (expires_at > created_at),
    CONSTRAINT agent_delegations_order_user_fkey
        FOREIGN KEY (work_order_id, user_id)
        REFERENCES agent_work_orders(id, user_id)
        ON DELETE CASCADE,
    CONSTRAINT agent_delegations_order_scope_fkey
        FOREIGN KEY (work_order_id, scope_snapshot_hash)
        REFERENCES agent_work_orders(id, scope_snapshot_hash)
        ON DELETE CASCADE,
    CONSTRAINT agent_delegations_assignee_user_fkey
        FOREIGN KEY (assignee_connection_id, user_id)
        REFERENCES agent_connections(id, user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_delegations_order_epoch_key
        UNIQUE (work_order_id, epoch),
    CONSTRAINT agent_delegations_id_order_key
        UNIQUE (id, work_order_id),
    CONSTRAINT agent_delegations_claim_identity_key
        UNIQUE (id, work_order_id, assignee_connection_id)
);

CREATE UNIQUE INDEX agent_delegations_current_idx
    ON agent_delegations(work_order_id)
    WHERE status IN ('PENDING_CLAIM', 'ACTIVE');

CREATE INDEX agent_delegations_assignee_status_idx
    ON agent_delegations(assignee_connection_id, status, work_order_id)
    WHERE assignee_connection_id IS NOT NULL;

CREATE TABLE agent_work_attempts (
    id UUID PRIMARY KEY,
    work_order_id UUID NOT NULL
        REFERENCES agent_work_orders(id) ON DELETE CASCADE,
    delegation_id UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    status TEXT NOT NULL CHECK (
        status IN (
            'OFFERED', 'CLAIMED', 'RUNNING', 'SUCCEEDED',
            'FAILED_RETRYABLE', 'FAILED_FINAL', 'CANCELLED',
            'TIMED_OUT', 'SUPERSEDED'
        )
    ),
    claim_handle_ciphertext BYTEA,
    claim_handle_hash BYTEA,
    secret_key_version BIGINT,
    claimed_connection_id UUID,
    claim_ack_deadline_at TIMESTAMPTZ,
    activity_deadline_at TIMESTAMPTZ,
    execution_deadline_at TIMESTAMPTZ NOT NULL,
    last_activity_kind TEXT,
    last_activity_at TIMESTAMPTZ,
    last_error_code TEXT,
    retry_of_attempt_id UUID,
    superseded_by_attempt_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_work_attempts_claim_secret_check CHECK (
        (
            claim_handle_ciphertext IS NULL
            AND claim_handle_hash IS NULL
            AND secret_key_version IS NULL
        )
        OR
        (
            claim_handle_ciphertext IS NOT NULL
            AND octet_length(claim_handle_ciphertext) > 0
            AND claim_handle_hash IS NOT NULL
            AND octet_length(claim_handle_hash) = 32
            AND secret_key_version IS NOT NULL
            AND secret_key_version > 0
        )
    ),
    CONSTRAINT agent_work_attempts_offer_check CHECK (
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
    CONSTRAINT agent_work_attempts_claimed_check CHECK (
        status NOT IN ('CLAIMED', 'RUNNING')
        OR (
            claimed_connection_id IS NOT NULL
            AND claim_handle_hash IS NOT NULL
            AND claim_ack_deadline_at IS NOT NULL
            AND last_activity_kind IS NOT NULL
            AND btrim(last_activity_kind) <> ''
            AND last_activity_at IS NOT NULL
        )
    ),
    CONSTRAINT agent_work_attempts_running_check CHECK (
        status <> 'RUNNING'
        OR (
            activity_deadline_at IS NOT NULL
            AND last_activity_kind IS NOT NULL
            AND btrim(last_activity_kind) <> ''
            AND last_activity_at IS NOT NULL
        )
    ),
    CONSTRAINT agent_work_attempts_completion_check CHECK (
        (
            status IN ('OFFERED', 'CLAIMED', 'RUNNING')
            AND completed_at IS NULL
        )
        OR
        (
            status IN (
                'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_FINAL',
                'CANCELLED', 'TIMED_OUT', 'SUPERSEDED'
            )
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
    ),
    CONSTRAINT agent_work_attempts_deadline_check CHECK (
        execution_deadline_at > created_at
        AND (
            claim_ack_deadline_at IS NULL
            OR claim_ack_deadline_at > created_at
        )
        AND (
            activity_deadline_at IS NULL
            OR activity_deadline_at > created_at
        )
        AND (
            claim_ack_deadline_at IS NULL
            OR claim_ack_deadline_at <= execution_deadline_at
        )
        AND (
            activity_deadline_at IS NULL
            OR activity_deadline_at <= execution_deadline_at
        )
        AND (last_activity_at IS NULL OR last_activity_at >= created_at)
        AND updated_at >= created_at
    ),
    CONSTRAINT agent_work_attempts_not_self_link_check CHECK (
        (retry_of_attempt_id IS NULL OR retry_of_attempt_id <> id)
        AND (superseded_by_attempt_id IS NULL OR superseded_by_attempt_id <> id)
    ),
    CONSTRAINT agent_work_attempts_order_generation_key
        UNIQUE (work_order_id, generation),
    CONSTRAINT agent_work_attempts_identity_key
        UNIQUE (id, work_order_id, generation),
    CONSTRAINT agent_work_attempts_id_order_key
        UNIQUE (id, work_order_id),
    CONSTRAINT agent_work_attempts_delegation_fkey
        FOREIGN KEY (delegation_id, work_order_id)
        REFERENCES agent_delegations(id, work_order_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_work_attempts_claimed_connection_fkey
        FOREIGN KEY (delegation_id, work_order_id, claimed_connection_id)
        REFERENCES agent_delegations(
            id, work_order_id, assignee_connection_id
        )
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT agent_work_attempts_retry_fkey
        FOREIGN KEY (retry_of_attempt_id, work_order_id)
        REFERENCES agent_work_attempts(id, work_order_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT agent_work_attempts_superseded_fkey
        FOREIGN KEY (superseded_by_attempt_id, work_order_id)
        REFERENCES agent_work_attempts(id, work_order_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX agent_work_attempts_current_idx
    ON agent_work_attempts(work_order_id)
    WHERE status IN ('OFFERED', 'CLAIMED', 'RUNNING');

CREATE UNIQUE INDEX agent_work_attempts_claim_handle_idx
    ON agent_work_attempts(claim_handle_hash)
    WHERE claim_handle_hash IS NOT NULL;

CREATE INDEX agent_work_attempts_claim_ack_deadline_idx
    ON agent_work_attempts(claim_ack_deadline_at, id)
    WHERE status = 'CLAIMED';

CREATE INDEX agent_work_attempts_activity_deadline_idx
    ON agent_work_attempts(activity_deadline_at, id)
    WHERE status = 'RUNNING';

CREATE INDEX agent_work_attempts_execution_deadline_idx
    ON agent_work_attempts(execution_deadline_at, id)
    WHERE status IN ('CLAIMED', 'RUNNING');

ALTER TABLE agent_work_orders
    ADD CONSTRAINT agent_work_orders_active_attempt_fkey
        FOREIGN KEY (active_attempt_id, id, generation)
        REFERENCES agent_work_attempts(id, work_order_id, generation)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE agent_connection_acquisition_intents (
    id UUID PRIMARY KEY,
    batch_id UUID NOT NULL,
    work_order_id UUID NOT NULL,
    user_id UUID NOT NULL,
    required_scopes_hash TEXT NOT NULL CHECK (
        required_scopes_hash ~ '^[0-9a-f]{64}$'
    ),
    previous_connection_id UUID,
    connection_requirement TEXT NOT NULL CHECK (
        connection_requirement IN (
            'DIFFERENT_CONNECTION', 'NEW_AUTHORIZATION'
        )
    ),
    min_generation_exclusive BIGINT,
    status TEXT NOT NULL CHECK (
        status IN ('WAITING', 'SATISFIED', 'CANCELLED', 'EXPIRED')
    ),
    satisfied_connection_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT agent_connection_intents_requirement_check CHECK (
        (
            connection_requirement = 'DIFFERENT_CONNECTION'
            AND previous_connection_id IS NOT NULL
            AND min_generation_exclusive IS NULL
        )
        OR
        (
            connection_requirement = 'NEW_AUTHORIZATION'
            AND min_generation_exclusive IS NOT NULL
            AND min_generation_exclusive >= 0
        )
    ),
    CONSTRAINT agent_connection_intents_state_check CHECK (
        (
            status = 'WAITING'
            AND satisfied_connection_id IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            status = 'SATISFIED'
            AND satisfied_connection_id IS NOT NULL
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
        OR
        (
            status IN ('CANCELLED', 'EXPIRED')
            AND satisfied_connection_id IS NULL
            AND completed_at IS NOT NULL
            AND completed_at >= created_at
        )
    ),
    CONSTRAINT agent_connection_intents_different_check CHECK (
        connection_requirement <> 'DIFFERENT_CONNECTION'
        OR satisfied_connection_id IS NULL
        OR satisfied_connection_id <> previous_connection_id
    ),
    CONSTRAINT agent_connection_intents_deadline_check
        CHECK (expires_at > created_at),
    CONSTRAINT agent_connection_intents_order_fkey
        FOREIGN KEY (work_order_id, batch_id, user_id)
        REFERENCES agent_work_orders(id, batch_id, user_id)
        ON DELETE CASCADE,
    CONSTRAINT agent_connection_intents_previous_fkey
        FOREIGN KEY (previous_connection_id, user_id)
        REFERENCES agent_connections(id, user_id)
        ON DELETE RESTRICT,
    CONSTRAINT agent_connection_intents_satisfied_fkey
        FOREIGN KEY (satisfied_connection_id, user_id)
        REFERENCES agent_connections(id, user_id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX agent_connection_intents_waiting_idx
    ON agent_connection_acquisition_intents(work_order_id)
    WHERE status = 'WAITING';

CREATE INDEX agent_connection_intents_expiry_idx
    ON agent_connection_acquisition_intents(expires_at, id)
    WHERE status = 'WAITING';

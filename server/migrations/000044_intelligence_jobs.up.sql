-- ADR-0038: one durable IntelligenceJob per executable PlanningTask or
-- ResearchRound, an append-only IntelligenceAttempt per provider execution,
-- and Steps that are observation only. WorkBatch lifecycle is not recreated
-- here: a user action's fan-out is projected from curation_action_id.

CREATE TABLE intelligence_jobs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    curation_id UUID NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
    -- The user action is the correlation source, so every job derived from one
    -- action is projected through this column instead of a batch aggregate.
    curation_action_id UUID NOT NULL
        REFERENCES curation_actions(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL
        CHECK (target_kind IN ('PLANNING_TASK', 'RESEARCH_ROUND')),
    planning_task_id UUID REFERENCES planning_tasks(id) ON DELETE CASCADE,
    research_round_id UUID REFERENCES research_rounds(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('MANAGED')),
    model_key TEXT,
    status TEXT NOT NULL CHECK (
        status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')
    ),
    failure_code TEXT,
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT intelligence_jobs_target_check CHECK (
        (target_kind = 'PLANNING_TASK') = (planning_task_id IS NOT NULL)
        AND (target_kind = 'RESEARCH_ROUND') = (research_round_id IS NOT NULL)
    ),
    -- Only MANAGED carries a model key: a Connector job's model is chosen by
    -- the user's own local agent.
    CONSTRAINT intelligence_jobs_model_key_check CHECK (
        (provider = 'MANAGED') = (model_key IS NOT NULL)
        AND (model_key IS NULL OR btrim(model_key) <> '')
    ),
    CONSTRAINT intelligence_jobs_completion_check CHECK (
        (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED'))
        = (completed_at IS NOT NULL)
    ),
    CONSTRAINT intelligence_jobs_failure_check CHECK (
        status = 'FAILED' OR failure_code IS NULL
    )
);

-- One job per target, ever. A replayed user command resolves to the same job
-- instead of dispatching the same work twice.
CREATE UNIQUE INDEX intelligence_jobs_planning_target_key
    ON intelligence_jobs(planning_task_id)
    WHERE planning_task_id IS NOT NULL;
CREATE UNIQUE INDEX intelligence_jobs_research_target_key
    ON intelligence_jobs(research_round_id)
    WHERE research_round_id IS NOT NULL;

CREATE INDEX intelligence_jobs_dispatch_idx
    ON intelligence_jobs(provider, created_at)
    WHERE status = 'PENDING';
CREATE INDEX intelligence_jobs_curation_idx
    ON intelligence_jobs(curation_id, created_at DESC);
CREATE INDEX intelligence_jobs_action_idx
    ON intelligence_jobs(curation_action_id);

-- Identity constraints for product-result provenance, mirroring the
-- agent_work_orders pattern in migrations 000025 and 000026.
ALTER TABLE intelligence_jobs
    ADD CONSTRAINT intelligence_jobs_planning_identity_key
        UNIQUE (id, planning_task_id, user_id),
    ADD CONSTRAINT intelligence_jobs_research_identity_key
        UNIQUE (id, research_round_id);

CREATE TABLE intelligence_attempts (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES intelligence_jobs(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0 AND ordinal <= 10),
    provider TEXT NOT NULL CHECK (provider IN ('MANAGED')),
    -- Provider dispatch idempotency. Separate from the user command key by
    -- design: a retry is a new dispatch of the same user request.
    request_key UUID NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED',
                   'EFFECT_UNKNOWN')
    ),
    failure_code TEXT,
    retryable BOOLEAN,
    deadline_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT intelligence_attempts_ordinal_key UNIQUE (job_id, ordinal),
    CONSTRAINT intelligence_attempts_request_key_key UNIQUE (request_key),
    CONSTRAINT intelligence_attempts_completion_check CHECK (
        (status IN ('RUNNING', 'EFFECT_UNKNOWN') AND completed_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
            AND completed_at IS NOT NULL AND completed_at >= started_at)
    ),
    CONSTRAINT intelligence_attempts_deadline_check CHECK (
        deadline_at > started_at
    )
);

-- One live attempt per job. EFFECT_UNKNOWN holds the slot on purpose: until a
-- re-query resolves whether the provider actually ran, dispatching again could
-- duplicate a side effect.
CREATE UNIQUE INDEX intelligence_attempts_active_key
    ON intelligence_attempts(job_id)
    WHERE status IN ('RUNNING', 'EFFECT_UNKNOWN');
CREATE INDEX intelligence_attempts_deadline_idx
    ON intelligence_attempts(deadline_at) WHERE status = 'RUNNING';
CREATE INDEX intelligence_attempts_unknown_idx
    ON intelligence_attempts(started_at) WHERE status = 'EFFECT_UNKNOWN';

-- Append-only progress observation. Losing every row here changes what the
-- user sees while waiting, never the job outcome.
CREATE TABLE intelligence_steps (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES intelligence_jobs(id) ON DELETE CASCADE,
    attempt_id UUID NOT NULL
        REFERENCES intelligence_attempts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    kind TEXT NOT NULL CHECK (
        kind IN ('INTERPRETING', 'SEARCHING_CATALOG', 'RANKING', 'SUBMITTING')
    ),
    status TEXT NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED')),
    reason_code TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT intelligence_steps_completion_check CHECK (
        (status = 'RUNNING' AND completed_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED')
            AND completed_at IS NOT NULL AND completed_at >= started_at)
    ),
    CONSTRAINT intelligence_steps_attempt_ordinal_key UNIQUE (attempt_id, ordinal)
);

CREATE INDEX intelligence_steps_job_idx
    ON intelligence_steps(job_id, attempt_id, ordinal);

-- Product results gain a third provenance model. Every row still chooses
-- exactly one, so an intelligence submission can never be confused with an
-- agent one.
ALTER TABLE planning_proposals
    ADD COLUMN intelligence_job_id UUID,
    ADD CONSTRAINT planning_proposals_intelligence_job_fkey
        FOREIGN KEY (intelligence_job_id, task_id, user_id)
        REFERENCES intelligence_jobs(id, planning_task_id, user_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    DROP CONSTRAINT planning_proposals_agent_control_provenance_check,
    ADD CONSTRAINT planning_proposals_provenance_check CHECK (
        num_nonnulls(
            agent_grant_id, agent_work_order_id, intelligence_job_id
        ) = 1
    );

CREATE UNIQUE INDEX planning_proposals_intelligence_idempotency_idx
    ON planning_proposals(task_id, intelligence_job_id, client_proposal_id)
    WHERE intelligence_job_id IS NOT NULL;

ALTER TABLE research_submissions
    ADD COLUMN intelligence_job_id UUID,
    ADD CONSTRAINT research_submissions_intelligence_job_fkey
        FOREIGN KEY (intelligence_job_id, research_round_id)
        REFERENCES intelligence_jobs(id, research_round_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    DROP CONSTRAINT research_submissions_agent_control_provenance_check,
    ADD CONSTRAINT research_submissions_provenance_check CHECK (
        num_nonnulls(
            agent_grant_id, agent_work_order_id, intelligence_job_id
        ) = 1
    );

CREATE UNIQUE INDEX research_submissions_intelligence_idempotency_idx
    ON research_submissions(
        research_round_id, intelligence_job_id, client_submission_id
    )
    WHERE intelligence_job_id IS NOT NULL;

ALTER TABLE research_catalog_observations
    ADD COLUMN intelligence_job_id UUID,
    ADD CONSTRAINT research_catalog_observations_intelligence_job_fkey
        FOREIGN KEY (intelligence_job_id, research_round_id)
        REFERENCES intelligence_jobs(id, research_round_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    DROP CONSTRAINT research_catalog_observations_agent_control_provenance_check,
    ADD CONSTRAINT research_catalog_observations_provenance_check CHECK (
        num_nonnulls(
            agent_grant_id, agent_work_order_id, intelligence_job_id
        ) = 1
    );

CREATE INDEX research_catalog_observations_intelligence_access_idx
    ON research_catalog_observations(
        research_round_id, user_id, intelligence_job_id, expires_at
    )
    WHERE intelligence_job_id IS NOT NULL;

-- The Server itself is now the initiator of derived work.
ALTER TABLE curation_actions
    DROP CONSTRAINT curation_actions_initiator_check,
    ADD CONSTRAINT curation_actions_initiator_check
        CHECK (initiator IN ('USER', 'MANAGED_RUNNER', 'INTELLIGENCE'));

-- The reservation's attempt reference was already declared a non-identity
-- diagnostic (000040 nulls it rather than cascading). Detaching the foreign key
-- lets it span the agent and intelligence generations, and lets the hard
-- cutover drop agent_work_attempts without touching counted spend.
ALTER TABLE managed_runner_reservations
    DROP CONSTRAINT managed_runner_reservations_attempt_id_fkey;

COMMENT ON COLUMN managed_runner_reservations.attempt_id IS
    'Diagnostic reference to intelligence_attempts.id (rows created before ADR-0038 reference agent_work_attempts.id). Intentionally no foreign key.';

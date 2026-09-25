-- ADR-0026 expand phase.
--
-- Curation becomes an independently addressable aggregate. Legacy plan_id and
-- status columns remain readable during the expand phase so the application
-- can be switched without inventing a dual-write compatibility API.
ALTER TABLE curations
    ADD COLUMN id UUID DEFAULT gen_random_uuid(),
    ADD COLUMN shopping_plan_id UUID,
    ADD COLUMN phase TEXT DEFAULT 'PLANNING',
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD COLUMN archived_by_user_id UUID
        REFERENCES users(id) ON DELETE RESTRICT;

UPDATE curations AS curation
SET
    id=gen_random_uuid(),
    shopping_plan_id=curation.plan_id,
    phase=CASE
        WHEN plan.status IN ('ACTIVE', 'COMPLETED') THEN 'CURATING'
        ELSE 'PLANNING'
    END
FROM shopping_plans AS plan
WHERE plan.id=curation.plan_id;

ALTER TABLE curations
    ALTER COLUMN id SET NOT NULL,
    ALTER COLUMN shopping_plan_id SET NOT NULL,
    ALTER COLUMN phase SET NOT NULL,
    ADD CONSTRAINT curations_id_unique UNIQUE (id),
    ADD CONSTRAINT curations_id_user_unique UNIQUE (id, user_id),
    ADD CONSTRAINT curations_user_id_id_unique UNIQUE (user_id, id),
    ADD CONSTRAINT curations_shopping_plan_unique UNIQUE (shopping_plan_id),
    ADD CONSTRAINT curations_user_shopping_plan_unique
        UNIQUE (user_id, shopping_plan_id),
    ADD CONSTRAINT curations_shopping_plan_fkey
        FOREIGN KEY (shopping_plan_id)
        REFERENCES shopping_plans(id) ON DELETE CASCADE,
    ADD CONSTRAINT curations_phase_check
        CHECK (phase IN ('PLANNING', 'CURATING')),
    ADD CONSTRAINT curations_archive_audit_check CHECK (
        (archived_at IS NULL AND archived_by_user_id IS NULL)
        OR
        (archived_at IS NOT NULL AND archived_by_user_id IS NOT NULL)
    );

ALTER TABLE plan_targets
    ADD COLUMN curation_id UUID,
    ADD COLUMN user_id UUID;

UPDATE plan_targets AS target
SET curation_id=curation.id, user_id=curation.user_id
FROM curations AS curation
WHERE curation.shopping_plan_id=target.plan_id;

ALTER TABLE plan_targets
    ALTER COLUMN curation_id SET NOT NULL,
    ALTER COLUMN user_id SET NOT NULL,
    ADD CONSTRAINT plan_targets_user_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    ADD CONSTRAINT plan_targets_user_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT plan_targets_user_id_unique UNIQUE (user_id, id),
    ADD CONSTRAINT plan_targets_user_id_curation_unique
        UNIQUE (user_id, id, curation_id);

CREATE INDEX idx_plan_targets_curation_order
    ON plan_targets(curation_id, order_index, id)
    WHERE removed_at IS NULL;

UPDATE shopping_sessions AS session
SET target_snapshot=jsonb_set(
    session.target_snapshot,
    '{curationId}',
    to_jsonb(target.curation_id::text),
    true
)
FROM plan_targets AS target
WHERE target.id=session.plan_target_id
  AND session.target_snapshot->>'curationId'
        IS DISTINCT FROM target.curation_id::text;

ALTER TABLE curation_runs
    ADD COLUMN curation_id UUID;

UPDATE curation_runs AS run
SET curation_id=curation.id
FROM curations AS curation
WHERE curation.shopping_plan_id=run.curation_plan_id
  AND curation.user_id=run.user_id;

-- CurationRun owns Planning materialization only. Research lifecycle belongs
-- to ResearchRound and AgentControl, so preview rows that had crossed the old
-- hand-off boundary are already completed Planning runs.
UPDATE curation_runs
SET
    status='COMPLETED',
    completed_at=COALESCE(completed_at, updated_at)
WHERE status IN ('RESEARCH_READY', 'RESEARCHING');

-- curation_runs already carries foreign keys from earlier migrations, so the
-- updates above queue row trigger events and PostgreSQL then refuses every
-- later ALTER TABLE on this table inside the same transaction (SQLSTATE
-- 55006). Flushing those checks here keeps the rest of the migration in one
-- transaction. A database with no rows to update never queues an event, which
-- is why a fresh-schema migration test cannot catch this.
SET CONSTRAINTS ALL IMMEDIATE;

DROP INDEX IF EXISTS idx_curation_runs_one_open_expansion;
ALTER TABLE curation_runs
    DROP CONSTRAINT IF EXISTS curation_runs_status_check,
    ADD CONSTRAINT curation_runs_status_check CHECK (
        status IN (
            'REQUESTED', 'MATERIALIZING', 'COMPLETED', 'FAILED', 'CANCELLED'
        )
    );

CREATE UNIQUE INDEX idx_curation_runs_one_open_expansion
    ON curation_runs(curation_plan_id)
    WHERE kind='EXPANSION'
      AND status IN ('REQUESTED', 'MATERIALIZING');

-- Added after the curation_runs DML above so the trigger-event flush covers it.
ALTER TABLE curation_runs
    ALTER COLUMN curation_id SET NOT NULL,
    ADD CONSTRAINT curation_runs_user_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE;

CREATE TABLE curation_actions (
    id UUID PRIMARY KEY,
    curation_id UUID NOT NULL,
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK (
        action_type IN (
            'INTENT_NEXT_STEP',
            'PLANNING_ADD_TARGETS',
            'PLANNING_START_CURATING',
            'CURATION_ADD_TARGETS',
            'TARGET_RESEARCH_AGAIN',
            'TARGET_REMOVE',
            'CANDIDATE_INTERACTION',
            'SELECTION_MUTATION',
            'PURCHASE_DIRECT',
            'PURCHASE_CART'
        )
    ),
    phase_at_request TEXT NOT NULL CHECK (
        phase_at_request IN ('HAVING_INTENT', 'PLANNING', 'CURATING')
    ),
    requested_transition_to TEXT CHECK (
        requested_transition_to IN ('PLANNING', 'CURATING')
    ),
    subject_type TEXT NOT NULL CHECK (
        subject_type IN (
            'INTENT', 'TARGET_LIST', 'CURATION', 'TARGET',
            'CANDIDATE', 'SELECTION'
        )
    ),
    subject_id TEXT,
    effect_kind TEXT NOT NULL CHECK (
        effect_kind IN ('NONE', 'AGENT_WORK', 'PURCHASE_THREAD')
    ),
    source_ref_type TEXT NOT NULL,
    source_ref_id TEXT NOT NULL,
    expected_curation_version BIGINT NOT NULL
        CHECK (expected_curation_version > 0),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT curation_actions_actor_curation_fkey
        FOREIGN KEY (actor_user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE,
    CONSTRAINT curation_actions_id_actor_unique UNIQUE (id, actor_user_id)
);

CREATE INDEX idx_curation_actions_timeline
    ON curation_actions(curation_id, created_at, id);

-- authorized_by_action_id remains AgentControl's internal command-request
-- lineage. Product authorization is recorded separately and never overloads
-- that established invariant.
ALTER TABLE agent_work_batches
    ADD COLUMN curation_action_id UUID,
    ADD CONSTRAINT agent_work_batches_curation_action_fkey
        FOREIGN KEY (curation_action_id, user_id)
        REFERENCES curation_actions(id, actor_user_id)
        ON DELETE RESTRICT;

CREATE INDEX idx_agent_work_batches_curation_action
    ON agent_work_batches(curation_action_id)
    WHERE curation_action_id IS NOT NULL;

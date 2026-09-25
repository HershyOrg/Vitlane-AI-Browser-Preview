-- ADR-0024 final destructive authority cutover.
--
-- Migrations 000024 through 000029 prepare the AgentControl and AuthAttempt
-- schemas. This final migration is intentionally separate: it destroys every
-- legacy Agent authority while preserving completed Planning/Research results
-- and the user's Candidate decisions. It may only run in the approved
-- maintenance window after the pre-cutover backup and restore rehearsal.

CREATE TABLE agent_auth_control_cutover_audits (
    migration_version TEXT PRIMARY KEY,
    captured_at TIMESTAMPTZ NOT NULL,
    pre_cutover_counts JSONB NOT NULL,
    preservation_hashes JSONB NOT NULL,
    post_cutover_counts JSONB NOT NULL
);

INSERT INTO agent_auth_control_cutover_audits(
    migration_version,
    captured_at,
    pre_cutover_counts,
    preservation_hashes,
    post_cutover_counts
)
VALUES (
    '000030_agent_auth_control_legacy_reset',
    NOW(),
    jsonb_build_object(
        'agentConnections', (SELECT COUNT(*) FROM agent_connections),
        'activeAgentConnections', (
            SELECT COUNT(*)
            FROM agent_connections
            WHERE status='ACTIVE' AND revoked_at IS NULL
        ),
        'oauthAccessTokens', (SELECT COUNT(*) FROM oauth_access_tokens),
        'oauthRefreshTokens', (SELECT COUNT(*) FROM oauth_refresh_tokens),
        'agentGrants', (SELECT COUNT(*) FROM agent_grants),
        'activeAgentGrants', (
            SELECT COUNT(*) FROM agent_grants WHERE revoked_at IS NULL
        ),
        'activeAgentGrantSessions', (
            SELECT COUNT(*) FROM agent_grant_sessions WHERE released_at IS NULL
        ),
        'agentGrantRequests', (SELECT COUNT(*) FROM agent_grant_requests),
        'openPlanningTasks', (
            SELECT COUNT(*) FROM planning_tasks WHERE status='REQUESTED'
        ),
        'openResearchRounds', (
            SELECT COUNT(*) FROM research_rounds WHERE status='REQUESTED'
        ),
        'planningProposals', (SELECT COUNT(*) FROM planning_proposals),
        'researchSubmissions', (SELECT COUNT(*) FROM research_submissions),
        'candidates', (SELECT COUNT(*) FROM candidates),
        'candidateInteractions', (SELECT COUNT(*) FROM candidate_interactions),
        'candidateInteractionEvents', (
            SELECT COUNT(*) FROM candidate_interaction_events
        )
    ),
    jsonb_build_object(
        'planningProposals', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || proposal_hash || ':' || validation_status,
                '|' ORDER BY id
            )
            FROM planning_proposals
        ), '')),
        'researchSubmissions', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || submission_hash || ':' || validation_status,
                '|' ORDER BY id
            )
            FROM research_submissions
        ), '')),
        'candidates', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || candidate_hash,
                '|' ORDER BY id
            )
            FROM candidates
        ), '')),
        'candidateInteractions', md5(COALESCE((
            SELECT string_agg(
                shopping_session_id::text || ':' || candidate_id::text || ':' ||
                pinned::text || ':' || sentiment || ':' || version::text,
                '|' ORDER BY shopping_session_id, candidate_id
            )
            FROM candidate_interactions
        ), '')),
        'candidateInteractionEvents', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || command_hash || ':' || action,
                '|' ORDER BY id
            )
            FROM candidate_interaction_events
        ), ''))
    ),
    '{}'::jsonb
);

-- The final reset must be the first runtime that can use the prepared schemas.
-- If a partially deployed new runtime has already accepted work or OAuth, stop
-- instead of destroying that new state under the legacy-data approval.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM agent_auth_attempts)
        OR EXISTS (SELECT 1 FROM agent_auth_attempt_cancel_requests)
        OR EXISTS (SELECT 1 FROM agent_credential_families)
        OR EXISTS (SELECT 1 FROM agent_connection_revoke_commands)
        OR EXISTS (SELECT 1 FROM agent_command_requests)
        OR EXISTS (SELECT 1 FROM agent_work_batches)
        OR EXISTS (SELECT 1 FROM agent_work_orders)
        OR EXISTS (SELECT 1 FROM agent_delegations)
        OR EXISTS (SELECT 1 FROM agent_work_attempts)
        OR EXISTS (SELECT 1 FROM agent_connection_acquisition_intents)
        OR EXISTS (SELECT 1 FROM oauth_authorization_codes)
    THEN
        RAISE EXCEPTION 'AGENT_AUTH_CONTROL_CUTOVER_NEW_STATE_EXISTS'
            USING ERRCODE='55000';
    END IF;
END
$$;

CREATE TEMP TABLE agent_auth_cutover_open_planning_tasks
ON COMMIT DROP
AS
SELECT id
FROM planning_tasks
WHERE status='REQUESTED';

CREATE TEMP TABLE agent_auth_cutover_open_research_rounds
ON COMMIT DROP
AS
SELECT
    round.id,
    round.shopping_session_id,
    feedback.id AS feedback_id,
    feedback.previous_round_id,
    feedback.previous_round_status
FROM research_rounds AS round
LEFT JOIN research_feedback AS feedback
  ON feedback.next_round_id=round.id
 AND feedback.status='ACTIVE'
WHERE round.status='REQUESTED';

-- An open Round must be the exact current Round of a RESEARCHING Session. A
-- retry Round additionally requires its previous terminal Round to be in the
-- SUPERSEDED state. Abort on a malformed preview graph rather than guessing.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM agent_auth_cutover_open_research_rounds AS open_round
        LEFT JOIN shopping_sessions AS session
          ON session.id=open_round.shopping_session_id
        WHERE session.id IS NULL
           OR session.status <> 'RESEARCHING'
           OR session.current_research_round_id IS DISTINCT FROM open_round.id
    ) THEN
        RAISE EXCEPTION 'AGENT_AUTH_CONTROL_CUTOVER_RESEARCH_SESSION_MISMATCH'
            USING ERRCODE='55000';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM agent_auth_cutover_open_research_rounds AS open_round
        JOIN research_rounds AS previous
          ON previous.id=open_round.previous_round_id
        WHERE previous.status <> 'SUPERSEDED'
           OR open_round.previous_round_status NOT IN (
               'RESULTS_READY', 'NO_RESULTS'
           )
    ) THEN
        RAISE EXCEPTION 'AGENT_AUTH_CONTROL_CUTOVER_RESEARCH_RETRY_MISMATCH'
            USING ERRCODE='55000';
    END IF;
END
$$;

-- Cancel legacy Planning without deleting completed Proposals. A CurationRun
-- tied to an open PlanningTask must close with the same terminal meaning so it
-- cannot remain as an unrecoverable REQUESTED/MATERIALIZING projection.
UPDATE curation_runs AS run
SET status='CANCELLED',
    completed_at=COALESCE(run.completed_at, NOW()),
    updated_at=NOW()
FROM agent_auth_cutover_open_planning_tasks AS open_task
WHERE run.planning_task_id=open_task.id
  AND run.status IN ('REQUESTED', 'MATERIALIZING');

UPDATE planning_tasks AS task
SET status='CANCELLED',
    current_agent_grant_id=NULL,
    completed_at=COALESCE(task.completed_at, NOW()),
    updated_at=NOW()
FROM agent_auth_cutover_open_planning_tasks AS open_task
WHERE task.id=open_task.id;

-- Cancelling "research again" restores the previous accepted result exactly as
-- the application command does. Cancelling an initial Round returns the
-- Session to READY. No Submission, Candidate, interaction, or decision row is
-- rewritten.
UPDATE research_rounds AS previous
SET status=open_round.previous_round_status,
    completed_at=NOW()
FROM agent_auth_cutover_open_research_rounds AS open_round
WHERE previous.id=open_round.previous_round_id
  AND previous.status='SUPERSEDED';

UPDATE research_feedback AS feedback
SET status='CANCELLED',
    cancelled_at=COALESCE(feedback.cancelled_at, NOW())
FROM agent_auth_cutover_open_research_rounds AS open_round
WHERE feedback.id=open_round.feedback_id;

UPDATE shopping_sessions AS session
SET status=CASE
        WHEN open_round.previous_round_id IS NULL THEN 'READY'
        ELSE 'REVIEWING'
    END,
    current_research_round_id=open_round.previous_round_id,
    version=session.version + 1,
    updated_at=NOW()
FROM agent_auth_cutover_open_research_rounds AS open_round
WHERE session.id=open_round.shopping_session_id;

UPDATE research_rounds AS round
SET status='CANCELLED',
    current_agent_grant_id=NULL,
    completed_at=COALESCE(round.completed_at, NOW())
FROM agent_auth_cutover_open_research_rounds AS open_round
WHERE round.id=open_round.id;

-- Reconcile affected completed-Planning runs after their open Research Rounds
-- were cancelled. Runs with a preserved result stay COMPLETED; otherwise they
-- return to RESEARCH_READY so the user can explicitly start new Agent work.
WITH affected_runs AS (
    SELECT DISTINCT target.created_by_curation_run_id AS id
    FROM agent_auth_cutover_open_research_rounds AS open_round
    JOIN shopping_sessions AS session
      ON session.id=open_round.shopping_session_id
    JOIN plan_targets AS target
      ON target.id=session.plan_target_id
    WHERE target.created_by_curation_run_id IS NOT NULL
),
run_state AS (
    SELECT
        run.id,
        COUNT(target.id) AS total,
        COUNT(target.id) FILTER (
            WHERE EXISTS (
                SELECT 1
                FROM shopping_sessions AS result_session
                JOIN research_rounds AS result_round
                  ON result_round.shopping_session_id=result_session.id
                WHERE result_session.plan_target_id=target.id
                  AND result_round.status IN ('RESULTS_READY', 'NO_RESULTS')
            )
        ) AS completed
    FROM curation_runs AS run
    JOIN affected_runs AS affected ON affected.id=run.id
    LEFT JOIN plan_targets AS target
      ON target.created_by_curation_run_id=run.id
     AND target.removed_at IS NULL
    GROUP BY run.id
)
UPDATE curation_runs AS run
SET status=CASE
        WHEN state.total=0 OR state.completed=state.total THEN 'COMPLETED'
        ELSE 'RESEARCH_READY'
    END,
    completed_at=CASE
        WHEN state.total=0 OR state.completed=state.total
            THEN COALESCE(run.completed_at, NOW())
        ELSE run.completed_at
    END,
    updated_at=NOW()
FROM run_state AS state
WHERE run.id=state.id
  AND run.status IN ('RESEARCH_READY', 'RESEARCHING');

-- Remove every live legacy bearer. Referenced AgentGrant/Connection rows remain
-- only as revoked provenance for immutable completed results; their plaintext
-- activation refs are replaced with deterministic non-secret tombstones.
UPDATE planning_tasks
SET current_agent_grant_id=NULL
WHERE current_agent_grant_id IS NOT NULL;

UPDATE research_rounds
SET current_agent_grant_id=NULL
WHERE current_agent_grant_id IS NOT NULL;

UPDATE agent_grant_sessions
SET released_at=COALESCE(released_at, NOW())
WHERE released_at IS NULL;

DELETE FROM agent_grant_requests;

UPDATE agent_grants
SET revoked_at=COALESCE(revoked_at, NOW()),
    expires_at=LEAST(expires_at, NOW()),
    activation_ref='revoked:' || id::text;

DELETE FROM oauth_access_tokens;
DELETE FROM oauth_refresh_tokens;

UPDATE agent_connections
SET status='REVOKED',
    credential_status=CASE
        WHEN connection_generation IS NULL THEN NULL
        ELSE 'REVOKED'
    END,
    revoked_at=COALESCE(revoked_at, NOW()),
    updated_at=NOW(),
    version=version + 1
WHERE status <> 'REVOKED' OR revoked_at IS NULL;

-- Close the additive-compatibility window opened by 000024. Historical
-- Connection/Grant rows remain only as terminal provenance for immutable
-- results. Every new usable credential must be created through AuthAttempt and
-- its managed CredentialFamily; the legacy Grant authority graph cannot be
-- recreated by an old application image or fixture after this migration.
ALTER TABLE agent_connections
    ADD CONSTRAINT agent_connections_cutover_managed_or_terminal_check CHECK (
        (
            connection_generation IS NULL
            AND resource IS NULL
            AND issuer IS NULL
            AND credential_status IS NULL
            AND current_credential_family_id IS NULL
            AND current_credential_epoch IS NULL
            AND source_auth_attempt_id IS NULL
            AND confirmation_deadline_at IS NULL
            AND status='REVOKED'
            AND revoked_at IS NOT NULL
        )
        OR
        (
            connection_generation IS NOT NULL
            AND resource IS NOT NULL
            AND issuer IS NOT NULL
            AND credential_status IS NOT NULL
            AND current_credential_family_id IS NOT NULL
            AND current_credential_epoch IS NOT NULL
            AND source_auth_attempt_id IS NOT NULL
            AND confirmation_deadline_at IS NOT NULL
        )
    );

ALTER TABLE agent_grants
    ADD CONSTRAINT agent_grants_cutover_terminal_provenance_check CHECK (
        revoked_at IS NOT NULL
        AND activation_ref='revoked:' || id::text
    );

ALTER TABLE agent_grant_sessions
    ADD CONSTRAINT agent_grant_sessions_cutover_released_check CHECK (
        released_at IS NOT NULL
    );

ALTER TABLE agent_grant_requests
    ADD CONSTRAINT agent_grant_requests_cutover_empty_check CHECK (FALSE);

ALTER TABLE planning_tasks
    ADD CONSTRAINT planning_tasks_cutover_no_agent_grant_check CHECK (
        current_agent_grant_id IS NULL
    );

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_cutover_no_agent_grant_check CHECK (
        current_agent_grant_id IS NULL
    );

ALTER TABLE oauth_access_tokens
    ALTER COLUMN credential_family_id SET NOT NULL,
    ALTER COLUMN credential_epoch SET NOT NULL;

ALTER TABLE oauth_refresh_tokens
    ALTER COLUMN credential_family_id SET NOT NULL,
    ALTER COLUMN credential_epoch SET NOT NULL,
    ALTER COLUMN refresh_generation SET NOT NULL;

UPDATE agent_auth_control_cutover_audits
SET post_cutover_counts=jsonb_build_object(
    'activeAgentConnections', (
        SELECT COUNT(*)
        FROM agent_connections
        WHERE status='ACTIVE' AND revoked_at IS NULL
    ),
    'oauthAccessTokens', (SELECT COUNT(*) FROM oauth_access_tokens),
    'oauthRefreshTokens', (SELECT COUNT(*) FROM oauth_refresh_tokens),
    'activeAgentGrants', (
        SELECT COUNT(*) FROM agent_grants WHERE revoked_at IS NULL
    ),
    'activeAgentGrantSessions', (
        SELECT COUNT(*) FROM agent_grant_sessions WHERE released_at IS NULL
    ),
    'agentGrantRequests', (SELECT COUNT(*) FROM agent_grant_requests),
    'openPlanningTasks', (
        SELECT COUNT(*) FROM planning_tasks WHERE status='REQUESTED'
    ),
    'openResearchRounds', (
        SELECT COUNT(*) FROM research_rounds WHERE status='REQUESTED'
    ),
    'planningTaskGrantPointers', (
        SELECT COUNT(*)
        FROM planning_tasks
        WHERE current_agent_grant_id IS NOT NULL
    ),
    'researchRoundGrantPointers', (
        SELECT COUNT(*)
        FROM research_rounds
        WHERE current_agent_grant_id IS NOT NULL
    )
)
WHERE migration_version='000030_agent_auth_control_legacy_reset';

DO $$
DECLARE
    expected_hashes JSONB;
    actual_hashes JSONB;
BEGIN
    SELECT preservation_hashes
    INTO expected_hashes
    FROM agent_auth_control_cutover_audits
    WHERE migration_version='000030_agent_auth_control_legacy_reset';

    actual_hashes := jsonb_build_object(
        'planningProposals', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || proposal_hash || ':' || validation_status,
                '|' ORDER BY id
            )
            FROM planning_proposals
        ), '')),
        'researchSubmissions', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || submission_hash || ':' || validation_status,
                '|' ORDER BY id
            )
            FROM research_submissions
        ), '')),
        'candidates', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || candidate_hash,
                '|' ORDER BY id
            )
            FROM candidates
        ), '')),
        'candidateInteractions', md5(COALESCE((
            SELECT string_agg(
                shopping_session_id::text || ':' || candidate_id::text || ':' ||
                pinned::text || ':' || sentiment || ':' || version::text,
                '|' ORDER BY shopping_session_id, candidate_id
            )
            FROM candidate_interactions
        ), '')),
        'candidateInteractionEvents', md5(COALESCE((
            SELECT string_agg(
                id::text || ':' || command_hash || ':' || action,
                '|' ORDER BY id
            )
            FROM candidate_interaction_events
        ), ''))
    );

    IF actual_hashes IS DISTINCT FROM expected_hashes THEN
        RAISE EXCEPTION 'AGENT_AUTH_CONTROL_CUTOVER_RESULT_PRESERVATION_FAILED'
            USING ERRCODE='55000';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM agent_connections
        WHERE status='ACTIVE' OR revoked_at IS NULL
    )
        OR EXISTS (SELECT 1 FROM oauth_access_tokens)
        OR EXISTS (SELECT 1 FROM oauth_refresh_tokens)
        OR EXISTS (SELECT 1 FROM agent_grants WHERE revoked_at IS NULL)
        OR EXISTS (
            SELECT 1 FROM agent_grant_sessions WHERE released_at IS NULL
        )
        OR EXISTS (SELECT 1 FROM agent_grant_requests)
        OR EXISTS (SELECT 1 FROM planning_tasks WHERE status='REQUESTED')
        OR EXISTS (SELECT 1 FROM research_rounds WHERE status='REQUESTED')
        OR EXISTS (
            SELECT 1
            FROM planning_tasks
            WHERE current_agent_grant_id IS NOT NULL
        )
        OR EXISTS (
            SELECT 1
            FROM research_rounds
            WHERE current_agent_grant_id IS NOT NULL
        )
    THEN
        RAISE EXCEPTION 'AGENT_AUTH_CONTROL_CUTOVER_POSTCONDITION_FAILED'
            USING ERRCODE='55000';
    END IF;
END
$$;

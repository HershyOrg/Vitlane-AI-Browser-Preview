ALTER TABLE research_rounds
    ADD COLUMN failure_reason_code TEXT,
    ADD COLUMN failure_retryable BOOLEAN;

ALTER TABLE research_rounds
    DROP CONSTRAINT IF EXISTS research_rounds_status_check;

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_status_check
        CHECK (status IN (
            'REQUESTED', 'RESULTS_READY', 'NO_RESULTS',
            'FAILED', 'CANCELLED', 'SUPERSEDED'
        ));

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_failure_shape_check
        CHECK (
            (
                status = 'FAILED'
                AND completed_at IS NOT NULL
                AND failure_reason_code IS NOT NULL
                AND char_length(btrim(failure_reason_code)) BETWEEN 1 AND 200
                AND failure_retryable IS NOT NULL
            )
            OR
            (
                status <> 'FAILED'
                AND failure_reason_code IS NULL
                AND failure_retryable IS NULL
            )
        );

-- A terminal Intelligence failure used to leave its Research aggregate open.
-- Repair those rows before the new binary starts polling them. CANCELLED is
-- included so a 57 down/up round-trip recovers the exact failure projection.
UPDATE research_rounds AS round
SET status = 'FAILED',
    failure_reason_code = job.failure_code,
    failure_retryable = job.retryable,
    completed_at = job.completed_at
FROM intelligence_jobs AS job
WHERE job.research_round_id = round.id
  AND job.status = 'FAILED'
  AND job.failure_code IS NOT NULL
  AND job.completed_at IS NOT NULL
  AND round.status IN ('REQUESTED', 'CANCELLED');

-- A failed re-research restores the previous completed Round and its visible
-- pool. Feedback is closed because this exact request reached a terminal
-- outcome; a later user request gets a new Round/Job/idempotency identity.
UPDATE research_rounds AS previous
SET status = feedback.previous_round_status,
    completed_at = COALESCE(previous.completed_at, failed.completed_at)
FROM research_feedback AS feedback
JOIN research_rounds AS failed ON failed.id = feedback.next_round_id
WHERE previous.id = feedback.previous_round_id
  AND failed.status = 'FAILED'
  AND previous.status = 'SUPERSEDED';

UPDATE research_feedback AS feedback
SET status = 'CANCELLED',
    cancelled_at = failed.completed_at
FROM research_rounds AS failed
WHERE failed.id = feedback.next_round_id
  AND failed.status = 'FAILED'
  AND feedback.status = 'ACTIVE';

UPDATE shopping_sessions AS session
SET status = 'REVIEWING',
    current_research_round_id = feedback.previous_round_id,
    version = session.version + 1,
    updated_at = failed.completed_at
FROM research_feedback AS feedback
JOIN research_rounds AS failed ON failed.id = feedback.next_round_id
WHERE session.id = failed.shopping_session_id
  AND session.current_research_round_id = failed.id
  AND failed.status = 'FAILED';

UPDATE shopping_sessions AS session
SET status = 'READY',
    version = session.version + 1,
    updated_at = failed.completed_at
FROM research_rounds AS failed
WHERE session.id = failed.shopping_session_id
  AND session.current_research_round_id = failed.id
  AND failed.status = 'FAILED'
  AND NOT EXISTS (
      SELECT 1
      FROM research_feedback AS feedback
      WHERE feedback.next_round_id = failed.id
  );

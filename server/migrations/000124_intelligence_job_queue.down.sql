-- 지연 attempt는 실행되지 않은 시도이므로 되돌릴 때 CANCELLED로 접는다.
UPDATE intelligence_attempts SET status = 'CANCELLED' WHERE status = 'DEFERRED';
DELETE FROM intelligence_attempts WHERE ordinal > 10;
ALTER TABLE intelligence_attempts
    DROP CONSTRAINT intelligence_attempts_completion_check,
    ADD CONSTRAINT intelligence_attempts_completion_check CHECK (
        (status IN ('RUNNING', 'EFFECT_UNKNOWN') AND completed_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
            AND completed_at IS NOT NULL AND completed_at >= started_at)
    ),
    DROP CONSTRAINT intelligence_attempts_ordinal_check,
    ADD CONSTRAINT intelligence_attempts_ordinal_check CHECK (ordinal > 0 AND ordinal <= 10),
    DROP CONSTRAINT intelligence_attempts_status_check,
    ADD CONSTRAINT intelligence_attempts_status_check CHECK (
        status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'EFFECT_UNKNOWN')
    );
UPDATE intelligence_jobs SET attempt_count = LEAST(attempt_count, 10);
DROP INDEX IF EXISTS intelligence_jobs_pending_claim_idx;
ALTER TABLE intelligence_jobs
    DROP COLUMN queue_reason,
    DROP COLUMN defer_count,
    DROP COLUMN not_before;

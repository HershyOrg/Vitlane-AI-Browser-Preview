-- 조사 실행 admission과 대기 큐 (2026-09-17 조사 성능 트랙 PR 2).
-- Job은 자원이 준비될 때까지 PENDING으로 남을 수 있고(not_before), attempt는
-- 실행 없이 DEFERRED로 닫힐 수 있다. 지연은 자동 재시도 3회에 세지 않으므로
-- defer_count를 따로 두고, attempt ordinal 상한은 지연분을 담도록 넓힌다.
ALTER TABLE intelligence_jobs
    ADD COLUMN not_before TIMESTAMPTZ,
    ADD COLUMN defer_count INTEGER NOT NULL DEFAULT 0 CHECK (defer_count >= 0),
    ADD COLUMN queue_reason TEXT;
CREATE INDEX intelligence_jobs_pending_claim_idx
    ON intelligence_jobs(provider, created_at) WHERE status = 'PENDING';

ALTER TABLE intelligence_attempts
    DROP CONSTRAINT intelligence_attempts_status_check,
    ADD CONSTRAINT intelligence_attempts_status_check CHECK (
        status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'EFFECT_UNKNOWN', 'DEFERRED')
    ),
    DROP CONSTRAINT intelligence_attempts_ordinal_check,
    ADD CONSTRAINT intelligence_attempts_ordinal_check CHECK (ordinal > 0 AND ordinal <= 60),
    DROP CONSTRAINT intelligence_attempts_completion_check,
    ADD CONSTRAINT intelligence_attempts_completion_check CHECK (
        (status IN ('RUNNING', 'EFFECT_UNKNOWN') AND completed_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'DEFERRED')
            AND completed_at IS NOT NULL AND completed_at >= started_at)
    );

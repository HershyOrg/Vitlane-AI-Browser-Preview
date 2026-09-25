DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM managed_runner_reservations
        WHERE status = 'UNKNOWN'
    ) THEN
        RAISE EXCEPTION
            'cannot downgrade while managed model UNKNOWN reservations exist'
            USING ERRCODE = '55000';
    END IF;
END $$;

DROP INDEX IF EXISTS managed_runner_reservations_request_key_idx;

ALTER TABLE managed_runner_reservations
    DROP CONSTRAINT managed_runner_reservations_status_check,
	DROP CONSTRAINT managed_runner_reservations_completion_check,
    DROP CONSTRAINT managed_runner_reservations_request_key_check,
    DROP COLUMN request_key,
    ADD CONSTRAINT managed_runner_reservations_status_check
        CHECK (status IN ('HELD', 'SETTLED', 'RELEASED')),
    ADD CONSTRAINT managed_runner_reservations_completion_check CHECK (
        (status = 'HELD' AND completed_at IS NULL)
        OR (status IN ('SETTLED', 'RELEASED') AND completed_at IS NOT NULL)
    );

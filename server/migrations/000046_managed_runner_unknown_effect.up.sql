-- A paid provider request can outlive the caller or the process. Expiry is not
-- proof that the request was never executed, so preserve conservative budget
-- headroom and the stable call key for reconciliation.
ALTER TABLE managed_runner_reservations
    DROP CONSTRAINT managed_runner_reservations_status_check,
	DROP CONSTRAINT managed_runner_reservations_completion_check,
    ADD COLUMN request_key TEXT;

UPDATE managed_runner_reservations
SET request_key = 'legacy:' || id::text
WHERE request_key IS NULL;

ALTER TABLE managed_runner_reservations
    ALTER COLUMN request_key SET NOT NULL,
    ADD CONSTRAINT managed_runner_reservations_request_key_check
        CHECK (btrim(request_key) <> ''),
    ADD CONSTRAINT managed_runner_reservations_status_check
        CHECK (status IN ('HELD', 'SETTLED', 'RELEASED', 'UNKNOWN')),
    ADD CONSTRAINT managed_runner_reservations_completion_check CHECK (
        (status = 'HELD' AND completed_at IS NULL)
        OR (status IN ('SETTLED', 'RELEASED', 'UNKNOWN')
            AND completed_at IS NOT NULL)
    );

CREATE UNIQUE INDEX managed_runner_reservations_request_key_idx
    ON managed_runner_reservations(request_key);

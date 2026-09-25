ALTER TABLE research_rounds
    DROP CONSTRAINT IF EXISTS research_rounds_failure_shape_check;

UPDATE research_rounds
SET status = 'CANCELLED',
    failure_reason_code = NULL,
    failure_retryable = NULL
WHERE status = 'FAILED';

ALTER TABLE research_rounds
    DROP CONSTRAINT IF EXISTS research_rounds_status_check;

ALTER TABLE research_rounds
    ADD CONSTRAINT research_rounds_status_check
        CHECK (status IN (
            'REQUESTED', 'RESULTS_READY', 'NO_RESULTS',
            'CANCELLED', 'SUPERSEDED'
        ));

ALTER TABLE research_rounds
    DROP COLUMN failure_retryable,
    DROP COLUMN failure_reason_code;

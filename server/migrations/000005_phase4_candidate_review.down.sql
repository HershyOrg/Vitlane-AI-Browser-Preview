ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_selected_candidate_fkey;
ALTER TABLE shopping_sessions
    DROP COLUMN IF EXISTS selected_candidate_id;

DROP TABLE IF EXISTS research_feedback;
DROP TABLE IF EXISTS candidate_decisions;

ALTER TABLE candidates
    DROP CONSTRAINT IF EXISTS candidates_id_session_unique;

UPDATE shopping_sessions
SET status = 'REVIEWING'
WHERE status = 'SELECTED';

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check;
ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY', 'RESEARCHING', 'REVIEWING'));

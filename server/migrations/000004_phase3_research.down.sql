DROP TABLE IF EXISTS candidates;

ALTER TABLE research_rounds
    DROP CONSTRAINT IF EXISTS research_rounds_result_submission_fkey;

DROP TABLE IF EXISTS research_submissions;

ALTER TABLE shopping_sessions
    DROP COLUMN IF EXISTS current_research_round_id;

DROP TABLE IF EXISTS research_rounds;
DROP TABLE IF EXISTS agent_capability_sessions;

UPDATE shopping_sessions
SET status = 'READY'
WHERE status IN ('RESEARCHING', 'REVIEWING');

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check;
ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY'));

DELETE FROM agent_capabilities
WHERE kind = 'RESEARCH';

ALTER TABLE agent_capabilities
    DROP CONSTRAINT IF EXISTS agent_capabilities_kind_check;
ALTER TABLE agent_capabilities
    ADD CONSTRAINT agent_capabilities_kind_check
        CHECK (kind IN ('PLANNING'));

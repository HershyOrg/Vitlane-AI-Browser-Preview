DROP INDEX IF EXISTS research_submissions_grant_idempotency_idx;
ALTER TABLE research_submissions
    DROP CONSTRAINT IF EXISTS research_submission_agent_provenance_check,
    DROP COLUMN IF EXISTS agent_grant_id;
ALTER TABLE research_submissions
    ALTER COLUMN agent_capability_id SET NOT NULL;

DROP INDEX IF EXISTS planning_proposals_grant_idempotency_idx;
ALTER TABLE planning_proposals
    DROP CONSTRAINT IF EXISTS planning_proposal_agent_provenance_check,
    DROP COLUMN IF EXISTS agent_grant_id;
ALTER TABLE planning_proposals
    ALTER COLUMN agent_capability_id SET NOT NULL;

ALTER TABLE research_rounds
    DROP COLUMN IF EXISTS last_agent_activity_at,
    DROP COLUMN IF EXISTS first_context_read_at,
    DROP COLUMN IF EXISTS first_discovered_at,
    DROP COLUMN IF EXISTS current_agent_grant_id;

ALTER TABLE planning_tasks
    DROP COLUMN IF EXISTS last_agent_activity_at,
    DROP COLUMN IF EXISTS first_context_read_at,
    DROP COLUMN IF EXISTS first_discovered_at,
    DROP COLUMN IF EXISTS current_agent_grant_id;

DROP TABLE IF EXISTS agent_grant_requests;
DROP TABLE IF EXISTS agent_grant_sessions;
DROP TABLE IF EXISTS agent_grants;
DROP TABLE IF EXISTS oauth_refresh_tokens;
DROP TABLE IF EXISTS oauth_access_tokens;
DROP TABLE IF EXISTS oauth_authorization_codes;
DROP TABLE IF EXISTS agent_connections;
DROP TABLE IF EXISTS agent_oauth_clients;

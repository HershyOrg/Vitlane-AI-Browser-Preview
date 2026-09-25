-- ADR-0040 §10: sensitive operator actions require a recent Google
-- re-authentication. authenticated_at records when the identity provider
-- last verified the person behind this session; existing sessions inherit
-- their creation time.
ALTER TABLE auth_sessions
    ADD COLUMN authenticated_at TIMESTAMPTZ;
UPDATE auth_sessions SET authenticated_at = created_at;
ALTER TABLE auth_sessions
    ALTER COLUMN authenticated_at SET NOT NULL;

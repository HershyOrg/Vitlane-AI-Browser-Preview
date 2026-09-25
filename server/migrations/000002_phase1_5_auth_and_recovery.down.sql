DROP INDEX IF EXISTS idx_shopping_plans_user_updated_at;
DROP INDEX IF EXISTS idx_oauth_login_attempts_expires_at;
DROP INDEX IF EXISTS idx_auth_sessions_active_lookup;
DROP TABLE IF EXISTS plan_creation_requests;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS oauth_login_attempts;
DROP TABLE IF EXISTS external_identities;
ALTER TABLE users
    DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS email;

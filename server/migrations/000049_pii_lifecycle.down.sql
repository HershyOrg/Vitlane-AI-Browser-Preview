-- Purged ciphertext cannot be restored, so this down migration fails closed
-- when purge evidence exists instead of silently re-tightening constraints
-- over rows that would violate them.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM shipping_profiles WHERE purged_at IS NOT NULL)
        OR EXISTS (SELECT 1 FROM users WHERE status = 'DELETED') THEN
        RAISE EXCEPTION
            'purged PII rows exist; the deletion pipeline cannot be rolled back';
    END IF;
END $$;

DROP INDEX IF EXISTS shipping_snapshots_purge_due_idx;
DROP INDEX IF EXISTS user_deletions_pending_idx;

ALTER TABLE shipping_profiles
    DROP CONSTRAINT IF EXISTS shipping_profiles_purge_check,
    DROP COLUMN IF EXISTS purged_at,
    ALTER COLUMN encrypted_payload SET NOT NULL,
    ALTER COLUMN payload_nonce SET NOT NULL;

DROP TABLE IF EXISTS user_deletions;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('ACTIVE', 'DELETION_REQUESTED'));

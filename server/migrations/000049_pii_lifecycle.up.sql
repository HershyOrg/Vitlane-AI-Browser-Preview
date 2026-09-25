-- GAP-020 / ADR-0040 §8: the deletion pipeline needs a terminal DELETED
-- state, a PII-free deletion ledger that survives restores through its
-- offsite export, and a purge shape for shipping_profiles mirroring the one
-- shipping_snapshots has carried since 000016.

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('ACTIVE', 'DELETION_REQUESTED', 'DELETED'));

-- One row per deletion decision. user_id and timestamps only — this ledger
-- is exported offsite and replayed after a restore, so it must never carry
-- PII. Rows are never deleted while the user row exists.
CREATE TABLE user_deletions (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    requested_at TIMESTAMPTZ NOT NULL,
    purged_at TIMESTAMPTZ,
    snapshots_retained BOOLEAN,
    CONSTRAINT user_deletions_purge_check CHECK (
        purged_at IS NULL OR purged_at >= requested_at
    ),
    CONSTRAINT user_deletions_retained_check CHECK (
        (purged_at IS NULL) = (snapshots_retained IS NULL)
    )
);

-- Users who asked for deletion before this migration enter the pipeline with
-- their last transition time as the request time.
INSERT INTO user_deletions(user_id, requested_at)
SELECT id, updated_at FROM users WHERE status = 'DELETION_REQUESTED'
ON CONFLICT (user_id) DO NOTHING;

-- Purging a profile erases the ciphertext in place; the row survives because
-- snapshots reference it with ON DELETE RESTRICT.
ALTER TABLE shipping_profiles
    ALTER COLUMN encrypted_payload DROP NOT NULL,
    ALTER COLUMN payload_nonce DROP NOT NULL,
    ADD COLUMN purged_at TIMESTAMPTZ,
    ADD CONSTRAINT shipping_profiles_purge_check CHECK (
        (purged_at IS NULL
            AND encrypted_payload IS NOT NULL AND payload_nonce IS NOT NULL)
        OR (purged_at IS NOT NULL
            AND encrypted_payload IS NULL AND payload_nonce IS NULL)
    );

CREATE INDEX user_deletions_pending_idx
    ON user_deletions(requested_at) WHERE purged_at IS NULL;
CREATE INDEX shipping_snapshots_purge_due_idx
    ON shipping_snapshots(purge_after)
    WHERE purged_at IS NULL AND purge_after IS NOT NULL;

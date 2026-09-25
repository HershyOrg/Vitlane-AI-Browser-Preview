CREATE TABLE account_rate_limit_buckets (
    policy_id TEXT NOT NULL,
    dimension TEXT NOT NULL,
    subject_key_version INTEGER NOT NULL CHECK (subject_key_version > 0),
    subject_hash BYTEA NOT NULL CHECK (octet_length(subject_hash) = 32),
    window_started_at TIMESTAMPTZ NOT NULL,
    window_seconds INTEGER NOT NULL CHECK (window_seconds > 0),
    rule_limit INTEGER NOT NULL CHECK (rule_limit > 0),
    hit_count INTEGER NOT NULL CHECK (hit_count > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (policy_id, dimension, subject_key_version, subject_hash),
    CHECK (expires_at = window_started_at + make_interval(secs => window_seconds))
);

CREATE INDEX idx_account_rate_limit_buckets_expiry
    ON account_rate_limit_buckets(expires_at);

ALTER TABLE wallet_registration_attempts
    ALTER COLUMN nonce DROP NOT NULL,
    ALTER COLUMN message DROP NOT NULL,
    ADD COLUMN secret_cleaned_at TIMESTAMPTZ;

UPDATE wallet_registration_attempts
SET nonce=NULL,
    message=NULL,
    secret_cleaned_at=COALESCE(completed_at, updated_at)
WHERE status <> 'PENDING';

ALTER TABLE wallet_registration_attempts
    ADD CONSTRAINT wallet_registration_attempts_secret_lifecycle CHECK (
        (
            status='PENDING'
            AND nonce IS NOT NULL
            AND message IS NOT NULL
            AND secret_cleaned_at IS NULL
        )
        OR
        (
            status <> 'PENDING'
            AND nonce IS NULL
            AND message IS NULL
            AND secret_cleaned_at IS NOT NULL
        )
    );

ALTER TABLE oauth_login_attempts
    ALTER COLUMN pkce_verifier DROP NOT NULL,
    ADD COLUMN secret_cleaned_at TIMESTAMPTZ;

UPDATE oauth_login_attempts
SET pkce_verifier=NULL,
    secret_cleaned_at=COALESCE(consumed_at, expires_at)
WHERE consumed_at IS NOT NULL OR expires_at <= NOW();

ALTER TABLE oauth_login_attempts
    ADD CONSTRAINT oauth_login_attempts_secret_lifecycle CHECK (
        (pkce_verifier IS NOT NULL AND secret_cleaned_at IS NULL)
        OR
        (pkce_verifier IS NULL AND secret_cleaned_at IS NOT NULL)
    );

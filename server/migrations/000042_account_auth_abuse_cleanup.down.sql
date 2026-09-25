ALTER TABLE oauth_login_attempts
    DROP CONSTRAINT IF EXISTS oauth_login_attempts_secret_lifecycle;

UPDATE oauth_login_attempts
SET pkce_verifier='redacted:' || id::text
WHERE pkce_verifier IS NULL;

ALTER TABLE oauth_login_attempts
    ALTER COLUMN pkce_verifier SET NOT NULL,
    DROP COLUMN IF EXISTS secret_cleaned_at;

ALTER TABLE wallet_registration_attempts
    DROP CONSTRAINT IF EXISTS wallet_registration_attempts_secret_lifecycle;

UPDATE wallet_registration_attempts
SET nonce='redacted:' || encode(nonce_hash, 'hex'),
    message='redacted:' || encode(message_hash, 'hex')
WHERE nonce IS NULL OR message IS NULL;

ALTER TABLE wallet_registration_attempts
    ALTER COLUMN nonce SET NOT NULL,
    ALTER COLUMN message SET NOT NULL,
    DROP COLUMN IF EXISTS secret_cleaned_at;

DROP INDEX IF EXISTS idx_account_rate_limit_buckets_expiry;
DROP TABLE IF EXISTS account_rate_limit_buckets;

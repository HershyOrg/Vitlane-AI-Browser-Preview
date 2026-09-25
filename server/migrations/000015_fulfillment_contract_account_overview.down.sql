DROP INDEX IF EXISTS idx_fulfillment_attempts_request_time;
DROP INDEX IF EXISTS idx_fulfillment_executions_request;
ALTER TABLE fulfillment_executions DROP COLUMN IF EXISTS request_id;
DROP TABLE IF EXISTS fulfillment_results;
DROP TABLE IF EXISTS fulfillment_attempts;
DROP TABLE IF EXISTS fulfillment_requests;
ALTER TABLE checkout_quotes
    DROP COLUMN IF EXISTS fulfillment_spec_hash,
    DROP COLUMN IF EXISTS fulfillment_spec;
ALTER TABLE purchases
    DROP COLUMN IF EXISTS buyer_profile_snapshot_hash,
    DROP COLUMN IF EXISTS buyer_profile_version,
    DROP COLUMN IF EXISTS buyer_profile_id;
DROP TABLE IF EXISTS user_policy_acceptances;
DROP INDEX IF EXISTS idx_buyer_profiles_one_active_default;
DROP TABLE IF EXISTS buyer_profiles;
DROP INDEX IF EXISTS idx_wallets_one_active_default;
ALTER TABLE wallets
    DROP COLUMN IF EXISTS disconnected_at,
    DROP COLUMN IF EXISTS is_default;
ALTER TABLE identity_assurances
    DROP CONSTRAINT IF EXISTS identity_assurances_level_check;
ALTER TABLE identity_assurances
    ADD CONSTRAINT identity_assurances_level_check
    CHECK (level IN (
        'DOJANG_VERIFIED_ADDRESS',
        'DOJANG_TEST_FAUCET',
        'MOCK_KYC'
    ));

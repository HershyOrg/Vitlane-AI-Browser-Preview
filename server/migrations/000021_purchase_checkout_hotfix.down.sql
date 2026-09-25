DROP INDEX IF EXISTS idx_user_approvals_user_idempotency;

ALTER TABLE user_approvals
    DROP CONSTRAINT IF EXISTS user_approvals_test_policy,
    DROP CONSTRAINT IF EXISTS user_approvals_approved_amount_positive,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS policy_version,
    DROP COLUMN IF EXISTS policy_id,
    DROP COLUMN IF EXISTS approved_currency,
    DROP COLUMN IF EXISTS approved_amount,
    DROP COLUMN IF EXISTS quote_hash,
    DROP COLUMN IF EXISTS purchase_hash;

ALTER TABLE purchase_intent_snapshots
    DROP CONSTRAINT IF EXISTS purchase_intent_candidate_configuration_arrays;

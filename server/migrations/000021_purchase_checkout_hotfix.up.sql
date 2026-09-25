-- Drop malformed v2 snapshots before enforcing the runtime array contract.
-- Their Purchase rows are either removed below when they have no history or
-- retained out of the application projection by the required snapshot join.
DELETE FROM purchase_intent_snapshots
WHERE NOT (
    candidate_configuration ? 'fields'
    AND candidate_configuration ? 'selections'
    AND jsonb_typeof(candidate_configuration->'fields') = 'array'
    AND jsonb_typeof(candidate_configuration->'selections') = 'array'
);

-- Remove pre-v2 draft rows that cannot satisfy the current Purchase API
-- contract. Rows with approval/audit/successor history are retained in storage
-- but are excluded by the v2 snapshot join in application reads.
DELETE FROM purchases AS purchase
WHERE purchase.status IN ('DRAFT', 'AWAITING_USER_APPROVAL')
  AND NOT EXISTS (
      SELECT 1
      FROM purchase_intent_snapshots AS snapshot
      WHERE snapshot.purchase_id = purchase.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_approvals AS approval
      WHERE approval.purchase_id = purchase.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM purchases AS successor
      WHERE successor.supersedes_purchase_id = purchase.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM pii_access_audit_events AS audit
      WHERE audit.purchase_id = purchase.id
  );

ALTER TABLE purchase_intent_snapshots
    ADD CONSTRAINT purchase_intent_candidate_configuration_arrays
    CHECK (
        candidate_configuration ? 'fields'
        AND candidate_configuration ? 'selections'
        AND jsonb_typeof(candidate_configuration->'fields') = 'array'
        AND jsonb_typeof(candidate_configuration->'selections') = 'array'
    );

-- One reselection command can reverse both the historical PIN/REJECT decision
-- and the currently selected Candidate. Preserve command replay grouping while
-- allowing one UNDO event per affected Candidate.
ALTER TABLE candidate_decisions
    DROP CONSTRAINT candidate_decisions_user_id_client_command_id_decision_key,
    ADD CONSTRAINT candidate_decisions_user_id_client_command_id_decision_key
        UNIQUE (user_id, client_command_id, decision, candidate_id);

-- The old two-request browser flow could leave a USER_APPROVED Purchase with a
-- Faucet/KYC assurance but no authorization. Return only those inert rows to
-- approval and discard the unusable approval so the exact ownership flow can
-- start again.
UPDATE purchases AS purchase
SET status = 'AWAITING_USER_APPROVAL',
    updated_at = NOW()
WHERE purchase.status = 'USER_APPROVED'
  AND EXISTS (
      SELECT 1
      FROM user_approvals AS approval
      JOIN identity_assurances AS assurance
        ON assurance.id = approval.assurance_id
      WHERE approval.purchase_id = purchase.id
        AND assurance.level <> 'WALLET_OWNERSHIP_ONLY'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM settlement_authorizations AS authz
      WHERE authz.purchase_id = purchase.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM settlement_payments AS payment
      WHERE payment.purchase_id = purchase.id
  );

DELETE FROM user_approvals AS approval
USING purchases AS purchase, identity_assurances AS assurance
WHERE purchase.id = approval.purchase_id
  AND assurance.id = approval.assurance_id
  AND assurance.level <> 'WALLET_OWNERSHIP_ONLY'
  AND purchase.status = 'AWAITING_USER_APPROVAL'
  AND NOT EXISTS (
      SELECT 1
      FROM settlement_authorizations AS authz
      WHERE authz.purchase_id = purchase.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM settlement_payments AS payment
      WHERE payment.purchase_id = purchase.id
  );

ALTER TABLE user_approvals
    ADD COLUMN purchase_hash TEXT,
    ADD COLUMN quote_hash TEXT,
    ADD COLUMN approved_amount NUMERIC(36, 18),
    ADD COLUMN approved_currency CHAR(3),
    ADD COLUMN policy_id TEXT,
    ADD COLUMN policy_version TEXT,
    ADD COLUMN idempotency_key TEXT;

UPDATE user_approvals AS approval
SET purchase_hash = purchase.purchase_hash,
    quote_hash = quote.quote_hash,
    approved_amount = quote.payable_total_amount,
    approved_currency = quote.currency,
    policy_id = 'PHASE5_TEST_SETTLEMENT',
    policy_version = '2026-07-24',
    idempotency_key = 'migration:' || approval.id::text
FROM purchases AS purchase, checkout_quotes AS quote
WHERE purchase.id = approval.purchase_id
  AND quote.id = approval.quote_id;

ALTER TABLE user_approvals
    ALTER COLUMN purchase_hash SET NOT NULL,
    ALTER COLUMN quote_hash SET NOT NULL,
    ALTER COLUMN approved_amount SET NOT NULL,
    ALTER COLUMN approved_currency SET NOT NULL,
    ALTER COLUMN policy_id SET NOT NULL,
    ALTER COLUMN policy_version SET NOT NULL,
    ALTER COLUMN idempotency_key SET NOT NULL,
    ADD CONSTRAINT user_approvals_approved_amount_positive
        CHECK (approved_amount > 0),
    ADD CONSTRAINT user_approvals_test_policy
        CHECK (
            policy_id = 'PHASE5_TEST_SETTLEMENT'
            AND policy_version = '2026-07-24'
        );

CREATE UNIQUE INDEX idx_user_approvals_user_idempotency
    ON user_approvals(user_id, idempotency_key);

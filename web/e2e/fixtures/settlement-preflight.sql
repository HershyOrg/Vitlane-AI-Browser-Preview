-- A persisted local-review database can contain an outbox transaction signed
-- for the previous ephemeral Anvil chain. Remove only the four fixed review
-- users' payment graph before workers start against the next chain.
DO $preflight$
BEGIN
  IF to_regclass('public.purchases') IS NULL THEN
    RETURN;
  END IF;

  -- Local review reuses a dedicated database between ephemeral Anvil runs.
  -- Immutable audit/snapshot triggers permit destructive cleanup only through
  -- the explicit development account-reset boundary.
  PERFORM set_config('vitlane.account_reset', 'on', true);

  EXECUTE $sql$
    DELETE FROM fulfillment_audit_events
    WHERE actor_user_id IN (
      'e5000000-0000-4000-8000-000000000001',
      'e5000000-0000-4000-8000-000000000101',
      'e5000000-0000-4000-8000-000000000102',
      'e5000000-0000-4000-8000-000000000103'
    )
    OR settlement_payment_id IN (
      SELECT payment.id
      FROM settlement_payments payment
      JOIN purchases purchase ON purchase.id=payment.purchase_id
      WHERE purchase.user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    )
  $sql$;
  EXECUTE $sql$
    DELETE FROM pii_access_audit_events
    WHERE purchase_id IN (
      SELECT id FROM purchases
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    )
  $sql$;
  EXECUTE $sql$
    DELETE FROM fulfillment_executions
    WHERE settlement_payment_id IN (
      SELECT payment.id
      FROM settlement_payments payment
      JOIN purchases purchase ON purchase.id=payment.purchase_id
      WHERE purchase.user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    )
  $sql$;
  EXECUTE $sql$
    DELETE FROM fulfillment_requests
    WHERE purchase_id IN (
      SELECT id FROM purchases
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    )
  $sql$;
  EXECUTE $sql$
    DELETE FROM purchase_intent_snapshots
    WHERE purchase_id IN (
      SELECT id FROM purchases
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    )
  $sql$;
  IF EXISTS (
    SELECT 1
    FROM pg_attribute
    WHERE attrelid='public.purchases'::regclass
      AND attname='supersedes_purchase_id'
      AND NOT attisdropped
  ) THEN
    EXECUTE $sql$
      UPDATE purchases
      SET supersedes_purchase_id=NULL
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    $sql$;
  END IF;
  IF to_regclass('public.purchase_origin_request_items') IS NOT NULL THEN
    EXECUTE $sql$
      DELETE FROM purchase_origin_request_items
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    $sql$;
  END IF;
  EXECUTE $sql$
    DELETE FROM purchases
    WHERE user_id IN (
      'e5000000-0000-4000-8000-000000000001',
      'e5000000-0000-4000-8000-000000000101',
      'e5000000-0000-4000-8000-000000000102',
      'e5000000-0000-4000-8000-000000000103'
    )
  $sql$;
  IF to_regclass('public.purchase_origin_requests') IS NOT NULL THEN
    EXECUTE $sql$
      DELETE FROM purchase_origin_requests
      WHERE user_id IN (
        'e5000000-0000-4000-8000-000000000001',
        'e5000000-0000-4000-8000-000000000101',
        'e5000000-0000-4000-8000-000000000102',
        'e5000000-0000-4000-8000-000000000103'
      )
    $sql$;
  END IF;
END
$preflight$;

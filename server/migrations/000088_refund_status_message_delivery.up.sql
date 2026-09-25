ALTER TABLE payment_customer_refunds
    ADD COLUMN support_notified_at timestamptz;

-- Cutover is forward-only for customer notifications. Historical completed
-- refunds predate the typed Messages contract and must not all be replayed to
-- customers when this migration is deployed. Refunds that reach SUCCEEDED
-- after this statement retain NULL and enter the durable notification queue.
UPDATE payment_customer_refunds
SET support_notified_at = updated_at
WHERE state = 'SUCCEEDED';

CREATE INDEX payment_customer_refunds_support_pending_idx
    ON payment_customer_refunds(updated_at, id)
    WHERE state = 'SUCCEEDED' AND support_notified_at IS NULL;

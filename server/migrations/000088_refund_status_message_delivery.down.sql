DROP INDEX IF EXISTS payment_customer_refunds_support_pending_idx;

ALTER TABLE payment_customer_refunds
    DROP COLUMN IF EXISTS support_notified_at;

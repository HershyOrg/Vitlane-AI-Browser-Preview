-- Order Funding: PayPal provider-reported capture economics, per-order Vitlane
-- reserve entries, and planned MerchantPayment obligations. This is a narrow
-- operational funding view, not a general ledger or treasury balance.

ALTER TABLE payment_funds_receipts
    ADD COLUMN processor_fee_minor BIGINT,
    ADD COLUMN net_receivable_minor BIGINT,
    ADD CONSTRAINT payment_funds_receipts_paypal_economics_check CHECK (
        (
            kind <> 'PAYPAL_CAPTURE'
            AND processor_fee_minor IS NULL
            AND net_receivable_minor IS NULL
        )
        OR (
            kind = 'PAYPAL_CAPTURE'
            AND (
                (processor_fee_minor IS NULL AND net_receivable_minor IS NULL)
                OR (
                    processor_fee_minor >= 0
                    AND net_receivable_minor >= 0
                    AND amount_minor - processor_fee_minor = net_receivable_minor
                )
            )
        )
    );

CREATE TABLE payment_order_reserve_entries (
    id UUID PRIMARY KEY,
    funds_receipt_id UUID NOT NULL
        REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    entry_type TEXT NOT NULL CHECK (entry_type IN ('ALLOCATE','RELEASE')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 2 AND 500),
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_payment_order_reserve_entries_order_created
    ON payment_order_reserve_entries(agency_order_id, created_at DESC);

-- MerchantPayment is the existing merchant-spend ledger. Make its PLANNED
-- state truthful for both existing open orders and all future plans so funding
-- checks include unexecuted merchant obligations without another hold table.
INSERT INTO merchant_payments(
    id, merchant_order_id, agency_order_id, amount_minor, currency,
    state, version, created_at, updated_at
)
SELECT md5(mo.id::text||':payment')::uuid, mo.id, mo.agency_order_id,
       (mo.checkout_snapshot->'authoritativeTotal'->>'amountMinor')::bigint,
       'USD',
       CASE
           WHEN mo.state='PLACED' THEN 'SUCCEEDED'
           WHEN mo.state IN ('FAILED','CANCELLED') THEN 'FAILED'
           WHEN mo.state='PLACEMENT_UNKNOWN' THEN 'OUTCOME_UNKNOWN'
           ELSE 'PLANNED'
       END,
       1, mo.created_at, mo.updated_at
FROM merchant_orders mo
WHERE (mo.checkout_snapshot->'authoritativeTotal'->>'amountMinor')::bigint > 0
  AND NOT EXISTS (
      SELECT 1 FROM merchant_payments payment WHERE payment.merchant_order_id=mo.id
  );

-- This cause is a customer-requested cancellation after placement whose
-- merchant confirmation makes cancellation effective. It is not merchant fault.
ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_cause_check;
UPDATE payment_customer_refunds
SET cause='CUSTOMER_CANCEL_AFTER_PLACEMENT_CONFIRMED'
WHERE cause='MERCHANT_CANCEL_CONFIRMED';
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_cause_check CHECK (cause IN (
        'ORDER_FAILURE','CUSTOMER_REQUEST',
        'CUSTOMER_CANCEL_PRE_EFFECT','DELAY_RULE_CANCEL',
        'MERCHANT_FAULT','CUSTOMER_CANCEL_AFTER_PLACEMENT_CONFIRMED'
    ));

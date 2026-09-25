-- 7-stage cursor를 Step 3 시점의 11-state/terminal_state 축으로 되돌린다.
-- 매핑은 결정적이지만 손실적이다(ISSUED/PAYMENT_PENDING, PAID/PROCESSING 구분은
-- 복원 불가 — 대표값으로 되돌린다).

ALTER TABLE payment_customer_payments
    ADD COLUMN terminal_state TEXT
        CHECK (terminal_state IN ('COMPLETED','REFUND_PENDING','REFUNDED'));
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_terminal_state_succeeded_check
        CHECK (terminal_state IS NULL OR state = 'SUCCEEDED');
UPDATE payment_customer_payments payment SET terminal_state = CASE
        WHEN process.terminal_reason IN ('COMPLETED_ALL','COMPLETED_PARTIAL')
            THEN 'COMPLETED'
        WHEN process.terminal_reason = 'REFUNDED_ALL' THEN 'REFUNDED'
        ELSE NULL
    END
FROM agency_order_processes process
WHERE process.agency_order_id = payment.agency_order_id
  AND payment.state = 'SUCCEEDED'
  AND process.state = 'TERMINAL';

ALTER TABLE agency_order_receipts
    DROP CONSTRAINT agency_order_receipts_terminal_state_check;
UPDATE agency_order_receipts SET terminal_state = CASE terminal_state
        WHEN 'COMPLETED_ALL' THEN 'COMPLETED'
        WHEN 'COMPLETED_PARTIAL' THEN 'COMPLETED'
        WHEN 'REFUNDED_ALL' THEN 'REFUNDED'
        ELSE terminal_state
    END;
ALTER TABLE agency_order_receipts
    ADD CONSTRAINT agency_order_receipts_terminal_state_check
        CHECK (terminal_state IN ('COMPLETED','REFUNDED'));

ALTER TABLE agency_order_processes
    DROP CONSTRAINT agency_order_processes_terminal_reason_check;
ALTER TABLE agency_order_processes
    DROP CONSTRAINT agency_order_processes_state_check;
UPDATE agency_order_processes SET state = CASE state
        WHEN 'WAITING_CUSTOMER_PAYMENT' THEN 'ISSUED'
        WHEN 'PROCUREMENT_IN_PROGRESS' THEN 'PROCESSING'
        WHEN 'LOGISTICS_IN_PROGRESS' THEN 'PROCESSING'
        WHEN 'PAYMENT_RECONCILIATION' THEN 'PAYMENT_ATTENTION'
        WHEN 'ATTENTION_REQUIRED' THEN 'PROCESSING_ATTENTION'
        WHEN 'RESOLUTION_IN_PROGRESS' THEN 'REFUND_PENDING'
        ELSE CASE terminal_reason
            WHEN 'REFUNDED_ALL' THEN 'REFUNDED'
            WHEN 'CANCELLED' THEN 'CANCELLED'
            WHEN 'EXPIRED' THEN 'EXPIRED'
            ELSE 'COMPLETED'
        END
    END;
ALTER TABLE agency_order_processes DROP COLUMN terminal_reason;
ALTER TABLE agency_order_processes
    ADD CONSTRAINT agency_order_processes_state_check CHECK (state IN (
        'ISSUED','PAYMENT_PENDING','PAID','PROCESSING','COMPLETED',
        'PAYMENT_ATTENTION','PROCESSING_ATTENTION','REFUND_PENDING',
        'REFUNDED','CANCELLED','EXPIRED'
    ));

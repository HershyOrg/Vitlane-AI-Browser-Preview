-- Phase 8 Step 4A(ADR-0052): 주문 진행 상태를 AgencyOrderProcess 7-stage cursor로
-- 재배선하고 Payment의 주문 국면 축(terminal_state)을 제거한다.
--
-- 데이터 inventory: agency_order_processes의 11-state row는 아래 CASE로 전량
-- 결정적으로 매핑된다(모호 row 없음 — 값 밖 상태는 CHECK가 막고 있었다).
-- payment_customer_payments.terminal_state는 process stage/terminal_reason과
-- 환불 원장(payment_customer_refunds)에서 재파생 가능하므로 컬럼째 제거한다.
-- 기존 receipts row의 terminal_state('COMPLETED'|'REFUNDED')는 immutable
-- 증거라 값을 바꾸지 않고 새 vocabulary만 CHECK에 추가한다.

-- 순서가 안전성의 전부다: 행이 있는 DB(운영/로컬 검수)에서 아래 UPDATE가
-- 신 어휘를 기록하는 순간 구 CHECK에 걸리지 않도록, 먼저 CHECK를 구∪신
-- 어휘로 넓힌 뒤 매핑하고, 마지막에 신 어휘 전용으로 좁힌다. (빈 DB만 쓰는
-- CI에서는 어느 순서든 통과해 이 결함이 보이지 않는다.)
ALTER TABLE agency_order_processes
    DROP CONSTRAINT agency_order_processes_state_check;
ALTER TABLE agency_order_processes
    ADD CONSTRAINT agency_order_processes_state_check CHECK (state IN (
        'ISSUED','PAYMENT_PENDING','PAID','PROCESSING','COMPLETED',
        'PAYMENT_ATTENTION','PROCESSING_ATTENTION','REFUND_PENDING',
        'REFUNDED','CANCELLED','EXPIRED',
        'WAITING_CUSTOMER_PAYMENT','PAYMENT_RECONCILIATION',
        'PROCUREMENT_IN_PROGRESS','LOGISTICS_IN_PROGRESS',
        'RESOLUTION_IN_PROGRESS','ATTENTION_REQUIRED','TERMINAL'
    ));

ALTER TABLE agency_order_processes ADD COLUMN terminal_reason TEXT;

UPDATE agency_order_processes process SET
    terminal_reason = CASE process.state
        WHEN 'COMPLETED' THEN CASE WHEN EXISTS (
            SELECT 1 FROM payment_customer_refunds refund
            JOIN payment_customer_payments payment
              ON payment.id = refund.customer_payment_id
            WHERE payment.agency_order_id = process.agency_order_id
              AND refund.state = 'SUCCEEDED'
        ) THEN 'COMPLETED_PARTIAL' ELSE 'COMPLETED_ALL' END
        WHEN 'REFUNDED' THEN 'REFUNDED_ALL'
        WHEN 'CANCELLED' THEN 'CANCELLED'
        WHEN 'EXPIRED' THEN 'EXPIRED'
        ELSE NULL
    END,
    state = CASE process.state
        WHEN 'ISSUED' THEN 'WAITING_CUSTOMER_PAYMENT'
        WHEN 'PAYMENT_PENDING' THEN 'WAITING_CUSTOMER_PAYMENT'
        WHEN 'PAID' THEN 'PROCUREMENT_IN_PROGRESS'
        WHEN 'PROCESSING' THEN 'PROCUREMENT_IN_PROGRESS'
        WHEN 'PAYMENT_ATTENTION' THEN 'PAYMENT_RECONCILIATION'
        WHEN 'PROCESSING_ATTENTION' THEN 'ATTENTION_REQUIRED'
        WHEN 'REFUND_PENDING' THEN 'RESOLUTION_IN_PROGRESS'
        ELSE 'TERMINAL'
    END;

ALTER TABLE agency_order_processes
    DROP CONSTRAINT agency_order_processes_state_check;
ALTER TABLE agency_order_processes
    ADD CONSTRAINT agency_order_processes_state_check CHECK (state IN (
        'WAITING_CUSTOMER_PAYMENT','PAYMENT_RECONCILIATION',
        'PROCUREMENT_IN_PROGRESS','LOGISTICS_IN_PROGRESS',
        'RESOLUTION_IN_PROGRESS','ATTENTION_REQUIRED','TERMINAL'
    ));
ALTER TABLE agency_order_processes
    ADD CONSTRAINT agency_order_processes_terminal_reason_check CHECK (
        ((state = 'TERMINAL') = (terminal_reason IS NOT NULL))
        AND (terminal_reason IS NULL OR terminal_reason IN (
            'COMPLETED_ALL','COMPLETED_PARTIAL','REFUNDED_ALL','CANCELLED','EXPIRED'
        ))
    );

ALTER TABLE agency_order_receipts
    DROP CONSTRAINT agency_order_receipts_terminal_state_check;
ALTER TABLE agency_order_receipts
    ADD CONSTRAINT agency_order_receipts_terminal_state_check CHECK (terminal_state IN (
        'COMPLETED','REFUNDED',
        'COMPLETED_ALL','COMPLETED_PARTIAL','REFUNDED_ALL'
    ));

ALTER TABLE payment_customer_payments DROP COLUMN terminal_state CASCADE;

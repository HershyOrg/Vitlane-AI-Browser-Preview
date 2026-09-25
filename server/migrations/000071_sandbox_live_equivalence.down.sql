-- ADR-0053 어휘 복원(데이터 비복원 — 삭제된 구버전 주문 그래프는 되돌리지
-- 않는다, ADR-0053 §4). 신 모델 Sandbox `PLACED` row가 존재하면 mode-조건
-- CHECK 복원이 fail-close된다 — 신 모델 데이터는 구 모델로 되돌릴 수 없다.

ALTER TABLE logistics_expected_units DROP CONSTRAINT logistics_expected_units_fulfillment_check;
ALTER TABLE logistics_expected_units ADD CONSTRAINT logistics_expected_units_fulfillment_check CHECK (fulfillment IN (
    'AWAITING_EFFECT','IN_TRANSIT_EXPECTED','DELIVERED_EXPECTED',
    'MISSING','WRONG_ACTUAL','LOST','RETURNED',
    'DELIVERY_RESOLUTION_PENDING','NONCONFORMING_RESOLUTION_PENDING',
    'RESOLVED','NONCONFORMING_RESOLVED',
    'SUPERSEDED_BY_CANCELLATION','NO_PLACEMENT','SIMULATED_NO_EFFECT'
));

ALTER TABLE merchant_order_units DROP CONSTRAINT merchant_order_units_disposition_check;
ALTER TABLE merchant_order_units ADD CONSTRAINT merchant_order_units_disposition_check CHECK (disposition IN (
    'PENDING','CUSTOMER_REFUND_DUE','CUSTOMER_REFUND_SATISFIED',
    'ZERO_VALUE_SATISFIED','NO_PAYMENT_EFFECT','SIMULATED_NO_REAL_FULFILLMENT'
));

ALTER TABLE merchant_orders DROP CONSTRAINT merchant_orders_state_check;
ALTER TABLE merchant_orders ADD CONSTRAINT merchant_orders_state_check CHECK (state IN (
    'PLANNED','READY_TO_PLACE','PLACEMENT_PENDING','PLACED',
    'PLACEMENT_UNKNOWN','FAILED','CANCELLED','SIMULATED'
));
ALTER TABLE merchant_orders ADD CONSTRAINT merchant_orders_check
    CHECK (state <> 'SIMULATED' OR execution_mode = 'SIMULATED_NO_EFFECT');
ALTER TABLE merchant_orders ADD CONSTRAINT merchant_orders_check1
    CHECK (state NOT IN ('READY_TO_PLACE','PLACEMENT_PENDING','PLACED','PLACEMENT_UNKNOWN')
           OR execution_mode = 'LIVE_MERCHANT_EFFECT');

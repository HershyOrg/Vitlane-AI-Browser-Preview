-- Phase 8 Step 5B(ADR-0052): 배송 예외 해소·30일 지연 rule·단순변심 OFF 게이트.
--
-- 1) DeliveryResolution: MISSING/WRONG_ACTUAL/LOST unit의 운영자 판정
--    write-once. REFUND 판정은 Payment의 MERCHANT_FAULT GROSS 환불 arm이
--    소비한다(물류 RESOLVED만으로 money terminal이 되지 않는다 — §7).
-- 2) Return 수동 lane(§9.3 축소): 오배송·하자 실물 회수와 merchant disposition,
--    간이 회수 원장 연결.
-- 3) 30일 지연 rule(FTC): 취소 kind DELAY_RULE 허용. SYSTEM 고지는
--    agency_order_customer_notices idempotency_key로 멱등이다.
-- 4) 단순변심 OFF: RefundRequest에 typed reason_code — 과실 계열만 허용한다.

CREATE TABLE logistics_delivery_resolutions (
    id UUID PRIMARY KEY,
    expected_unit_id UUID NOT NULL UNIQUE
        REFERENCES logistics_expected_units(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    -- 판정 시점의 예외 종류를 고정한다(이후 fulfillment가 바뀌어도 증거 유지).
    cause TEXT NOT NULL CHECK (cause IN ('MISSING','WRONG_ACTUAL','LOST')),
    decision TEXT NOT NULL CHECK (decision IN ('REFUND','DELIVERED_OK')),
    note TEXT CHECK (note IS NULL OR char_length(note) <= 2000),
    decided_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_logistics_delivery_resolutions_order
    ON logistics_delivery_resolutions(agency_order_id);

CREATE TABLE logistics_returns (
    id UUID PRIMARY KEY,
    expected_unit_id UUID NOT NULL UNIQUE
        REFERENCES logistics_expected_units(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN (
        'REQUESTED','RETURN_IN_TRANSIT','RECEIVED','MERCHANT_RETURNED',
        'CLOSED','CANCELLED'
    )),
    -- 실물 처분은 merchant 회수 원장과 연결된다(간이 원장 — ADR-0052 §10.4).
    merchant_disposition TEXT CHECK (merchant_disposition IS NULL OR merchant_disposition IN (
        'RESTOCKED','MERCHANT_REFUNDED','DISCARDED','UNRESOLVED'
    )),
    note TEXT CHECK (note IS NULL OR char_length(note) <= 2000),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- 30일 지연 rule 취소(FTC): PRE_EFFECT 외 DELAY_RULE kind를 허용한다.
ALTER TABLE agency_order_cancellations
    DROP CONSTRAINT agency_order_cancellations_kind_check;
ALTER TABLE agency_order_cancellations
    ADD CONSTRAINT agency_order_cancellations_kind_check
        CHECK (kind IN ('PRE_EFFECT','DELAY_RULE'));

-- 단순변심 OFF 게이트: 과실 계열 typed reason만 접수한다(ADR-0052 §1 —
-- CHANGE_OF_MIND는 어휘에 없다: 구조적으로 요청 불가).
ALTER TABLE agency_order_refund_requests
    ADD COLUMN reason_code TEXT NOT NULL DEFAULT 'OTHER_SERVICE_FAULT'
        CHECK (reason_code IN (
            'ITEM_NOT_RECEIVED','ITEM_DAMAGED_DEFECTIVE','WRONG_ITEM_RECEIVED',
            'ORDER_DELAYED','OTHER_SERVICE_FAULT'
        ));
ALTER TABLE agency_order_refund_requests
    ALTER COLUMN reason_code DROP DEFAULT;
-- 상세 사유는 선택 입력이다(코드가 필수, 텍스트는 보조).
ALTER TABLE agency_order_refund_requests
    DROP CONSTRAINT agency_order_refund_requests_reason_check;
ALTER TABLE agency_order_refund_requests
    ADD CONSTRAINT agency_order_refund_requests_reason_check
        CHECK (char_length(reason) <= 500);

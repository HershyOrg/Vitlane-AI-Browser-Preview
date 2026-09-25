-- Phase 8 Step 4C(ADR-0052): LiveExecute 모드 계약과 취소·FEE_RETAINED 환불.
--
-- 1) 환불 cause taxonomy(§2.5)와 FEE_RETAINED basis. 고객 사유 cause는 slice의
--    pass-through 컴포넌트만 환불하고 fee는 RETAINED로 종결한다(신 disclosure
--    이후 발행 주문 한정 — 구주문은 GROSS fallback을 코드가 소유한다).
-- 2) LIVE_MERCHANT_EFFECT의 실행 증거 축: MerchantPayment(계획된 지출)와
--    MerchantCharge(관찰된 실제 debit) — Step 6 활성화 전에는 write 경로가
--    fail-close라 row가 생기지 않는다.
-- 3) merchant 회수 간이 원장(ADR-0052 §5.2 — slice 기계 대신 checkout 스코프).
-- 4) 결제 후·merchant effect 전 고객 취소의 write-once 기록.

ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_basis_check;
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_basis_check
        CHECK (basis IN ('ORDER_SLICES','ORDER_SLICES_PASS_THROUGH'));
ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_cause_check;
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_cause_check CHECK (cause IN (
        'ORDER_FAILURE','CUSTOMER_REQUEST',
        'CUSTOMER_CANCEL_PRE_EFFECT','DELAY_RULE_CANCEL',
        'MERCHANT_FAULT','MERCHANT_CANCEL_CONFIRMED'
    ));

ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED',
        'CUSTOMER_CANCELLED','MERCHANT_ORDER_PLACED'
    ));

CREATE TABLE agency_order_cancellations (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind = 'PRE_EFFECT'),
    refund_basis TEXT NOT NULL CHECK (refund_basis IN ('GROSS','FEE_RETAINED')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE merchant_payments (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL UNIQUE REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    state TEXT NOT NULL CHECK (state IN (
        'PLANNED','EXECUTION_PENDING','SUCCEEDED','FAILED','OUTCOME_UNKNOWN',
        'NONCONFORMING_CHARGE'
    )),
    -- 결제수단 원문 금지 — 운영자 계정의 non-secret safe reference만 둔다.
    payment_method_safe_ref TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE merchant_charges (
    id UUID PRIMARY KEY,
    merchant_payment_id UUID REFERENCES merchant_payments(id) ON DELETE RESTRICT,
    merchant_order_id UUID REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    shop_domain TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    kind TEXT NOT NULL CHECK (kind IN ('EXPECTED','NONCONFORMING','EXCESS')),
    evidence_ref TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (shop_domain, evidence_ref)
);

-- 간이 회수 원장(ADR-0052 §5.2): checkout 스코프, 수동 대사. 고객 환불과
-- 절연된 Vitlane 방향 회수만 추적한다.
CREATE TABLE procurement_recovery_entries (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    cause TEXT NOT NULL CHECK (cause IN (
        'CHARGE_WITHOUT_ORDER','MERCHANT_CANCEL','RETURN','COST_ADJUSTMENT','OTHER'
    )),
    expected_amount_minor BIGINT NOT NULL CHECK (expected_amount_minor >= 0),
    received_amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (received_amount_minor >= 0),
    state TEXT NOT NULL CHECK (state IN (
        'EXPECTED','RECEIVED','WAIVED','LOSS','OVER_RECOVERED'
    )),
    evidence_ref TEXT,
    note TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

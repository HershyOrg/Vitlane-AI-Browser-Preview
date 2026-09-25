-- Phase 8 Step 3 PR-2 (ADR-0050): 상품 unit 단위 환불 — 발행 시 고정 slice,
-- 고객 환불 요청/심사, PayPal 부분 환불 claim.

CREATE TABLE agency_order_refund_slices (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    line_id TEXT NOT NULL,
    unit_index INTEGER NOT NULL CHECK (unit_index > 0),
    shop_domain TEXT NOT NULL,
    pass_through_cent BIGINT NOT NULL CHECK (pass_through_cent >= 0),
    fee_variable_cent BIGINT NOT NULL CHECK (fee_variable_cent >= 0),
    fee_fixed_cent BIGINT NOT NULL CHECK (fee_fixed_cent >= 0),
    slice_total_cent BIGINT NOT NULL CHECK (
        slice_total_cent = pass_through_cent + fee_variable_cent + fee_fixed_cent
    ),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, line_id, unit_index)
);

CREATE TABLE agency_order_refund_requests (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('REQUESTED','REVIEWING','RESOLVED')),
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 2 AND 500),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_refund_requests_queue
    ON agency_order_refund_requests(state, created_at);

CREATE TABLE agency_order_refund_request_items (
    id UUID PRIMARY KEY,
    request_id UUID NOT NULL REFERENCES agency_order_refund_requests(id) ON DELETE RESTRICT,
    slice_id UUID NOT NULL REFERENCES agency_order_refund_slices(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('PENDING','APPROVED','REJECTED')),
    decision_reason TEXT,
    decided_by UUID REFERENCES users(id) ON DELETE RESTRICT,
    decided_at TIMESTAMPTZ,
    UNIQUE (request_id, slice_id)
);

-- 같은 slice에 미종결(PENDING/APPROVED) 요청 항목은 최대 하나 — 이중 요청·이중
-- 승인을 요청 층에서 차단한다. 거절된 항목은 재요청을 막지 않는다.
CREATE UNIQUE INDEX idx_agency_order_refund_request_items_open
    ON agency_order_refund_request_items(slice_id)
    WHERE state IN ('PENDING','APPROVED');

-- Payment: slice 단위 환불 claim. slice 하나는 CustomerRefund 하나에만 귀속된다
-- (ADR-0050 슬림 코어 불변 3 — 의무 중복 금지).
CREATE TABLE payment_refund_slice_claims (
    id UUID PRIMARY KEY,
    customer_refund_id UUID NOT NULL
        REFERENCES payment_customer_refunds(id) ON DELETE RESTRICT,
    funds_receipt_id UUID NOT NULL
        REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    slice_id UUID NOT NULL REFERENCES agency_order_refund_slices(id) ON DELETE RESTRICT,
    amount_cent BIGINT NOT NULL CHECK (amount_cent >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (funds_receipt_id, slice_id)
);

-- 환불 basis 일반화: FULL_ORDER_FAILURE 전용 스키마를 ORDER_SLICES(부분집합)로
-- 통일한다. PR-1은 아직 배포 전이므로 rows가 없어 안전한 교체다.
ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_basis_check,
    DROP CONSTRAINT payment_customer_refunds_obligation_kind_check,
    DROP CONSTRAINT payment_customer_refunds_customer_payment_id_basis_key,
    ADD CONSTRAINT payment_customer_refunds_basis_check
        CHECK (basis IN ('ORDER_SLICES')),
    ADD CONSTRAINT payment_customer_refunds_obligation_kind_check
        CHECK (obligation_kind IN ('MANDATORY_SYSTEM','POLICY_APPROVED')),
    ADD COLUMN cause TEXT NOT NULL DEFAULT 'ORDER_FAILURE'
        CHECK (cause IN ('ORDER_FAILURE','CUSTOMER_REQUEST'));
ALTER TABLE payment_customer_refunds ALTER COLUMN cause DROP DEFAULT;

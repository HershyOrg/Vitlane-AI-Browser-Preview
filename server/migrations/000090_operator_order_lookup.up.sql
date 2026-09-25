-- 운영자 exact order lookup: 권위 owner 테이블은 읽기 전용으로 대사하고,
-- 원문 query는 저장하지 않은 채 누가 어떤 종류를 조회해 어떤 결과를 얻었는지
-- append-only로 감사한다.

CREATE TABLE ordering_operator_order_lookup_audits (
    id UUID PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('LOOKUP','DETAIL_VIEW')),
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    identifier_type TEXT NOT NULL CHECK (identifier_type IN (
        'AUTO','AGENCY_ORDER_ID','PAYMENT_ID','PAYPAL_ORDER_ID',
        'PAYPAL_CAPTURE_ID','PAYPAL_REFUND_ID','MERCHANT_ORDER_ID',
        'MERCHANT_ORDER_REF','SHIPMENT_ID','TRACKING_REF','GIWA_TX_HASH'
    )),
    environment TEXT NOT NULL CHECK (environment IN ('ANY','SANDBOX','TESTNET','LIVE')),
    query_hash TEXT NOT NULL CHECK (char_length(query_hash) = 64),
    qualifier_hash TEXT CHECK (qualifier_hash IS NULL OR char_length(qualifier_hash) = 64),
    outcome TEXT NOT NULL CHECK (outcome IN ('MATCHED','NOT_FOUND','AMBIGUOUS')),
    matched_by TEXT CHECK (matched_by IS NULL OR matched_by IN (
        'AGENCY_ORDER_ID','PAYMENT_ID','PAYPAL_ORDER_ID','PAYPAL_CAPTURE_ID',
        'PAYPAL_REFUND_ID','MERCHANT_ORDER_ID','MERCHANT_ORDER_REF',
        'SHIPMENT_ID','TRACKING_REF','GIWA_TX_HASH'
    )),
    agency_order_id UUID REFERENCES agency_orders(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_operator_order_lookup_audits_actor_time
    ON ordering_operator_order_lookup_audits(operator_user_id, created_at DESC);
CREATE INDEX idx_operator_order_lookup_audits_order_time
    ON ordering_operator_order_lookup_audits(agency_order_id, created_at DESC)
    WHERE agency_order_id IS NOT NULL;

CREATE INDEX idx_payment_paypal_attempts_capture
    ON payment_paypal_attempts(capture_id) WHERE capture_id IS NOT NULL;
CREATE INDEX idx_merchant_orders_external_ref_lookup
    ON merchant_orders(external_order_ref) WHERE external_order_ref IS NOT NULL;
CREATE INDEX idx_logistics_shipments_tracking_lookup
    ON logistics_shipments(tracking_ref);

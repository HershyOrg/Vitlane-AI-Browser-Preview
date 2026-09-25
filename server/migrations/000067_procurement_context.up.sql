-- Phase 8 Step 4B(ADR-0052): Procurement Bounded Context 수직 절편.
--
-- 합류 지점을 accepted CustomerFundsReceipt handoff로 전환한다: Payment의
-- accepted receipt(payment_funds_receipts.accepted)가 durable fact이고,
-- Procurement가 inbox로 원자 소비해 manifest/expected unit/Shop별
-- MerchantOrder/Unit/Task root를 전수 생성한다.
--
-- 기존 agency_order_execution_units는 신규 write 0의 immutable archive로
-- 동결한다(테이블·row·감사 보존, 코드 제거로 보장). 감사 테이블은 연속성을
-- 위해 재사용하며 merchant_order 참조 축을 추가한다.

CREATE TABLE procurement_receipt_inbox (
    funds_receipt_id UUID PRIMARY KEY REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    consumed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE procurement_manifests (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT,
    funds_receipt_id UUID NOT NULL UNIQUE REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    snapshot_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE merchant_orders (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    manifest_id UUID NOT NULL REFERENCES procurement_manifests(id) ON DELETE RESTRICT,
    merchant_id TEXT NOT NULL,
    shop_domain TEXT NOT NULL,
    checkout_ordinal INTEGER NOT NULL CHECK (checkout_ordinal > 0),
    checkout_snapshot JSONB NOT NULL,
    execution_mode TEXT NOT NULL CHECK (execution_mode IN (
        'SIMULATED_NO_EFFECT','LIVE_MERCHANT_EFFECT'
    )),
    state TEXT NOT NULL CHECK (state IN (
        'PLANNED','READY_TO_PLACE','PLACEMENT_PENDING','PLACED',
        'PLACEMENT_UNKNOWN','FAILED','CANCELLED','SIMULATED'
    )),
    -- SIMULATED terminal은 SIMULATED_NO_EFFECT 모드에서만, 실제 placement
    -- lifecycle은 LIVE_MERCHANT_EFFECT 모드에서만 가능하다.
    CHECK (state <> 'SIMULATED' OR execution_mode = 'SIMULATED_NO_EFFECT'),
    CHECK (state NOT IN ('READY_TO_PLACE','PLACEMENT_PENDING','PLACED','PLACEMENT_UNKNOWN')
           OR execution_mode = 'LIVE_MERCHANT_EFFECT'),
    failure_code TEXT,
    -- 실제 Shop 주문 참조는 LIVE 모드에서만 존재한다. SIMULATED는 실제처럼
    -- 보이는 외부 참조를 만들지 않는다(v6 §8.1).
    external_order_ref TEXT,
    CHECK (external_order_ref IS NULL OR execution_mode = 'LIVE_MERCHANT_EFFECT'),
    result_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, checkout_ordinal)
);

CREATE UNIQUE INDEX idx_merchant_orders_external_ref
    ON merchant_orders(shop_domain, external_order_ref)
    WHERE external_order_ref IS NOT NULL;

CREATE TABLE merchant_order_units (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    line_id TEXT NOT NULL,
    unit_index INTEGER NOT NULL CHECK (unit_index > 0),
    slice_id UUID NOT NULL UNIQUE REFERENCES agency_order_refund_slices(id) ON DELETE RESTRICT,
    disposition TEXT NOT NULL CHECK (disposition IN (
        'PENDING','CUSTOMER_REFUND_DUE','CUSTOMER_REFUND_SATISFIED',
        'ZERO_VALUE_SATISFIED','NO_PAYMENT_EFFECT','SIMULATED_NO_REAL_FULFILLMENT'
    )),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, line_id, unit_index)
);

CREATE TABLE merchant_order_execution_tasks (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL UNIQUE REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN (
        'QUEUED','CLAIMED','IN_PROGRESS','SUCCEEDED','FAILED','CANCELLED','OUTCOME_UNKNOWN'
    )),
    assigned_operator_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ,
    lease_until TIMESTAMPTZ,
    handled_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((state IN ('CLAIMED','IN_PROGRESS')) = (assigned_operator_user_id IS NOT NULL AND lease_until IS NOT NULL)
           OR state IN ('SUCCEEDED','FAILED','CANCELLED','OUTCOME_UNKNOWN'))
);

CREATE INDEX idx_merchant_order_tasks_state_updated
    ON merchant_order_execution_tasks(state, updated_at);

-- 감사 연속성: 기존 append-only 감사 테이블을 재사용하고 merchant_order 축을
-- 추가한다. 과거 row(execution_unit 축)는 불변이다.
ALTER TABLE agency_order_execution_audits
    ALTER COLUMN execution_unit_id DROP NOT NULL;
ALTER TABLE agency_order_execution_audits
    ADD COLUMN merchant_order_id UUID REFERENCES merchant_orders(id) ON DELETE RESTRICT;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_subject_check
        CHECK ((execution_unit_id IS NOT NULL) OR (merchant_order_id IS NOT NULL));
ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED'
    ));

ALTER TABLE agency_order_pii_access_audits
    ALTER COLUMN execution_unit_id DROP NOT NULL;
-- continue_url 열람 감사는 배송 snapshot을 갖지 않는다.
ALTER TABLE agency_order_pii_access_audits
    ALTER COLUMN shipping_snapshot_id DROP NOT NULL;
ALTER TABLE agency_order_pii_access_audits
    ADD CONSTRAINT agency_order_pii_access_audits_snapshot_check
        CHECK (action <> 'SHIPPING_ADDRESS_REVEAL' OR shipping_snapshot_id IS NOT NULL);
ALTER TABLE agency_order_pii_access_audits
    ADD COLUMN merchant_order_id UUID REFERENCES merchant_orders(id) ON DELETE RESTRICT;
ALTER TABLE agency_order_pii_access_audits
    ADD CONSTRAINT agency_order_pii_access_audits_subject_check
        CHECK ((execution_unit_id IS NOT NULL) OR (merchant_order_id IS NOT NULL));
ALTER TABLE agency_order_pii_access_audits
    DROP CONSTRAINT agency_order_pii_access_audits_action_check;
ALTER TABLE agency_order_pii_access_audits
    ADD CONSTRAINT agency_order_pii_access_audits_action_check CHECK (action IN (
        'SHIPPING_ADDRESS_REVEAL','CONTINUE_URL_REVEAL'
    ));

-- CustomerNotice — 주문 스코프 고지함(ADR-0052 §2.6 최소형, AgencyOrder 소유).
CREATE TABLE agency_order_customer_notices (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('SYSTEM','OPERATOR')),
    -- 본문은 짧은 안내문이다. PII 원문·provider 원문 금지는 코드 계약이 소유한다.
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    idempotency_key TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    read_at TIMESTAMPTZ
);

CREATE INDEX idx_agency_order_customer_notices_user
    ON agency_order_customer_notices(user_id, created_at DESC);

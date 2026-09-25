-- Phase 8 Step 5A(ADR-0052): Logistics core — expected unit 등록, Shipment/
-- allocation, 수동 tracking evidence(append-only)와 unit fulfillment.
--
-- Phase 8의 유일한 물리 진실 입력은 운영자 evidence다(자동 carrier polling
-- 비범위). SIMULATED lane은 실물 이동이 없으므로 등록 즉시 SIMULATED_NO_EFFECT
-- 로 닫히고, LIVE lane의 merchant effect는 등록 ACK가 barrier다.

CREATE TABLE logistics_expected_units (
    id UUID PRIMARY KEY,
    merchant_order_unit_id UUID NOT NULL UNIQUE
        REFERENCES merchant_order_units(id) ON DELETE RESTRICT,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    line_id TEXT NOT NULL,
    unit_index INTEGER NOT NULL CHECK (unit_index > 0),
    slice_id UUID NOT NULL REFERENCES agency_order_refund_slices(id) ON DELETE RESTRICT,
    fulfillment TEXT NOT NULL CHECK (fulfillment IN (
        'AWAITING_EFFECT','IN_TRANSIT_EXPECTED','DELIVERED_EXPECTED',
        'MISSING','WRONG_ACTUAL','LOST','RETURNED',
        'DELIVERY_RESOLUTION_PENDING','NONCONFORMING_RESOLUTION_PENDING',
        'RESOLVED','NONCONFORMING_RESOLVED',
        'SUPERSEDED_BY_CANCELLATION','NO_PLACEMENT','SIMULATED_NO_EFFECT'
    )),
    registered_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_logistics_expected_units_order
    ON logistics_expected_units(agency_order_id);

CREATE TABLE logistics_shipments (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    carrier TEXT NOT NULL CHECK (char_length(carrier) BETWEEN 1 AND 100),
    tracking_ref TEXT NOT NULL CHECK (char_length(tracking_ref) BETWEEN 1 AND 200),
    state TEXT NOT NULL CHECK (state IN (
        'CREATED','LABEL_CREATED','IN_TRANSIT','OUT_FOR_DELIVERY','DELIVERED',
        'EXCEPTION','LOST','RETURN_TO_SENDER','RETURNED','CANCELLED_NO_EFFECT',
        'EXCEPTION_RECONCILIATION'
    )),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (carrier, tracking_ref)
);

CREATE TABLE logistics_shipment_allocations (
    id UUID PRIMARY KEY,
    shipment_id UUID NOT NULL REFERENCES logistics_shipments(id) ON DELETE RESTRICT,
    expected_unit_id UUID NOT NULL
        REFERENCES logistics_expected_units(id) ON DELETE RESTRICT,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL
);

-- unit당 active allocation 최대 1 (계약 v7 §9.2 — history는 row로 보존).
CREATE UNIQUE INDEX idx_logistics_allocations_active_unit
    ON logistics_shipment_allocations(expected_unit_id)
    WHERE active;

CREATE TABLE logistics_shipment_events (
    id UUID PRIMARY KEY,
    shipment_id UUID NOT NULL REFERENCES logistics_shipments(id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    note TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    dedupe_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (shipment_id, dedupe_hash)
);

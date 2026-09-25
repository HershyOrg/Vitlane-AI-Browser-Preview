-- ADR-0070: Order-MO-Unit 리듀서.
--
-- agency_order_process_merchant_orders: OrderProcessManager가 결정 transaction에서
-- 갱신하는 MO 단위 결정 사영(phase·attention·intent). process 행과 같은
-- single-writer 규약을 따른다 — ordering/process/infra 밖에서 쓰는 SQL은 0이다.
-- 고객·운영자 사영은 이 행을 JOIN으로 읽는다(결정=Process, 고지=Projection).
--
-- order_process_decisions: 결정 감사 기록(append-only). 권위는 여전히
-- agency_order_processes 행이며 이 테이블을 재생해 상태를 재구축하지 않는다
-- (AGENTS §7). 타임트래블은 version 순으로 스냅샷을 읽는 것이다.

CREATE TABLE agency_order_process_merchant_orders (
    merchant_order_id TEXT PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    allocation_id TEXT,
    phase TEXT NOT NULL CHECK (phase IN (
        'PLANNED','FUNDING','PURCHASING','PLACED','FULFILLING','DELIVERED',
        'COMPENSATING','COMPENSATED','FAILED','CANCELLED'
    )),
    owner_state TEXT NOT NULL,
    funding_state TEXT,
    lock_state TEXT,
    compensation_state TEXT,
    compensation_action TEXT,
    compensation_cause TEXT,
    failure_code TEXT,
    attention_code TEXT,
    attention_command_id TEXT,
    intent_kind TEXT,
    intent_outcome TEXT,
    intent_code TEXT,
    decided_version BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_process_merchant_orders_order
    ON agency_order_process_merchant_orders(agency_order_id);

CREATE TABLE order_process_decisions (
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL,
    seq_from BIGINT NOT NULL,
    seq_to BIGINT NOT NULL,
    stage_before TEXT,
    stage_after TEXT NOT NULL,
    terminal_reason TEXT,
    last_reason_code TEXT,
    stage_changed BOOLEAN NOT NULL,
    merchant_order_diff JSONB NOT NULL DEFAULT '[]'::jsonb,
    commands JSONB NOT NULL DEFAULT '[]'::jsonb,
    wake_at TIMESTAMPTZ,
    workflow JSONB NOT NULL,
    decided_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agency_order_id, version)
);

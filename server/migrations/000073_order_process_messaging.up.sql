-- ADR-0056: OrderProcessManager의 durable 메시지 레일.
--
-- order_process_events: owner Context가 자기 상태 변경과 같은 transaction에서
-- append하는 과거형 사실 기록. 주문별 seq는 발행 transaction이
-- pg_advisory_xact_lock 아래 채번한다 — xact-scope 락이라 seq 순서가 곧 커밋
-- 순서이고, Manager의 커서 소비(seq > last_applied_seq)가 안전하다. seq는
-- 단조이면 되고 조밀할 필요는 없다(dedup으로 버려진 채번은 gap이 된다).
--
-- order_process_commands: Manager만 결정 transaction에서 INSERT하고(발행 컬럼),
-- target owner executor만 실행 컬럼(state·attempt·result)을 갱신한다 —
-- settlement_command_outbox의 컬럼 단위 소유 규약을 일반화한 것이다.
--
-- agency_order_processes: Manager 소비 커서(last_applied_seq), 시간 기반 결정
-- 타이머(wake_at — 30일 지연 rule 고지), 이벤트 fold 관찰 스냅샷(workflow)을
-- 추가한다. 이 테이블의 writer는 컷오버 후 ordering/process 하나다(single-writer).

CREATE TABLE order_process_events (
    id BIGSERIAL PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    seq BIGINT NOT NULL CHECK (seq > 0),
    source TEXT NOT NULL CHECK (source IN (
        'AGENCYORDER','PAYMENT','SETTLEMENT','PROCUREMENT','LOGISTICS',
        'CUSTOMER','OPERATOR','TIMER','WATCHDOG','SYSTEM'
    )),
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    dedup_key TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    -- applied_* 컬럼은 Manager 전용이다(소비 마킹 + 결정 version 감사 연결).
    applied_at TIMESTAMPTZ,
    applied_version BIGINT,
    UNIQUE (agency_order_id, seq),
    UNIQUE (dedup_key),
    CHECK ((applied_at IS NULL) = (applied_version IS NULL))
);

-- full scan 대체: Manager는 미소비 이벤트가 있는 주문만 본다.
CREATE INDEX idx_order_process_events_unapplied
    ON order_process_events(agency_order_id, seq)
    WHERE applied_at IS NULL;

CREATE TABLE order_process_commands (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    target TEXT NOT NULL CHECK (target IN (
        'AGENCYORDER','PAYMENT','PROCUREMENT','LOGISTICS'
    )),
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    -- 결정적 business key — 같은 결정이 재실행돼도 중복 발행이 0이다.
    idempotency_key TEXT NOT NULL,
    caused_by_event_id BIGINT NOT NULL
        REFERENCES order_process_events(id) ON DELETE RESTRICT,
    state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN (
        'PENDING','SUCCEEDED','REJECTED','EXHAUSTED'
    )),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    last_error_code TEXT,
    result JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (idempotency_key),
    CHECK ((state IN ('SUCCEEDED','REJECTED','EXHAUSTED')) = (completed_at IS NOT NULL))
);

CREATE INDEX idx_order_process_commands_due
    ON order_process_commands(target, next_attempt_at)
    WHERE state = 'PENDING';
CREATE INDEX idx_order_process_commands_order
    ON order_process_commands(agency_order_id);

ALTER TABLE agency_order_processes
    ADD COLUMN last_applied_seq BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN wake_at TIMESTAMPTZ,
    ADD COLUMN workflow JSONB NOT NULL DEFAULT '{}';

CREATE INDEX idx_agency_order_processes_wake
    ON agency_order_processes(wake_at)
    WHERE wake_at IS NOT NULL;

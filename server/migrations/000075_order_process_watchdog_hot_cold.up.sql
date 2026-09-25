-- ADR-0056 watchdog 비용을 bounded hot/cold scan으로 제한한다.
-- hot은 최근 process를 updated_at으로 고정 개수만 고르고, cold는 전체
-- agency_order_id keyset을 이 singleton cursor로 재시작 뒤에도 순환한다.

CREATE TABLE order_process_watchdog_cursors (
    lane TEXT PRIMARY KEY CHECK (lane = 'COLD'),
    after_agency_order_id UUID,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_processes_watchdog_hot
    ON agency_order_processes(updated_at DESC, agency_order_id);

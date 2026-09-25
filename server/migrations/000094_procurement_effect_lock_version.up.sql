-- ADR-0070 §4.2: procurement effect lock 전이를 process 이벤트로 보고한다.
-- 이벤트 dedup key는 행 version에 결정적으로 붙으므로(재기록 no-op) lock 행에
-- version을 둔다. 같은 (state, funding_state) 재방문(FUNDING_PENDING →
-- FUNDING_UNKNOWN → FUNDING_PENDING)도 별개 전이로 보고된다.
ALTER TABLE procurement_effect_locks
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1);

-- Support conversation (ADR-0059): 사용자당 단일 연속 1:1 대화. 대화 identity는
-- user_id다 — thread 테이블 없이 "답변 대기"는 마지막 메시지 author로 파생한다.
-- 운영자의 주문별 "고객 안내"는 이 대화의 주문 첨부 메시지로 흡수된다.
-- (SYSTEM 지연 rule 고지는 process 정합 결합 때문에 기존
--  agency_order_customer_notices 레일에 남는다 — ADR-0059 §4.)

CREATE TABLE support_messages (
    id UUID PRIMARY KEY,
    -- 대화를 소유한 고객. 운영자 답변도 고객의 user_id 아래에 쌓인다.
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    author TEXT NOT NULL CHECK (author IN ('CUSTOMER','OPERATOR')),
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    -- 구조화 주문 참조 — 링크는 이 필드로만 성립한다(본문 파싱 링크화 금지,
    -- ADR-0059 §5). 고객·운영자 대칭으로 첨부하며, 소유권 검증(고객=자기
    -- 주문, 운영자=대상 고객 소유 주문)은 app 계층이 강제한다.
    agency_order_id UUID REFERENCES agency_orders(id) ON DELETE RESTRICT,
    -- OPERATOR 메시지의 작성 운영자.
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    idempotency_key TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    -- 고객 읽음 워터마크(OPERATOR 메시지 전용). 읽음 처리는 set-based UPDATE다.
    customer_read_at TIMESTAMPTZ
);

CREATE INDEX idx_support_messages_user_created
    ON support_messages(user_id, created_at DESC, id DESC);

-- 고객 안 읽음 뱃지 COUNT 전용 부분 인덱스.
CREATE INDEX idx_support_messages_customer_unread
    ON support_messages(user_id)
    WHERE author <> 'CUSTOMER' AND customer_read_at IS NULL;

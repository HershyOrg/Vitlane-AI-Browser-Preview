-- ADR-0062: 고객에게 메시지를 보내지 않고 마지막 고객 메시지를 운영 완료로
-- 처리하는 append-only 기록. 메시지 원문과 author는 불변으로 둔다.

CREATE TABLE support_no_reply_resolutions (
    customer_message_id UUID PRIMARY KEY
        REFERENCES support_messages(id) ON DELETE CASCADE,
    handled_by_user_id UUID NOT NULL
        REFERENCES users(id) ON DELETE RESTRICT,
    handled_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_support_no_reply_resolutions_handler
    ON support_no_reply_resolutions(handled_by_user_id, handled_at DESC);

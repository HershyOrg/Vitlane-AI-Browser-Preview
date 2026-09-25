-- 이전 application image로 rollback할 수 있도록 빈 schema만 복원한다.
-- up migration에서 삭제된 문의 row는 backup 없이는 복구되지 않는다.
CREATE TABLE marketing_inquiries (
    id UUID PRIMARY KEY,
    message TEXT NOT NULL CHECK (
        char_length(btrim(message)) BETWEEN 1 AND 2000
    ),
    email TEXT NOT NULL CHECK (
        email = lower(btrim(email)) AND char_length(email) BETWEEN 3 AND 254
    ),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'IN_REVIEW', 'REPLIED', 'ARCHIVED')),
    idempotency_key TEXT NOT NULL UNIQUE
        CHECK (char_length(idempotency_key) BETWEEN 16 AND 120),
    request_hash BYTEA NOT NULL,
    request_fingerprint BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX marketing_inquiries_created_idx
    ON marketing_inquiries(created_at DESC);
CREATE INDEX marketing_inquiries_status_created_idx
    ON marketing_inquiries(status, created_at DESC);
CREATE INDEX marketing_inquiries_email_created_idx
    ON marketing_inquiries(email, created_at DESC);
CREATE INDEX marketing_inquiries_fingerprint_created_idx
    ON marketing_inquiries(request_fingerprint, created_at DESC);

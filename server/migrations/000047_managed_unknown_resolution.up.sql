-- GAP-024 / ADR-0040 §9: an UNKNOWN cost reservation may only be closed by an
-- audited operator decision backed by provider evidence. The primary key on
-- reservation_id makes the resolution exactly-once at the database level.
CREATE TABLE managed_runner_reservation_resolutions (
    reservation_id UUID PRIMARY KEY
        REFERENCES managed_runner_reservations(id) ON DELETE RESTRICT,
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('SETTLED', 'RELEASED')),
    reason_detail TEXT NOT NULL
        CHECK (char_length(reason_detail) BETWEEN 8 AND 500),
    -- Where the provider evidence lives (billing row, usage export line),
    -- never the evidence payload itself.
    evidence_reference TEXT NOT NULL CHECK (btrim(evidence_reference) <> ''),
    settled_amount_micros BIGINT
        CHECK (settled_amount_micros IS NULL OR settled_amount_micros >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT managed_runner_reservation_resolutions_amount_check CHECK (
        (outcome = 'SETTLED') OR (settled_amount_micros IS NULL)
    )
);

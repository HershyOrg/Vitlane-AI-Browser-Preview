CREATE TABLE agency_order_outbox (
    id UUID PRIMARY KEY,
    aggregate_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK (event_type = 'PaymentInstructionIssued.v2'),
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','CONSUMED','CONFLICT')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    UNIQUE (aggregate_id, event_type)
);

CREATE INDEX idx_agency_order_outbox_pending
    ON agency_order_outbox(state, available_at) WHERE state = 'PENDING';

CREATE TABLE procurement_receipt_inbox (
    funds_receipt_id UUID PRIMARY KEY REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    consumed_at TIMESTAMPTZ NOT NULL
);

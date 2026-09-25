CREATE TABLE payment_order_reserve_entries (
    id UUID PRIMARY KEY,
    funds_receipt_id UUID NOT NULL
        REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    entry_type TEXT NOT NULL CHECK (entry_type IN ('ALLOCATE','RELEASE')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 2 AND 500),
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_payment_order_reserve_entries_order_created
    ON payment_order_reserve_entries(agency_order_id, created_at DESC);

ALTER TABLE agency_order_provider_capabilities
    DROP CONSTRAINT agency_order_provider_capabilities_capability_kind_check;

ALTER TABLE agency_order_provider_capabilities
    ADD CONSTRAINT agency_order_provider_capabilities_capability_kind_check
    CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT','BUYER_CONTEXT'));

ALTER TABLE agency_order_sheet_sessions
    ADD COLUMN cleanup_state TEXT NOT NULL DEFAULT 'NOT_REQUIRED'
        CHECK (cleanup_state IN ('NOT_REQUIRED','PENDING','RUNNING','SUCCEEDED')),
    ADD COLUMN cleanup_attempt_count INTEGER NOT NULL DEFAULT 0
        CHECK (cleanup_attempt_count >= 0),
    ADD COLUMN cleanup_available_at TIMESTAMPTZ,
    ADD COLUMN cleanup_lease_until TIMESTAMPTZ,
    ADD COLUMN cleanup_last_error_code TEXT,
    ADD COLUMN cleanup_completed_at TIMESTAMPTZ;

UPDATE agency_order_sheet_sessions
SET cleanup_state = 'PENDING',
    cleanup_available_at = now()
WHERE state = 'EXPIRED';

CREATE INDEX idx_agency_order_sheet_cleanup_jobs
    ON agency_order_sheet_sessions(cleanup_state, cleanup_available_at)
    WHERE cleanup_state IN ('PENDING','RUNNING');

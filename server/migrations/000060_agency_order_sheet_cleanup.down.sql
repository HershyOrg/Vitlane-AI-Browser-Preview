DELETE FROM agency_order_provider_capabilities
WHERE capability_kind = 'BUYER_CONTEXT';

DROP INDEX IF EXISTS idx_agency_order_sheet_cleanup_jobs;

ALTER TABLE agency_order_sheet_sessions
    DROP COLUMN IF EXISTS cleanup_completed_at,
    DROP COLUMN IF EXISTS cleanup_last_error_code,
    DROP COLUMN IF EXISTS cleanup_lease_until,
    DROP COLUMN IF EXISTS cleanup_available_at,
    DROP COLUMN IF EXISTS cleanup_attempt_count,
    DROP COLUMN IF EXISTS cleanup_state;

ALTER TABLE agency_order_provider_capabilities
    DROP CONSTRAINT agency_order_provider_capabilities_capability_kind_check;

ALTER TABLE agency_order_provider_capabilities
    ADD CONSTRAINT agency_order_provider_capabilities_capability_kind_check
    CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT'));

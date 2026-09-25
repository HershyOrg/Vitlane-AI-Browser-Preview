-- LEGACY_V0 public rationale duplicates columns retained by the old schema.
-- V1 review records and internal notes do not, so fail closed rather than
-- discarding customer/operator decision evidence on rollback.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agency_order_refund_requests
        WHERE review_contract_version='PUBLIC_RATIONALE_V1'
    ) OR EXISTS (
        SELECT 1 FROM agency_order_refund_request_items
        WHERE review_contract_version='PUBLIC_RATIONALE_V1'
           OR internal_note IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot rollback refund review context with retained v1 evidence'
            USING ERRCODE = '23000';
    END IF;
END;
$$;

ALTER TABLE agency_order_refund_request_items
    DROP CONSTRAINT IF EXISTS refund_request_items_internal_note_check,
    DROP CONSTRAINT IF EXISTS refund_request_items_public_rationale_required_check,
    DROP CONSTRAINT IF EXISTS refund_request_items_public_rationale_bounds_check,
    DROP CONSTRAINT IF EXISTS refund_request_items_review_contract_check,
    DROP COLUMN IF EXISTS review_contract_version,
    DROP COLUMN IF EXISTS internal_note,
    DROP COLUMN IF EXISTS public_rationale;

ALTER TABLE agency_order_refund_requests
    DROP CONSTRAINT IF EXISTS refund_requests_public_rationale_check,
    DROP CONSTRAINT IF EXISTS refund_requests_review_contract_check,
    DROP COLUMN IF EXISTS review_contract_version,
    DROP COLUMN IF EXISTS public_rationale;

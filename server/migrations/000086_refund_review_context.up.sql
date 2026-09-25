-- Live-readiness: refund review must retain the customer's explanation and
-- the operator's customer-visible decision basis. Internal notes stay
-- operator-only. Legacy rows remain readable without inventing evidence;
-- every new V1 row is fail-closed by the checks below.

ALTER TABLE agency_order_refund_requests
    ADD COLUMN public_rationale TEXT,
    ADD COLUMN review_contract_version TEXT NOT NULL DEFAULT 'LEGACY_V0';

UPDATE agency_order_refund_requests
SET public_rationale = NULLIF(BTRIM(reason), '');

ALTER TABLE agency_order_refund_requests
    ALTER COLUMN review_contract_version DROP DEFAULT,
    ADD CONSTRAINT refund_requests_review_contract_check CHECK (
        review_contract_version IN ('LEGACY_V0', 'PUBLIC_RATIONALE_V1')
    ),
    ADD CONSTRAINT refund_requests_public_rationale_check CHECK (
        review_contract_version = 'LEGACY_V0'
        OR (
            public_rationale IS NOT NULL
            AND char_length(BTRIM(public_rationale)) BETWEEN 1 AND 500
        )
    );

ALTER TABLE agency_order_refund_request_items
    ADD COLUMN public_rationale TEXT,
    ADD COLUMN internal_note TEXT,
    ADD COLUMN review_contract_version TEXT NOT NULL DEFAULT 'LEGACY_V0';

UPDATE agency_order_refund_request_items
SET public_rationale = NULLIF(BTRIM(decision_reason), '');

ALTER TABLE agency_order_refund_request_items
    ALTER COLUMN review_contract_version DROP DEFAULT,
    ADD CONSTRAINT refund_request_items_review_contract_check CHECK (
        review_contract_version IN ('LEGACY_V0', 'PUBLIC_RATIONALE_V1')
    ),
    ADD CONSTRAINT refund_request_items_public_rationale_bounds_check CHECK (
        public_rationale IS NULL
        OR char_length(BTRIM(public_rationale)) BETWEEN 1 AND 2000
    ),
    ADD CONSTRAINT refund_request_items_public_rationale_required_check CHECK (
        review_contract_version = 'LEGACY_V0'
        OR state = 'PENDING'
        OR public_rationale IS NOT NULL
    ),
    ADD CONSTRAINT refund_request_items_internal_note_check CHECK (
        internal_note IS NULL OR char_length(internal_note) <= 4000
    );

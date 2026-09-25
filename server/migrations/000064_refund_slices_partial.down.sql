-- pre-effect 전용 down (docs/phases/phase-8 §16.2-8).
ALTER TABLE payment_customer_refunds
    DROP COLUMN cause,
    DROP CONSTRAINT payment_customer_refunds_basis_check,
    DROP CONSTRAINT payment_customer_refunds_obligation_kind_check,
    ADD CONSTRAINT payment_customer_refunds_basis_check
        CHECK (basis = 'FULL_ORDER_FAILURE'),
    ADD CONSTRAINT payment_customer_refunds_obligation_kind_check
        CHECK (obligation_kind = 'MANDATORY_SYSTEM'),
    ADD CONSTRAINT payment_customer_refunds_customer_payment_id_basis_key
        UNIQUE (customer_payment_id, basis);

DROP TABLE payment_refund_slice_claims;
DROP TABLE agency_order_refund_request_items;
DROP TABLE agency_order_refund_requests;
DROP TABLE agency_order_refund_slices;

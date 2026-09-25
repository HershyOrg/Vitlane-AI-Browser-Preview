-- GIWA legacy full-refund command hard cutover.
--
-- The active refund model is payment-owned slice REFUND_PARTIAL. Legacy REFUND
-- commands were planned from agency_order_execution_units, which is an
-- immutable archive rather than a current operational projection. This TEST
-- rail has no real-value facts, so compatibility and drain paths are removed.

DELETE FROM settlement_command_outbox WHERE purpose = 'REFUND';

DROP INDEX idx_settlement_command_lifecycle_purpose;
CREATE UNIQUE INDEX idx_settlement_command_lifecycle_purpose
    ON settlement_command_outbox(settlement_payment_id, purpose)
    WHERE purpose = 'COMPLETE';

ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_partial_shape_check,
    DROP CONSTRAINT settlement_command_outbox_purpose_check;

ALTER TABLE settlement_command_outbox
    ADD CONSTRAINT settlement_command_outbox_purpose_check
        CHECK (purpose IN ('COMPLETE', 'REFUND_PARTIAL')),
    ADD CONSTRAINT settlement_command_outbox_partial_shape_check CHECK (
        (
            purpose = 'COMPLETE'
            AND customer_refund_id IS NULL
            AND pass_through_part IS NULL
            AND fee_part IS NULL
            AND refund_key IS NULL
        )
        OR (
            purpose = 'REFUND_PARTIAL'
            AND customer_refund_id IS NOT NULL
            AND pass_through_part IS NOT NULL
            AND fee_part IS NOT NULL
            AND (pass_through_part > 0 OR fee_part > 0)
            AND refund_key ~ '^0x[0-9a-f]{64}$'
            AND fulfillment_hash IS NULL
        )
    );

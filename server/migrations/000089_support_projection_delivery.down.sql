DROP INDEX IF EXISTS idx_order_cancellations_support_pending;
ALTER TABLE agency_order_cancellations
    DROP COLUMN IF EXISTS support_notified_at;

DROP INDEX IF EXISTS idx_delivery_resolutions_support_pending;
ALTER TABLE logistics_delivery_resolutions
    DROP COLUMN IF EXISTS support_notified_at;

DROP INDEX IF EXISTS idx_procurement_requests_support_resolution_pending;
DROP INDEX IF EXISTS idx_procurement_requests_support_request_pending;
ALTER TABLE procurement_customer_requests
    DROP COLUMN IF EXISTS support_resolution_notified_at,
    DROP COLUMN IF EXISTS support_request_notified_at;

DROP INDEX IF EXISTS idx_procurement_decisions_support_pending;
ALTER TABLE procurement_decision_records
    DROP COLUMN IF EXISTS support_notified_at;

DROP INDEX IF EXISTS idx_refund_requests_support_decision_pending;
DROP INDEX IF EXISTS idx_refund_requests_support_request_pending;
ALTER TABLE agency_order_refund_requests
    DROP COLUMN IF EXISTS support_decision_notified_at,
    DROP COLUMN IF EXISTS support_request_notified_at;

ALTER TABLE support_messages
    DROP CONSTRAINT IF EXISTS support_messages_idempotency_namespace_shape_check;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM support_messages WHERE business_card_type='ORDER_CANCELLATION') THEN
        RAISE EXCEPTION 'cannot remove ORDER_CANCELLATION while cards exist';
    END IF;
END $$;

ALTER TABLE support_messages
    DROP CONSTRAINT support_messages_content_shape_check;

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_content_shape_check CHECK (
        (content_kind='TEXT' AND author IN ('CUSTOMER','OPERATOR')
         AND business_card_type IS NULL AND business_reference_type IS NULL
         AND business_reference_id IS NULL AND action_required=FALSE
         AND resolves_card_id IS NULL AND public_payload IS NULL)
        OR
        (content_kind='BUSINESS_CARD'
         AND business_card_type IN (
            'PROCUREMENT_REQUEST','PROCUREMENT_RESPONSE','PROCUREMENT_DECISION',
            'REFUND_REQUEST','REFUND_DECISION','REFUND_STATUS',
            'PAYPAL_DISPUTE','PAYPAL_DISPUTE_STATUS',
            'DELIVERY_DELAY','DELIVERY_RESOLUTION')
         AND business_reference_type IN (
            'PROCUREMENT','REFUND','PAYPAL_DISPUTE','DELIVERY')
         AND ((business_reference_type='PROCUREMENT' AND business_card_type IN (
                'PROCUREMENT_REQUEST','PROCUREMENT_RESPONSE','PROCUREMENT_DECISION'))
           OR (business_reference_type='REFUND' AND business_card_type IN (
                'REFUND_REQUEST','REFUND_DECISION','REFUND_STATUS'))
           OR (business_reference_type='PAYPAL_DISPUTE' AND business_card_type IN (
                'PAYPAL_DISPUTE','PAYPAL_DISPUTE_STATUS'))
           OR (business_reference_type='DELIVERY' AND business_card_type IN (
                'DELIVERY_DELAY','DELIVERY_RESOLUTION')))
         AND char_length(business_reference_id) BETWEEN 1 AND 160
         AND jsonb_typeof(public_payload)='object'
         AND octet_length(public_payload::text) <= 32768
         AND NOT (action_required AND resolves_card_id IS NOT NULL))
    );

DROP INDEX IF EXISTS uq_support_messages_idempotency_namespace_key;

DO $$
BEGIN
    IF EXISTS (
        SELECT idempotency_key FROM support_messages
        WHERE idempotency_key IS NOT NULL
        GROUP BY idempotency_key HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore global Support idempotency key uniqueness';
    END IF;
END $$;

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_idempotency_key_key UNIQUE (idempotency_key),
    DROP COLUMN IF EXISTS idempotency_namespace;

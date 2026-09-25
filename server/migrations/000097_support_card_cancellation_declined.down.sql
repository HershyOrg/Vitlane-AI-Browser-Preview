-- 거절 카드 행이 남아 있으면 종전 CHECK 재적용이 실패한다(fail-closed).
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
            'ORDER_CANCELLATION','REFUND_REQUEST','REFUND_DECISION','REFUND_STATUS',
            'PAYPAL_DISPUTE','PAYPAL_DISPUTE_STATUS',
            'DELIVERY_DELAY','DELIVERY_RESOLUTION')
         AND business_reference_type IN (
            'PROCUREMENT','REFUND','PAYPAL_DISPUTE','DELIVERY')
         AND ((business_reference_type='PROCUREMENT' AND business_card_type IN (
                'PROCUREMENT_REQUEST','PROCUREMENT_RESPONSE','PROCUREMENT_DECISION',
                'ORDER_CANCELLATION'))
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

-- ADR-0050 Settlement v2 forward-fix: migration 65가 purpose 어휘는
-- REFUND_PARTIAL까지 넓혔지만 migration 13의 익명 fulfillment_hash CHECK는
-- COMPLETE/REFUND만 허용한 채 남겼다. migration 75가 legacy REFUND를 제거한 뒤의
-- 현행 두 목적만 남기도록 적용 이력이 있는 migration은 수정하지 않고 교체한다.
ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_check,
    ADD CONSTRAINT settlement_command_outbox_fulfillment_hash_shape_check
        CHECK (
            (
                purpose = 'COMPLETE'
                AND fulfillment_hash IS NOT NULL
            )
            OR (
                purpose = 'REFUND_PARTIAL'
                AND fulfillment_hash IS NULL
            )
        );

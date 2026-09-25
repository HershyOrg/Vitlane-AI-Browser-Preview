-- Settlement outbox 확장 (ADR-0050 PR-4): GIWA rail의 slice 기반 부분환불을
-- 기존 커맨드 기계(nonce 유일성·잠금 순서·상태기계)에 그대로 태운다.
--
-- (payment_id, purpose) PK로는 payment당 REFUND 1건만 가능하므로 surrogate PK로
-- 넓히되, 전액 커맨드(COMPLETE/REFUND)의 1-per-purpose 불변은 부분 유니크로
-- 보존한다. nonce·tx_hash 유일성 인덱스는 단일 테이블에 남아 그대로 유효하다.

ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_pkey;

ALTER TABLE settlement_command_outbox
    ADD COLUMN id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ADD COLUMN customer_refund_id UUID
        REFERENCES payment_customer_refunds(id) ON DELETE RESTRICT,
    ADD COLUMN pass_through_part BIGINT CHECK (pass_through_part >= 0),
    ADD COLUMN fee_part BIGINT CHECK (fee_part >= 0),
    ADD COLUMN refund_key TEXT;

ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_purpose_check;
ALTER TABLE settlement_command_outbox
    ADD CONSTRAINT settlement_command_outbox_purpose_check
        CHECK (purpose IN ('COMPLETE', 'REFUND', 'REFUND_PARTIAL'));

-- 전액 커맨드는 partial 컬럼이 전부 NULL, REFUND_PARTIAL은 전부 NOT NULL이며
-- 최소 한 축은 0보다 커야 한다(컨트랙트 ZeroRefund와 동일 계약).
ALTER TABLE settlement_command_outbox
    ADD CONSTRAINT settlement_command_outbox_partial_shape_check CHECK (
        (
            purpose IN ('COMPLETE', 'REFUND')
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

CREATE UNIQUE INDEX idx_settlement_command_lifecycle_purpose
    ON settlement_command_outbox(settlement_payment_id, purpose)
    WHERE purpose IN ('COMPLETE', 'REFUND');

-- 승인된 환불 1건 = 커맨드 1건. sweeper replay가 중복 행을 만들 수 없다.
CREATE UNIQUE INDEX idx_settlement_command_customer_refund
    ON settlement_command_outbox(customer_refund_id)
    WHERE customer_refund_id IS NOT NULL;

-- refund_key는 contract usedRefundKeys와 1:1인 replay 방지 identity다.
CREATE UNIQUE INDEX idx_settlement_command_refund_key
    ON settlement_command_outbox(refund_key)
    WHERE refund_key IS NOT NULL;

-- GIWA rail을 payment 슬림 코어에 합류시킨다 (ADR-0050 "결제 수단은 어댑터").
-- FINALIZED 수납이 중립 CustomerPayment(SUCCEEDED)+FundsReceipt로 기록되면
-- slice·요청 심사·claims·환불 원장이 rail 분기 없이 GIWA에 그대로 작동한다.
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_rail_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_rail_check
        CHECK (rail IN ('PAYPAL', 'GIWA'));
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_provider_environment_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX', 'TEST'));
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_asset_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_asset_check
        CHECK (asset IN ('USD', 'TVITUSD'));

ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_kind_check;
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_kind_check
        CHECK (kind IN ('PAYPAL_CAPTURE', 'GIWA_FINALIZED_PAY'));
ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_provider_environment_check;
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX', 'TEST'));
ALTER TABLE payment_funds_receipts
    ALTER COLUMN capture_id DROP NOT NULL,
    ALTER COLUMN paypal_order_id DROP NOT NULL,
    ADD COLUMN order_hash TEXT,
    ADD COLUMN pay_tx_hash TEXT;

-- 수납 식별자는 rail typed다: PayPal은 capture, GIWA는 orderHash+pay tx.
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_kind_shape_check CHECK (
        (
            kind = 'PAYPAL_CAPTURE'
            AND capture_id IS NOT NULL AND paypal_order_id IS NOT NULL
            AND order_hash IS NULL AND pay_tx_hash IS NULL
        )
        OR (
            kind = 'GIWA_FINALIZED_PAY'
            AND capture_id IS NULL AND paypal_order_id IS NULL
            AND order_hash ~ '^0x[0-9a-f]{64}$'
            AND pay_tx_hash ~ '^0x[0-9a-f]{64}$'
        )
    );

CREATE UNIQUE INDEX idx_payment_funds_receipts_giwa_order
    ON payment_funds_receipts(provider_environment, order_hash)
    WHERE order_hash IS NOT NULL;

-- 현행 집합은 000010이 정의한 ('CLAIM','APPROVE','PAY','COMPLETE','REFUND')다.
ALTER TABLE chain_transactions
    DROP CONSTRAINT chain_transactions_purpose_check;
ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_purpose_check
        CHECK (purpose IN ('CLAIM', 'APPROVE', 'PAY', 'COMPLETE', 'REFUND', 'REFUND_PARTIAL'));

-- Step 4C 스키마를 되돌린다. 이미 pass-through basis 환불·취소·LIVE 증거
-- row가 있으면 FK/CHECK가 fail-close로 중단한다(외부 fact 삭제 down 없음).

ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED'
    ));

DROP TABLE procurement_recovery_entries;
DROP TABLE merchant_charges;
DROP TABLE merchant_payments;
DROP TABLE agency_order_cancellations;

ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_cause_check;
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_cause_check
        CHECK (cause IN ('ORDER_FAILURE','CUSTOMER_REQUEST'));
ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_basis_check;
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_basis_check
        CHECK (basis IN ('ORDER_SLICES'));

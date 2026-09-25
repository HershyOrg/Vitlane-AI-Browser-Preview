-- Procurement Context 테이블을 제거하고 감사 테이블 축을 Step 4A 시점으로
-- 되돌린다. merchant_order 축 감사 row가 있으면 fail-close로 중단된다
-- (FK RESTRICT — 감사 row를 지우는 down은 만들지 않는다).

ALTER TABLE agency_order_pii_access_audits
    DROP CONSTRAINT agency_order_pii_access_audits_action_check;
ALTER TABLE agency_order_pii_access_audits
    ADD CONSTRAINT agency_order_pii_access_audits_action_check
        CHECK (action = 'SHIPPING_ADDRESS_REVEAL');
ALTER TABLE agency_order_pii_access_audits
    DROP CONSTRAINT agency_order_pii_access_audits_subject_check;
ALTER TABLE agency_order_pii_access_audits
    DROP CONSTRAINT agency_order_pii_access_audits_snapshot_check;
ALTER TABLE agency_order_pii_access_audits DROP COLUMN merchant_order_id;
ALTER TABLE agency_order_pii_access_audits
    ALTER COLUMN shipping_snapshot_id SET NOT NULL;
ALTER TABLE agency_order_pii_access_audits
    ALTER COLUMN execution_unit_id SET NOT NULL;

ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED'
    ));
ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_subject_check;
ALTER TABLE agency_order_execution_audits DROP COLUMN merchant_order_id;
ALTER TABLE agency_order_execution_audits
    ALTER COLUMN execution_unit_id SET NOT NULL;

DROP TABLE agency_order_customer_notices;
DROP TABLE merchant_order_execution_tasks;
DROP TABLE merchant_order_units;
DROP TABLE merchant_orders;
DROP TABLE procurement_manifests;
DROP TABLE procurement_receipt_inbox;

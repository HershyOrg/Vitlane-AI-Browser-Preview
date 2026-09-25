-- 회수 원장 기입(운영정합 5차 PR-D): 수취 기입·수동 생성·포기·삭제를
-- 기존 실행 감사 테이블에 남기기 위해 action 어휘를 확장한다.
ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED',
        'CUSTOMER_CANCELLED','MERCHANT_ORDER_PLACED',
        'RECOVERY_CREATED','RECOVERY_RECORDED','RECOVERY_WAIVED','RECOVERY_DELETED'
    ));

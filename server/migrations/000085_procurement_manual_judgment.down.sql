-- Manual decisions, customer answers, and effect locks are retained operating
-- evidence. Rolling back their schema after any row exists would silently
-- erase the basis for a merchant action, so such a release must be forward-
-- fixed instead.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM procurement_decision_records)
       OR EXISTS (SELECT 1 FROM procurement_customer_requests)
       OR EXISTS (SELECT 1 FROM procurement_effect_locks) THEN
        RAISE EXCEPTION 'cannot rollback procurement manual judgment with retained evidence'
            USING ERRCODE = '23000';
    END IF;
END;
$$;

ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED',
        'CUSTOMER_CANCELLED','MERCHANT_ORDER_PLACED',
        'RECOVERY_CREATED','RECOVERY_RECORDED','RECOVERY_WAIVED','RECOVERY_DELETED'
    ));

DROP TABLE IF EXISTS procurement_effect_locks;
DROP TABLE IF EXISTS procurement_customer_requests;
DROP TABLE IF EXISTS procurement_decision_records;

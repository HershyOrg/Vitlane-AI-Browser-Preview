ALTER TABLE payment_mo_compensations ADD COLUMN support_notified_at TIMESTAMPTZ;
ALTER TABLE logistics_delivery_resolutions ADD COLUMN support_notified_at TIMESTAMPTZ;
ALTER TABLE agency_order_cancellations ADD COLUMN support_notified_at TIMESTAMPTZ;
ALTER TABLE procurement_customer_requests
    ADD COLUMN support_request_notified_at TIMESTAMPTZ,
    ADD COLUMN support_resolution_notified_at TIMESTAMPTZ;
ALTER TABLE procurement_decision_records ADD COLUMN support_notified_at TIMESTAMPTZ;
ALTER TABLE agency_order_refund_requests
    ADD COLUMN support_request_notified_at TIMESTAMPTZ,
    ADD COLUMN support_decision_notified_at TIMESTAMPTZ;
ALTER TABLE order_process_commands DROP CONSTRAINT order_process_commands_target_check;
ALTER TABLE order_process_commands ADD CONSTRAINT order_process_commands_target_check
    CHECK (target IN ('AGENCYORDER','PAYMENT','PROCUREMENT','LOGISTICS'));

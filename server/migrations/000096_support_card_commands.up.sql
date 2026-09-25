-- ADR-0070 §4.5: 고객 통신(Support 카드)을 process 커맨드 레일로 편입한다.
-- 리듀서가 owner 이벤트에서 support.publish_card.v1 커맨드를 발행하고 SUPPORT
-- executor가 owner 사실을 참조 id로 읽어 카드를 만든다. owner 행의 nullable
-- delivery marker(support_*_notified_at)와 4개 투영 워커는 폐기한다.
--
-- fail-closed: 아직 전달되지 않은 marker 행이 남아 있으면(종전 워커의 큐)
-- 컷오버를 거부한다 — 배포 전에 워커가 큐를 비워야 한다(release runbook gate).
DO $support_cutover_guard$
BEGIN
    IF EXISTS (SELECT 1 FROM agency_order_refund_requests
               WHERE support_request_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM agency_order_refund_requests
                  WHERE state='RESOLVED' AND support_decision_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM procurement_decision_records
                  WHERE decision='UNABLE_TO_PURCHASE' AND support_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM procurement_customer_requests
                  WHERE support_request_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM procurement_customer_requests
                  WHERE state<>'PENDING' AND support_resolution_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM agency_order_cancellations WHERE support_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM logistics_delivery_resolutions WHERE support_notified_at IS NULL)
       OR EXISTS (SELECT 1 FROM payment_mo_compensations
                  WHERE state='SUCCEEDED' AND support_notified_at IS NULL)
    THEN
        RAISE EXCEPTION
            'cannot cut Support cards over to process commands while owner delivery markers are pending'
            USING ERRCODE='55000';
    END IF;
END
$support_cutover_guard$;

ALTER TABLE order_process_commands DROP CONSTRAINT order_process_commands_target_check;
ALTER TABLE order_process_commands ADD CONSTRAINT order_process_commands_target_check
    CHECK (target IN ('AGENCYORDER','PAYMENT','PROCUREMENT','LOGISTICS','SUPPORT'));

ALTER TABLE agency_order_refund_requests
    DROP COLUMN support_request_notified_at,
    DROP COLUMN support_decision_notified_at;
ALTER TABLE procurement_decision_records DROP COLUMN support_notified_at;
ALTER TABLE procurement_customer_requests
    DROP COLUMN support_request_notified_at,
    DROP COLUMN support_resolution_notified_at;
ALTER TABLE agency_order_cancellations DROP COLUMN support_notified_at;
ALTER TABLE logistics_delivery_resolutions DROP COLUMN support_notified_at;
ALTER TABLE payment_mo_compensations DROP COLUMN support_notified_at;

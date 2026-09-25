-- Step 5B 구조 되돌림. 판정·회수 fact row가 있으면 FK RESTRICT로 fail-close
-- 된다(운영 evidence 삭제 down 없음).

ALTER TABLE agency_order_refund_requests
    DROP CONSTRAINT agency_order_refund_requests_reason_check;
ALTER TABLE agency_order_refund_requests
    ADD CONSTRAINT agency_order_refund_requests_reason_check
        CHECK (char_length(reason) BETWEEN 2 AND 500);
ALTER TABLE agency_order_refund_requests DROP COLUMN reason_code;

ALTER TABLE agency_order_cancellations
    DROP CONSTRAINT agency_order_cancellations_kind_check;
ALTER TABLE agency_order_cancellations
    ADD CONSTRAINT agency_order_cancellations_kind_check
        CHECK (kind = 'PRE_EFFECT');

DROP TABLE logistics_returns;
DROP TABLE logistics_delivery_resolutions;

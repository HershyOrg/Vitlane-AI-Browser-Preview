-- Phase 8 Sandbox–Live 동등화(ADR-0053): ExecutionMode는 상태기계를 바꾸지
-- 않는다. 구 SIMULATED 지름길 모델의 주문 그래프를 삭제(소유자 결정 — 전
-- 환경 TEST, legalSale=false)하고 Sandbox 전용 상태 어휘 3종을 제거한다.
--
-- 삭제 기준: merchant_orders를 가진 pre-동등화 주문 전부. LIVE는 fail-close라
-- 실행된 적이 없으므로 이 집합 = 구 지름길 모델의 산물이다. 아직 조달 root가
-- 없는 in-flight 주문은 새 모델로 계속 진행한다(삭제하지 않음).
-- CHECK 축소는 삭제 뒤에 온다(#183 교훈 — 위반 row가 없을 때만 좁힌다).

CREATE TEMPORARY TABLE equivalence_legacy_orders ON COMMIT DROP AS
SELECT DISTINCT agency_order_id AS id FROM merchant_orders;

-- Logistics: event/allocation -> shipment -> 판정/회수 -> 기대 unit
DELETE FROM logistics_shipment_events WHERE shipment_id IN (
    SELECT id FROM logistics_shipments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM logistics_shipment_allocations WHERE shipment_id IN (
    SELECT id FROM logistics_shipments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM logistics_shipments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM logistics_delivery_resolutions
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM logistics_returns
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM logistics_expected_units
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- 4C 재무·취소 기록
DELETE FROM merchant_charges
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM merchant_payments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM procurement_recovery_entries
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_cancellations
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- 주문 증거·감사·고지
DELETE FROM agency_order_receipts
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_pii_access_audits
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_execution_audits
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_execution_units
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_customer_notices
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- Procurement root
DELETE FROM merchant_order_execution_tasks
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM merchant_order_units
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM merchant_orders
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM procurement_manifests
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM procurement_receipt_inbox
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_processes
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- 환불·결제 계열(claims -> request items/requests -> attempts/operations ->
-- refunds -> receipts -> paypal -> payments -> slices)
DELETE FROM payment_refund_slice_claims WHERE slice_id IN (
    SELECT id FROM agency_order_refund_slices
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM agency_order_refund_request_items WHERE request_id IN (
    SELECT id FROM agency_order_refund_requests
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM agency_order_refund_requests
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM payment_external_operations
    WHERE owner_kind='CUSTOMER_REFUND_ATTEMPT' AND owner_id IN (
        SELECT attempt.id FROM payment_customer_refund_attempts attempt
        JOIN payment_customer_refunds refund ON refund.id=attempt.customer_refund_id
        WHERE refund.agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM payment_customer_refund_attempts WHERE customer_refund_id IN (
    SELECT id FROM payment_customer_refunds
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM settlement_command_outbox WHERE customer_refund_id IN (
    SELECT id FROM payment_customer_refunds
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM payment_customer_refunds
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM payment_funds_receipts
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM payment_external_operations
    WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id IN (
        SELECT attempt.id FROM payment_paypal_attempts attempt
        JOIN payment_customer_payments payment ON payment.id=attempt.customer_payment_id
        WHERE payment.agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM payment_paypal_attempts WHERE customer_payment_id IN (
    SELECT id FROM payment_customer_payments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders));
DELETE FROM payment_customer_payments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_refund_slices
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- Settlement(GIWA) 계열 — outbox 전액 커맨드·chain_transactions는
-- settlement_payments CASCADE로 정리된다(로컬 검수 reset과 동일 전제).
DELETE FROM settlement_authorizations
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM settlement_payments
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);

-- AgencyOrder 본체
DELETE FROM agency_order_payment_consents
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_outbox
    WHERE aggregate_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_audits
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_order_payment_instructions
    WHERE agency_order_id IN (SELECT id FROM equivalence_legacy_orders);
DELETE FROM agency_orders
    WHERE id IN (SELECT id FROM equivalence_legacy_orders);

-- 어휘 축소: SIMULATED terminal·mode-조건 상태기계 CHECK 폐기.
-- external_order_ref의 LIVE 전용 CHECK(merchant_orders_check2)는 유지한다.
ALTER TABLE merchant_orders DROP CONSTRAINT merchant_orders_check;
ALTER TABLE merchant_orders DROP CONSTRAINT merchant_orders_check1;
ALTER TABLE merchant_orders DROP CONSTRAINT merchant_orders_state_check;
ALTER TABLE merchant_orders ADD CONSTRAINT merchant_orders_state_check CHECK (state IN (
    'PLANNED','READY_TO_PLACE','PLACEMENT_PENDING','PLACED',
    'PLACEMENT_UNKNOWN','FAILED','CANCELLED'
));

ALTER TABLE merchant_order_units DROP CONSTRAINT merchant_order_units_disposition_check;
ALTER TABLE merchant_order_units ADD CONSTRAINT merchant_order_units_disposition_check CHECK (disposition IN (
    'PENDING','CUSTOMER_REFUND_DUE','CUSTOMER_REFUND_SATISFIED',
    'ZERO_VALUE_SATISFIED','NO_PAYMENT_EFFECT'
));

ALTER TABLE logistics_expected_units DROP CONSTRAINT logistics_expected_units_fulfillment_check;
ALTER TABLE logistics_expected_units ADD CONSTRAINT logistics_expected_units_fulfillment_check CHECK (fulfillment IN (
    'AWAITING_EFFECT','IN_TRANSIT_EXPECTED','DELIVERED_EXPECTED',
    'MISSING','WRONG_ACTUAL','LOST','RETURNED',
    'DELIVERY_RESOLUTION_PENDING','NONCONFORMING_RESOLUTION_PENDING',
    'RESOLVED','NONCONFORMING_RESOLVED',
    'SUPERSEDED_BY_CANCELLATION','NO_PLACEMENT'
));

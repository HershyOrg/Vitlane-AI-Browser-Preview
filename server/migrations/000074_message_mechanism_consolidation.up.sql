-- ADR-0056 컷오버 B: 연결 메커니즘을 order_process_events/commands 한 규격으로
-- 수렴한다.
--
-- agency_order_outbox: PaymentInstructionIssued.v2 단일 타입의 write-only
-- 잔재였다(소비자 0 — GIWA의 CONSUMED 마킹은 bookkeeping뿐). order.issued
-- 이벤트가 대체하므로 무보존 폐기한다.
--
-- procurement_receipt_inbox: accepted receipt → 조달 plan의 원자적 handoff는
-- funds_receipt.recorded 이벤트 → plan_from_receipt 커맨드(결정적 idempotency
-- key)가 대체한다. plan 멱등성은 procurement_manifests의 funds_receipt_id
-- unique가 계속 보장한다.

DROP TABLE IF EXISTS agency_order_outbox;
DROP TABLE IF EXISTS procurement_receipt_inbox;

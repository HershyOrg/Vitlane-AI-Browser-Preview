DO $$
DECLARE
  terminal_count integer;
BEGIN
  SELECT count(*) INTO terminal_count
  FROM agency_orders AS agency_order
  JOIN agency_order_payment_instructions AS instruction
    ON instruction.agency_order_id = agency_order.id
  JOIN settlement_authorizations AS auth_record
    ON auth_record.agency_order_id = agency_order.id
  JOIN settlement_payments AS payment
    ON payment.agency_order_id = agency_order.id
  JOIN agency_order_processes AS process
    ON process.agency_order_id = agency_order.id
  JOIN merchant_orders AS merchant_order
    ON merchant_order.agency_order_id = agency_order.id
  JOIN merchant_order_execution_tasks AS execution_task
    ON execution_task.merchant_order_id = merchant_order.id
  JOIN payment_mo_funding_positions AS funding_position
    ON funding_position.allocation_id = merchant_order.allocation_id
  JOIN agency_order_receipts AS receipt
    ON receipt.agency_order_id = agency_order.id
  JOIN chain_transactions AS pay_transaction
    ON pay_transaction.settlement_payment_id = payment.id
   AND pay_transaction.purpose = 'PAY'
  JOIN chain_events AS pay_event
    ON pay_event.chain_id = pay_transaction.chain_id
   AND pay_event.tx_hash = pay_transaction.tx_hash
   AND pay_event.event_name = 'PaymentEscrowed'
  JOIN chain_transactions AS complete_transaction
    ON complete_transaction.settlement_payment_id = payment.id
   AND complete_transaction.purpose = 'COMPLETE'
  JOIN chain_events AS complete_event
    ON complete_event.chain_id = complete_transaction.chain_id
   AND complete_event.tx_hash = complete_transaction.tx_hash
   AND complete_event.event_name = 'PaymentCompleted'
  WHERE agency_order.user_id = 'e5000000-0000-4000-8000-000000000101'
    AND agency_order.status = 'ISSUED'
    AND instruction.state = 'CONSUMED'
    AND instruction.amount_minor * 10000 = payment.amount_base_units
    AND auth_record.order_hash = payment.order_hash
    AND payment.state = 'COMPLETED'
    AND payment.safe_block IS NOT NULL
    AND payment.finalized_block IS NOT NULL
    AND payment.complete_tx_hash IS NOT NULL
    AND process.state = 'TERMINAL'
    AND process.terminal_reason IN ('COMPLETED_ALL','COMPLETED_PARTIAL')
    AND merchant_order.state = 'PLACED'
    AND merchant_order.execution_mode = 'SIMULATED_NO_EFFECT'
    AND merchant_order.external_order_ref IS NOT NULL
    AND merchant_order.placement_evidence_kind = 'SANDBOX_TEST_EVIDENCE'
    AND merchant_order.placement_receipt_safe_ref IS NOT NULL
    -- placement evidence는 승인 상한의 exact-equality가 아니라 실제 지출이다.
    -- 판매처 금액이 내려간 정상 구매도 양수·상한 이하이면 유효하다(ADR-0066).
    AND merchant_order.placement_actual_amount_minor > 0
    AND merchant_order.placement_actual_amount_minor <=
      (merchant_order.checkout_snapshot->'authoritativeTotal'->>'amountMinor')::bigint
    AND merchant_order.placement_evidence_source = 'RECEIPT'
    AND merchant_order.placement_evidence_hash IS NOT NULL
    AND merchant_order.placement_observed_at IS NOT NULL
    AND merchant_order.placement_recorded_by_user_id IS NOT NULL
    AND merchant_order.placement_recorded_at IS NOT NULL
    AND execution_task.state = 'SUCCEEDED'
    AND funding_position.rail = 'GIWA'
    AND funding_position.state = 'ACTIVE'
    -- GIWA Begin은 외부 Capture를 흉내 내지 않고 local ACTIVE만 기록한다.
    AND NOT EXISTS (
      SELECT 1 FROM payment_external_operations operation
      WHERE operation.owner_kind = 'MO_FUNDING_POSITION'
        AND operation.owner_id = funding_position.id
    )
    -- clean cut: 정상 완료 MO에는 compensation aggregate가 생기지 않는다.
    AND NOT EXISTS (
      SELECT 1 FROM payment_mo_compensations compensation
      WHERE compensation.allocation_id = merchant_order.allocation_id
    )
    AND receipt.terminal_state IN ('COMPLETED_ALL','COMPLETED_PARTIAL')
    AND receipt.legal_sale = FALSE
    AND receipt.terminal_tx_hash = payment.complete_tx_hash
    AND pay_event.order_hash = payment.order_hash
    AND complete_event.order_hash = payment.order_hash
    AND pay_transaction.state = 'FINALIZED'
    AND complete_transaction.state = 'FINALIZED';

  IF terminal_count <> 1 THEN
    RAISE EXCEPTION 'expected one terminal AgencyOrder payment graph, found %', terminal_count;
  END IF;

  -- ADR-0053: Sandbox도 LIVE와 동일 경로다 — PLACED unit은 전부 기대 등록되고
  -- 수령 확인(DELIVERED_EXPECTED)까지 도달해야 terminal이다. 또한 synthetic
  -- 지출 기록과 DELIVERED shipment가 실물 워크플로 등가의 증거로 남아야 한다.
  IF EXISTS (
    SELECT 1
    FROM merchant_order_units AS unit
    JOIN merchant_orders AS merchant_order
      ON merchant_order.id = unit.merchant_order_id
    LEFT JOIN logistics_expected_units AS expected
      ON expected.merchant_order_unit_id = unit.id
    WHERE merchant_order.state = 'PLACED'
      AND (expected.id IS NULL OR expected.fulfillment <> 'DELIVERED_EXPECTED')
  ) THEN
    RAISE EXCEPTION 'PLACED units must be registered and delivered-confirmed';
  END IF;

  IF EXISTS (
    SELECT 1 FROM merchant_orders AS merchant_order
    WHERE merchant_order.state = 'PLACED'
      AND NOT EXISTS (
        SELECT 1 FROM merchant_payments AS payment
        WHERE payment.merchant_order_id = merchant_order.id
      )
  ) THEN
    RAISE EXCEPTION 'PLACED orders must carry a (synthetic) merchant payment record';
  END IF;

  IF EXISTS (
    SELECT 1 FROM merchant_orders AS merchant_order
    WHERE merchant_order.state = 'PLACED'
      AND NOT EXISTS (
        SELECT 1 FROM logistics_shipments AS shipment
        WHERE shipment.merchant_order_id = merchant_order.id
          AND shipment.state = 'DELIVERED'
      )
  ) THEN
    RAISE EXCEPTION 'PLACED orders must have a DELIVERED shipment';
  END IF;

  -- ADR-0054: legacy Purchase/Fulfillment schema는 migration 72에서 삭제됐다.
  -- "legacy fulfillment work 미생성"과 "settlement source 단일화"는 이제 스키마
  -- 구조가 강제하므로 별도 단언이 없다.
END $$;

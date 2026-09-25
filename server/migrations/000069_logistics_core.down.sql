-- Logistics core 테이블 제거. 관찰된 물리 fact row가 있으면 FK RESTRICT로
-- fail-close된다(외부 fact 삭제 down 없음).

DROP TABLE logistics_shipment_events;
DROP TABLE logistics_shipment_allocations;
DROP TABLE logistics_shipments;
DROP TABLE logistics_expected_units;

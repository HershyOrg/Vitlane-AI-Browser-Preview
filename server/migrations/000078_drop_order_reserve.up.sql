-- 운영정합 3차 D-f: 보전액은 실제 자금 이동이 없는 종이 원장이었고, 차단
-- 게이트의 레버로만 쓰였다. funding은 순수 계산 view로 전환되며 이 원장은
-- 무보존 폐기한다(ADR-0054 방식). 부족 추적은 파생 지표(vitlaneBurden)가 담당.
DROP TABLE IF EXISTS payment_order_reserve_entries;

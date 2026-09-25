package postgres

import (
	"strings"
	"testing"
)

// ADR-0070: 주문 단위 facts는 집계를 싣지 않고, MO·Unit 관찰은 identity 단위
// 스냅샷 질의가 whole-MO compensation 행에서 읽는다(폐기된 slice 사영 금지).
func TestWatchdogFactsUseWholeMOCompensationProjection(t *testing.T) {
	for _, retired := range []string{
		"agency_order_refund_slices", "payment_refund_slice_claims",
		"payment_customer_refunds",
	} {
		if strings.Contains(watchdogFactsSQL, retired) ||
			strings.Contains(merchantOrderSnapshotSQL, retired) {
			t.Fatalf("watchdog still reads retired projection %q", retired)
		}
	}
	if strings.Contains(watchdogFactsSQL, "payment_mo_compensations") {
		t.Fatal("order-level facts must not aggregate whole-MO compensations")
	}
	if !strings.Contains(merchantOrderSnapshotSQL, "payment_mo_compensations") ||
		!strings.Contains(merchantOrderSnapshotSQL, "logistics_expected_units") ||
		!strings.Contains(merchantOrderSnapshotSQL, "procurement_effect_locks") {
		t.Fatal("merchant order snapshot does not read whole-MO owner facts")
	}
}

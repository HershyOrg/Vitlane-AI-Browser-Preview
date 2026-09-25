package procmsg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestKeyBuilders(t *testing.T) {
	if got := EventDedupKey("customer_payment", "pay-1", "SUCCEEDED"); got != "customer_payment:pay-1:SUCCEEDED" {
		t.Fatalf("event dedup key: %q", got)
	}
	at := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	want := "timer:order-1:DELAY_RULE_NOTICE:" + "1787356800"
	if got := TimerDedupKey("order-1", TimerKindDelayRuleNotice, at); got != want {
		t.Fatalf("timer dedup key: %q want %q", got, want)
	}
	if got := EffectIdempotencyKey(EffectCompensateMO, "order-1", "mo-1"); got != "payment.execute_mo_compensation.v1:order-1:mo-1" {
		t.Fatalf("command key: %q", got)
	}
	if got := EffectIdempotencyKey(EffectIssueReceipt, "order-1"); got != "agencyorder.issue_receipt.v1:order-1" {
		t.Fatalf("command key without scope: %q", got)
	}
}

func TestWholeMOPayloadsCarryImmutableTarget(t *testing.T) {
	raw, err := json.Marshal(RefundReviewDecidedPayload{
		RequestID: "request-1", MerchantOrderID: "mo-1",
		AllocationID: "allocation-1", Decision: "APPROVED",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if !strings.Contains(encoded, `"merchantOrderId":"mo-1"`) ||
		!strings.Contains(encoded, `"allocationId":"allocation-1"`) ||
		strings.Contains(strings.ToLower(encoded), "slice") {
		t.Fatalf("payload=%s", encoded)
	}
}

// 필수 필드가 비면 DB를 만지기 전에 거절한다 — 규격 위반은 조용히 넘어가지
// 않는다.
func TestAppendEventRejectsInvalidEnvelope(t *testing.T) {
	cases := []ProcessEvent{
		{},
		{AgencyOrderID: "order-1", Source: SourcePayment, Type: EventCustomerPaymentStateChanged},
		{AgencyOrderID: "order-1", Source: SourcePayment, DedupKey: "k"},
		{AgencyOrderID: "order-1", Type: EventCustomerPaymentStateChanged, DedupKey: "k"},
	}
	for _, event := range cases {
		if _, err := AppendEvent(context.Background(), nil, event, time.Now()); err != ErrEventInvalid {
			t.Fatalf("want ErrEventInvalid for %+v, got %v", event, err)
		}
	}
}

package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestSupportProjectionDeliveryMigrationSeparatesKeysAndKeepsOwnerQueues(t *testing.T) {
	payload, err := os.ReadFile("../../../../migrations/000089_support_projection_delivery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	for _, required := range []string{
		"idempotency_namespace IN ('EXTERNAL','BUSINESS_CARD')",
		"uq_support_messages_idempotency_namespace_key",
		"ON support_messages(idempotency_namespace, idempotency_key)",
		"support_messages_idempotency_namespace_shape_check",
		"'ORDER_CANCELLATION'",
		"support_request_notified_at",
		"support_decision_notified_at",
		"support_resolution_notified_at",
		"WHERE state='RESOLVED'",
		"SET support_notified_at=created_at",
		"WHERE state<>'PENDING'",
		"idx_delivery_resolutions_support_pending",
		"idx_order_cancellations_support_pending",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("000089 migration missing %q", required)
		}
	}
}

// ADR-0070 D3: 취소 거절 카드 kind가 DB CHECK에도 있어야 SUPPORT executor의
// insert가 content shape 제약에 걸리지 않는다.
func TestSupportDeclinedCancellationMigrationAdmitsTheNewCardKind(t *testing.T) {
	payload, err := os.ReadFile("../../../../migrations/000097_support_card_cancellation_declined.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	for _, required := range []string{
		"support_messages_content_shape_check",
		"'ORDER_CANCELLATION','ORDER_CANCELLATION_DECLINED'",
		"business_reference_type='PROCUREMENT'",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("000097 migration missing %q", required)
		}
	}
	if strings.Count(source, "'ORDER_CANCELLATION_DECLINED'") != 2 {
		t.Fatalf("declined card must be admitted in both the kind list and the PROCUREMENT reference branch")
	}
}

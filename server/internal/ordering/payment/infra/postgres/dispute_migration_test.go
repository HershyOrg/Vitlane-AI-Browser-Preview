package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestPayPalDisputeMigrationKeepsEnvironmentAndRefundBoundaries(t *testing.T) {
	up, err := os.ReadFile("../../../../../migrations/000087_paypal_dispute_cases.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../../../migrations/000087_paypal_dispute_cases.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	upSQL := string(up)
	for _, required := range []string{
		"UNIQUE (environment, dispute_id)",
		"funds_receipt_id, customer_payment_id, agency_order_id, environment, capture_id",
		"PAYPAL_RESOLUTION_CENTER",
		"public_rationale TEXT NOT NULL",
		"evidence_hash TEXT NOT NULL",
		"CUSTOMER.DISPUTE.CREATED",
		"CUSTOMER.DISPUTE.UPDATED",
		"CUSTOMER.DISPUTE.RESOLVED",
		"trg_payment_paypal_dispute_actions_append_only",
		"TG_OP = 'DELETE'",
		"current_setting('vitlane.account_reset', true) = 'on'",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("000087 up migration missing %q", required)
		}
	}
	downSQL := string(down)
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS payment_paypal_dispute_cases") ||
		!strings.Contains(downSQL, "payment_funds_receipts_dispute_identity_unique") {
		t.Fatal("000087 down migration does not remove dispute-owned schema")
	}
	for _, required := range []string{
		"EXISTS (SELECT 1 FROM payment_paypal_dispute_cases)",
		"EXISTS (SELECT 1 FROM payment_paypal_dispute_manual_actions)",
		"cannot rollback PayPal disputes with retained evidence",
		"USING ERRCODE = '23000'",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("000087 down migration missing retained-evidence guard %q", required)
		}
	}
}

package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestRefundReviewMigrationRequiresPublicRationaleForV1Rows(t *testing.T) {
	legacyReason, err := os.ReadFile("../../../../../migrations/000070_resolution_delay_rule.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../../../../migrations/000086_refund_review_context.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../../../migrations/000086_refund_review_context.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"public_rationale TEXT",
		"internal_note TEXT",
		"review_contract_version TEXT NOT NULL DEFAULT 'LEGACY_V0'",
		"'PUBLIC_RATIONALE_V1'",
		"refund_requests_public_rationale_check",
		"refund_request_items_public_rationale_required_check",
		"char_length(BTRIM(public_rationale)) BETWEEN 1 AND 2000",
		"char_length(internal_note) <= 4000",
	} {
		if !strings.Contains(string(up), fragment) {
			t.Errorf("up migration is missing refund review contract fragment %q", fragment)
		}
	}
	for _, fragment := range []string{
		"DROP CONSTRAINT agency_order_refund_requests_reason_check",
		"CHECK (char_length(reason) <= 500)",
	} {
		if !strings.Contains(string(legacyReason), fragment) {
			t.Errorf("000070 must keep one-character customer rationale compatible: missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"DROP COLUMN IF EXISTS internal_note",
		"DROP COLUMN IF EXISTS public_rationale",
		"DROP COLUMN IF EXISTS review_contract_version",
		"review_contract_version='PUBLIC_RATIONALE_V1'",
		"internal_note IS NOT NULL",
		"cannot rollback refund review context with retained v1 evidence",
	} {
		if !strings.Contains(string(down), fragment) {
			t.Errorf("down migration is missing refund review cleanup %q", fragment)
		}
	}
}

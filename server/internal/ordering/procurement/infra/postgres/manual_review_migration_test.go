package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestManualReviewDownMigrationPreservesRetainedEvidence(t *testing.T) {
	down, err := os.ReadFile("../../../../../migrations/000085_procurement_manual_judgment.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL := string(down)
	for _, fragment := range []string{
		"EXISTS (SELECT 1 FROM procurement_decision_records)",
		"EXISTS (SELECT 1 FROM procurement_customer_requests)",
		"EXISTS (SELECT 1 FROM procurement_effect_locks)",
		"cannot rollback procurement manual judgment with retained evidence",
		"USING ERRCODE = '23000'",
	} {
		if !strings.Contains(downSQL, fragment) {
			t.Errorf("000085 down migration is missing retained-evidence guard %q", fragment)
		}
	}
}

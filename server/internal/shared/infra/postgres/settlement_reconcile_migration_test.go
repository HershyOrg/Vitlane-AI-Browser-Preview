package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestSettlementReconcileMigrationKeepsUnknownSeparateFromFailure(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(
		"../../../../migrations/000043_phase6_settlement_reconcile.up.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	sql := string(body)
	for _, required := range []string{
		"'OBSERVATION_UNKNOWN'",
		"observation_attempt_count",
		"next_observation_at",
		"observation_exhausted_at",
		"idx_chain_transactions_observation_due",
		"settlement_reconcile_audit",
		"'OBSERVATION_POLICY'",
		"'FINALIZED_CONTRACT_STATE'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
	if strings.Contains(sql, "UPDATE chain_transactions\nSET state='FAILED'") {
		t.Fatal("migration must not reinterpret old pending transactions as failed")
	}
}

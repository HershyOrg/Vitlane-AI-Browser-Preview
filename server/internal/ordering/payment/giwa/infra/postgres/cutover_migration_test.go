package postgres

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacyRefundCommandCutoverMigrationContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	path := filepath.Clean(filepath.Join(
		filepath.Dir(filename), "../../../../../../migrations",
		"000075_giwa_legacy_refund_command_cutover.up.sql",
	))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sql := string(body)
	for _, required := range []string{
		"DELETE FROM settlement_command_outbox WHERE purpose = 'REFUND'",
		"CHECK (purpose IN ('COMPLETE', 'REFUND_PARTIAL'))",
		"WHERE purpose = 'COMPLETE'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration is missing hard-cutover contract %q", required)
		}
	}
}

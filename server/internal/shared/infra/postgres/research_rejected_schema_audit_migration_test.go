package postgres

import (
	"strings"
	"testing"
)

func TestResearchRejectedSchemaAuditMigrationAllowsRejectedLegacyOnly(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000029_research_rejected_schema_audit.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000029_research_rejected_schema_audit.down.sql",
	)

	for _, fragment := range []string{
		"DROP CONSTRAINT research_submissions_current_schema_check",
		"validation_status = 'REJECTED'",
		"'vitlane.research-submission.v3'",
		"'vitlane.research-submission.v4'",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}
	for _, fragment := range []string{
		"DELETE FROM research_submissions",
		"validation_status = 'REJECTED'",
		"DROP CONSTRAINT research_submissions_current_schema_check",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
	if strings.Contains(
		down,
		"validation_status = 'REJECTED'\n            OR schema_version",
	) {
		t.Error("down migration must restore the strict accepted-schema constraint")
	}
}

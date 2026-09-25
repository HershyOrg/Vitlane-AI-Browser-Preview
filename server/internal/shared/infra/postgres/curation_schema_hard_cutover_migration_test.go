package postgres

import (
	"strings"
	"testing"
)

func TestCurationSchemaHardCutoverMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000038_intent_curation_schema_hard_cutover.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000038_intent_curation_schema_hard_cutover.down.sql",
	)

	for _, fragment := range []string{
		"DROP COLUMN IF EXISTS curation_plan_id",
		"ON curation_runs(curation_id, created_at DESC)",
		"ON curation_runs(curation_id)",
		"ADD CONSTRAINT curations_pkey PRIMARY KEY (id)",
		"DROP COLUMN IF EXISTS plan_id",
		"DROP COLUMN IF EXISTS status",
		"DROP COLUMN IF EXISTS closed_at",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing canonical cutover fragment %q", fragment)
		}
	}

	for _, forbidden := range []string{
		"REFERENCES curations(plan_id)",
		"ON curation_runs(curation_plan_id",
		"ADD COLUMN curation_plan_id",
		"SET status='CLOSED'",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration retains legacy Curation coupling %q", forbidden)
		}
	}

	for _, fragment := range []string{
		"ADD COLUMN plan_id UUID",
		"ADD COLUMN status TEXT DEFAULT 'OPEN'",
		"ADD COLUMN closed_at TIMESTAMPTZ",
		"ADD CONSTRAINT curations_pkey PRIMARY KEY (plan_id)",
		"ADD COLUMN curation_plan_id UUID",
		"REFERENCES curations(plan_id) ON DELETE CASCADE",
		"ON curation_runs(curation_plan_id, created_at DESC)",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing expand-schema fragment %q", fragment)
		}
	}
}

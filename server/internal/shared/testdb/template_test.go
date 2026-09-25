package testdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) { os.Exit(Run(m)) }
func TestClonesKeepConstraintsAndIsolateRowsAndSchema(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	directory := t.TempDir()
	body := []byte("CREATE TABLE parent(id integer PRIMARY KEY CHECK(id > 0)); CREATE TABLE child(id integer REFERENCES parent(id));")
	if err := os.WriteFile(filepath.Join(directory, "000001.up.sql"), body, 0600); err != nil {
		t.Fatal(err)
	}
	first := Open(t, ctx, base, directory)
	if _, err := first.DB.ExecContext(ctx, "INSERT INTO parent VALUES(1); ALTER TABLE parent ADD COLUMN extra text"); err != nil {
		t.Fatal(err)
	}
	second := Open(t, ctx, base, directory)
	var count int
	if err := second.DB.QueryRowContext(ctx, "SELECT count(*) FROM parent").Scan(&count); err != nil || count != 0 {
		t.Fatalf("row isolation: count=%d err=%v", count, err)
	}
	if _, err := second.DB.ExecContext(ctx, "SELECT extra FROM parent"); err == nil {
		t.Fatal("schema modification leaked to template")
	}
	if _, err := second.DB.ExecContext(ctx, "INSERT INTO parent VALUES(-1)"); err == nil {
		t.Fatal("CHECK lost in clone")
	}
	if _, err := second.DB.ExecContext(ctx, "INSERT INTO child VALUES(99)"); err == nil {
		t.Fatal("foreign key lost in clone")
	}
	if err := second.DB.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration ledger: count=%d err=%v", count, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "000002.up.sql"), []byte("ALTER TABLE parent ADD COLUMN new_version text"), 0600); err != nil {
		t.Fatal(err)
	}
	third := Open(t, ctx, base, directory)
	if _, err := third.DB.ExecContext(ctx, "SELECT new_version FROM parent"); err != nil {
		t.Fatal("migration content change reused stale template:", err)
	}
	if err := third.DB.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("new ledger: count=%d err=%v", count, err)
	}
}

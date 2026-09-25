// Package testdb builds per-test PostgreSQL databases from an in-process,
// migration-content-addressed template. Production code must not import it.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	postgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type template struct {
	name          string
	admin         *postgres.Database
	cleanupParent context.Context
}

var templates = struct {
	sync.Mutex
	entries map[string]template
}{entries: map[string]template{}}

func Enabled() bool { return os.Getenv("VITLANE_TEST_DB_TEMPLATES") == "true" }

func name() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "vitlane_test_" + hex.EncodeToString(suffix[:]), nil
}
func dsn(base, database string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}
func migrationKey(base, directory string) (string, error) {
	files, err := filepath.Glob(filepath.Join(directory, "*.up.sql"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no migrations in template source")
	}
	hash := sha256.New()
	hash.Write([]byte(base))
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "\x00%s\x00%d\x00", filepath.Base(file), len(body))
		hash.Write(body)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func ensureTemplate(ctx context.Context, base, directory string) (template, error) {
	key, err := migrationKey(base, directory)
	if err != nil {
		return template{}, err
	}
	if cached, ok := templates.entries[key]; ok {
		return cached, nil
	}
	admin, err := postgres.Open(ctx, base)
	if err != nil {
		return template{}, err
	}
	id, err := name()
	if err != nil {
		admin.Close()
		return template{}, err
	}
	if _, err = admin.DB.ExecContext(ctx, `CREATE DATABASE "`+id+`" TEMPLATE template0`); err != nil {
		admin.Close()
		return template{}, err
	}
	success := false
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			admin.DB.ExecContext(cleanup, `DROP DATABASE "`+id+`" WITH (FORCE)`)
			admin.Close()
		}
	}()
	target, err := dsn(base, id)
	if err != nil {
		return template{}, err
	}
	db, err := postgres.Open(ctx, target)
	if err != nil {
		return template{}, err
	}
	err = db.Migrate(ctx, directory)
	closeErr := db.Close() // No template sessions may remain when PostgreSQL clones it.
	if err != nil {
		return template{}, err
	}
	if closeErr != nil {
		return template{}, closeErr
	}
	cached := template{name: id, admin: admin, cleanupParent: context.WithoutCancel(ctx)}
	templates.entries[key] = cached
	success = true
	return cached, nil
}

// Open clones only a pristine schema. Test rows, connections and transactions
// belong to the clone, and are never reused by another test.
func Open(t *testing.T, ctx context.Context, base, directory string) *postgres.Database {
	t.Helper()
	started := time.Now()
	templates.Lock()
	cached, err := ensureTemplate(ctx, base, directory)
	if err != nil {
		templates.Unlock()
		t.Fatal(err)
	}
	id, err := name()
	if err == nil {
		_, err = cached.admin.DB.ExecContext(ctx, `CREATE DATABASE "`+id+`" TEMPLATE "`+cached.name+`"`)
	}
	templates.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	target, err := dsn(base, id)
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Open(ctx, target)
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if _, err := cached.admin.DB.ExecContext(cleanup, `DROP DATABASE "`+id+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("test database template+clone ready in %s", time.Since(started).Round(time.Millisecond))
	return db
}

// Run releases templates only after all test-level clone cleanups have run.
func Run(m *testing.M) int {
	code := m.Run()
	templates.Lock()
	defer templates.Unlock()
	for _, cached := range templates.entries {
		ctx, cancel := context.WithTimeout(cached.cleanupParent, 15*time.Second)
		if _, err := cached.admin.DB.ExecContext(ctx, `DROP DATABASE "`+cached.name+`" WITH (FORCE)`); err != nil {
			fmt.Fprintln(os.Stderr, "test template cleanup failed")
			code = 1
		}
		cancel()
		_ = cached.admin.Close()
	}
	templates.entries = map[string]template{}
	return code
}

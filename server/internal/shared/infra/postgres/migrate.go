package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

func (d *Database) Migrate(ctx context.Context, directory string) error {
	ctx = runtimepolicy.WithWorkClass(ctx, runtimepolicy.Admin)
	acquireContext, cancelAcquire := runtimepolicy.WithTimeout(
		ctx, d.config.AcquireTimeout,
	)
	lockConnection, err := d.DB.Conn(acquireContext)
	if err != nil {
		classified := classifyContextError(
			err, acquireContext, "POSTGRES_POOL_ACQUIRE_TIMEOUT",
		)
		cancelAcquire()
		return fmt.Errorf("open migration lock connection: %w", classified)
	}
	cancelAcquire()
	defer lockConnection.Close()
	lockContext, cancelLock := runtimepolicy.WithTimeout(
		ctx, d.config.MigrationLockTimeout,
	)
	if _, err := lockConnection.ExecContext(lockContext,
		`SELECT pg_advisory_lock(hashtextextended('vitlane.schema_migrations', 0))`,
	); err != nil {
		classified := classifyContextError(
			err, lockContext, "POSTGRES_MIGRATION_LOCK_TIMEOUT",
		)
		cancelLock()
		return fmt.Errorf("lock schema migrations: %w", classified)
	}
	cancelLock()
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = lockConnection.ExecContext(
			unlockContext,
			`SELECT pg_advisory_unlock(hashtextextended('vitlane.schema_migrations', 0))`,
		)
	}()

	if _, err := d.Queryer(ctx).ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema migrations: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read migrations %q: %w", directory, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		if err := d.Queryer(ctx).QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}
		body, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := d.WithinTransaction(ctx, func(txContext context.Context) error {
			if _, err := d.Queryer(txContext).ExecContext(txContext, string(body)); err != nil {
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
			if _, err := d.Queryer(txContext).ExecContext(txContext,
				`INSERT INTO schema_migrations(version) VALUES ($1)`, name,
			); err != nil {
				return fmt.Errorf("record migration %s: %w", name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

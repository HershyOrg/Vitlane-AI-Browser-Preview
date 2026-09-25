package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

func TestPoolWaitAndStatementTimeoutsAreBounded(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	t.Run("pool acquisition through query", func(t *testing.T) {
		config := DefaultConfig()
		config.MaxOpenConnections = 2
		config.MaxIdleConnections = 2
		config.AcquireTimeout = 50 * time.Millisecond
		config.Interactive.Query = 500 * time.Millisecond
		database, err := OpenWithConfig(context.Background(), databaseURL, config)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		held, err := database.DB.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldSecond, err := database.DB.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer heldSecond.Close()
		_, err = database.Queryer(context.Background()).ExecContext(
			context.Background(), `SELECT 1`,
		)
		assertDeadlineFault(t, err)
		if stats := database.Stats(); stats.WaitCount == 0 || stats.WaitDuration <= 0 {
			t.Fatalf("pool stats did not observe wait: %#v", stats)
		}
	})

	t.Run("statement timeout rolls back transaction", func(t *testing.T) {
		config := DefaultConfig()
		config.Interactive.Query = 50 * time.Millisecond
		config.Interactive.Transaction = 500 * time.Millisecond
		database, err := OpenWithConfig(context.Background(), databaseURL, config)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		verificationContext := runtimepolicy.WithWorkClass(
			context.Background(), runtimepolicy.Admin,
		)
		table := fmt.Sprintf("runtime_timeout_%d", time.Now().UnixNano())
		if _, err := database.Queryer(verificationContext).ExecContext(
			verificationContext, `CREATE TABLE `+table+` (value TEXT PRIMARY KEY)`,
		); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = database.Queryer(verificationContext).ExecContext(
				verificationContext, `DROP TABLE IF EXISTS `+table,
			)
		}()
		key := fmt.Sprintf("runtime-timeout-%d", time.Now().UnixNano())
		err = database.WithinTransaction(context.Background(), func(ctx context.Context) error {
			if _, err := database.Queryer(ctx).ExecContext(
				ctx, `INSERT INTO `+table+`(value) VALUES ($1)`, key,
			); err != nil {
				return err
			}
			_, err := database.Queryer(ctx).ExecContext(ctx, `SELECT pg_sleep(0.2)`)
			return err
		})
		assertDeadlineFault(t, err)
		var exists bool
		if err := database.Queryer(verificationContext).QueryRowContext(
			verificationContext,
			`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE value=$1)`, key,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("timed out transaction was not rolled back")
		}
	})
}

func TestTransactionLockTimeoutUsesWorkClassPolicy(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config := DefaultConfig()
	config.MaxOpenConnections = 2
	config.MaxIdleConnections = 2
	config.Worker.Query = time.Second
	config.Worker.Transaction = 2 * time.Second
	config.Worker.Lock = 50 * time.Millisecond
	database, err := OpenWithConfig(context.Background(), databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	lockKey := time.Now().UnixNano()
	holder, err := database.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(
		context.Background(), `SELECT pg_advisory_xact_lock($1)`, lockKey,
	); err != nil {
		t.Fatal(err)
	}
	workerContext := runtimepolicy.WithWorkClass(context.Background(), runtimepolicy.Worker)
	err = database.WithinTransaction(workerContext, func(ctx context.Context) error {
		_, err := database.Queryer(ctx).ExecContext(
			ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey,
		)
		return err
	})
	assertDeadlineFault(t, err)
}

func TestMigrationLockTimeoutIsIndependentFromAdminStatementLockTimeout(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config := DefaultConfig()
	config.Admin.Lock = 25 * time.Millisecond
	config.MigrationLockTimeout = 2 * time.Second
	database, err := OpenWithConfig(context.Background(), databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	holder, err := database.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(context.Background(),
		`SELECT pg_advisory_lock(hashtextextended('vitlane.schema_migrations', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock(hashtextextended('vitlane.schema_migrations', 0))`)

	unlockResult := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		unlockContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, unlockErr := holder.ExecContext(unlockContext,
			`SELECT pg_advisory_unlock(hashtextextended('vitlane.schema_migrations', 0))`)
		unlockResult <- unlockErr
	}()
	startedAt := time.Now()
	migrationContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.Migrate(migrationContext, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := <-unlockResult; err != nil {
		t.Fatalf("unlock schema migration guard: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed < 100*time.Millisecond {
		t.Fatalf("migration did not wait for the held advisory lock: %s", elapsed)
	}
}

func TestMigrationLockTimeoutIsClassified(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config := DefaultConfig()
	config.MigrationLockTimeout = 50 * time.Millisecond
	database, err := OpenWithConfig(context.Background(), databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	holder, err := database.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(context.Background(),
		`SELECT pg_advisory_lock(hashtextextended('vitlane.schema_migrations', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock(hashtextextended('vitlane.schema_migrations', 0))`)

	err = database.Migrate(context.Background(), t.TempDir())
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.DeadlineExceeded ||
		failure.Reason != "POSTGRES_MIGRATION_LOCK_TIMEOUT" {
		t.Fatalf("migration lock fault=%#v err=%v", failure, err)
	}
}

func assertDeadlineFault(t *testing.T, err error) {
	t.Helper()
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.DeadlineExceeded {
		t.Fatalf("deadline fault=%#v err=%v", failure, err)
	}
}

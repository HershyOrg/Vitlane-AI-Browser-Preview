package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type Row interface {
	Scan(...any) error
}

type Rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) Row
	QueryContext(context.Context, string, ...any) (Rows, error)
}

type rawQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type txContextKey struct{}

type Database struct {
	DB     *sql.DB
	config Config
}

type TimeoutClass struct {
	Query       time.Duration
	Transaction time.Duration
	Lock        time.Duration
}

type Config struct {
	MaxOpenConnections   int
	MaxIdleConnections   int
	ConnMaxIdleTime      time.Duration
	ConnMaxLifetime      time.Duration
	AcquireTimeout       time.Duration
	MigrationLockTimeout time.Duration
	Interactive          TimeoutClass
	Worker               TimeoutClass
	Admin                TimeoutClass
}

func DefaultConfig() Config {
	return Config{
		MaxOpenConnections:   40,
		MaxIdleConnections:   10,
		ConnMaxIdleTime:      5 * time.Minute,
		ConnMaxLifetime:      30 * time.Minute,
		AcquireTimeout:       2 * time.Second,
		MigrationLockTimeout: 2 * time.Minute,
		Interactive: TimeoutClass{
			Query: 3 * time.Second, Transaction: 8 * time.Second,
			Lock: time.Second,
		},
		Worker: TimeoutClass{
			Query: 5 * time.Second, Transaction: 15 * time.Second,
			Lock: time.Second,
		},
		Admin: TimeoutClass{
			Query: 60 * time.Second, Transaction: 2 * time.Minute,
			Lock: 5 * time.Second,
		},
	}
}

func (c Config) Validate() error {
	if c.MaxOpenConnections < 2 || c.MaxOpenConnections > 200 {
		return fmt.Errorf("PostgreSQL max open connections must be between 2 and 200")
	}
	if c.MaxIdleConnections < 0 || c.MaxIdleConnections > c.MaxOpenConnections {
		return fmt.Errorf("PostgreSQL max idle connections must be between 0 and max open")
	}
	if c.ConnMaxIdleTime <= 0 || c.ConnMaxLifetime <= 0 ||
		c.ConnMaxLifetime < c.ConnMaxIdleTime || c.AcquireTimeout <= 0 ||
		c.MigrationLockTimeout <= 0 {
		return fmt.Errorf("PostgreSQL connection timeouts must be positive and lifetime must cover idle time")
	}
	if c.ConnMaxIdleTime > time.Hour || c.ConnMaxLifetime > 24*time.Hour ||
		c.AcquireTimeout > 30*time.Second || c.MigrationLockTimeout > 10*time.Minute {
		return fmt.Errorf("PostgreSQL connection timeouts exceed operational upper bounds")
	}
	for name, class := range map[string]TimeoutClass{
		"interactive": c.Interactive, "worker": c.Worker, "migration/admin": c.Admin,
	} {
		if class.Query <= 0 || class.Transaction <= 0 || class.Lock <= 0 ||
			class.Transaction < class.Query || class.Query > 5*time.Minute ||
			class.Transaction > 10*time.Minute || class.Lock > time.Minute {
			return fmt.Errorf("PostgreSQL %s timeout class is invalid", name)
		}
	}
	return nil
}

func Open(ctx context.Context, databaseURL string) (*Database, error) {
	return OpenWithConfig(ctx, databaseURL, DefaultConfig())
}

func OpenWithConfig(
	ctx context.Context,
	databaseURL string,
	config Config,
) (*Database, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(config.MaxOpenConnections)
	db.SetMaxIdleConns(config.MaxIdleConnections)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	pingContext, cancel := runtimepolicy.WithTimeout(ctx, config.AcquireTimeout)
	defer cancel()
	if err := db.PingContext(pingContext); err != nil {
		db.Close()
		return nil, classifyContextError(err, pingContext, "POSTGRES_PING_TIMEOUT")
	}
	return &Database{DB: db, config: config}, nil
}

func (d *Database) Close() error {
	return d.DB.Close()
}

func (d *Database) Ping(ctx context.Context) error {
	pingContext, cancel := runtimepolicy.WithTimeout(ctx, d.config.AcquireTimeout)
	defer cancel()
	return classifyContextError(
		d.DB.PingContext(pingContext), pingContext, "POSTGRES_PING_TIMEOUT",
	)
}

func (d *Database) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, exists := ctx.Value(txContextKey{}).(*sql.Tx); exists {
		return fn(ctx)
	}
	timeouts := d.timeoutClass(runtimepolicy.WorkClassFrom(ctx))
	txContext, cancelTransaction := runtimepolicy.WithTimeout(ctx, timeouts.Transaction)
	defer cancelTransaction()
	acquireContext, cancelAcquire := runtimepolicy.WithTimeout(txContext, d.config.AcquireTimeout)
	connection, err := d.DB.Conn(acquireContext)
	if err != nil {
		classified := classifyContextError(err, acquireContext, "POSTGRES_POOL_ACQUIRE_TIMEOUT")
		cancelAcquire()
		return classified
	}
	cancelAcquire()
	defer connection.Close()
	tx, err := connection.BeginTx(txContext, nil)
	if err != nil {
		return classifyContextError(err, txContext, "POSTGRES_BEGIN_TIMEOUT")
	}
	if _, err := tx.ExecContext(txContext, `
		SELECT set_config('statement_timeout', $1, true),
		       set_config('lock_timeout', $2, true)
	`, postgresDuration(timeouts.Query), postgresDuration(timeouts.Lock)); err != nil {
		_ = tx.Rollback()
		return classifyContextError(err, txContext, "POSTGRES_TRANSACTION_POLICY_FAILED")
	}
	transactionContext := sharedapp.MarkTransaction(context.WithValue(txContext, txContextKey{}, tx))
	if err := fn(transactionContext); err != nil {
		_ = tx.Rollback()
		return classifyContextError(err, txContext, "POSTGRES_TRANSACTION_TIMEOUT")
	}
	if err := tx.Commit(); err != nil {
		return fault.Wrap(
			err, fault.ExternalEffectUnknown, "POSTGRES_COMMIT_UNKNOWN", false,
		)
	}
	return nil
}

func (d *Database) Queryer(ctx context.Context) queryer {
	if tx, ok := ctx.Value(txContextKey{}).(*sql.Tx); ok {
		return timedQueryer{delegate: tx, timeout: d.timeoutClass(runtimepolicy.WorkClassFrom(ctx)).Query}
	}
	return timedQueryer{
		delegate: d.DB, database: d.DB,
		acquireTimeout: d.config.AcquireTimeout,
		timeout:        d.timeoutClass(runtimepolicy.WorkClassFrom(ctx)).Query,
	}
}

func (d *Database) Config() Config { return d.config }

type Stats struct {
	MaxOpenConnections int           `json:"maxOpenConnections"`
	OpenConnections    int           `json:"openConnections"`
	InUse              int           `json:"inUse"`
	Idle               int           `json:"idle"`
	WaitCount          int64         `json:"waitCount"`
	WaitDuration       time.Duration `json:"waitDurationNanoseconds"`
	MaxIdleClosed      int64         `json:"maxIdleClosed"`
	MaxIdleTimeClosed  int64         `json:"maxIdleTimeClosed"`
	MaxLifetimeClosed  int64         `json:"maxLifetimeClosed"`
}

func (d *Database) Stats() Stats {
	stats := d.DB.Stats()
	return Stats{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections, InUse: stats.InUse, Idle: stats.Idle,
		WaitCount: stats.WaitCount, WaitDuration: stats.WaitDuration,
		MaxIdleClosed: stats.MaxIdleClosed, MaxIdleTimeClosed: stats.MaxIdleTimeClosed,
		MaxLifetimeClosed: stats.MaxLifetimeClosed,
	}
}

func (d *Database) timeoutClass(class runtimepolicy.WorkClass) TimeoutClass {
	switch class {
	case runtimepolicy.Worker:
		return d.config.Worker
	case runtimepolicy.Admin:
		return d.config.Admin
	default:
		return d.config.Interactive
	}
}

type timedQueryer struct {
	delegate       rawQueryer
	database       *sql.DB
	acquireTimeout time.Duration
	timeout        time.Duration
}

func (q timedQueryer) ExecContext(
	ctx context.Context, statement string, args ...any,
) (sql.Result, error) {
	delegate, release, err := q.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	queryContext, cancel := runtimepolicy.WithTimeout(ctx, q.timeout)
	defer cancel()
	result, err := delegate.ExecContext(queryContext, statement, args...)
	return result, classifyContextError(err, queryContext, "POSTGRES_QUERY_TIMEOUT")
}

func (q timedQueryer) QueryRowContext(
	ctx context.Context, statement string, args ...any,
) Row {
	delegate, release, err := q.acquire(ctx)
	if err != nil {
		return &timedRow{err: err}
	}
	queryContext, cancel := runtimepolicy.WithTimeout(ctx, q.timeout)
	row := &timedRow{
		row: delegate.QueryRowContext(queryContext, statement, args...),
		ctx: queryContext,
	}
	row.release = func() {
		row.once.Do(func() {
			cancel()
			release()
		})
	}
	row.stop = context.AfterFunc(queryContext, row.release)
	return row
}

func (q timedQueryer) QueryContext(
	ctx context.Context, statement string, args ...any,
) (Rows, error) {
	delegate, release, err := q.acquire(ctx)
	if err != nil {
		return nil, err
	}
	queryContext, cancel := runtimepolicy.WithTimeout(ctx, q.timeout)
	rows, err := delegate.QueryContext(queryContext, statement, args...)
	if err != nil {
		classified := classifyContextError(err, queryContext, "POSTGRES_QUERY_TIMEOUT")
		cancel()
		release()
		return nil, classified
	}
	timed := &timedRows{
		rows: rows, ctx: queryContext, cancel: cancel, releaseConnection: release,
	}
	timed.stop = context.AfterFunc(queryContext, timed.release)
	return timed, nil
}

func (q timedQueryer) acquire(ctx context.Context) (rawQueryer, func(), error) {
	if q.database == nil {
		return q.delegate, func() {}, nil
	}
	acquireContext, cancel := runtimepolicy.WithTimeout(ctx, q.acquireTimeout)
	connection, err := q.database.Conn(acquireContext)
	if err != nil {
		classified := classifyContextError(
			err, acquireContext, "POSTGRES_POOL_ACQUIRE_TIMEOUT",
		)
		cancel()
		return nil, nil, classified
	}
	cancel()
	return connection, func() { _ = connection.Close() }, nil
}

type timedRow struct {
	row     *sql.Row
	err     error
	ctx     context.Context
	release func()
	stop    func() bool
	once    sync.Once
}

func (r *timedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.stop != nil {
		r.stop()
	}
	defer r.release()
	return classifyContextError(
		r.row.Scan(dest...), r.ctx, "POSTGRES_QUERY_TIMEOUT",
	)
}

type timedRows struct {
	rows              *sql.Rows
	ctx               context.Context
	cancel            context.CancelFunc
	releaseConnection func()
	stop              func() bool
	once              sync.Once
}

func (r *timedRows) Next() bool {
	next := r.rows.Next()
	if !next {
		r.release()
	}
	return next
}
func (r *timedRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *timedRows) Close() error {
	err := r.rows.Close()
	r.release()
	return classifyContextError(err, r.ctx, "POSTGRES_QUERY_TIMEOUT")
}
func (r *timedRows) Err() error {
	return classifyContextError(
		r.rows.Err(), r.ctx, "POSTGRES_QUERY_TIMEOUT",
	)
}

func (r *timedRows) release() {
	r.once.Do(func() {
		if r.stop != nil {
			r.stop()
		}
		_ = r.rows.Close()
		r.cancel()
		if r.releaseConnection != nil {
			r.releaseConnection()
		}
	})
}

func postgresDuration(value time.Duration) string {
	return fmt.Sprintf("%dms", value.Milliseconds())
}

func classifyContextError(err error, ctx context.Context, reason string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fault.Wrap(err, fault.DeadlineExceeded, reason, true)
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return fault.Wrap(err, fault.CallerCancelled, "POSTGRES_CALLER_CANCELLED", false)
	default:
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case "57014":
				return fault.Wrap(
					err, fault.DeadlineExceeded,
					"POSTGRES_STATEMENT_TIMEOUT", true,
				)
			case "55P03":
				return fault.Wrap(
					err, fault.DeadlineExceeded,
					"POSTGRES_LOCK_TIMEOUT", true,
				)
			}
		}
		return err
	}
}

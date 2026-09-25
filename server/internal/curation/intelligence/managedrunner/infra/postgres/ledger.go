package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

const serverScopeID = "SERVER"

type Ledger struct {
	database *sharedpostgres.Database
}

func NewLedger(database *sharedpostgres.Database) *Ledger {
	return &Ledger{database: database}
}

// LockCounters takes both ledger rows in one fixed order: SERVER first, then
// the user. Every caller uses this one helper, so two concurrent reservations
// can never grab the rows in opposite orders and deadlock.
//
// The rows are upserted before locking because a first-of-the-day call has no
// row to lock, and two such calls racing would otherwise both insert.
func (l *Ledger) LockCounters(
	ctx context.Context,
	usageDate time.Time,
	userID string,
	now time.Time,
) (server runnerdomain.UsageCounters, user runnerdomain.UsageCounters, err error) {

	server, err = l.lockScope(ctx, usageDate, "SERVER", serverScopeID, now)
	if err != nil {
		return server, user, err
	}
	if userID == "" {
		return server, user, nil
	}
	user, err = l.lockScope(ctx, usageDate, "USER", userID, now)
	return server, user, err
}

func (l *Ledger) lockScope(
	ctx context.Context,
	usageDate time.Time,
	scope string,
	scopeID string,
	now time.Time,
) (runnerdomain.UsageCounters, error) {
	queryer := l.database.Queryer(ctx)
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO managed_runner_usage_daily(
			usage_date, scope, scope_id, reserved_micros, settled_micros,
			request_count, input_tokens, output_tokens, updated_at
		) VALUES ($1,$2,$3,0,0,0,0,0,$4)
		ON CONFLICT (usage_date, scope, scope_id) DO NOTHING
	`, usageDate, scope, scopeID, now); err != nil {
		return runnerdomain.UsageCounters{}, fmt.Errorf(
			"ensure managed runner usage row: %w", err,
		)
	}
	var counters runnerdomain.UsageCounters
	err := queryer.QueryRowContext(ctx, `
		SELECT reserved_micros, settled_micros, request_count,
		       input_tokens, output_tokens
		FROM managed_runner_usage_daily
		WHERE usage_date=$1 AND scope=$2 AND scope_id=$3
		FOR UPDATE
	`, usageDate, scope, scopeID).Scan(
		&counters.ReservedMicros, &counters.SettledMicros,
		&counters.RequestCount, &counters.InputTokens,
		&counters.OutputTokens,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runnerdomain.UsageCounters{}, nil
	}
	if err != nil {
		return runnerdomain.UsageCounters{}, fmt.Errorf(
			"lock managed runner usage row: %w", err,
		)
	}
	return counters, nil
}

// ReadCounters is the lock-free read used by the usage endpoint.
func (l *Ledger) ReadCounters(
	ctx context.Context,
	usageDate time.Time,
	userID string,
) (server runnerdomain.UsageCounters, user runnerdomain.UsageCounters, err error) {
	server, err = l.readScope(ctx, usageDate, "SERVER", serverScopeID)
	if err != nil {
		return server, user, err
	}
	if userID == "" {
		return server, user, nil
	}
	user, err = l.readScope(ctx, usageDate, "USER", userID)
	return server, user, err
}

func (l *Ledger) readScope(
	ctx context.Context,
	usageDate time.Time,
	scope string,
	scopeID string,
) (runnerdomain.UsageCounters, error) {
	var counters runnerdomain.UsageCounters
	err := l.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT reserved_micros, settled_micros, request_count,
		       input_tokens, output_tokens
		FROM managed_runner_usage_daily
		WHERE usage_date=$1 AND scope=$2 AND scope_id=$3
	`, usageDate, scope, scopeID).Scan(
		&counters.ReservedMicros, &counters.SettledMicros,
		&counters.RequestCount, &counters.InputTokens,
		&counters.OutputTokens,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runnerdomain.UsageCounters{}, nil
	}
	if err != nil {
		return runnerdomain.UsageCounters{}, fmt.Errorf(
			"read managed runner usage: %w", err,
		)
	}
	return counters, nil
}

func (l *Ledger) ListServerUsage(
	ctx context.Context,
	from time.Time,
	through time.Time,
) ([]runnerdomain.DailyUsage, error) {
	rows, err := l.database.Queryer(ctx).QueryContext(ctx, `
		SELECT usage_date, reserved_micros, settled_micros, request_count,
		       input_tokens, output_tokens
		FROM managed_runner_usage_daily
		WHERE scope='SERVER' AND scope_id=$1
		  AND usage_date >= $2 AND usage_date <= $3
		ORDER BY usage_date
	`, serverScopeID, from, through)
	if err != nil {
		return nil, fmt.Errorf("list managed runner server usage: %w", err)
	}
	defer rows.Close()
	usage := make([]runnerdomain.DailyUsage, 0)
	for rows.Next() {
		var day runnerdomain.DailyUsage
		if err := rows.Scan(
			&day.UsageDate, &day.Counters.ReservedMicros,
			&day.Counters.SettledMicros, &day.Counters.RequestCount,
			&day.Counters.InputTokens, &day.Counters.OutputTokens,
		); err != nil {
			return nil, fmt.Errorf("scan managed runner server usage: %w", err)
		}
		usage = append(usage, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed runner server usage: %w", err)
	}
	return usage, nil
}

func (l *Ledger) AddReserved(
	ctx context.Context,
	usageDate time.Time,
	userID string,
	amountMicros int64,
	now time.Time,
) error {
	if err := l.addScope(
		ctx, usageDate, "SERVER", serverScopeID,
		amountMicros, 0, 1, runnerdomain.TokenUsage{}, now,
	); err != nil {
		return err
	}
	if userID == "" {
		return nil
	}
	return l.addScope(
		ctx, usageDate, "USER", userID,
		amountMicros, 0, 1, runnerdomain.TokenUsage{}, now,
	)
}

// Settle swaps the reservation for the real cost on both scopes. reservedMicros
// is subtracted and settledMicros added in the same statement so the row is
// never transiently double-counted.
func (l *Ledger) Settle(
	ctx context.Context,
	usageDate time.Time,
	userID string,
	reservedMicros int64,
	settledMicros int64,
	usage runnerdomain.TokenUsage,
	now time.Time,
) error {
	if err := l.addScope(
		ctx, usageDate, "SERVER", serverScopeID,
		-reservedMicros, settledMicros, 0, usage, now,
	); err != nil {
		return err
	}
	if userID == "" {
		return nil
	}
	return l.addScope(
		ctx, usageDate, "USER", userID,
		-reservedMicros, settledMicros, 0, usage, now,
	)
}

func (l *Ledger) addScope(
	ctx context.Context,
	usageDate time.Time,
	scope string,
	scopeID string,
	reservedDelta int64,
	settledDelta int64,
	requestDelta int64,
	usage runnerdomain.TokenUsage,
	now time.Time,
) error {
	// GREATEST(...,0) keeps the non-negative CHECK satisfied if a reconciler
	// release races a settle for the same reservation. Losing a few micros of
	// headroom is preferable to a constraint violation aborting the pipeline.
	result, err := l.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE managed_runner_usage_daily
		SET reserved_micros=GREATEST(reserved_micros + $4, 0),
		    settled_micros=settled_micros + $5,
		    request_count=request_count + $6,
		    input_tokens=input_tokens + $7,
		    output_tokens=output_tokens + $8,
		    updated_at=$9
		WHERE usage_date=$1 AND scope=$2 AND scope_id=$3
	`, usageDate, scope, scopeID, reservedDelta, settledDelta,
		requestDelta, usage.InputTokens, usage.OutputTokens, now)
	if err != nil {
		return fmt.Errorf("update managed runner usage: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update managed runner usage rows: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf(
			"update managed runner usage: %d rows affected", affected,
		)
	}
	return nil
}

func (l *Ledger) InsertReservation(
	ctx context.Context,
	reservation runnerdomain.Reservation,
) error {
	_, err := l.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO managed_runner_reservations(
			id, usage_date, user_id, attempt_id, request_key,
			amount_micros, status, expires_at, created_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, reservation.ID, reservation.UsageDate, nullableString(reservation.UserID),
		nullableString(reservation.AttemptID), reservation.RequestKey,
		reservation.AmountMicros, reservation.Status, reservation.ExpiresAt, reservation.CreatedAt,
		reservation.CompletedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" &&
			postgresError.ConstraintName == "managed_runner_reservations_request_key_idx" {
			return fmt.Errorf("%w: request key", runnerdomain.ErrReservationExists)
		}
		return fmt.Errorf("insert managed runner reservation: %w", err)
	}
	return nil
}

// CloseReservation moves a HELD row to a terminal state. It matches on
// status='HELD' so a settle arriving after the reconciler already released the
// row reports zero rows instead of double-refunding the ledger.
func (l *Ledger) CloseReservation(
	ctx context.Context,
	id string,
	status runnerdomain.ReservationStatus,
	now time.Time,
) (bool, error) {
	result, err := l.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE managed_runner_reservations
		SET status=$2, completed_at=$3
		WHERE id=$1 AND status='HELD'
	`, id, status, now)
	if err != nil {
		return false, fmt.Errorf("close managed runner reservation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(
			"close managed runner reservation rows: %w", err,
		)
	}
	return affected == 1, nil
}

// ExpiredReservations returns HELD rows past their expiry. A crashed process
// leaves budget held forever without this sweep.
func (l *Ledger) ExpiredReservations(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]runnerdomain.Reservation, error) {
	rows, err := l.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, usage_date, COALESCE(user_id::text,''), attempt_id, request_key, amount_micros,
		       status, expires_at, created_at
		FROM managed_runner_reservations
		WHERE status='HELD' AND expires_at <= $1
		ORDER BY expires_at, id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired managed runner reservations: %w", err)
	}
	defer rows.Close()
	var reservations []runnerdomain.Reservation
	for rows.Next() {
		var value runnerdomain.Reservation
		var attemptID sql.NullString
		if err := rows.Scan(
			&value.ID, &value.UsageDate, &value.UserID, &attemptID,
			&value.RequestKey, &value.AmountMicros, &value.Status, &value.ExpiresAt,
			&value.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan managed runner reservation: %w", err)
		}
		value.AttemptID = attemptID.String
		reservations = append(reservations, value)
	}
	return reservations, rows.Err()
}

// ReservationForUpdate row-locks one reservation so the GAP-024 resolution
// decision and the counter adjustment commit in the same transaction.
func (l *Ledger) ReservationForUpdate(
	ctx context.Context,
	id string,
) (runnerdomain.Reservation, bool, error) {
	var value runnerdomain.Reservation
	var attemptID sql.NullString
	err := l.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, usage_date, COALESCE(user_id::text,''), attempt_id, request_key, amount_micros,
		       status, expires_at, created_at, completed_at
		FROM managed_runner_reservations
		WHERE id=$1
		FOR UPDATE
	`, id).Scan(
		&value.ID, &value.UsageDate, &value.UserID, &attemptID,
		&value.RequestKey, &value.AmountMicros, &value.Status, &value.ExpiresAt,
		&value.CreatedAt, &value.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runnerdomain.Reservation{}, false, nil
	}
	if err != nil {
		return runnerdomain.Reservation{}, false, fmt.Errorf(
			"lock managed runner reservation: %w", err,
		)
	}
	value.AttemptID = attemptID.String
	return value, true, nil
}

// ResolveUnknownReservation transitions an UNKNOWN row to its audited
// terminal state. Matching on status='UNKNOWN' keeps the resolution
// exactly-once under concurrency and across restarts.
func (l *Ledger) ResolveUnknownReservation(
	ctx context.Context,
	id string,
	status runnerdomain.ReservationStatus,
	now time.Time,
) (bool, error) {
	result, err := l.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE managed_runner_reservations
		SET status=$2, completed_at=$3
		WHERE id=$1 AND status='UNKNOWN'
	`, id, status, now)
	if err != nil {
		return false, fmt.Errorf("resolve managed runner reservation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(
			"resolve managed runner reservation rows: %w", err,
		)
	}
	return affected == 1, nil
}

func (l *Ledger) InsertReservationResolution(
	ctx context.Context,
	resolution runnerdomain.ReservationResolution,
) error {
	_, err := l.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO managed_runner_reservation_resolutions(
			reservation_id, operator_user_id, outcome, reason_detail,
			evidence_reference, settled_amount_micros, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, resolution.ReservationID, resolution.OperatorUserID, resolution.Outcome,
		resolution.ReasonDetail, resolution.EvidenceReference,
		resolution.SettledAmountMicros, resolution.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return fmt.Errorf(
				"%w: already resolved", runnerdomain.ErrReservationNotUnknown,
			)
		}
		return fmt.Errorf("insert managed runner reservation resolution: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

package app

import (
	"context"
	"time"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

// LedgerPort is the durable daily cost ledger. LockCounters must always take
// the SERVER row before the USER row so concurrent reservations cannot deadlock
// by grabbing them in opposite orders.
type LedgerPort interface {
	LockCounters(
		ctx context.Context, usageDate time.Time, userID string, now time.Time,
	) (server runnerdomain.UsageCounters, user runnerdomain.UsageCounters, err error)
	ReadCounters(
		ctx context.Context, usageDate time.Time, userID string,
	) (server runnerdomain.UsageCounters, user runnerdomain.UsageCounters, err error)
	ListServerUsage(
		ctx context.Context, from time.Time, through time.Time,
	) ([]runnerdomain.DailyUsage, error)
	AddReserved(
		ctx context.Context, usageDate time.Time, userID string,
		amountMicros int64, now time.Time,
	) error
	Settle(
		ctx context.Context, usageDate time.Time, userID string,
		reservedMicros int64, settledMicros int64,
		usage runnerdomain.TokenUsage, now time.Time,
	) error
	InsertReservation(ctx context.Context, reservation runnerdomain.Reservation) error
	CloseReservation(
		ctx context.Context, id string,
		status runnerdomain.ReservationStatus, now time.Time,
	) (bool, error)
	ExpiredReservations(
		ctx context.Context, now time.Time, limit int,
	) ([]runnerdomain.Reservation, error)
	// GAP-024 operator reconciliation. ReservationForUpdate row-locks the
	// reservation so the resolve decision and the counter adjustment commit
	// together; ResolveUnknownReservation only transitions UNKNOWN rows.
	ReservationForUpdate(
		ctx context.Context, id string,
	) (runnerdomain.Reservation, bool, error)
	ResolveUnknownReservation(
		ctx context.Context, id string,
		status runnerdomain.ReservationStatus, now time.Time,
	) (bool, error)
	InsertReservationResolution(
		ctx context.Context, resolution runnerdomain.ReservationResolution,
	) error
}

// ModelPort is the only place a provider SDK is allowed to appear. It takes a
// rendered prompt and returns raw JSON plus the token usage the ledger settles
// against.
type ModelPort interface {
	Complete(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

type ModelRequest struct {
	Images       []ModelImage
	Model        runnerdomain.Model
	RequestKey   string
	SystemPrompt string
	UserPrompt   string
	// SchemaName and Schema constrain the response to structured JSON so the
	// pipeline never has to parse prose.
	SchemaName string
	Schema     map[string]any
}

type ModelResponse struct {
	Content string
	Usage   runnerdomain.TokenUsage
}

type ModelImage struct {
	ObservationID string
	URL           string
}

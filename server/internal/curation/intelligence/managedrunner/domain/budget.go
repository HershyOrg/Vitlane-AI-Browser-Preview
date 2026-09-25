package domain

import "time"

// Limits are the daily caps from ADR-0032 section 7, in USD micros.
//
// ServerAdmission is deliberately below ServerHard: new model calls stop at the
// admission line so work already in flight can still settle inside the hard cap
// instead of being abandoned half-done.
type Limits struct {
	ServerHard      int64
	ServerAdmission int64
	UserDaily       int64
}

func DefaultLimits() Limits {
	return Limits{
		ServerHard:      10_000_000, // $10.00
		ServerAdmission: 8_000_000,  // $8.00
		UserDaily:       100_000,    // $0.10
	}
}

func (l Limits) Valid() bool {
	return l.ServerHard > 0 && l.UserDaily > 0 &&
		l.ServerAdmission > 0 && l.ServerAdmission <= l.ServerHard
}

// UsageCounters is one row of the daily ledger.
type UsageCounters struct {
	ReservedMicros int64
	SettledMicros  int64
	RequestCount   int64
	InputTokens    int64
	OutputTokens   int64
}

func (c UsageCounters) CommittedMicros() int64 {
	return c.ReservedMicros + c.SettledMicros
}

func (c UsageCounters) TotalTokens() int64 {
	return c.InputTokens + c.OutputTokens
}

// DailyUsage is the server-level read model used by the operator history.
// Tokens are recorded only after a provider response settles. A held
// reservation contributes to CommittedMicros but never invents token usage.
type DailyUsage struct {
	UsageDate time.Time
	Counters  UsageCounters
}

// Admit decides whether one model call of worstCost may proceed. The server cap
// is checked first so a single user cannot be told they have room when the
// server does not.
func (l Limits) Admit(
	server UsageCounters,
	user UsageCounters,
	worstCost int64,
) error {
	if worstCost <= 0 {
		return ErrModelResponse
	}
	if server.CommittedMicros()+worstCost > l.ServerAdmission {
		return ErrServerDailyLimit
	}
	if user.CommittedMicros()+worstCost > l.UserDaily {
		return ErrUserDailyLimit
	}
	return nil
}

// ServerExhausted reports whether new managed work should be refused outright.
// The Web uses this to block MANAGED plan submission before the user types an
// intent that cannot run.
func (l Limits) ServerExhausted(server UsageCounters) bool {
	return server.CommittedMicros() >= l.ServerAdmission
}

// UsageDate is the ledger bucket. UTC is fixed so the reset boundary does not
// move with the server's local timezone or with the user's.
func UsageDate(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

type ReservationStatus string

const (
	ReservationHeld     ReservationStatus = "HELD"
	ReservationSettled  ReservationStatus = "SETTLED"
	ReservationReleased ReservationStatus = "RELEASED"
	ReservationUnknown  ReservationStatus = "UNKNOWN"
)

// Reservation is the durable record of budget taken before a model call. A
// process crash cannot prove the paid request did not run, so an expired HELD
// row becomes UNKNOWN and keeps its reserved headroom until reconciled.
type Reservation struct {
	ID           string
	UsageDate    time.Time
	UserID       string
	AttemptID    string
	RequestKey   string
	AmountMicros int64
	Status       ReservationStatus
	ExpiresAt    time.Time
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// ReservationResolution is the audited operator decision that closes an
// UNKNOWN reservation (GAP-024). EvidenceReference points at where the
// provider billing/usage evidence lives; it never carries the evidence body.
type ReservationResolution struct {
	ReservationID       string
	OperatorUserID      string
	Outcome             ReservationStatus
	ReasonDetail        string
	EvidenceReference   string
	SettledAmountMicros *int64
	CreatedAt           time.Time
}

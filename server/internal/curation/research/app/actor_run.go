package app

import (
	"context"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// Actor runs are the only paid path that keeps charging after the request
// that started it returns, so every run is written down before it starts and
// settled when it ends. The ledger answers three operational questions: what
// this month cost, whether an attempt already paid for its run, and which run
// to abort when the Round is cancelled.
const (
	ActorRunRunning   = "RUNNING"
	ActorRunSucceeded = "SUCCEEDED"
	ActorRunFailed    = "FAILED"
	ActorRunAborted   = "ABORTED"
)

type ActorRun struct {
	RunKey            string
	UserID            string
	APIID             string
	Source            string
	ProviderRunID     string
	Status            string
	ItemCount         int
	CostMicros        int64
	ReservedMicros    int64
	BudgetLimitMicros int64
	StartedAt         time.Time
	FinishedAt        *time.Time
}

// ActorRunLedger is optional: without it the gateway refuses to start a run,
// because an unrecorded run is an unbounded bill.
type ActorRunLedger interface {
	// BeginActorRun writes a RUNNING row for the key. The second result is
	// false when the key already has a row, whose current state is returned:
	// one attempt never pays twice for the same mall.
	BeginActorRun(context.Context, ActorRun) (ActorRun, bool, error)
	// FinishActorRun settles the row. Costs are provider-reported micros USD.
	FinishActorRun(context.Context, ActorRun) error
	// ActorSpendMicros totals settled and in-flight cost since a point in time.
	ActorSpendMicros(context.Context, time.Time) (int64, error)
}

// ActorSearchRequest is one mall's discovery through a paid Actor.
type ActorSearchRequest struct {
	Mall   researchdomain.KRMall
	Query  string
	RunKey string
	UserID string
	Limit  int
}

// ActorSearchResult mirrors the public-JSON mall result so the Round treats
// every discovery path the same way.
type ActorSearchResult struct {
	Products   []researchdomain.ExternalProductObservation
	Items      int
	Rejected   int
	CostMicros int64
	Reused     bool
}

// ActorMonthStart is the window the monthly budget cap is measured over.
func ActorMonthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

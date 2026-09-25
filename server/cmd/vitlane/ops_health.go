package main

import (
	"context"
	"net/http"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// chainHeadsReader is the read-only view of the settlement gateway the ops
// health report needs. A nil phase5 runtime means the reader is never used.
type chainHeadsReader interface {
	Heads(context.Context) (settlementapp.ChainHeads, error)
}

// opsCoreHealth is the part of the report that decides public readiness:
// PostgreSQL plus the local state required to serve requests (ADR-0040 §3).
type opsCoreHealth struct {
	Status       string               `json:"status"`
	ReasonCodes  []string             `json:"reasonCodes"`
	DatabasePool sharedpostgres.Stats `json:"databasePool"`
}

// opsHealthReport separates core availability from settlement runtime
// degradation so a GIWA/worker fault never reads as a core outage. The
// observed section carries PII-free aggregates; thresholds and alerting
// live in the host watch layer (ADR-0040 §6).
type opsHealthReport struct {
	Status              string                  `json:"status"`
	Core                opsCoreHealth           `json:"core"`
	DegradedReasonCodes []string                `json:"degradedReasonCodes"`
	Settlement          *settlementHealthReport `json:"settlement,omitempty"`
	HTTP                httpMetricsSnapshot     `json:"http"`
	Observed            *opsAggregates          `json:"observed,omitempty"`
	Host                *opsHostFacts           `json:"host,omitempty"`
}

// opsReporter bundles everything one ops health report needs so the three
// consumers — the operator API, the loopback listener for the host watch and
// the time-series sampler — assemble the exact same document.
type opsReporter struct {
	database         *sharedpostgres.Database
	metrics          *httpMetrics
	settlementHealth *settlementRuntimeHealth
	chain            chainHeadsReader
	chainID          uint64
	contractAddress  string
	activeKeyVersion string
	hostFactsPath    string
}

func (r *opsReporter) report(ctx context.Context, now time.Time) opsHealthReport {
	report := buildOpsHealthReport(
		ctx, now, r.database, r.metrics, r.settlementHealth, r.chain,
		r.chainID, r.contractAddress, r.activeKeyVersion,
	)
	report.Host = readHostFacts(r.hostFactsPath, now)
	return report
}

type opsAggregates struct {
	PIIAccess        piiAccessSummary    `json:"piiAccess"`
	PIILifecycle     piiLifecycleSummary `json:"piiLifecycle"`
	Intelligence     intelligenceSummary `json:"intelligence"`
	CostReservations reservationSummary  `json:"costReservations"`
	Accounts         accountSummary      `json:"accounts"`
}

type piiLifecycleSummary struct {
	// PendingDeletions counts deletion requests the pipeline has not purged
	// yet (grace window or backlog).
	PendingDeletions int64 `json:"pendingDeletions"`
	// StaleKeyRows counts unpurged ciphertext rows still sealed under a
	// non-active key version; rotation is complete when this reaches zero.
	StaleKeyRows int64 `json:"staleKeyRows"`
}

type piiAccessSummary struct {
	Granted24h int64 `json:"granted24h"`
	Denied24h  int64 `json:"denied24h"`
	// BrokenChainLinks counts events whose previous_event_hash does not match
	// the prior event in the same AgencyOrder chain. Any value above zero is an
	// audit-integrity incident (alert A-17).
	BrokenChainLinks int64 `json:"brokenChainLinks"`
}

type intelligenceSummary struct {
	Attempts24h       int64   `json:"attempts24h"`
	Failed24h         int64   `json:"failed24h"`
	FailureRate24h    float64 `json:"failureRate24h"`
	EffectUnknownOpen int64   `json:"effectUnknownOpen"`
}

type reservationSummary struct {
	UnknownCount            int64 `json:"unknownCount"`
	OldestUnknownAgeSeconds int64 `json:"oldestUnknownAgeSeconds"`
}

type accountSummary struct {
	ActiveSessions  int64 `json:"activeSessions"`
	UsersCreated24h int64 `json:"usersCreated24h"`
	ActiveUsers24h  int64 `json:"activeUsers24h"`
}

func collectOpsAggregates(
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
	activeKeyVersion string,
) (opsAggregates, error) {
	var aggregates opsAggregates
	since := now.Add(-24 * time.Hour)
	queryer := database.Queryer(ctx)

	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE outcome='GRANTED'),
		       count(*) FILTER (WHERE outcome='DENIED')
		FROM agency_order_pii_access_audits
		WHERE created_at > $1
	`, since).Scan(
		&aggregates.PIIAccess.Granted24h, &aggregates.PIIAccess.Denied24h,
	); err != nil {
		return aggregates, err
	}

	// Link check over the whole ledger: hashes were sealed at write time with
	// nanosecond timestamps the database does not retain, so recomputing the
	// preimage is impossible; what must always hold is that every event links
	// to its predecessor within the AgencyOrder chain.
	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT previous_event_hash,
			       LAG(event_hash) OVER (
			           PARTITION BY agency_order_id ORDER BY created_at, id
			       ) AS expected_previous
			FROM agency_order_pii_access_audits
		) chain
		WHERE COALESCE(previous_event_hash, '') <> COALESCE(expected_previous, '')
	`).Scan(&aggregates.PIIAccess.BrokenChainLinks); err != nil {
		return aggregates, err
	}

	if err := queryer.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM user_deletions WHERE purged_at IS NULL),
			(SELECT count(*) FROM shipping_profiles
			 WHERE purged_at IS NULL AND key_version <> $1)
			+ (SELECT count(*) FROM shipping_snapshots
			   WHERE purged_at IS NULL AND key_version <> $1)
	`, activeKeyVersion).Scan(
		&aggregates.PIILifecycle.PendingDeletions,
		&aggregates.PIILifecycle.StaleKeyRows,
	); err != nil {
		return aggregates, err
	}

	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE started_at > $1),
		       count(*) FILTER (WHERE started_at > $1 AND status = 'FAILED'),
		       count(*) FILTER (WHERE status = 'EFFECT_UNKNOWN')
		FROM intelligence_attempts
	`, since).Scan(
		&aggregates.Intelligence.Attempts24h,
		&aggregates.Intelligence.Failed24h,
		&aggregates.Intelligence.EffectUnknownOpen,
	); err != nil {
		return aggregates, err
	}
	if aggregates.Intelligence.Attempts24h > 0 {
		aggregates.Intelligence.FailureRate24h =
			float64(aggregates.Intelligence.Failed24h) /
				float64(aggregates.Intelligence.Attempts24h)
	}

	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(EXTRACT(EPOCH FROM ($1::timestamptz - min(created_at)))::bigint, 0)
		FROM managed_runner_reservations
		WHERE status = 'UNKNOWN'
	`, now).Scan(
		&aggregates.CostReservations.UnknownCount,
		&aggregates.CostReservations.OldestUnknownAgeSeconds,
	); err != nil {
		return aggregates, err
	}

	if err := queryer.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM auth_sessions
			 WHERE revoked_at IS NULL AND expires_at > $1),
			(SELECT count(*) FROM users WHERE created_at > $2),
			(SELECT count(DISTINCT user_id) FROM auth_sessions
			 WHERE created_at > $2)
	`, now, since).Scan(
		&aggregates.Accounts.ActiveSessions,
		&aggregates.Accounts.UsersCreated24h,
		&aggregates.Accounts.ActiveUsers24h,
	); err != nil {
		return aggregates, err
	}

	return aggregates, nil
}

func buildOpsHealthReport(
	ctx context.Context,
	now time.Time,
	database *sharedpostgres.Database,
	metrics *httpMetrics,
	settlementHealth *settlementRuntimeHealth,
	chain chainHeadsReader,
	chainID uint64,
	contractAddress string,
	activeKeyVersion string,
) opsHealthReport {
	report := opsHealthReport{
		Status: "ready",
		Core: opsCoreHealth{
			Status: "ready", ReasonCodes: []string{},
			DatabasePool: database.Stats(),
		},
		DegradedReasonCodes: []string{},
	}
	if metrics != nil {
		report.HTTP = metrics.snapshot(now)
	}
	if err := database.Ping(ctx); err != nil {
		report.Core.Status = "unavailable"
		report.Core.ReasonCodes = append(report.Core.ReasonCodes, "DATABASE_UNAVAILABLE")
	}
	if settlementHealth != nil {
		settlement := settlementHealth.report(
			ctx, now, database, chain, chainID, contractAddress,
		)
		report.Settlement = &settlement
		report.DegradedReasonCodes = append(
			report.DegradedReasonCodes, settlement.ReasonCodes...,
		)
	}
	if report.Core.Status == "ready" {
		if aggregates, err := collectOpsAggregates(
			ctx, database, now, activeKeyVersion,
		); err != nil {
			report.DegradedReasonCodes = append(
				report.DegradedReasonCodes, "OPS_AGGREGATES_UNAVAILABLE",
			)
		} else {
			report.Observed = &aggregates
		}
	}
	switch {
	case report.Core.Status != "ready":
		report.Status = "unavailable"
	case len(report.DegradedReasonCodes) > 0:
		report.Status = "degraded"
	}
	return report
}

// newCoreReadinessHandler serves GET /readyz. It only fails when the core
// cannot serve requests; settlement degradation stays out of this signal so
// container healthchecks and deploy gates do not restart a healthy app.
func newCoreReadinessHandler(database *sharedpostgres.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pingContext, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := database.Ping(pingContext); err != nil {
			httpapi.WriteJSON(
				w, http.StatusServiceUnavailable,
				map[string]string{"status": "unavailable"},
			)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// newOpsHealthHandler serves GET /api/v1/admin/ops/health behind operator
// authentication. HTTP 503 means core is down; degraded settlement runtime
// returns 200 with reason codes so operators can tell the two apart.
func newOpsHealthHandler(reporter *opsReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		healthContext, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		report := reporter.report(healthContext, time.Now().UTC())
		status := http.StatusOK
		if report.Status == "unavailable" {
			status = http.StatusServiceUnavailable
		}
		httpapi.WriteJSON(w, status, report)
	}
}

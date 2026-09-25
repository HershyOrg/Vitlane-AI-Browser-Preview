package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// The sampler persists one ops health snapshot every five minutes so the
// dashboard can chart trends across restarts. This is a first-party table
// with a fixed retention, not a metrics platform: ADR-0040 §6 still stands —
// no Prometheus/Grafana process appears, thresholds and alerting stay in the
// host watch, and losing this table loses charts, never alerting.
const (
	opsSampleInterval    = 5 * time.Minute
	opsSampleFirstDelay  = time.Minute
	opsSampleRetention   = 35 * 24 * time.Hour
	opsSamplesMaxHours   = 720
	opsSamplesMaxPoints  = 600
	opsSampleWorkTimeout = 30 * time.Second
)

func insertOpsHealthSample(
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
	report opsHealthReport,
) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	queryer := database.Queryer(ctx)
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO ops_health_samples (sampled_at, report)
		VALUES ($1, $2::jsonb)
	`, now, string(payload)); err != nil {
		return err
	}
	// Retention rides along with every insert; deleting a handful of rows
	// every five minutes keeps the table from ever needing maintenance.
	_, err = queryer.ExecContext(ctx, `
		DELETE FROM ops_health_samples WHERE sampled_at < $1
	`, now.Add(-opsSampleRetention))
	return err
}

// opsSamplePoint is the flat, chart-ready projection of one stored report.
// Sections that were absent when the sample was taken (settlement runtime
// off, host facts missing) stay null instead of pretending to be zero.
type opsSamplePoint struct {
	SampledAt               string   `json:"sampledAt"`
	Status                  string   `json:"status"`
	Requests5m              uint64   `json:"requests5m"`
	ServerErrors5m          uint64   `json:"serverErrors5m"`
	DBPoolInUse             int      `json:"dbPoolInUse"`
	ActiveSessions          *int64   `json:"activeSessions,omitempty"`
	IntelligenceFailureRate *float64 `json:"intelligenceFailureRate24h,omitempty"`
	UnknownReservations     *int64   `json:"unknownReservations,omitempty"`
	PIIGranted24h           *int64   `json:"piiGranted24h,omitempty"`
	PIIDenied24h            *int64   `json:"piiDenied24h,omitempty"`
	CursorLagBlocks         *int64   `json:"cursorLagBlocks,omitempty"`
	DiskUsedPct             *int64   `json:"diskUsedPct,omitempty"`
	MemoryUsedPct           *int64   `json:"memoryUsedPct,omitempty"`
}

func samplePointFrom(sampledAt time.Time, report opsHealthReport) opsSamplePoint {
	point := opsSamplePoint{
		SampledAt:      sampledAt.UTC().Format(time.RFC3339),
		Status:         report.Status,
		Requests5m:     report.HTTP.RequestsLast5m,
		ServerErrors5m: report.HTTP.ServerErrorsLast5m,
		DBPoolInUse:    report.Core.DatabasePool.InUse,
	}
	if observed := report.Observed; observed != nil {
		point.ActiveSessions = &observed.Accounts.ActiveSessions
		point.IntelligenceFailureRate = &observed.Intelligence.FailureRate24h
		point.UnknownReservations = &observed.CostReservations.UnknownCount
		point.PIIGranted24h = &observed.PIIAccess.Granted24h
		point.PIIDenied24h = &observed.PIIAccess.Denied24h
	}
	if settlement := report.Settlement; settlement != nil &&
		settlement.FinalizedCursorSeen && settlement.RPCFinalizedBlock > 0 {
		lag := int64(settlement.RPCFinalizedBlock) - int64(settlement.FinalizedCursor)
		if lag < 0 {
			lag = 0
		}
		point.CursorLagBlocks = &lag
	}
	if host := report.Host; host != nil {
		point.DiskUsedPct = &host.DiskUsedPct
		point.MemoryUsedPct = &host.MemoryUsedPct
	}
	return point
}

type opsSamplesResponse struct {
	Hours  int              `json:"hours"`
	Points []opsSamplePoint `json:"points"`
}

// newOpsSamplesHandler serves GET /api/v1/admin/ops/samples?hours=N behind
// operator authentication, decimated to a bounded point count so a 30-day
// window stays a lightweight payload.
func newOpsSamplesHandler(database *sharedpostgres.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		hours := 24
		if raw := r.URL.Query().Get("hours"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > opsSamplesMaxHours {
				httpapi.WriteError(
					w, http.StatusBadRequest, "INVALID_SAMPLE_WINDOW",
					"hours는 1 이상 720 이하의 정수여야 합니다.",
				)
				return
			}
			hours = parsed
		}
		cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		rows, err := database.Queryer(r.Context()).QueryContext(r.Context(), `
			SELECT sampled_at, report
			FROM ops_health_samples
			WHERE sampled_at > $1
			ORDER BY sampled_at
		`, cutoff)
		if err != nil {
			httpapi.WriteError(
				w, http.StatusInternalServerError, "SAMPLES_UNAVAILABLE",
				"시계열 샘플을 불러오지 못했습니다.",
			)
			return
		}
		defer rows.Close()
		points := make([]opsSamplePoint, 0, 512)
		for rows.Next() {
			var sampledAt time.Time
			var payload []byte
			if err := rows.Scan(&sampledAt, &payload); err != nil {
				httpapi.WriteError(
					w, http.StatusInternalServerError, "SAMPLES_UNAVAILABLE",
					"시계열 샘플을 불러오지 못했습니다.",
				)
				return
			}
			var report opsHealthReport
			if err := json.Unmarshal(payload, &report); err != nil {
				// One corrupt row must not blank the whole chart.
				continue
			}
			points = append(points, samplePointFrom(sampledAt, report))
		}
		if err := rows.Err(); err != nil {
			httpapi.WriteError(
				w, http.StatusInternalServerError, "SAMPLES_UNAVAILABLE",
				"시계열 샘플을 불러오지 못했습니다.",
			)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, opsSamplesResponse{
			Hours: hours, Points: decimateSamples(points, opsSamplesMaxPoints),
		})
	}
}

// decimateSamples keeps every k-th point (always including the newest) so
// wide windows stay bounded without hiding the current state.
func decimateSamples(points []opsSamplePoint, limit int) []opsSamplePoint {
	if limit <= 0 || len(points) <= limit {
		return points
	}
	stride := (len(points) + limit - 1) / limit
	kept := make([]opsSamplePoint, 0, limit)
	for index := len(points) - 1; index >= 0; index -= stride {
		kept = append(kept, points[index])
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
}

// runOpsHealthSampler is the background writer. The first sample lands one
// minute after boot so a fresh deploy becomes visible on the charts quickly,
// then the cadence matches the host watch (five minutes).
func runOpsHealthSampler(
	ctx context.Context,
	reporter *opsReporter,
	database *sharedpostgres.Database,
	logger *slog.Logger,
) {
	timer := time.NewTimer(opsSampleFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		sampleContext, cancel := context.WithTimeout(ctx, opsSampleWorkTimeout)
		now := time.Now().UTC()
		report := reporter.report(sampleContext, now)
		if err := insertOpsHealthSample(
			sampleContext, database, now, report,
		); err != nil && ctx.Err() == nil {
			logger.Warn("ops health sample failed",
				"event", "ops.sample_failed", "error", err)
		}
		cancel()
		timer.Reset(opsSampleInterval)
	}
}

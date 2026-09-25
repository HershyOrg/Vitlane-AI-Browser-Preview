package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

// SaveRoundOutcome runs inside the Round completion transaction. A replayed
// completion for the same Round overwrites the row rather than failing the
// transaction that already fenced the Round.
func (r *Repository) SaveRoundOutcome(ctx context.Context, outcome researchapp.ResearchRoundOutcome) error {
	coverage := outcome.SourceCoverage
	if coverage == nil {
		coverage = []researchapp.SourceCoverage{}
	}
	raw, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	duration := outcome.Duration.Milliseconds()
	if duration < 0 {
		duration = 0
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO research_round_outcomes(
			round_id, user_id, curation_id, plan_target_id, attempt_id, country, mode,
			observed_count, duplicate_count, rejected_count, admitted_count,
			evaluated_count, unevaluated_count, source_coverage, duration_milliseconds, completed_at
		) VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (round_id) DO UPDATE SET
			attempt_id=EXCLUDED.attempt_id, country=EXCLUDED.country, mode=EXCLUDED.mode,
			observed_count=EXCLUDED.observed_count, duplicate_count=EXCLUDED.duplicate_count,
			rejected_count=EXCLUDED.rejected_count, admitted_count=EXCLUDED.admitted_count,
			evaluated_count=EXCLUDED.evaluated_count, unevaluated_count=EXCLUDED.unevaluated_count,
			source_coverage=EXCLUDED.source_coverage, duration_milliseconds=EXCLUDED.duration_milliseconds,
			completed_at=EXCLUDED.completed_at
	`, outcome.RoundID, outcome.UserID, outcome.CurationID, outcome.TargetID, outcome.AttemptID,
		outcome.Country, outcome.Mode, outcome.ObservedCount, outcome.DuplicateCount, outcome.RejectedCount,
		outcome.AdmittedCount, outcome.EvaluatedCount, outcome.UnevaluatedCount, raw, duration,
		outcome.CompletedAt.UTC())
	if err != nil {
		return fmt.Errorf("insert research round outcome: %w", err)
	}
	return nil
}

// ReadResearchRoundSummary aggregates the durable ledgers over [since, now].
// It never calls a provider and never reads pool projections.
func (r *Repository) ReadResearchRoundSummary(ctx context.Context, since, now time.Time) (researchapp.ResearchRoundSummary, error) {
	q := r.database.Queryer(ctx)
	summary := researchapp.ResearchRoundSummary{
		SchemaVersion: researchapp.ResearchRoundSummarySchemaVersion,
		Since:         since.UTC(), GeneratedAt: now.UTC(),
		Rounds:       []researchapp.ResearchRoundStatusCount{},
		Routes:       []researchapp.ResearchRouteSummary{},
		Failures:     []researchapp.ResearchRoundFailureCount{},
		Sources:      []researchapp.ResearchRoundSourceCount{},
		APICalls:     []researchapp.CatalogAPICallCount{},
		Admitted:     researchapp.ResearchAdmittedSummary{Buckets: []researchapp.ResearchAdmittedBucket{}},
		Steps:        []researchapp.ResearchStepDuration{},
		Attempts:     []researchapp.ResearchAttemptCount{},
		Reservations: []researchapp.ManagedReservationCount{},
	}
	rows, err := q.QueryContext(ctx, `
		SELECT COALESCE(context_snapshot->'researchScope'->>'country',''), status, count(*)
		FROM research_rounds WHERE created_at >= $1 AND created_at <= $2
		GROUP BY 1,2 ORDER BY 1,2`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ResearchRoundStatusCount
		if err = rows.Scan(&row.Country, &row.Status, &row.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Rounds = append(summary.Rounds, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	// The Round keeps the terminal reason; the last FAILED Intelligence step
	// says where the attempt gave up (query, catalog search, ranking, submit).
	rows, err = q.QueryContext(ctx, `
		SELECT r.failure_reason_code, COALESCE(r.failure_retryable,false), COALESCE(s.kind,''), count(*)
		FROM research_rounds r
		LEFT JOIN LATERAL (
			SELECT st.kind FROM intelligence_jobs j
			JOIN intelligence_steps st ON st.job_id=j.id
			WHERE j.research_round_id=r.id AND st.status='FAILED'
			ORDER BY st.started_at DESC LIMIT 1
		) s ON true
		WHERE r.status='FAILED' AND r.completed_at >= $1 AND r.completed_at <= $2
		GROUP BY 1,2,3 ORDER BY count(*) DESC, 1, 3`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ResearchRoundFailureCount
		var code sql.NullString
		if err = rows.Scan(&code, &row.Retryable, &row.StepKind, &row.Count); err != nil {
			rows.Close()
			return summary, err
		}
		row.FailureCode = code.String
		summary.Failures = append(summary.Failures, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT o.country, COALESCE(c->>'source',''), COALESCE(c->>'status',''), COALESCE(c->>'reasonCode',''), count(*)
		FROM research_round_outcomes o, jsonb_array_elements(o.source_coverage) c
		WHERE o.completed_at >= $1 AND o.completed_at <= $2
		GROUP BY 1,2,3,4 ORDER BY 1,2,3,4`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ResearchRoundSourceCount
		if err = rows.Scan(&row.Country, &row.Source, &row.Status, &row.ReasonCode, &row.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Sources = append(summary.Sources, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT source,
		       CASE WHEN outcome='RUNNING' AND started_at < $2::timestamptz - interval '90 seconds' THEN 'CATALOG_RESULT_UNKNOWN' ELSE outcome END,
		       billable, count(*)
		FROM research_catalog_api_calls
		WHERE started_at >= $1 AND started_at <= $2
		GROUP BY 1,2,3 ORDER BY 1,2,3`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.CatalogAPICallCount
		if err = rows.Scan(&row.APIID, &row.Outcome, &row.Billable, &row.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.APICalls = append(summary.APICalls, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	var zero, low, mid, high, full int64
	var median sql.NullFloat64
	if err = q.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE admitted_count = 0),
		       count(*) FILTER (WHERE admitted_count BETWEEN 1 AND 3),
		       count(*) FILTER (WHERE admitted_count BETWEEN 4 AND 7),
		       count(*) FILTER (WHERE admitted_count BETWEEN 8 AND 15),
		       count(*) FILTER (WHERE admitted_count >= 16),
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY admitted_count),
		       COALESCE(sum(evaluated_count),0), COALESCE(sum(unevaluated_count),0)
		FROM research_round_outcomes WHERE completed_at >= $1 AND completed_at <= $2`, since, now,
	).Scan(&summary.Admitted.Rounds, &zero, &low, &mid, &high, &full, &median,
		&summary.Evaluation.Evaluated, &summary.Evaluation.Unevaluated); err != nil {
		return summary, err
	}
	summary.Admitted.Median = median.Float64
	summary.Admitted.Buckets = []researchapp.ResearchAdmittedBucket{
		{Label: "0", Count: zero}, {Label: "1-3", Count: low}, {Label: "4-7", Count: mid},
		{Label: "8-15", Count: high}, {Label: "16+", Count: full},
	}
	// Stage wall time over research jobs. Only completed steps carry a
	// duration; an open step is progress, not a measurement.
	rows, err = q.QueryContext(ctx, `
		SELECT st.kind, count(*),
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM st.completed_at - st.started_at)), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM st.completed_at - st.started_at)), 0)
		FROM intelligence_steps st
		JOIN intelligence_jobs j ON j.id = st.job_id
		WHERE j.target_kind = 'RESEARCH_ROUND' AND st.completed_at IS NOT NULL
		  AND st.started_at >= $1 AND st.started_at <= $2
		GROUP BY 1 ORDER BY 1`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ResearchStepDuration
		if err = rows.Scan(&row.Kind, &row.Count, &row.P50Seconds, &row.P95Seconds); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Steps = append(summary.Steps, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	// Every research attempt in the window, including the ones a later
	// automatic retry or a parked EFFECT_UNKNOWN hides from the Round row.
	rows, err = q.QueryContext(ctx, `
		SELECT a.status, COALESCE(a.failure_code,''), count(*)
		FROM intelligence_attempts a
		JOIN intelligence_jobs j ON j.id = a.job_id
		WHERE j.target_kind = 'RESEARCH_ROUND' AND a.started_at >= $1 AND a.started_at <= $2
		GROUP BY 1,2 ORDER BY count(*) DESC, 1, 2`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ResearchAttemptCount
		if err = rows.Scan(&row.Status, &row.FailureCode, &row.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Attempts = append(summary.Attempts, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	// Managed budget reservations by status. HELD and UNKNOWN rows still count
	// against the daily caps, which is what explains a QUOTA_EXCEEDED attempt.
	rows, err = q.QueryContext(ctx, `
		SELECT status, count(*), COALESCE(sum(amount_micros),0)
		FROM managed_runner_reservations
		WHERE created_at >= $1 AND created_at <= $2
		GROUP BY 1 ORDER BY 1`, since, now)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var row researchapp.ManagedReservationCount
		if err = rows.Scan(&row.Status, &row.Count, &row.AmountMicros); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Reservations = append(summary.Reservations, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return summary, err
	}

	summary.Routes = []researchapp.ResearchRouteSummary{}
	routes, err := q.QueryContext(ctx, `SELECT route_id,policy_version,product_vertical,decision,reason,count(*),avg(pressure) FROM research_route_decisions WHERE created_at>=$1 AND created_at<=$2 GROUP BY 1,2,3,4,5 ORDER BY 1,3,4,5`, since, now)
	if err != nil {
		return summary, err
	}
	defer routes.Close()
	for routes.Next() {
		var item researchapp.ResearchRouteSummary
		if err := routes.Scan(&item.RouteID, &item.PolicyVersion, &item.Vertical, &item.Decision, &item.Reason, &item.Count, &item.MeanPressure); err != nil {
			return summary, err
		}
		summary.Routes = append(summary.Routes, item)
	}
	return summary, routes.Err()
}

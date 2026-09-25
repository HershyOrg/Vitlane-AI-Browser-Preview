package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadAmazonUsage(ctx context.Context) (researchapp.CatalogAPIUsage, error) {
	result := researchapp.CatalogAPIUsage{SchemaVersion: "vitlane.catalog-api-usage.v3", Source: researchdomain.SourceAmazon, Failures24h: []researchapp.CatalogAPIFailureCount{}}
	var limit, used, remaining sql.NullInt64
	var reset, observed, baseline sql.NullTime
	var free sql.NullBool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT quota_limit,provider_used,provider_remaining,reset_at,observed_at,baseline_at,is_free,refresh_failure FROM research_catalog_api_quota WHERE source='AMAZON'`).Scan(&limit, &used, &remaining, &reset, &observed, &baseline, &free, &result.QuotaRefreshFailure)
	if err != nil {
		return result, fmt.Errorf("read Amazon quota: %w", err)
	}
	if limit.Valid && used.Valid && remaining.Valid && reset.Valid && observed.Valid {
		result.Quota = &researchapp.CatalogAPIQuota{Limit: limit.Int64, Used: used.Int64, Remaining: remaining.Int64, ResetAt: reset.Time, ObservedAt: observed.Time, IsFree: free.Bool}
		if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT count(*) FROM research_catalog_api_calls WHERE source='AMAZON' AND billable AND started_at >= $1`, baseline.Time).Scan(&result.AttemptsSinceObservation); err != nil {
			return result, err
		}
		estimate := max(int64(0), remaining.Int64-result.AttemptsSinceObservation)
		result.EstimatedRemaining = &estimate
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT CASE WHEN outcome='RUNNING' AND started_at<now()-interval '15 seconds' THEN 'AMAZON_RESULT_UNKNOWN' ELSE outcome END, count(*) FROM research_catalog_api_calls WHERE source='AMAZON' AND started_at>=now()-interval '24 hours' GROUP BY 1`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var code string
		var count int64
		if err = rows.Scan(&code, &count); err != nil {
			rows.Close()
			return result, err
		}
		result.Requests24h += count
		if code == "SUCCESS" {
			result.Succeeded24h += count
		} else if code != "RUNNING" {
			result.Failures24h = append(result.Failures24h, researchapp.CatalogAPIFailureCount{ReasonCode: code, Count: count})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	result.Operations24h, err = r.catalogOperationUsage(ctx, "AMAZON")
	if err != nil {
		return result, err
	}
	var at time.Time
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT outcome,completed_at FROM research_catalog_api_calls WHERE source='AMAZON' AND outcome NOT IN ('RUNNING','SUCCESS') ORDER BY completed_at DESC,id DESC LIMIT 1`).Scan(&result.LastFailureCode, &at)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.LastFailureAt = &at
	return result, nil
}
func (r *Repository) SaveAmazonQuota(ctx context.Context, q researchapp.CatalogAPIQuota, baseline time.Time) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_catalog_api_quota SET quota_limit=$1,provider_used=$2,provider_remaining=$3,reset_at=$4,observed_at=$5,baseline_at=$6,is_free=$7,refresh_failure='' WHERE source='AMAZON'`, q.Limit, q.Used, q.Remaining, q.ResetAt, q.ObservedAt, baseline, q.IsFree)
	return err
}
func (r *Repository) RecordAmazonQuotaFailure(ctx context.Context, code string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_catalog_api_quota SET refresh_failure=$1 WHERE source='AMAZON'`, code)
	return err
}
func (r *Repository) ReserveAmazonCall(ctx context.Context, operation string, now time.Time) (string, error) {
	var id string
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var enabled bool
		if err := q.QueryRowContext(tx, `SELECT enabled FROM research_catalog_source_control WHERE source='AMAZON' FOR SHARE`).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return fault.New(fault.ProviderUnavailable, "AMAZON_SOURCE_DISABLED", false)
		}
		var remaining sql.NullInt64
		var baseline, reset sql.NullTime
		if err := q.QueryRowContext(tx, `SELECT provider_remaining,baseline_at,reset_at FROM research_catalog_api_quota WHERE source='AMAZON' FOR UPDATE`).Scan(&remaining, &baseline, &reset); err != nil {
			return err
		}
		if !remaining.Valid || !baseline.Valid || !reset.Valid || !reset.Time.After(now) {
			return fault.New(fault.ProviderUnavailable, "AMAZON_QUOTA_UNCONFIRMED", true)
		}
		var attempts, active int64
		var latest sql.NullTime
		if err := q.QueryRowContext(tx, `SELECT count(*) FILTER (WHERE billable AND started_at >= $1),count(*) FILTER (WHERE billable AND outcome='RUNNING' AND started_at > $2::timestamptz - interval '15 seconds'), max(started_at) FILTER (WHERE billable) FROM research_catalog_api_calls WHERE source='AMAZON'`, baseline.Time, now).Scan(&attempts, &active, &latest); err != nil {
			return err
		}
		if attempts >= remaining.Int64 {
			return fault.New(fault.QuotaExceeded, "AMAZON_QUOTA_EXHAUSTED", false)
		}
		if operation == "FEED" {
			var feedCalls int64
			if err := q.QueryRowContext(tx, "SELECT count(*) FROM research_catalog_api_calls WHERE source='AMAZON' AND operation='FEED' AND billable AND started_at>$1::timestamptz-interval '24 hours'", now).Scan(&feedCalls); err != nil {
				return err
			}
			if r.backgroundAmazonDailyCalls <= 0 || feedCalls >= int64(r.backgroundAmazonDailyCalls) || remaining.Int64-attempts <= r.backgroundAmazonForegroundReserve {
				return fault.New(fault.QuotaExceeded, "AMAZON_BACKGROUND_BUDGET_EXHAUSTED", false)
			}
		}
		if active >= 2 || (latest.Valid && now.Sub(latest.Time) < time.Second) {
			f := fault.New(fault.RateLimited, "AMAZON_RATE_LIMITED", true)
			f.RetryAfter = time.Second
			return f
		}
		return q.QueryRowContext(tx, `INSERT INTO research_catalog_api_calls(source,operation,started_at) VALUES('AMAZON',$1,$2) RETURNING id::text`, operation, now).Scan(&id)
	})
	if err != nil {
		for _, code := range []string{"AMAZON_QUOTA_EXHAUSTED", "AMAZON_RATE_LIMITED", "AMAZON_QUOTA_UNCONFIRMED", "AMAZON_BACKGROUND_BUDGET_EXHAUSTED"} {
			if strings.Contains(err.Error(), code) {
				_, recordErr := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_catalog_api_calls(source,operation,outcome,billable,started_at,completed_at) VALUES('AMAZON',$1,$2,false,$3,$3)`, operation, code, now)
				if recordErr != nil {
					return "", recordErr
				}
				break
			}
		}
	}
	return id, err
}
func (r *Repository) CompleteAmazonCall(ctx context.Context, id, code string, now time.Time) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_catalog_api_calls SET outcome=$2,completed_at=$3 WHERE id=$1 AND outcome='RUNNING'`, id, code, now)
	return err
}

// Quota lookups do not consume the paid search allowance, but are real API calls.
func (r *Repository) RecordAmazonQuotaCall(ctx context.Context, outcome string, started, finished time.Time) error {
	_, e := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_catalog_api_calls(source,operation,outcome,billable,started_at,completed_at) VALUES('AMAZON','USAGE',$1,false,$2,$3)`, outcome, started, finished)
	return e
}

func (r *Repository) ConfigureBackgroundAmazon(dailyCalls int, foregroundReserve int64) {
	r.backgroundAmazonDailyCalls = max(0, dailyCalls)
	r.backgroundAmazonForegroundReserve = max(0, foregroundReserve)
}

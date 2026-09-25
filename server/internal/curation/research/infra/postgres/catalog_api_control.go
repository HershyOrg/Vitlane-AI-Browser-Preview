package postgres

import (
	"context"
	"database/sql"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

func (r *Repository) ReadProviderUsage(ctx context.Context, id string) (researchapp.CatalogProviderUsage, error) {
	d, ok := r.catalogDefinition(id)
	if !ok {
		return researchapp.CatalogProviderUsage{}, fault.New(fault.InvalidInput, "CATALOG_API_INVALID", false)
	}
	result := researchapp.CatalogProviderUsage{SchemaVersion: "vitlane.catalog-api-usage.v4", CatalogAPIDefinition: d, Failures24h: []researchapp.CatalogAPIFailureCount{}}
	var limit, used, remaining sql.NullInt64
	var reset, observed, baseline sql.NullTime
	var free sql.NullBool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT c.enabled,c.version,c.updated_at,q.quota_limit,q.provider_used,q.provider_remaining,q.reset_at,q.observed_at,q.baseline_at,q.is_free FROM research_catalog_source_control c JOIN research_catalog_api_quota q ON q.source=c.source WHERE c.source=$1`, id).Scan(&result.Control.Enabled, &result.Control.Version, &result.Control.UpdatedAt, &limit, &used, &remaining, &reset, &observed, &baseline, &free)
	if err != nil {
		return result, err
	}
	if limit.Valid && used.Valid && remaining.Valid && reset.Valid && observed.Valid && baseline.Valid {
		result.Quota = &researchapp.CatalogProviderQuota{Limit: limit.Int64, Used: used.Int64, Remaining: remaining.Int64, ResetAt: reset.Time, ObservedAt: observed.Time}
		var attempts int64
		if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT count(*) FROM research_catalog_api_calls WHERE source=$1 AND billable AND started_at >= $2`, id, baseline.Time).Scan(&attempts); err != nil {
			return result, err
		}
		estimate := max(int64(0), remaining.Int64-attempts)
		result.EstimatedRemaining = &estimate
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT CASE WHEN outcome='RUNNING' AND started_at<now()-make_interval(secs=>$2) THEN 'CATALOG_RESULT_UNKNOWN' ELSE outcome END,count(*) FROM research_catalog_api_calls WHERE source=$1 AND started_at>=now()-interval '24 hours' GROUP BY 1`, id, providerCallLeaseSeconds(id))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var count int64
		if err = rows.Scan(&code, &count); err != nil {
			return result, err
		}
		if !providerAdmissionDenied(code) {
			result.Requests24h += count
		}
		switch code {
		case "SUCCESS", "RUNNING":
		case researchapp.CatalogOutcomeProductNotFound:
			// A completed call that found no product is reported on its own so
			// operators can watch how often 11st serves non-product pages.
			result.NotFound24h += count
		default:
			result.Failures24h = append(result.Failures24h, researchapp.CatalogAPIFailureCount{ReasonCode: code, Count: count})
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	state, err := r.providerResources(ctx, id, time.Now().UTC(), "SEARCH")
	if err != nil {
		return result, err
	}
	result.Resources = &state
	result.Operations24h, err = r.catalogOperationUsage(ctx, id)
	return result, err
}

func (r *Repository) UpdateProviderControl(ctx context.Context, id, operator string, enabled bool, version int64, now time.Time) (researchapp.AmazonSourceControl, error) {
	var result researchapp.AmazonSourceControl
	if _, ok := r.catalogDefinition(id); !ok || operator == "" || version < 1 {
		return result, fault.New(fault.InvalidInput, "CATALOG_CONTROL_INVALID", false)
	}
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if err := q.QueryRowContext(tx, `SELECT enabled,version,updated_at FROM research_catalog_source_control WHERE source=$1 FOR UPDATE`, id).Scan(&result.Enabled, &result.Version, &result.UpdatedAt); err != nil {
			return err
		}
		if version != result.Version {
			return fault.New(fault.Conflict, "CATALOG_CONTROL_VERSION_CONFLICT", false)
		}
		if enabled == result.Enabled {
			return nil
		}
		result.Enabled = enabled
		result.Version++
		result.UpdatedAt = now
		if _, err := q.ExecContext(tx, `UPDATE research_catalog_source_control SET enabled=$2,version=$3,updated_at=$4 WHERE source=$1`, id, enabled, result.Version, now); err != nil {
			return err
		}
		_, err := q.ExecContext(tx, `INSERT INTO research_catalog_source_control_audit(source,version,enabled,operator_user_id,changed_at) VALUES($1,$2,$3,$4,$5)`, id, result.Version, enabled, operator, now)
		return err
	})
	return result, err
}

func (r *Repository) ReserveProviderCall(ctx context.Context, id, operation string, now time.Time) (string, error) {
	_, ok := r.catalogDefinition(id)
	if !ok || (operation != "SEARCH" && operation != "DETAIL" && operation != "USAGE" && operation != "FEED" && operation != "FEED_PAGE" && operation != "LINK_RESOLVE") {
		return "", fault.New(fault.InvalidInput, "CATALOG_API_INVALID", false)
	}
	var callID string
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var enabled bool
		if err := q.QueryRowContext(tx, `SELECT enabled FROM research_catalog_source_control WHERE source=$1 FOR SHARE`, id).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return fault.New(fault.ProviderUnavailable, "CATALOG_API_DISABLED", false)
		}
		// A quota-row lock serializes admission for this API across replicas.
		var locked string
		if err := q.QueryRowContext(tx, `SELECT source FROM research_catalog_api_quota WHERE source=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		state, err := r.providerResources(tx, id, now, operation)
		if err != nil {
			return err
		}
		if !state.CanStart {
			f := fault.New(fault.ProviderUnavailable, state.Reason, researchapp.CatalogAdmissionTransient(state.Reason))
			if state.ReadyAt != nil {
				f.RetryAfter = max(time.Duration(0), state.ReadyAt.Sub(now))
			}
			return f
		}
		return q.QueryRowContext(tx, `INSERT INTO research_catalog_api_calls(source,operation,billable,started_at) VALUES($1,$2,$3,$4) RETURNING id::text`, id, operation, operation != "USAGE", now).Scan(&callID)
	})
	if f, ok := fault.As(err); ok && providerAdmissionDenied(f.Reason) {
		// A local rejection is observable but is not an upstream request or a
		// quota charge. Record it after the rejected transaction rolls back.
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, recordErr := r.database.Queryer(finalCtx).ExecContext(finalCtx, `INSERT INTO research_catalog_api_calls(source,operation,billable,outcome,started_at,completed_at) VALUES($1,$2,false,$3,$4,$4)`, id, operation, f.Reason, now)
		if recordErr != nil {
			return "", fault.New(fault.InternalFailure, "CATALOG_ADMISSION_RECORD_FAILED", true)
		}
	}
	return callID, err
}

func (r *Repository) CompleteProviderCall(ctx context.Context, id, outcome string, status, retry int, now time.Time) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_catalog_api_calls SET outcome=$2,billable=CASE WHEN $2='CATALOG_ACTOR_BUDGET_EXHAUSTED' THEN false ELSE billable END,http_status=NULLIF($3,0),retry_after_seconds=NULLIF($4,0),completed_at=$5 WHERE id=$1 AND outcome='RUNNING'`, id, outcome, status, retry, now)
	return err
}

func (r *Repository) SaveProviderQuota(ctx context.Context, id string, q researchapp.CatalogAPIQuota, baseline time.Time) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_catalog_api_quota SET quota_limit=$2,provider_used=$3,provider_remaining=$4,reset_at=$5,observed_at=$6,baseline_at=$7,is_free=$8,refresh_failure='' WHERE source=$1`, id, q.Limit, q.Used, q.Remaining, q.ResetAt, q.ObservedAt, baseline, q.IsFree)
	return err
}

func providerAdmissionDenied(code string) bool {
	switch code {
	case "CATALOG_API_DISABLED", "CATALOG_API_RATE_LIMITED", "CATALOG_QUOTA_UNCONFIRMED", "CATALOG_QUOTA_EXHAUSTED", "CATALOG_LOCAL_DAILY_LIMIT", "CATALOG_ACTOR_BUDGET_EXHAUSTED":
		return true
	}
	return false
}

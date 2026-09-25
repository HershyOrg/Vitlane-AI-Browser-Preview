package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

// Configuration is applied once by the composition root before serving traffic.
// Reads and admission use the same effective limits on every replica.
func (r *Repository) ConfigureCatalogResources(limits map[string]researchapp.CatalogLocalLimits, actorCap int64) {
	r.catalogLimits = limits
	r.actorMonthlyCapMicros = actorCap
}
func (r *Repository) catalogDefinition(id string) (researchapp.CatalogAPIDefinition, bool) {
	d, ok := researchapp.CatalogAPIDefinitionFor(id)
	if l, exists := r.catalogLimits[id]; exists {
		d.RequestsPerMinute = l.RequestsPerMinute
		d.DailyLimit = l.DailyLimit
		d.MaxConcurrent = l.MaxConcurrent
	}
	return d, ok
}

func (r *Repository) providerResources(ctx context.Context, id string, now time.Time, operation string) (researchapp.ProviderResourceState, error) {
	d, _ := r.catalogDefinition(id)
	q := r.database.Queryer(ctx)
	var enabled bool
	var limit, remaining sql.NullInt64
	var baseline, reset, observed sql.NullTime
	var free sql.NullBool
	if err := q.QueryRowContext(ctx, `SELECT c.enabled,q.quota_limit,q.provider_remaining,q.baseline_at,q.reset_at,q.observed_at,q.is_free
 FROM research_catalog_source_control c JOIN research_catalog_api_quota q ON q.source=c.source WHERE c.source=$1`, id).Scan(&enabled, &limit, &remaining, &baseline, &reset, &observed, &free); err != nil {
		return researchapp.ProviderResourceState{}, err
	}
	operationScope := ""
	if id == "TELEGRAM_JIRUM" {
		operationScope = operation
	}
	var attempts, minute, day, active int64
	var cooldown, oldestMinute, oldestDay, last sql.NullTime
	if err := q.QueryRowContext(ctx, `SELECT
 count(*) FILTER(WHERE billable AND started_at>=$2),
 count(*) FILTER(WHERE started_at>$3::timestamptz-interval '1 minute' AND outcome NOT IN ('CATALOG_API_DISABLED','CATALOG_API_RATE_LIMITED','CATALOG_QUOTA_UNCONFIRMED','CATALOG_QUOTA_EXHAUSTED','CATALOG_LOCAL_DAILY_LIMIT','CATALOG_ACTOR_BUDGET_EXHAUSTED')),
 count(*) FILTER(WHERE billable AND started_at>$3::timestamptz-interval '24 hours'),
 count(*) FILTER(WHERE outcome='RUNNING' AND started_at>$3::timestamptz-make_interval(secs=>$4)),
 max(completed_at+make_interval(secs=>retry_after_seconds)) FILTER(WHERE retry_after_seconds>0 AND ($5='' OR operation=$5)),
 min(started_at) FILTER(WHERE started_at>$3::timestamptz-interval '1 minute' AND billable),
 min(started_at) FILTER(WHERE started_at>$3::timestamptz-interval '24 hours' AND billable),
 max(started_at) FILTER(WHERE billable)
 FROM research_catalog_api_calls WHERE source=$1`, id, baseline, now, providerCallLeaseSeconds(id), operationScope).Scan(&attempts, &minute, &day, &active, &cooldown, &oldestMinute, &oldestDay, &last); err != nil {
		return researchapp.ProviderResourceState{}, err
	}
	constraints := []researchapp.ResourceConstraint{}
	add := func(key, kind string, used, cap int64, reason string, ready *time.Time) {
		constraints = append(constraints, researchapp.ResourceConstraint{ID: key, Kind: kind, Used: used, Limit: cap, Reason: reason, ReadyAt: ready})
	}
	reason := ""
	if !enabled {
		reason = "CATALOG_API_DISABLED"
	}
	add("control:"+id, "CONTROL", 0, 1, reason, nil)
	reason = ""
	var ready *time.Time
	if minute >= int64(d.RequestsPerMinute) {
		reason = "CATALOG_API_RATE_LIMITED"
		at := now.Add(time.Minute)
		if oldestMinute.Valid {
			at = oldestMinute.Time.Add(time.Minute)
		}
		ready = &at
	}
	if id == "DAISOMALL_HTML" && last.Valid && last.Time.Add(30*time.Second).After(now) {
		reason = "CATALOG_API_RATE_LIMITED"
		at := last.Time.Add(30 * time.Second)
		ready = &at
	}
	add(researchapp.CatalogRateResource(id)+":rate", "RATE", minute, int64(d.RequestsPerMinute), reason, ready)
	reason = ""
	ready = nil
	if active >= int64(max(1, d.MaxConcurrent)) {
		reason = "CATALOG_API_RATE_LIMITED"
		at := now.Add(2 * time.Second)
		ready = &at
	}
	add("api:"+id+":concurrency", "CONCURRENCY", active, int64(max(1, d.MaxConcurrent)), reason, ready)
	if cooldown.Valid && cooldown.Time.After(now) {
		at := cooldown.Time
		add("api:"+id+":cooldown", "COOLDOWN", 1, 1, "CATALOG_API_RATE_LIMITED", &at)
	}
	cost := "FREE"
	if d.NeedsQuota && (!free.Valid || !free.Bool) {
		cost = "INCLUDED"
	}
	if strings.HasPrefix(id, "APIFY_") {
		cost = "METERED"
	}
	if id == "TELEGRAM_JIRUM" {
		perMinute, perDay := researchapp.TelegramOperationLimits(operation)
		if perDay > 0 {
			var opDay, opMinute int64
			var oldest sql.NullTime
			if err := q.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE billable),count(*) FILTER(WHERE billable AND started_at>$3::timestamptz-interval '1 minute'),min(started_at) FILTER(WHERE billable)
 FROM research_catalog_api_calls WHERE source=$1 AND operation=$2 AND started_at>$3::timestamptz-interval '24 hours'`, id, operation, now).Scan(&opDay, &opMinute, &oldest); err != nil {
				return researchapp.ProviderResourceState{}, err
			}
			reason := ""
			var ready *time.Time
			if opDay >= int64(perDay) {
				reason = "CATALOG_LOCAL_DAILY_LIMIT"
				if oldest.Valid {
					at := oldest.Time.Add(24 * time.Hour)
					ready = &at
				}
			}
			add("api:"+id+":"+operation+":day", "QUOTA", opDay, int64(perDay), reason, ready)
			reason = ""
			ready = nil
			if opMinute >= int64(perMinute) {
				reason = "CATALOG_API_RATE_LIMITED"
				at := now.Add(time.Minute)
				ready = &at
			}
			add("api:"+id+":"+operation+":minute", "RATE", opMinute, int64(perMinute), reason, ready)
		}
	}
	if operation != "USAGE" {
		reason = ""
		ready = nil
		if day >= int64(d.DailyLimit) {
			reason = "CATALOG_LOCAL_DAILY_LIMIT"
			if oldestDay.Valid {
				at := oldestDay.Time.Add(24 * time.Hour)
				ready = &at
			}
		}
		add("api:"+id+":day", "QUOTA", day, int64(d.DailyLimit), reason, ready)
		if d.NeedsQuota {
			reason = ""
			ready = nil
			if !remaining.Valid || !baseline.Valid || !reset.Valid || !reset.Time.After(now) || !observed.Valid || now.Sub(observed.Time) > 24*time.Hour {
				reason = "CATALOG_QUOTA_UNCONFIRMED"
			} else if attempts >= remaining.Int64 {
				reason = "CATALOG_QUOTA_EXHAUSTED"
				at := reset.Time
				ready = &at
			}
			used := max(int64(0), limit.Int64-remaining.Int64+attempts)
			add("quota:"+d.QuotaScope, "QUOTA", used, limit.Int64, reason, ready)
		}
		if strings.HasPrefix(id, "APIFY_") {
			spend, err := r.ActorSpendMicros(ctx, researchapp.ActorMonthStart(now))
			if err != nil {
				return researchapp.ProviderResourceState{}, err
			}
			cap := r.actorMonthlyCapMicros
			if cap <= 0 {
				cap = 5000000
			}
			reason = ""
			ready = nil
			if spend+researchapp.CatalogActorReservationMicros(id) > cap {
				reason = "CATALOG_ACTOR_BUDGET_EXHAUSTED"
				at := researchapp.ActorMonthStart(now).AddDate(0, 1, 0)
				ready = &at
			}
			add("account:apify:usd", "BUDGET", spend, cap, reason, ready)
		}
	}
	return researchapp.ComposeProviderResources(cost, now, constraints), nil
}
func providerCallLeaseSeconds(id string) int {
	if strings.HasPrefix(id, "APIFY_") {
		return 180
	}
	return 90
}

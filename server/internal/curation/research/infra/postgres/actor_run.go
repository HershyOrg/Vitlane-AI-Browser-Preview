package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

// BeginActorRun claims the run key. The insert is the claim: a second attempt
// with the same key reads the existing row instead of starting a paid run.
func (r *Repository) BeginActorRun(ctx context.Context, run researchapp.ActorRun) (researchapp.ActorRun, bool, error) {
	var result researchapp.ActorRun
	started := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		// One shared account reservation lock; never fan out writes to other Actors.
		if _, err := q.ExecContext(tx, `SELECT pg_advisory_xact_lock(hashtextextended('research:apify:account',0))`); err != nil {
			return err
		}
		existing, err := r.readActorRun(tx, run.RunKey)
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		cap := run.BudgetLimitMicros
		if cap <= 0 {
			cap = r.actorMonthlyCapMicros
		}
		if cap <= 0 {
			cap = 5000000
		}
		reserve := run.ReservedMicros
		if reserve <= 0 {
			reserve = researchapp.CatalogActorReservationMicros(run.APIID)
		}
		spend, err := r.ActorSpendMicros(tx, researchapp.ActorMonthStart(run.StartedAt))
		if err != nil {
			return err
		}
		if spend+reserve > cap {
			return fault.New(fault.QuotaExceeded, "CATALOG_ACTOR_BUDGET_EXHAUSTED", false)
		}
		_, err = q.ExecContext(tx, `INSERT INTO research_actor_runs(run_key,user_id,api_id,source,provider_run_id,status,item_count,cost_micros,reserved_micros,started_at)
  VALUES($1,$2,$3,$4,'',$5,0,0,$6,$7)`, run.RunKey, run.UserID, run.APIID, run.Source, researchapp.ActorRunRunning, reserve, run.StartedAt.UTC())
		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `INSERT INTO research_provider_account_usage(account_id,period_start,used_micros) VALUES('apify',$1,$2)
          ON CONFLICT(account_id,period_start) DO UPDATE SET used_micros=research_provider_account_usage.used_micros+EXCLUDED.used_micros`, researchapp.ActorMonthStart(run.StartedAt), reserve)
		if err != nil {
			return err
		}
		run.Status = researchapp.ActorRunRunning
		run.ReservedMicros = reserve
		result = run
		started = true
		return nil
	})
	return result, started, err
}

func (r *Repository) AttachActorRun(ctx context.Context, key, id string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_actor_runs SET provider_run_id=$2 WHERE run_key=$1 AND status='RUNNING' AND provider_run_id=''`, key, id)
	return err
}

func (r *Repository) readActorRun(ctx context.Context, key string) (researchapp.ActorRun, error) {
	var run researchapp.ActorRun
	var finished sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT run_key,user_id,api_id,source,provider_run_id,status,item_count,cost_micros,reserved_micros,started_at,finished_at
		FROM research_actor_runs WHERE run_key=$1
	`, key).Scan(&run.RunKey, &run.UserID, &run.APIID, &run.Source, &run.ProviderRunID,
		&run.Status, &run.ItemCount, &run.CostMicros, &run.ReservedMicros, &run.StartedAt, &finished)
	if err != nil {
		return researchapp.ActorRun{}, err
	}
	if finished.Valid {
		at := finished.Time.UTC()
		run.FinishedAt = &at
	}
	return run, nil
}

// FinishActorRun settles a run. A run that already finished keeps its first
// terminal state and cost; only a still-running row is settled.
func (r *Repository) FinishActorRun(ctx context.Context, run researchapp.ActorRun) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if _, err := q.ExecContext(tx, `SELECT pg_advisory_xact_lock(hashtextextended('research:apify:account',0))`); err != nil {
			return err
		}
		previous, err := r.readActorRun(tx, run.RunKey)
		if err != nil {
			return err
		}
		if previous.Status != researchapp.ActorRunRunning {
			return nil
		}
		finished := time.Now().UTC()
		if run.FinishedAt != nil {
			finished = run.FinishedAt.UTC()
		}
		cost := max(int64(0), run.CostMicros)
		if run.Status != researchapp.ActorRunSucceeded || run.ProviderRunID == "" {
			cost = max(cost, previous.ReservedMicros)
		}
		providerID := run.ProviderRunID
		if providerID == "" {
			providerID = previous.ProviderRunID
		}
		_, err = q.ExecContext(tx, `UPDATE research_actor_runs SET provider_run_id=$2,status=$3,item_count=$4,cost_micros=$5,finished_at=$6 WHERE run_key=$1 AND status='RUNNING'`,
			run.RunKey, providerID, run.Status, max(0, run.ItemCount), cost, finished)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `UPDATE research_provider_account_usage SET used_micros=GREATEST(0,used_micros+$2) WHERE account_id='apify' AND period_start=$1`,
			researchapp.ActorMonthStart(previous.StartedAt), cost-previous.ReservedMicros)
		return err
	})
}

// ActorSpendMicros totals what the Actors cost since a point in time,
// in-flight runs included, so the cap cannot be crossed by runs that have not
// reported yet.
func (r *Repository) ActorSpendMicros(ctx context.Context, since time.Time) (int64, error) {
	var total sql.NullInt64
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT SUM(used_micros) FROM research_provider_account_usage WHERE account_id='apify' AND period_start >= $1
	`, researchapp.ActorMonthStart(since)).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

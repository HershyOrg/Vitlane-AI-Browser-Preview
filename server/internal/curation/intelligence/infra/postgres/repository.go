package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database       *sharedpostgres.Database
	ids            idGenerator
	threadsEnabled bool
}

type idGenerator interface {
	NewID() string
}

func NewRepository(
	database *sharedpostgres.Database,
	ids idGenerator,
) *Repository {
	return &Repository{database: database, ids: ids}
}

func (r *Repository) InsertJob(
	ctx context.Context,
	job intelligencedomain.Job,
) error {
	planningTaskID, researchRoundID := targetColumns(job.Target)
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO intelligence_jobs(
			id, user_id, curation_id, curation_action_id, plan_id,
			target_kind, planning_task_id, research_round_id,
			provider, model_key, status, attempt_count, created_at, updated_at,interpretation_action_id,interpretation_revision
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,0,$12,$12,$13,$14)
	`,
		job.ID, job.UserID, job.CurationID, job.CurationActionID, job.PlanID,
		string(job.Target.Kind), planningTaskID, researchRoundID,
		string(job.Provider), job.ModelKey, string(job.Status), job.CreatedAt, interpretationTarget(job.Target), nullableRevision(job.Target),
	)
	if err == nil && r.threadsEnabled {
		return r.attachThread(ctx, job)
	}
	return err
}

func (r *Repository) FindJobByTarget(
	ctx context.Context,
	userID string,
	target intelligencedomain.JobTarget,
) (intelligencedomain.Job, bool, error) {
	if target.Kind == intelligencedomain.TargetActionInterpretation {
		j, e := scanJob(r.database.Queryer(ctx).QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM intelligence_jobs WHERE user_id=$1 AND interpretation_action_id=$2 AND interpretation_revision=$3`, jobColumns), userID, target.ID, target.Revision))
		if errors.Is(e, sql.ErrNoRows) {
			return j, false, nil
		}
		return j, e == nil, e
	}
	column := "planning_task_id"
	if target.Kind == intelligencedomain.TargetResearchRound {
		column = "research_round_id"
	}
	row := r.database.Queryer(ctx).QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM intelligence_jobs
		WHERE user_id = $1 AND %s = $2
	`, jobColumns, column), userID, target.ID)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return intelligencedomain.Job{}, false, nil
	}
	if err != nil {
		return intelligencedomain.Job{}, false, err
	}
	return job, true, nil
}

func (r *Repository) GetJob(
	ctx context.Context,
	userID string,
	jobID string,
) (intelligencedomain.Job, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM intelligence_jobs WHERE user_id = $1 AND id = $2
	`, jobColumns), userID, jobID)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return intelligencedomain.Job{}, intelligencedomain.ErrJobNotFound
	}
	return job, err
}

// ClaimPending applies execution admission and opens one attempt per admitted
// job in the same transaction. Every non-research job kind shares a short lane
// so research can never starve interpretation or planning. Research rounds are
// admitted only while the user, the curation and the whole runner stay under
// their caps; the rest stay PENDING in claim order, which is created_at and
// then the Target's order within its curation so results land top to bottom.
// A job whose not-before time has not passed waits out its backoff or resource
// wait. SKIP LOCKED keeps several workers from competing for the same row, and
// the partial unique index on live attempts is the backstop if two ever do.
func (r *Repository) ClaimPending(
	ctx context.Context,
	kind intelligencedomain.ProviderKind,
	policy intelligenceapp.ClaimPolicy,
	deadline time.Duration,
	now time.Time,
) ([]intelligenceapp.ClaimedJob, error) {
	if !policy.Valid() {
		return nil, fmt.Errorf("intelligence claim policy is invalid")
	}
	var claimed []intelligenceapp.ClaimedJob
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		executor := r.database.Queryer(txContext)
		var pending []intelligencedomain.Job
		collect := func(rows sharedpostgres.Rows) error {
			defer rows.Close()
			for rows.Next() {
				job, scanErr := scanJob(rows)
				if scanErr != nil {
					return scanErr
				}
				pending = append(pending, job)
			}
			return rows.Err()
		}
		laneRows, err := executor.QueryContext(txContext, fmt.Sprintf(`
			SELECT %s FROM intelligence_jobs
			WHERE status = 'PENDING' AND provider = $1
			  AND target_kind <> 'RESEARCH_ROUND'
			  AND (not_before IS NULL OR not_before <= $3)
			ORDER BY created_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		`, jobColumns), string(kind), policy.OtherLane, now)
		if err != nil {
			return err
		}
		if err := collect(laneRows); err != nil {
			return err
		}

		var running int
		if err := executor.QueryRowContext(txContext, `
			SELECT count(*) FROM intelligence_jobs
			WHERE status = 'RUNNING' AND provider = $1 AND target_kind = 'RESEARCH_ROUND'
		`, string(kind)).Scan(&running); err != nil {
			return err
		}
		if free := policy.ResearchGlobal - running; free > 0 {
			idRows, err := executor.QueryContext(txContext, `
				WITH running AS (
					SELECT user_id, curation_id FROM intelligence_jobs
					WHERE status = 'RUNNING' AND provider = $1 AND target_kind = 'RESEARCH_ROUND'
				), pending AS (
					SELECT j.id, j.user_id, j.curation_id, j.created_at,
					       COALESCE(t.order_index, 0) AS order_index
					FROM intelligence_jobs j
					LEFT JOIN research_rounds rr ON rr.id = j.research_round_id
					LEFT JOIN shopping_sessions ss ON ss.id = rr.shopping_session_id
					LEFT JOIN plan_targets t ON t.id = ss.plan_target_id
					WHERE j.status = 'PENDING' AND j.provider = $1 AND j.target_kind = 'RESEARCH_ROUND'
					  AND (j.not_before IS NULL OR j.not_before <= $2)
				), ranked AS (
					SELECT p.id, p.created_at, p.order_index,
					       row_number() OVER (PARTITION BY p.user_id ORDER BY p.created_at, p.order_index, p.id) AS user_rank,
					       row_number() OVER (PARTITION BY p.curation_id ORDER BY p.created_at, p.order_index, p.id) AS curation_rank,
					       (SELECT count(*) FROM running x WHERE x.user_id = p.user_id) AS user_running,
					       (SELECT count(*) FROM running x WHERE x.curation_id = p.curation_id) AS curation_running
					FROM pending p
				)
				SELECT id FROM ranked
				WHERE user_running + user_rank <= $3 AND curation_running + curation_rank <= $4
				ORDER BY created_at, order_index, id
				LIMIT $5
			`, string(kind), now, policy.ResearchPerUser, policy.ResearchPerCuration, free)
			if err != nil {
				return err
			}
			var ids []string
			for idRows.Next() {
				var id string
				if err := idRows.Scan(&id); err != nil {
					idRows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := idRows.Err(); err != nil {
				idRows.Close()
				return err
			}
			idRows.Close()
			if len(ids) > 0 {
				researchRows, err := executor.QueryContext(txContext, fmt.Sprintf(`
					SELECT %s FROM intelligence_jobs
					WHERE id::text = ANY($1::text[]) AND status = 'PENDING'
					FOR UPDATE SKIP LOCKED
				`, jobColumns), ids)
				if err != nil {
					return err
				}
				lane := len(pending)
				if err := collect(researchRows); err != nil {
					return err
				}
				// The lock query returns rows in storage order; restore the
				// admission order (created_at, target order) decided above.
				rank := make(map[string]int, len(ids))
				for index, id := range ids {
					rank[id] = index
				}
				research := pending[lane:]
				sort.SliceStable(research, func(i, j int) bool {
					return rank[research[i].ID] < rank[research[j].ID]
				})
			}
		}

		for _, job := range pending {
			ordinal := job.AttemptCount + 1
			if job.ExecutedAttempts() >= intelligencedomain.MaximumAttempts || ordinal > 60 {
				// The ceiling is reached: close the job rather than leaving it
				// pending forever with nothing able to pick it up.
				if err := closeJob(
					txContext, executor, job.ID, intelligencedomain.JobFailed,
					intelligencedomain.ReasonInternalFailure, false, now,
				); err != nil {
					return err
				}
				continue
			}
			attempt, attemptErr := intelligencedomain.NewAttempt(
				r.ids.NewID(), job, r.ids.NewID(), ordinal, deadline, now,
			)
			if attemptErr != nil {
				return attemptErr
			}
			if _, err := executor.ExecContext(txContext, `
				INSERT INTO intelligence_attempts(
					id, job_id, user_id, ordinal, provider, request_key,
					status, deadline_at, started_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			`,
				attempt.ID, attempt.JobID, attempt.UserID, attempt.Ordinal,
				string(attempt.Provider), attempt.RequestKey,
				string(attempt.Status), attempt.DeadlineAt, attempt.StartedAt,
			); err != nil {
				return err
			}
			if _, err := executor.ExecContext(txContext, `
				UPDATE intelligence_jobs
				SET status = 'RUNNING', attempt_count = $2, not_before = NULL,
				    queue_reason = NULL, updated_at = $3
				WHERE id = $1
			`, job.ID, ordinal, now); err != nil {
				return err
			}
			job.Status = intelligencedomain.JobRunning
			job.AttemptCount = ordinal
			job.NotBefore = nil
			job.QueueReason = ""
			claimed = append(claimed, intelligenceapp.ClaimedJob{
				Job: job, Attempt: attempt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// DeferJob closes the attempt as DEFERRED and returns the job to PENDING with
// a not-before time. The job keeps its executed-attempt count, so a wait for
// a catalog slot or a model slot never spends a retry.
func (r *Repository) DeferJob(
	ctx context.Context,
	job intelligencedomain.Job,
	attempt intelligencedomain.Attempt,
	notBefore time.Time,
	reason string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		q := r.database.Queryer(txContext)
		var status, attemptStatus string
		var ordinal int
		if err := q.QueryRowContext(txContext, `
			SELECT j.status, i.status, j.attempt_count
			FROM intelligence_jobs j JOIN intelligence_attempts i ON i.job_id = j.id
			WHERE j.id = $1 AND i.id = $2
			FOR UPDATE OF j, i
		`, job.ID, attempt.ID).Scan(&status, &attemptStatus, &ordinal); err != nil {
			return err
		}
		if status != string(intelligencedomain.JobRunning) ||
			attemptStatus != string(intelligencedomain.AttemptRunning) ||
			ordinal != attempt.Ordinal {
			return intelligencedomain.ErrJobClosed
		}
		if _, err := q.ExecContext(txContext, `
			UPDATE intelligence_attempts
			SET status = 'DEFERRED', failure_code = NULLIF($2,''), retryable = TRUE,
			    completed_at = $3
			WHERE id = $1
		`, attempt.ID, reason, now); err != nil {
			return err
		}
		_, err := q.ExecContext(txContext, `
			UPDATE intelligence_jobs
			SET status = 'PENDING', not_before = $2, defer_count = defer_count + 1,
			    queue_reason = NULLIF($3,''), failure_code = NULL, retryable = FALSE,
			    updated_at = $4
			WHERE id = $1
		`, job.ID, notBefore, reason, now)
		return err
	})
}

func (r *Repository) CloseAttempt(
	ctx context.Context,
	attempt intelligencedomain.Attempt,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE intelligence_attempts
		SET status = $2, failure_code = NULLIF($3,''), retryable = $4,
		    completed_at = $5
		WHERE id = $1
	`,
		attempt.ID, string(attempt.Status), attempt.FailureCode,
		attempt.Retryable, attempt.CompletedAt,
	)
	return err
}

func (r *Repository) CloseJob(
	ctx context.Context,
	jobID string,
	status intelligencedomain.JobStatus,
	failureCode string,
	retryable bool,
	now time.Time,
) error {
	return closeJob(
		ctx, r.database.Queryer(ctx), jobID, status, failureCode, retryable, now,
	)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func closeJob(
	ctx context.Context,
	executor execer,
	jobID string,
	status intelligencedomain.JobStatus,
	failureCode string,
	retryable bool,
	now time.Time,
) error {
	_, err := executor.ExecContext(ctx, `
		UPDATE intelligence_jobs
		SET status = $2, failure_code = NULLIF($3,''), retryable = $4,
		    completed_at = $5, updated_at = $5
		WHERE id = $1 AND status IN ('PENDING', 'RUNNING')
	`, jobID, string(status), failureCode, retryable, now)
	return err
}

// ReopenJob returns a closed job to PENDING. The attempt count is deliberately
// not reset: it is the bound that stops a failure from being retried forever.
func (r *Repository) ReopenJob(
	ctx context.Context,
	jobID string,
	notBefore *time.Time,
	now time.Time,
) error {
	if r.threadsEnabled {
		return r.reopenThreadJob(ctx, jobID, now)
	}
	return r.reopenJob(ctx, jobID, notBefore, now)
}

// reopenJob returns a retryable FAILED job to PENDING. A backoff time keeps it
// out of the claim until it passes; the ceiling counts executed attempts only.
func (r *Repository) reopenJob(ctx context.Context, jobID string, notBefore *time.Time, now time.Time) error {
	var reason any
	if notBefore != nil {
		reason = intelligencedomain.ReasonRetryBackoff
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE intelligence_jobs
		SET status = 'PENDING', failure_code = NULL, retryable = FALSE,
		    completed_at = NULL, not_before = $4, queue_reason = $5, updated_at = $2
		WHERE id = $1 AND status = 'FAILED' AND retryable = TRUE
		  AND attempt_count - defer_count < $3
	`, jobID, now, intelligencedomain.MaximumAttempts, notBefore, reason)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return intelligencedomain.ErrRetryExhausted
	}
	return nil
}

func (r *Repository) OverdueAttempts(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]intelligencedomain.Attempt, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, job_id, user_id, ordinal, provider, request_key, status,
		       COALESCE(failure_code,''), COALESCE(retryable,FALSE),
		       deadline_at, started_at, completed_at
		FROM intelligence_attempts
		WHERE status = 'RUNNING' AND deadline_at <= $1
		ORDER BY deadline_at
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []intelligencedomain.Attempt
	for rows.Next() {
		var attempt intelligencedomain.Attempt
		var provider, status string
		if err := rows.Scan(
			&attempt.ID, &attempt.JobID, &attempt.UserID, &attempt.Ordinal,
			&provider, &attempt.RequestKey, &status, &attempt.FailureCode,
			&attempt.Retryable, &attempt.DeadlineAt, &attempt.StartedAt,
			&attempt.CompletedAt,
		); err != nil {
			return nil, err
		}
		attempt.Provider = intelligencedomain.ProviderKind(provider)
		attempt.Status = intelligencedomain.AttemptStatus(status)
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

// OrphanRunningJobs finds RUNNING jobs whose latest attempt is no longer
// RUNNING. The latest attempt's status and reason travel with the job so the
// reconciler can close it with the same explanation the attempt recorded.
func (r *Repository) OrphanRunningJobs(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]intelligenceapp.OrphanJob, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT j.id, j.user_id, COALESCE(a.status,''), COALESCE(a.failure_code,'')
		FROM intelligence_jobs j
		LEFT JOIN LATERAL (
			SELECT status, failure_code FROM intelligence_attempts
			WHERE job_id = j.id ORDER BY ordinal DESC LIMIT 1
		) a ON true
		WHERE j.status = 'RUNNING' AND j.updated_at < $1
		  AND NOT EXISTS (
			SELECT 1 FROM intelligence_attempts live
			WHERE live.job_id = j.id AND live.status = 'RUNNING'
		  )
		ORDER BY j.updated_at
		LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, err
	}
	type orphanRow struct {
		jobID, userID, status, failureCode string
	}
	var found []orphanRow
	for rows.Next() {
		var row orphanRow
		if err := rows.Scan(&row.jobID, &row.userID, &row.status, &row.failureCode); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	orphans := make([]intelligenceapp.OrphanJob, 0, len(found))
	for _, row := range found {
		job, err := r.GetJob(ctx, row.userID, row.jobID)
		if err != nil {
			if errors.Is(err, intelligencedomain.ErrJobNotFound) {
				continue
			}
			return nil, err
		}
		orphans = append(orphans, intelligenceapp.OrphanJob{
			Job: job, LatestAttemptStatus: intelligencedomain.AttemptStatus(row.status),
			FailureCode: row.failureCode,
		})
	}
	return orphans, nil
}

func (r *Repository) InsertStep(
	ctx context.Context,
	step intelligencedomain.Step,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO intelligence_steps(
			id, job_id, attempt_id, user_id, ordinal, kind, status, started_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`,
		step.ID, step.JobID, step.AttemptID, step.UserID, step.Ordinal,
		string(step.Kind), string(step.Status), step.StartedAt,
	)
	return err
}

func (r *Repository) UpdateStep(
	ctx context.Context,
	step intelligencedomain.Step,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE intelligence_steps
		SET status = $2, reason_code = $3, completed_at = $4
		WHERE id = $1
	`, step.ID, string(step.Status), step.ReasonCode, step.CompletedAt)
	return err
}

// ListCurationJobs projects one curation's jobs with the steps of their most
// recent attempt. This is the single source the workspace poll reads, replacing
// the separate agent-work and runner-progress polls.
func (r *Repository) ListCurationJobs(
	ctx context.Context,
	userID string,
	curationID string,
) ([]intelligenceapp.JobProgress, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT j.id, j.execution_action_id, j.target_kind,
		       COALESCE(j.planning_task_id::text, j.research_round_id::text,j.interpretation_action_id::text),
		       j.provider, j.status, COALESCE(j.failure_code,''), j.retryable,
		       j.attempt_count,
		       COALESCE((
		           SELECT a.status
		           FROM intelligence_attempts a
		           WHERE a.job_id = j.id
		           ORDER BY a.ordinal DESC
		           LIMIT 1
		       ), ''),
		       COALESCE(j.queue_reason, ''), j.not_before,
		       CASE WHEN j.status = 'PENDING' AND j.target_kind = 'RESEARCH_ROUND' THEN (
		           SELECT count(*) FROM intelligence_jobs p
		           LEFT JOIN research_rounds prr ON prr.id = p.research_round_id
		           LEFT JOIN shopping_sessions pss ON pss.id = prr.shopping_session_id
		           LEFT JOIN plan_targets pt ON pt.id = pss.plan_target_id
		           WHERE p.user_id = j.user_id AND p.status = 'PENDING'
		             AND p.target_kind = 'RESEARCH_ROUND'
		             AND (p.created_at, COALESCE(pt.order_index, 0), p.id)
		                 < (j.created_at, COALESCE(jt.order_index, 0), j.id)
		       ) END
		FROM intelligence_jobs j
		LEFT JOIN research_rounds jrr ON jrr.id = j.research_round_id
		LEFT JOIN shopping_sessions jss ON jss.id = jrr.shopping_session_id
		LEFT JOIN plan_targets jt ON jt.id = jss.plan_target_id
		WHERE j.user_id = $1 AND j.curation_id = $2
		ORDER BY j.created_at
	`, userID, curationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var progress []intelligenceapp.JobProgress
	for rows.Next() {
		var item intelligenceapp.JobProgress
		var notBefore sql.NullTime
		var ahead sql.NullInt64
		if err := rows.Scan(
			&item.JobID, &item.ActionID, &item.TargetKind, &item.TargetID, &item.Provider,
			&item.Status, &item.FailureCode, &item.Retryable, &item.Attempt,
			&item.LatestAttemptStatus, &item.QueueReason, &notBefore, &ahead,
		); err != nil {
			return nil, err
		}
		if notBefore.Valid {
			at := notBefore.Time
			item.NotBefore = &at
		}
		if ahead.Valid {
			count := int(ahead.Int64)
			item.QueueAhead = &count
		}
		item.Target = intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetKind(item.TargetKind),
			ID:   item.TargetID,
		}
		item.Steps = []intelligencedomain.Step{}
		progress = append(progress, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range progress {
		steps, err := r.listLatestAttemptSteps(ctx, userID, progress[index].JobID)
		if err != nil {
			return nil, err
		}
		progress[index].Steps = steps
	}
	return progress, nil
}

func (r *Repository) listLatestAttemptSteps(
	ctx context.Context,
	userID string,
	jobID string,
) ([]intelligencedomain.Step, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT s.id, s.job_id, s.attempt_id, s.user_id, s.ordinal, s.kind,
		       s.status, s.reason_code, s.started_at, s.completed_at
		FROM intelligence_steps s
		JOIN intelligence_attempts a ON a.id = s.attempt_id
		WHERE s.user_id = $1 AND s.job_id = $2
		  AND a.ordinal = (
			SELECT MAX(ordinal) FROM intelligence_attempts WHERE job_id = $2
		  )
		ORDER BY s.ordinal
	`, userID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []intelligencedomain.Step{}
	for rows.Next() {
		var step intelligencedomain.Step
		var kind, status string
		if err := rows.Scan(
			&step.ID, &step.JobID, &step.AttemptID, &step.UserID, &step.Ordinal,
			&kind, &status, &step.ReasonCode, &step.StartedAt, &step.CompletedAt,
		); err != nil {
			return nil, err
		}
		step.Kind = intelligencedomain.StepKind(kind)
		step.Status = intelligencedomain.StepStatus(status)
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

const jobColumns = `id, user_id, curation_id, curation_action_id, plan_id,
	target_kind, planning_task_id, research_round_id, provider,
	COALESCE(model_key,''), status, COALESCE(failure_code,''), retryable,
	attempt_count, created_at, updated_at, completed_at,execution_action_id,interpretation_action_id,COALESCE(interpretation_revision,0),
	not_before, defer_count, COALESCE(queue_reason,'')`

type scanner interface {
	Scan(destination ...any) error
}

func scanJob(row scanner) (intelligencedomain.Job, error) {
	var job intelligencedomain.Job
	var targetKind, provider, status string
	var planningTaskID, researchRoundID, interpretationActionID sql.NullString
	var notBefore sql.NullTime
	if err := row.Scan(
		&job.ID, &job.UserID, &job.CurationID, &job.CurationActionID,
		&job.PlanID, &targetKind, &planningTaskID, &researchRoundID,
		&provider, &job.ModelKey, &status, &job.FailureCode, &job.Retryable,
		&job.AttemptCount, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.ExecutionActionID, &interpretationActionID, &job.Target.Revision,
		&notBefore, &job.DeferCount, &job.QueueReason,
	); err != nil {
		return intelligencedomain.Job{}, err
	}
	if notBefore.Valid {
		at := notBefore.Time
		job.NotBefore = &at
	}
	job.Target = intelligencedomain.JobTarget{
		Revision: job.Target.Revision,
		Kind:     intelligencedomain.TargetKind(targetKind),
	}
	if interpretationActionID.Valid {
		job.Target.ID = interpretationActionID.String
	} else if planningTaskID.Valid {
		job.Target.ID = planningTaskID.String
	} else if researchRoundID.Valid {
		job.Target.ID = researchRoundID.String
	}
	job.Provider = intelligencedomain.ProviderKind(provider)
	job.Status = intelligencedomain.JobStatus(status)
	return job, nil
}

func targetColumns(
	target intelligencedomain.JobTarget,
) (planningTaskID, researchRoundID any) {
	if target.Kind == intelligencedomain.TargetPlanningTask {
		return target.ID, nil
	}
	if target.Kind == intelligencedomain.TargetResearchRound {
		return nil, target.ID
	}
	return nil, nil
}

// LockActionAdmission prevents concurrent transactions for one user from both
// passing the global action count. It is a transaction-scoped lock, so every
// caller must hold an ambient product transaction through job creation.
func (r *Repository) LockActionAdmission(
	ctx context.Context,
	userID string,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtextextended('intelligence.action-admission:' || $1, 0)
		)
	`, userID)
	if err != nil {
		return fmt.Errorf("lock intelligence action admission: %w", err)
	}
	return nil
}

// CountActiveActions counts the user's in-flight CurationActions, not their
// jobs: one action fans out into a job per target, and the limit the user
// experiences is on the actions they started.
func (r *Repository) CountActiveActions(
	ctx context.Context,
	userID string,
) (int, error) {
	if r.threadsEnabled {
		return r.threadActiveCount(ctx, userID)
	}
	var total int
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*) FROM (
 SELECT DISTINCT curation_action_id FROM intelligence_jobs WHERE user_id=$1 AND status IN ('PENDING','RUNNING')
 UNION ALL SELECT id FROM curation_conversation_requests WHERE user_id=$1 AND status='RESOLVING' AND id::text<>COALESCE(current_setting('vitlane.conversation_request',true),'')
 UNION ALL SELECT DISTINCT curation_id FROM phase8_research_pool_commands WHERE user_id=$1 AND status='RUNNING' AND lease_expires_at>now() AND idempotency_key LIKE 'finding:%'
 ) active
	`, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count active intelligence actions: %w", err)
	}
	return total, nil
}

// PlanHasActiveJobs answers whether this plan is still working. A plan admits
// one action at a time, and an action is finished only when every job it
// produced is terminal — that is what stops a second action from racing the
// first one's targets.
func (r *Repository) PlanHasActiveJobs(
	ctx context.Context,
	userID string,
	planID string,
) (bool, error) {
	if r.threadsEnabled {
		return r.threadActive(ctx, userID, planID, true)
	}
	var active bool
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM intelligence_jobs
			WHERE user_id = $1 AND plan_id = $2
			  AND status IN ('PENDING', 'RUNNING')
		) OR EXISTS(SELECT 1 FROM curation_conversation_requests cr JOIN curations c ON c.id=cr.curation_id WHERE cr.user_id=$1 AND c.shopping_plan_id=$2 AND cr.status='RESOLVING' AND cr.id::text<>COALESCE(current_setting('vitlane.conversation_request',true),'')) OR EXISTS(SELECT 1 FROM phase8_research_pool_commands pc JOIN curations c ON c.id=pc.curation_id WHERE pc.user_id=$1 AND c.shopping_plan_id=$2 AND pc.status='RUNNING' AND pc.lease_expires_at>now() AND pc.idempotency_key LIKE 'finding:%')
	`, userID, planID).Scan(&active); err != nil {
		return false, fmt.Errorf("check active plan jobs: %w", err)
	}
	return active, nil
}

func (r *Repository) CurationHasActiveJobs(
	ctx context.Context,
	userID string,
	curationID string,
) (bool, error) {
	if r.threadsEnabled {
		return r.threadActive(ctx, userID, curationID, false)
	}
	var active bool
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM intelligence_jobs
			WHERE user_id = $1 AND curation_id = $2
			  AND status IN ('PENDING', 'RUNNING')
		) OR EXISTS(SELECT 1 FROM curation_conversation_requests WHERE user_id=$1 AND curation_id=$2 AND status='RESOLVING' AND id::text<>COALESCE(current_setting('vitlane.conversation_request',true),'')) OR EXISTS(SELECT 1 FROM phase8_research_pool_commands pc JOIN curations c ON c.id=pc.curation_id WHERE pc.user_id=$1 AND pc.curation_id=$2 AND pc.status='RUNNING' AND pc.lease_expires_at>now() AND pc.idempotency_key LIKE 'finding:%')
	`, userID, curationID).Scan(&active); err != nil {
		return false, fmt.Errorf("check active curation jobs: %w", err)
	}
	return active, nil
}

// CancelAction closes every job the action produced and reports the product
// targets that were holding work, so the caller can close them in the same
// transaction. A RUNNING attempt is left to its own reconciler: its provider
// call is already in flight and its result, if it lands, belongs to the
// attempt row rather than to a job the user has abandoned.
func (r *Repository) CancelAction(
	ctx context.Context,
	userID string,
	curationActionID string,
	now time.Time,
) ([]intelligenceapp.CancelledTarget, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		UPDATE intelligence_jobs
		SET status = 'CANCELLED', completed_at = $3, updated_at = $3,
		    failure_code = NULL, retryable = FALSE
		WHERE user_id = $1 AND execution_action_id = $2
		  AND status IN ('PENDING', 'RUNNING')
		RETURNING id, target_kind, planning_task_id, research_round_id
	`, userID, curationActionID, now)
	if err != nil {
		return nil, fmt.Errorf("cancel intelligence action: %w", err)
	}
	defer rows.Close()
	cancelled := make([]intelligenceapp.CancelledTarget, 0)
	for rows.Next() {
		var (
			jobID      string
			targetKind string
			taskID     sql.NullString
			roundID    sql.NullString
		)
		if err := rows.Scan(&jobID, &targetKind, &taskID, &roundID); err != nil {
			return nil, fmt.Errorf("scan cancelled job: %w", err)
		}
		target := intelligenceapp.CancelledTarget{JobID: jobID}
		target.Target.Kind = intelligencedomain.TargetKind(targetKind)
		if taskID.Valid {
			target.Target.ID = taskID.String
		}
		if roundID.Valid {
			target.Target.ID = roundID.String
		}
		cancelled = append(cancelled, target)
	}
	return cancelled, rows.Err()
}

func interpretationTarget(t intelligencedomain.JobTarget) any {
	if t.Kind == intelligencedomain.TargetActionInterpretation {
		return t.ID
	}
	return nil
}
func nullableRevision(t intelligencedomain.JobTarget) any {
	if t.Kind == intelligencedomain.TargetActionInterpretation {
		return t.Revision
	}
	return nil
}

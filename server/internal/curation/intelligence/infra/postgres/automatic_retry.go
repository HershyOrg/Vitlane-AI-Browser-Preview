package postgres

import (
	"context"
	intelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"time"
)

// RetryFailedAttempt closes a failed attempt and reopens the job after the
// backoff, counting executed attempts only: a deferred wait is not a retry.
func (r *Repository) RetryFailedAttempt(ctx context.Context, job intelligence.Job, attempt intelligence.Attempt, notBefore *time.Time, now time.Time) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var cid string
		if err := q.QueryRowContext(tx, `SELECT id::text FROM curations WHERE id=$1 FOR UPDATE`, job.CurationID).Scan(&cid); err != nil {
			return err
		}
		var current, owner, actionStatus, attemptStatus string
		var ordinal, deferred int
		if err := q.QueryRowContext(tx, `SELECT j.status,j.execution_action_id::text,COALESCE(CASE WHEN a.thread_id IS NOT NULL THEN a.status END,''),i.status,j.attempt_count,j.defer_count FROM intelligence_jobs j JOIN intelligence_attempts i ON i.job_id=j.id LEFT JOIN curation_actions a ON a.id=j.execution_action_id WHERE j.id=$1 AND i.id=$2 FOR UPDATE OF j,i`, job.ID, attempt.ID).Scan(&current, &owner, &actionStatus, &attemptStatus, &ordinal, &deferred); err != nil {
			return err
		}
		if current != "RUNNING" || attemptStatus != "RUNNING" || owner != job.ExecutionActionID || ordinal != attempt.Ordinal {
			return intelligence.ErrJobClosed
		}
		// Interpretation/primitive result may commit just before its Job closes.
		if actionStatus == "SUCCEEDED" || actionStatus == "WAITING_SELECTION" {
			attempt.Status = intelligence.AttemptSucceeded
			attempt.FailureCode = ""
			attempt.Retryable = false
			if err := r.CloseAttempt(tx, attempt); err != nil {
				return err
			}
			return r.CloseJob(tx, job.ID, intelligence.JobSucceeded, "", false, now)
		}
		if ordinal-deferred >= intelligence.MaximumAutomaticAttempts {
			return intelligence.ErrRetryExhausted
		}
		if r.threadsEnabled && actionStatus != "" && actionStatus != "RUNNING" {
			return intelligence.ErrJobClosed
		}
		if err := r.CloseAttempt(tx, attempt); err != nil {
			return err
		}
		if err := r.CloseJob(tx, job.ID, intelligence.JobFailed, attempt.FailureCode, true, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `SELECT set_config('vitlane.automatic_retry',$1,true)`, job.ID); err != nil {
			return err
		}
		return r.reopenJob(tx, job.ID, notBefore, now)
	})
}

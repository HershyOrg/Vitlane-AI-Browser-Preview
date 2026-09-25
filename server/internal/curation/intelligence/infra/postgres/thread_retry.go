package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// Reuse the existing retry primitive and attempt ceiling. A retry gets a new
// request thread; the completed thread retains its immutable failure receipts.
func (r *Repository) reopenThreadJob(ctx context.Context, jobID string, now time.Time) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var cid string
		// Match intake/worker lock ordering: Curation before Job and admission lock.
		if err := q.QueryRowContext(tx, `SELECT c.id FROM curations c JOIN intelligence_jobs j ON j.curation_id=c.id WHERE j.id=$1 AND c.archived_at IS NULL FOR UPDATE OF c`, jobID).Scan(&cid); err != nil {
			return err
		}
		job, err := scanJob(q.QueryRowContext(tx, fmt.Sprintf(`SELECT %s FROM intelligence_jobs WHERE id=$1 FOR UPDATE`, jobColumns), jobID))
		if err != nil {
			return err
		}
		if job.Status != intelligence.JobFailed || !job.Retryable || job.ExecutedAttempts() >= intelligence.MaximumAttempts {
			return intelligence.ErrRetryExhausted
		}
		var old d.CurationThread
		var oldID, oldStatus string
		var raw []byte
		err = q.QueryRowContext(tx, `SELECT t.id,t.status,t.data FROM curation_thread_jobs l JOIN curation_threads t ON t.id=l.thread_id WHERE l.job_id=$1`, jobID).Scan(&oldID, &oldStatus, &raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if oldStatus == "INTERPRETING" || oldStatus == "WAITING_SELECTION" || oldStatus == "RUNNING" {
				return fault.New(fault.Conflict, "CURATION_THREAD_ACTIVE", false)
			}
			if err = json.Unmarshal(raw, &old); err != nil {
				return err
			}
		}
		var rid, mode string
		var version int64
		if err = q.QueryRowContext(tx, `SELECT COALESCE(current_setting('vitlane.conversation_request',true),''),COALESCE(m.mode,'AUTO'),c.version FROM curations c LEFT JOIN curation_control_modes m ON m.curation_id=c.id WHERE c.id=$1`, cid).Scan(&rid, &mode, &version); err != nil {
			return err
		}
		if rid == "" {
			rid = r.ids.NewID()
			if _, err = q.ExecContext(tx, `SELECT curation_admit_request($1,$2,$3::uuid,'RETRY','',$3::text,'RESOLVING')`, job.UserID, cid, rid); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, `SELECT set_config('vitlane.conversation_request',$1,true)`, rid); err != nil {
				return err
			}
		}
		// The legacy retry trigger dispatches the admitted request and freezes its
		// previous response. Reopening and attaching the thread commit together.
		if err = r.reopenJob(tx, jobID, nil, now); err != nil {
			return err
		}

		if oldID != "" {
			rows, e := q.QueryContext(tx, `SELECT execution FROM curation_actions WHERE thread_id=$1 ORDER BY sequence`, oldID)
			if e != nil {
				return e
			}
			for rows.Next() {
				var raw []byte
				if e = rows.Scan(&raw); e != nil {
					rows.Close()
					return e
				}
				var a d.CurationAction
				if e = json.Unmarshal(raw, &a); e != nil {
					rows.Close()
					return e
				}
				old.Actions = append(old.Actions, a)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
		kind := "RESEARCH_AGAIN"
		if job.Target.Kind == intelligence.TargetPlanningTask {
			kind = "PLANNING"
		}
		step := d.CurationAction{ID: r.ids.NewID(), Type: d.ActionTypeForPrimitive(kind), Status: "RUNNING", Jobs: []d.ActionJobResult{}, Effects: []d.ActionEffect{}}
		for _, previous := range old.Actions {
			if previous.ID == job.ExecutionActionID {
				step.Type = previous.Type
				step.TargetID, step.TargetLabel, step.Instruction = previous.TargetID, previous.TargetLabel, previous.Instruction
				step.Budget, step.Criteria = previous.Budget, previous.Criteria
				step.InputRevision, step.Answers = previous.InputRevision, previous.Answers
				break
			}
		}
		if job.Target.Kind == intelligence.TargetActionInterpretation {
			step.InputRevision = job.Target.Revision
		}

		step.RetryOfActionID = job.ExecutionActionID
		step.Decisions = []d.ActionDecision{{ID: r.ids.NewID(), Kind: "ACTION", Result: "RETRY", Source: "MANUAL", ReasonCode: "USER_RETRY_FAILED_JOB", ActionIDs: []string{step.ID}}}
		origin := old.Origin
		if origin == "" {
			origin = "PRIMITIVE"
		}
		next := d.CurationThread{SchemaVersion: d.ThreadSchema, ID: rid, CurationID: cid, Mode: mode, Origin: origin, Request: old.Request, RetryOfThreadID: oldID, RetryOfJobID: jobID, TargetLabels: old.TargetLabels, Status: "RUNNING", Revision: 1, ExpectedCurationVersion: version, Actions: []d.CurationAction{step}, CreatedAt: now, UpdatedAt: now}
		replacements := map[string]string{job.ExecutionActionID: step.ID}
		found := false
		for _, prior := range old.Actions {
			if prior.ID == job.ExecutionActionID {
				found = true
				continue
			}
			if !found || prior.Status != "SKIPPED" {
				continue
			}
			renewed := d.CurationAction{ID: r.ids.NewID(), Type: prior.Type, TargetID: prior.TargetID, TargetLabel: prior.TargetLabel, Instruction: prior.Instruction, Budget: prior.Budget, Criteria: prior.Criteria, Status: "PENDING", InputRevision: prior.InputRevision, RetryOfActionID: prior.ID, GeneratedByActionID: prior.GeneratedByActionID}
			replacements[prior.ID] = renewed.ID
			if v, ok := replacements[renewed.GeneratedByActionID]; ok {
				renewed.GeneratedByActionID = v
			}
			if renewed.Type == d.CurationActionStartResearch {
				if v, ok := replacements[renewed.Instruction]; ok {
					renewed.Instruction = v
				}
			}
			next.Actions = append(next.Actions, renewed)
		}
		raw, err = json.Marshal(next)
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(tx, `INSERT INTO curation_threads(id,user_id,curation_id,plan_id,status,request_hash,revision,data,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,'RUNNING',$1::text,1,$5,$6,$6)`, rid, job.UserID, cid, job.PlanID, raw, now); err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `INSERT INTO curation_thread_jobs(job_id,thread_id,action_id) VALUES($1,$2,$3) ON CONFLICT(job_id) DO UPDATE SET thread_id=EXCLUDED.thread_id,action_id=EXCLUDED.action_id`, jobID, rid, step.ID)

		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `UPDATE intelligence_jobs SET execution_action_id=$2 WHERE id=$1`, jobID, step.ID)
		return err
	})
}

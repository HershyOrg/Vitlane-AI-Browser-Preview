package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	auto "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) EnableThreads() { r.threadsEnabled = true }
func (r *Repository) attachThread(ctx context.Context, j intelligence.Job) error {
	scope := a.ThreadExecutionFrom(ctx)
	q := r.database.Queryer(ctx)
	if scope.ThreadID == "" {
		scope = a.ThreadExecution{ThreadID: j.CurationActionID, ActionID: j.CurationActionID}
		kind := "RESEARCH_AGAIN"
		if j.Target.Kind == intelligence.TargetPlanningTask {
			kind = "PLANNING"
		}
		var request, mode string
		var version int64
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(run.instruction,fb.feedback,p.original_intent),COALESCE(m.mode,'AUTO'),c.version FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id LEFT JOIN curation_control_modes m ON m.curation_id=c.id LEFT JOIN curation_runs run ON run.planning_task_id=$2 LEFT JOIN research_feedback fb ON fb.next_round_id=$3 WHERE c.id=$1`, j.CurationID, nullableThreadTarget(j, true), nullableThreadTarget(j, false)).Scan(&request, &mode, &version); err != nil {
			return err
		}
		t := d.CurationThread{SchemaVersion: d.ThreadSchema, ID: scope.ThreadID, CurationID: j.CurationID, Mode: mode, Origin: "PRIMITIVE", Request: request, Status: "RUNNING", Revision: 1, ExpectedCurationVersion: version, Actions: []d.CurationAction{{ID: scope.ActionID, Type: d.ActionTypeForPrimitive(kind), Status: "RUNNING", Jobs: []d.ActionJobResult{}, Effects: []d.ActionEffect{}}}, CreatedAt: j.CreatedAt, UpdatedAt: j.CreatedAt}

		var actionType, subject string
		if err := q.QueryRowContext(ctx, `SELECT action_type,COALESCE(subject_id,'') FROM curation_actions WHERE id=$1`, j.CurationActionID).Scan(&actionType, &subject); err != nil {
			return err
		}
		t.Actions[0].Type = d.CurationActionType(actionType)
		t.Actions[0].Instruction = request
		if actionType == "TARGET_RESEARCH_AGAIN" {
			t.Actions[0].TargetID = subject
		}
		if actionType == "INTENT_NEXT_STEP" && mode == "AUTO" {
			id := r.ids.NewID()
			decisions := auto.InitialActionDecisions(id)
			for n := range decisions {
				decisions[n].ActionIDs = []string{j.CurationActionID}
			}
			t.Actions[0].GeneratedByActionID = id
			t.Actions = append([]d.CurationAction{{ID: id, Type: d.CurationActionAutoStart, Instruction: request, Status: "SUCCEEDED", Decisions: decisions, Jobs: []d.ActionJobResult{}, Effects: []d.ActionEffect{}, Answers: []d.ThreadAnswer{}, DecisionIDs: []string{}}}, t.Actions...)
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO curation_threads(id,user_id,curation_id,plan_id,status,request_hash,revision,data,created_at,updated_at) VALUES($1,$2,$3,$4,'RUNNING',$7,1,$5,$6,$6) ON CONFLICT(id) DO NOTHING`, scope.ThreadID, j.UserID, j.CurationID, j.PlanID, raw, j.CreatedAt, scope.ThreadID)
		if err != nil {
			return err
		}
	}
	var valid bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_threads WHERE id=$1 AND user_id=$2 AND curation_id=$3 AND status IN ('INTERPRETING','RUNNING'))`, scope.ThreadID, j.UserID, j.CurationID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fault.New(fault.Conflict, "CURATION_THREAD_CANCELLED", false)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO curation_thread_jobs(job_id,thread_id,action_id) VALUES($1,$2,$3) ON CONFLICT(job_id) DO NOTHING`, j.ID, scope.ThreadID, scope.ActionID)
	return err
}
func nullableThreadTarget(j intelligence.Job, planning bool) any {
	if (j.Target.Kind == intelligence.TargetPlanningTask) == planning {
		return j.Target.ID
	}
	return nil
}
func (r *Repository) threadActiveCount(ctx context.Context, user string) (int, error) {
	var n int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT count(*) FROM (
 SELECT id FROM curation_threads WHERE user_id=$1 AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING') AND id::text<>$2
 UNION ALL SELECT DISTINCT j.curation_action_id FROM intelligence_jobs j LEFT JOIN curation_thread_jobs l ON l.job_id=j.id WHERE j.user_id=$1 AND j.status IN ('PENDING','RUNNING') AND l.job_id IS NULL
 UNION ALL SELECT id FROM curation_conversation_requests WHERE user_id=$1 AND status='RESOLVING' AND id::text<>COALESCE(current_setting('vitlane.conversation_request',true),'')
 UNION ALL SELECT DISTINCT curation_id FROM phase8_research_pool_commands WHERE user_id=$1 AND status='RUNNING' AND lease_expires_at>now() AND idempotency_key LIKE 'finding:%'
 ) active`, user, a.ThreadExecutionFrom(ctx).ThreadID).Scan(&n)
	return n, err
}
func (r *Repository) threadActive(ctx context.Context, user, id string, byPlan bool) (bool, error) {
	var active bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM curation_threads WHERE user_id=$1 AND (($4 AND plan_id=$2) OR (NOT $4 AND curation_id=$2)) AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING') AND id::text<>$3
 UNION ALL SELECT 1 FROM intelligence_jobs j LEFT JOIN curation_thread_jobs l ON l.job_id=j.id WHERE j.user_id=$1 AND (($4 AND j.plan_id=$2) OR (NOT $4 AND j.curation_id=$2)) AND j.status IN ('PENDING','RUNNING') AND l.job_id IS NULL
 UNION ALL SELECT 1 FROM curation_conversation_requests cr JOIN curations c ON c.id=cr.curation_id WHERE cr.user_id=$1 AND (($4 AND c.shopping_plan_id=$2) OR (NOT $4 AND cr.curation_id=$2)) AND cr.status='RESOLVING' AND cr.id::text<>COALESCE(current_setting('vitlane.conversation_request',true),'')
 UNION ALL SELECT 1 FROM phase8_research_pool_commands pc JOIN curations c ON c.id=pc.curation_id WHERE pc.user_id=$1 AND (($4 AND c.shopping_plan_id=$2) OR (NOT $4 AND pc.curation_id=$2)) AND pc.status='RUNNING' AND pc.lease_expires_at>now() AND pc.idempotency_key LIKE 'finding:%'
 )`, user, id, a.ThreadExecutionFrom(ctx).ThreadID, byPlan).Scan(&active)
	return active, err
}

func (r *Repository) HasThread(ctx context.Context, id string) (bool, error) {
	var yes bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_thread_jobs WHERE job_id=$1)`, id).Scan(&yes)
	return yes, err
}

func (r *Repository) ThreadCriteriaDecided(ctx context.Context, id string) (bool, error) {
	var yes bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_thread_jobs l JOIN curation_threads t ON t.id=l.thread_id WHERE l.job_id=$1 AND true)`, id).Scan(&yes)
	return yes, err
}

func (r *Repository) ThreadExecutionContext(ctx context.Context, job string, expected ...string) (context.Context, error) {
	if !r.threadsEnabled {
		return ctx, nil
	}
	var thread, step string
	var feedback bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT l.thread_id::text,l.action_id::text,false FROM curation_thread_jobs l JOIN curation_threads t ON t.id=l.thread_id WHERE l.job_id=$1`, job).Scan(&thread, &step, &feedback)
	if errors.Is(err, sql.ErrNoRows) {
		return a.WithLegacyJobExecution(ctx, job), nil
	}
	if err != nil {
		return ctx, err
	}
	if len(expected) == 2 && expected[1] != step {
		return ctx, fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
	}
	ctx = a.WithThreadExecution(ctx, thread, step)
	if len(expected) == 2 {
		ctx = a.WithActionAttempt(ctx, job, expected[0])
	}
	if feedback {
		ctx = a.WithThreadFeedbackCriteria(ctx)
	}
	return ctx, nil
}

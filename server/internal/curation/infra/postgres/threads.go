package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

type storedThread struct {
	d.CurationThread
	AuthSession string `json:"authSession"`
}

func (r *Repository) LockThreadCuration(ctx context.Context, user, id string) error {
	return r.lockBudgetOwner(ctx, user, id)
}
func (r *Repository) ReadControlMode(ctx context.Context, user, id string) (d.ControlMode, error) {
	var m d.ControlMode
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT COALESCE(m.mode,'AUTO'),COALESCE(m.version,1) FROM curations c LEFT JOIN curation_control_modes m ON m.curation_id=c.id WHERE c.user_id=$1 AND c.id=$2 AND c.archived_at IS NULL`, user, id).Scan(&m.Mode, &m.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return m, d.ErrCurationNotFound
	}
	return m, err
}
func (r *Repository) SaveControlMode(ctx context.Context, user, id string, m d.ControlMode) (d.ControlMode, error) {
	if m.Mode != "AUTO" && m.Mode != "MANUAL" {
		return m, fault.New(fault.InvalidInput, "CURATION_MODE_INVALID", false)
	}
	if err := r.LockThreadCuration(ctx, user, id); err != nil {
		return m, err
	}
	if err := r.GuardThreadMutation(ctx, user, id); err != nil {
		return m, err
	}
	old, err := r.ReadControlMode(ctx, user, id)
	if err != nil {
		return m, err
	}
	if old.Version != m.Version {
		return m, fault.New(fault.Conflict, "CURATION_MODE_CHANGED", false)
	}
	m.Version++
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_control_modes(curation_id,mode,version) VALUES($1,$2,$3) ON CONFLICT(curation_id) DO UPDATE SET mode=EXCLUDED.mode,version=EXCLUDED.version`, id, m.Mode, m.Version)
	return m, err
}
func (r *Repository) GuardThreadMutation(ctx context.Context, user, id string) error {
	var active string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id::text FROM curation_threads WHERE user_id=$1 AND curation_id=$2 AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')`, user, id).Scan(&active)
	scope := a.ThreadExecutionFrom(ctx)
	if scope.ThreadID != "" {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if active != scope.ThreadID {
			return fault.New(fault.Conflict, "CURATION_THREAD_CANCELLED", false)
		}

		var allowed bool
		e := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_actions a WHERE a.thread_id=$1 AND a.id=$2 AND a.status IN('PENDING','RUNNING') AND NOT EXISTS(SELECT 1 FROM curation_actions prior WHERE prior.thread_id=a.thread_id AND prior.sequence<a.sequence AND prior.status NOT IN('SUCCEEDED','SKIPPED')) AND ($3='' OR EXISTS(SELECT 1 FROM intelligence_jobs j JOIN intelligence_attempts i ON i.job_id=j.id WHERE j.id::text=$3 AND i.id::text=$4 AND j.execution_action_id=a.id AND i.curation_action_id=a.id AND j.status='RUNNING' AND i.status='RUNNING')))`, scope.ThreadID, scope.ActionID, scope.JobID, scope.AttemptID).Scan(&allowed)
		if e != nil {
			return e
		}
		if !allowed {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		var busy bool
		e := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intelligence_jobs j LEFT JOIN curation_thread_jobs l ON l.job_id=j.id WHERE j.user_id=$1 AND j.curation_id=$2 AND j.status IN ('PENDING','RUNNING') AND l.job_id IS NULL) OR EXISTS(SELECT 1 FROM phase8_research_pool_commands WHERE user_id=$1 AND curation_id=$2 AND status='RUNNING' AND lease_expires_at>now() AND idempotency_key LIKE 'finding:%')`, user, id).Scan(&busy)
		if e != nil {
			return e
		}
		if busy && scope.LegacyJobID == "" {
			return fault.New(fault.Conflict, "CURATION_THREAD_ACTIVE", false)
		}
		return nil
	}
	if err != nil {
		return err
	}
	return fault.New(fault.Conflict, "CURATION_THREAD_ACTIVE", false)
}

// threadColumns are the curation_threads columns a Thread read decodes after
// its id. Actions live in curation_actions and are read separately.
const threadColumns = `user_id,curation_id,plan_id,request_hash,revision,status,data,created_at,updated_at`

// threadActionColumns are the curation_actions columns scanCurationAction reads.
const threadActionColumns = `id,curation_id,actor_user_id,action_type,phase_at_request,requested_transition_to,subject_type,subject_id,effect_kind,source_ref_type,source_ref_id,expected_curation_version,request_hash,created_at,execution`

func (r *Repository) ReadThread(ctx context.Context, user, id string) (d.CurationThread, error) {
	var row d.CurationThread
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT `+threadColumns+` FROM curation_threads WHERE user_id=$1 AND id=$2`, user, id).Scan(&row.UserID, &row.CurationID, &row.PlanID, &row.RequestHash, &row.Revision, &row.Status, &raw, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row, fault.New(fault.InvalidInput, "CURATION_THREAD_NOT_FOUND", false)
	}
	if err != nil {
		return row, err
	}
	t, err := decodeThread(id, row, raw)
	if err != nil {
		return row, err
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT `+threadActionColumns+` FROM curation_actions WHERE thread_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return row, err
	}
	defer rows.Close()
	for rows.Next() {
		action, err := scanCurationAction(rows)
		if err != nil {
			return row, err
		}
		normalizeActionCollections(&action)
		t.Actions = append(t.Actions, action)
	}
	if err = rows.Err(); err != nil {
		return row, err
	}
	return t, nil
}

// decodeThread restores a Thread from its row and stored document. A single
// read passes the id the caller asked with; a list passes the stored id.
func decodeThread(id string, row d.CurationThread, raw []byte) (d.CurationThread, error) {
	var stored storedThread
	if err := json.Unmarshal(raw, &stored); err != nil {
		return d.CurationThread{}, err
	}
	stored.ID = id
	stored.UserID = row.UserID
	stored.CurationID = row.CurationID
	stored.PlanID = row.PlanID
	stored.RequestHash = row.RequestHash
	stored.Revision = row.Revision
	stored.Status = row.Status
	stored.CreatedAt = row.CreatedAt
	stored.UpdatedAt = row.UpdatedAt
	stored.AuthSessionID = stored.AuthSession
	stored.Actions = []d.CurationAction{}
	return stored.CurationThread, nil
}

// ThreadForAction lets the legacy action-cancel route settle the owning Thread
// in the same transaction as the Jobs instead of leaving it active.
func (r *Repository) ThreadForAction(ctx context.Context, user, action string) (string, string, bool, error) {
	var curation, thread string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT curation_id::text, thread_id::text FROM curation_actions WHERE id=$1 AND actor_user_id=$2 AND thread_id IS NOT NULL`, action, user).Scan(&curation, &thread)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return curation, thread, true, nil
}

func (r *Repository) InsertThread(ctx context.Context, t d.CurationThread) error {
	normalizeThreadActions(&t)
	if t.Origin == "REQUEST" || t.Origin == "MANUAL" {
		if _, err := r.database.Queryer(ctx).ExecContext(ctx, `SELECT curation_admit_request($1,$2,$3,'THREAD',$4,$5,'DISPATCHED',$6)`, t.UserID, t.CurationID, t.ID, t.Request, t.RequestHash, t.ExpectedConversationVersion); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(storedThread{t, t.AuthSessionID})
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_threads(id,user_id,curation_id,plan_id,status,request_hash,revision,data,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, t.ID, t.UserID, t.CurationID, t.PlanID, t.Status, t.RequestHash, t.Revision, raw, t.CreatedAt)
	return err
}
func (r *Repository) SaveThread(ctx context.Context, t d.CurationThread) error {
	normalizeThreadActions(&t)
	if !t.Active() {
		if _, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE curation_conversation_requests cr SET status='COMPLETE',response_ready=true WHERE cr.id=$1 AND cr.mode='THREAD' AND NOT EXISTS(SELECT 1 FROM curation_thread_jobs WHERE thread_id=$1)`, t.ID); err != nil {
			return err
		}
	}
	if t.TargetLabels == nil {
		t.TargetLabels = map[string]string{}
	}
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id::text,title FROM plan_targets WHERE user_id=$1 AND curation_id=$2`, t.UserID, t.CurationID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, label string
		if e = rows.Scan(&id, &label); e != nil {
			rows.Close()
			return e
		}
		t.TargetLabels[id] = label
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}

	raw, err := json.Marshal(storedThread{t, t.AuthSessionID})
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE curation_threads SET status=$3,revision=$4,data=$5,updated_at=$6 WHERE user_id=$1 AND id=$2 AND (status<>$3 OR revision<>$4 OR data<>$5::jsonb)`, t.UserID, t.ID, t.Status, t.Revision, raw, time.Now().UTC())
	return err
}

// ListThreads returns the Curation's control mode with its Threads. The mode
// read is also the existence and archive check, so the list carries it and the
// Web reads both in one request (ADR-0081).
func (r *Repository) ListThreads(ctx context.Context, user, id string) (d.ControlMode, []d.CurationThread, error) {
	mode, err := r.ReadControlMode(ctx, user, id)
	if err != nil {
		return mode, nil, err
	}
	threads, err := r.readThreads(ctx, `SELECT id::text,`+threadColumns+` FROM curation_threads WHERE user_id=$1 AND curation_id=$2 ORDER BY created_at DESC,id DESC LIMIT 100`, user, id)
	if err != nil {
		return mode, nil, err
	}
	return mode, threads, nil
}
func (r *Repository) PendingThreads(ctx context.Context) ([]d.CurationThread, error) {
	return r.readThreads(ctx, `SELECT id::text,`+threadColumns+` FROM curation_threads WHERE status IN ('INTERPRETING','RUNNING') ORDER BY updated_at LIMIT 32`)
}

// readThreads reads the Thread rows and then every Action of those Threads in
// one more query, so a list costs two statements whatever its length (ADR-0081).
// The rows close before the Actions are read: a transaction has one connection.
func (r *Repository) readThreads(ctx context.Context, query string, args ...any) ([]d.CurationThread, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []d.CurationThread{}
	index := map[string]int{}
	ids := []string{}
	for rows.Next() {
		var id string
		var row d.CurationThread
		var raw []byte
		if err = rows.Scan(&id, &row.UserID, &row.CurationID, &row.PlanID, &row.RequestHash, &row.Revision, &row.Status, &raw, &row.CreatedAt, &row.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		t, e := decodeThread(id, row, raw)
		if e != nil {
			rows.Close()
			return nil, e
		}
		index[id] = len(out)
		ids = append(ids, id)
		out = append(out, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	actions, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT thread_id::text,`+threadActionColumns+` FROM curation_actions WHERE thread_id=ANY($1::uuid[]) ORDER BY thread_id,sequence`, ids)
	if err != nil {
		return nil, err
	}
	defer actions.Close()
	for actions.Next() {
		var thread string
		action, e := scanCurationAction(threadActionRow{actions, &thread})
		if e != nil {
			return nil, e
		}
		normalizeActionCollections(&action)
		n := index[thread]
		out[n].Actions = append(out[n].Actions, action)
	}
	if err = actions.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// threadActionRow reads the owning thread_id ahead of the columns
// scanCurationAction expects.
type threadActionRow struct {
	rows   curationActionRowScanner
	thread *string
}

func (r threadActionRow) Scan(dest ...any) error {
	return r.rows.Scan(append([]any{r.thread}, dest...)...)
}
func (r *Repository) ActionJobs(ctx context.Context, thread, step string) ([]d.ActionJobResult, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT j.id::text,COALESCE((SELECT id::text FROM intelligence_attempts i WHERE i.job_id=j.id AND i.curation_action_id=j.execution_action_id ORDER BY ordinal DESC LIMIT 1),''),j.execution_action_id::text,COALESCE(s.plan_target_id::text,''),
 CASE WHEN p.status='COMPLETED' OR rr.status IN ('RESULTS_READY','NO_RESULTS','SUPERSEDED') THEN 'SUCCEEDED' ELSE j.status END,
 COALESCE(j.failure_code,''),j.target_kind,COALESCE(rr.status::text,''),
 CASE WHEN rr.completed_at IS NOT NULL THEN (SELECT count(*) FROM phase8_research_candidates pc WHERE pc.user_id=j.user_id AND pc.plan_target_id=s.plan_target_id AND pc.first_seen_at>=rr.created_at AND pc.first_seen_at<=rr.completed_at) ELSE 0 END,
 COALESCE(o.round_id::text,''),COALESCE(o.observed_count,0),COALESCE(o.duplicate_count,0),COALESCE(o.rejected_count,0),COALESCE(o.admitted_count,0),COALESCE(o.evaluated_count,0),COALESCE(o.unevaluated_count,0),COALESCE(o.source_coverage,'[]'::jsonb)
 FROM curation_thread_jobs l JOIN intelligence_jobs j ON j.id=l.job_id
 LEFT JOIN planning_tasks p ON p.id=j.planning_task_id
 LEFT JOIN research_rounds rr ON rr.id=j.research_round_id
 LEFT JOIN shopping_sessions s ON s.id=rr.shopping_session_id
 LEFT JOIN research_round_outcomes o ON o.round_id=rr.id
 WHERE l.thread_id=$1 AND l.action_id=$2 ORDER BY j.created_at,j.id`, thread, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []d.ActionJobResult{}
	for rows.Next() {
		var v d.ActionJobResult
		var kind, roundStatus string
		var count int
		var facts d.ResearchFacts
		var coverage []byte
		if err = rows.Scan(&v.JobID, &v.AttemptID, &v.ActionID, &v.TargetID, &v.Status, &v.ReasonCode, &kind, &roundStatus, &count,
			&facts.RoundID, &facts.Observed, &facts.Duplicates, &facts.Rejected, &facts.Admitted, &facts.Evaluated, &facts.Unevaluated, &coverage); err != nil {
			return nil, err
		}
		// The Round outcome ledger is what a response may quote: how much each
		// source returned and how much of it was compared (ADR-0086).
		if facts.RoundID != "" && v.Status == "SUCCEEDED" {
			facts.Sources = []d.ResearchSourceFact{}
			if err = json.Unmarshal(coverage, &facts.Sources); err != nil {
				return nil, err
			}
			if facts.Sources == nil {
				facts.Sources = []d.ResearchSourceFact{}
			}
			v.Facts = &facts
		}
		v.Kind = kind
		v.Effects = []d.ActionEffect{}
		if v.Status == "SUCCEEDED" && kind == "RESEARCH_ROUND" && roundStatus == "NO_RESULTS" {
			// An honest empty round is a completed Job, not a failure; the
			// receipt says so instead of reporting zero added candidates.
			v.Effects = append(v.Effects, d.ActionEffect{Kind: "NO_RESULTS", TargetID: v.TargetID})
		} else if v.Status == "SUCCEEDED" && kind == "RESEARCH_ROUND" {
			v.Effects = append(v.Effects, d.ActionEffect{Kind: "CANDIDATES_ADDED", TargetID: v.TargetID, Count: count})
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) InitializeControlMode(ctx context.Context, id, mode string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_control_modes(curation_id,mode) VALUES($1,$2)`, id, mode)
	return err
}

// Accepted planning, membership, allocations and this receipt share one transaction.
func (r *Repository) RecordPlanningThreadResult(ctx context.Context, user, curation, job string, before d.BudgetLedger, targets []d.PlanTarget, budgetDecision *d.ActionDecision) error {
	var id, stepID string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT thread_id::text,action_id::text FROM curation_thread_jobs WHERE job_id=$1`, job).Scan(&id, &stepID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	t, err := r.ReadThread(ctx, user, id)
	if err != nil {
		return err
	}
	if !t.Active() {
		return fault.New(fault.Conflict, "CURATION_THREAD_CANCELLED", false)
	}
	after, err := r.ReadBudget(ctx, user, curation, false)
	if err != nil {
		return err
	}
	var step *d.CurationAction
	for i := range t.Actions {
		if t.Actions[i].ID == stepID {
			step = &t.Actions[i]
			break
		}
	}
	if step == nil {
		return fault.New(fault.Conflict, "CURATION_ACTION_MISSING", false)
	}
	for _, target := range targets {
		step.Effects = append(step.Effects, d.ActionEffect{Kind: "TARGET_ADDED", TargetID: string(target.ID), TargetLabel: target.Title, Count: 1})
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if before.Enabled || after.Enabled {
		step.Effects = append(step.Effects, d.ActionEffect{Kind: "BUDGET_CHANGED", Before: b, After: a})
	}
	if budgetDecision == nil {
		result := "KEEP"
		source := "MANUAL"
		reason := "EXPLICIT_SETTINGS"
		if len(before.Allocations) > 0 && after.Enabled {
			result = "SET_TOTAL"
			source = "DETERMINISTIC"
			reason = "PRESERVE_TOTAL_FOR_MEMBERSHIP"
			if t.Mode == "AUTO" {
				source = "MANAGED"
				reason = "AUTO_ALLOCATION_WITH_PRESERVED_TOTAL"
			}
		}
		budgetDecision = &d.ActionDecision{Kind: "BUDGET", Result: result, Source: source, ReasonCode: reason}
	}
	budgetDecision.ID = job + ":budget"
	budgetDecision.ActionIDs = []string{stepID}
	step.Decisions = append(step.Decisions, *budgetDecision)
	for _, kind := range []string{"ACTION", "TARGET", "CONDITIONS"} {
		result := "INITIALIZE"
		if step.Type == d.CurationActionCurationAddTargets || step.Type == d.CurationActionPlanningAddTargets {
			if kind == "ACTION" {
				result = "ADD_TARGET"
			}
			if kind == "TARGET" {
				result = "NEW_TARGETS"
			}
		}
		step.Decisions = append(step.Decisions, d.ActionDecision{ID: job + ":" + kind, Kind: kind, Result: result, Source: "MANAGED", ReasonCode: "PLANNING_RESULT_ACCEPTED", ActionIDs: []string{stepID}})
	}
	return r.SaveThread(ctx, t)
}
func (r *Repository) recordManualThread(ctx context.Context, user, curation, id, kind, target string, effects []d.ActionEffect, command d.CurationAction) error {
	if scope := a.ThreadExecutionFrom(ctx); scope.ThreadID != "" {
		return nil
	}
	commandID := id
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT md5($1)::uuid::text`, curation+":"+kind+":"+commandID).Scan(&id); err != nil {
		return err
	}
	var plan string
	var version int64
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT shopping_plan_id::text,version FROM curations WHERE user_id=$1 AND id=$2`, user, curation).Scan(&plan, &version); err != nil {
		return err
	}
	mode, err := r.ReadControlMode(ctx, user, curation)
	if err != nil {
		return err
	}
	label := ""
	if target != "" {
		if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT title FROM plan_targets WHERE user_id=$1 AND curation_id=$2 AND id=$3`, user, curation, target).Scan(&label); err != nil {
			return err
		}
	}
	for i := range effects {
		if effects[i].TargetID == target {
			effects[i].TargetLabel = label
		}
	}
	now := time.Now().UTC()
	t := d.CurationThread{SchemaVersion: d.ThreadSchema, ID: id, UserID: user, CurationID: curation, PlanID: plan, Mode: mode.Mode, Origin: "MANUAL", Status: "SUCCEEDED", Revision: 1, ExpectedCurationVersion: version, RequestHash: id, Actions: []d.CurationAction{{ID: id, Type: d.ActionTypeForPrimitive(kind), TargetID: target, TargetLabel: label, Status: "SUCCEEDED", Effects: effects, Jobs: []d.ActionJobResult{}}}, CreatedAt: now, UpdatedAt: now}
	t.Actions[0].Budget, t.Actions[0].Criteria = command.Budget, command.Criteria
	if kind == "BUDGET" || kind == "CRITERIA" {
		decisionKind := "BUDGET"
		result := "BUDGET"
		if kind == "CRITERIA" {
			decisionKind = "CONDITIONS"
			result = "SET_CRITERIA"
		}
		t.Actions[0].Decisions = append(t.Actions[0].Decisions, d.ActionDecision{ID: id + ":setting", Kind: decisionKind, Result: result, Source: "MANUAL", ReasonCode: "EXPLICIT_COMMAND", TargetID: target, TargetLabel: label, ActionIDs: []string{id}})
	}
	if err := r.InsertThread(ctx, t); err != nil {
		return err
	}
	return r.SaveThread(ctx, t)
}

func normalizeActionCollections(a *d.CurationAction) {
	if a.Jobs == nil {
		a.Jobs = []d.ActionJobResult{}
	}
	if a.Effects == nil {
		a.Effects = []d.ActionEffect{}
	}
	if a.Decisions == nil {
		a.Decisions = []d.ActionDecision{}
	}
	if a.Answers == nil {
		a.Answers = []d.ThreadAnswer{}
	}
	if a.DecisionIDs == nil {
		a.DecisionIDs = []string{}
	}
	normalizeQuestion := func(q *d.ThreadQuestion) {
		if q == nil {
			return
		}
		for i := range q.Options {
			for j := range q.Options[i].Actions {
				normalizeActionCollections(&q.Options[i].Actions[j])
			}
		}
	}
	normalizeQuestion(a.Question)
	for i := range a.Questions {
		normalizeQuestion(&a.Questions[i])
	}
}
func normalizeThreadActions(t *d.CurationThread) {
	for i := range t.Actions {
		a := &t.Actions[i]
		a.ThreadID = t.ID
		a.Sequence = i
		normalizeActionCollections(a)
	}
}

package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	cv "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	rd "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

// PrepareConversationAction prevents a legacy exact endpoint from reusing a
// conversation request ID for a different action. The caller owns the Curation lock.
func (r *Repository) PrepareConversationAction(ctx context.Context, in c.RecordCurationActionInput) error {
	if scope := c.ThreadExecutionFrom(ctx); scope.ThreadID != "" {
		return r.DispatchContext(ctx, scope.ThreadID)
	}
	var mode, status, body, decision, target string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT cr.mode,cr.status,cr.body,COALESCE(ar.decision,''),COALESCE(ar.target_id::text,'') FROM curation_conversation_requests cr LEFT JOIN curation_auto_resolutions ar ON ar.id=cr.id WHERE cr.id=$1`, in.ActionID).Scan(&mode, &status, &body, &decision, &target)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		conflict := status == "COMPLETE" || mode == "RETRY" || mode == "RECOVERED"
		if mode == "AUTO" {
			conflict = conflict || body != in.Body
			switch decision {
			case "ADD_TARGET":
				conflict = conflict || (in.Type != d.CurationActionCurationAddTargets && in.Type != d.CurationActionPlanningAddTargets)
			case "RESEARCH_AGAIN":
				conflict = conflict || in.Type != d.CurationActionTargetResearchAgain || in.SubjectID == nil || *in.SubjectID != target
			default:
				conflict = true
			}
		}
		if conflict {
			return fault.New(fault.Conflict, "CONVERSATION_IDEMPOTENCY_CONFLICT", false)
		}
	}
	return r.DispatchContext(ctx, in.ActionID)
}

func (r *Repository) DispatchContext(ctx context.Context, id string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `SELECT set_config('vitlane.conversation_request',$1,true)`, id)
	return err
}
func (r *Repository) Read(ctx context.Context, user, cid string) (cv.Conversation, error) {
	out := cv.Conversation{SchemaVersion: "vitlane.curation-conversation.v1", Requests: []cv.Request{}, Messages: []d.FollowUp{}}
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT COALESCE(cc.version,0),EXISTS(SELECT 1 FROM curation_conversation_requests WHERE curation_id=c.id AND (NOT response_ready OR generating)) FROM curations c LEFT JOIN curation_conversations cc ON cc.curation_id=c.id WHERE c.user_id=$1 AND c.id=$2`, user, cid).Scan(&out.Version, &out.Unfinished)
	if errors.Is(err, sql.ErrNoRows) {
		return out, d.ErrCurationNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id,body,mode,status,COALESCE(action_id::text,''),created_at FROM (SELECT * FROM curation_conversation_requests WHERE user_id=$1 AND curation_id=$2 ORDER BY created_at DESC,id DESC LIMIT 100) recent ORDER BY created_at,id`, user, cid)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var q cv.Request
		if err = rows.Scan(&q.ID, &q.Body, &q.Mode, &q.Status, &q.ActionID, &q.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Requests = append(out.Requests, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = r.database.Queryer(ctx).QueryContext(ctx, `SELECT id,response_id,kind,status,version,content,created_at FROM (SELECT * FROM curation_follow_ups WHERE user_id=$1 AND curation_id=$2 ORDER BY created_at DESC,id DESC LIMIT 200) recent ORDER BY created_at,id`, user, cid)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m d.FollowUp
		var content []byte
		if err = rows.Scan(&m.ID, &m.ResponseID, &m.Kind, &m.Status, &m.Version, &content, &m.CreatedAt); err != nil {
			return out, err
		}
		if err = json.Unmarshal(content, &m.Content); err != nil {
			return out, err
		}
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}
func (r *Repository) LockMessage(ctx context.Context, in cv.ResponseInput) (d.FollowUp, int64, error) {
	var m d.FollowUp
	var version int64
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT version FROM curations WHERE user_id=$1 AND id=$2 AND archived_at IS NULL FOR UPDATE`, in.UserID, in.CurationID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return m, 0, d.ErrCurationNotFound
	}
	if err != nil {
		return m, 0, err
	}
	var content, payload []byte
	var fresh sql.NullString
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id,response_id,kind,status,version,content,payload,fingerprint,COALESCE(response_request_id::text,''),created_at,curation_follow_up_context(curation_id,(payload->>'targetId')::uuid) FROM curation_follow_ups WHERE user_id=$1 AND curation_id=$2 AND id=$3 FOR UPDATE`, in.UserID, in.CurationID, in.MessageID).Scan(&m.ID, &m.ResponseID, &m.Kind, &m.Status, &m.Version, &content, &payload, &m.Fingerprint, &m.ResponseRequestID, &m.CreatedAt, &fresh)
	if errors.Is(err, sql.ErrNoRows) {
		return m, 0, fault.New(fault.InvalidInput, "FOLLOW_UP_NOT_FOUND", false)
	}
	if err != nil {
		return m, 0, err
	}
	if err = json.Unmarshal(content, &m.Content); err != nil {
		return m, 0, err
	}
	if payload != nil {
		if err = json.Unmarshal(payload, &m.Payload); err != nil {
			return m, 0, err
		}
	}
	if m.Status == "PENDING" && m.Kind == "PROPOSAL" && (!fresh.Valid || fresh.String != m.Fingerprint) {
		return m, 0, fault.New(fault.Conflict, "FOLLOW_UP_CONTEXT_CHANGED", false)
	}
	return m, version, nil
}
func (r *Repository) SaveResponse(ctx context.Context, in cv.ResponseInput, status string) error {
	res, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE curation_follow_ups SET status=$4,version=version+1,response_request_id=$5 WHERE user_id=$1 AND curation_id=$2 AND id=$3 AND status='PENDING' AND version=$6`, in.UserID, in.CurationID, in.MessageID, status, in.ClientRequestID, in.ExpectedVersion)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fault.New(fault.Conflict, "FOLLOW_UP_SUPERSEDED", false)
	}
	return nil
}
func (r *Repository) AdmitManual(ctx context.Context, in cv.RequestInput) (bool, error) {
	bytes, _ := json.Marshal(in)
	hash := sha256.Sum256(bytes)
	digest := hex.EncodeToString(hash[:])
	var stored string
	var user, cid string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT user_id,curation_id,request_hash FROM curation_conversation_requests WHERE id=$1`, in.ClientRequestID).Scan(&user, &cid, &stored)
	if err == nil {
		if user != in.UserID || cid != in.CurationID || stored != digest {
			return false, fault.New(fault.Conflict, "CONVERSATION_IDEMPOTENCY_CONFLICT", false)
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if in.Mode == "RETRY" {
		var owner string
		if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT curation_id FROM intelligence_jobs WHERE user_id=$1 AND id=$2`, in.UserID, in.JobID).Scan(&owner); err != nil {
			return false, err
		}
		if owner != in.CurationID {
			return false, d.ErrCurationNotFound
		}
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `SELECT curation_admit_request($1,$2,$3,$4,$5,$6,'RESOLVING',$7)`, in.UserID, in.CurationID, in.ClientRequestID, in.Mode, in.Request, digest, in.ExpectedConversationVersion)
	if err != nil {
		return false, err
	}
	var state string
	if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT status FROM curation_conversation_requests WHERE id=$1`, in.ClientRequestID).Scan(&state); err != nil {
		return false, err
	}
	return state != "RESOLVING", nil
}

// ClaimResponse groups the whole admitted pipeline. It publishes facts before
// optional generation, commits its one-shot checkpoint and never queries a catalog.
func (r *Repository) ClaimResponse(ctx context.Context) (*cv.Generation, error) {
	var generation *cv.Generation
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		// Classification may be abandoned without dispatching an action. A late
		// classifier cannot Resolve once this terminal checkpoint has committed.
		if _, e := q.ExecContext(tx, `UPDATE curation_auto_resolutions SET status='NEEDS_SELECTION',decision='NEEDS_SELECTION',source='MANAGED',reason_code='AUTO_RESOLUTION_INTERRUPTED' WHERE status='PENDING' AND created_at<now()-interval '2 minutes'`); e != nil {
			return e
		}
		// Abandon optional generation after a worker interruption; never re-call it.
		if _, err := q.ExecContext(tx, `UPDATE curation_conversation_requests SET generating=false WHERE generating AND generation_started_at<now()-interval '1 minute'`); err != nil {
			return err
		}
		var id, user, cid, status, code, mode, locale string
		var created time.Time
		err := q.QueryRowContext(tx, `SELECT cr.id,cr.user_id,cr.curation_id,cr.status,cr.result_code,cr.mode,cr.content_locale,cr.created_at FROM curation_conversation_requests cr WHERE NOT cr.response_ready AND cr.status<>'RESOLVING' AND NOT EXISTS(SELECT 1 FROM curation_threads t WHERE t.id=cr.id AND t.status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')) AND (cr.status='COMPLETE' OR EXISTS(SELECT 1 FROM intelligence_jobs j WHERE j.curation_id=cr.curation_id AND (j.created_at>=cr.created_at OR j.curation_action_id=cr.action_id))) AND NOT EXISTS(SELECT 1 FROM intelligence_jobs j WHERE j.curation_id=cr.curation_id AND j.status IN ('PENDING','RUNNING')) ORDER BY cr.created_at LIMIT 1`).Scan(&id, &user, &cid, &status, &code, &mode, &locale, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var latest string
		// Intake also snapshots the previous request, so always lock Curation before
		// the request row. Recheck the checkpoint after acquiring both locks.
		if err = q.QueryRowContext(tx, `SELECT COALESCE(cc.latest_request_id::text,'') FROM curations c LEFT JOIN curation_conversations cc ON cc.curation_id=c.id WHERE c.id=$1 FOR UPDATE OF c`, cid).Scan(&latest); err != nil {
			return err
		}
		var ready bool
		if err = q.QueryRowContext(tx, `SELECT response_ready FROM curation_conversation_requests WHERE id=$1 FOR UPDATE`, id).Scan(&ready); err != nil {
			return err
		}
		if ready {
			return nil
		}

		g := &cv.Generation{UserID: user, CurationID: cid, ResponseID: id, Locale: locale}
		messages := []d.FollowUp{}
		if status == "COMPLETE" {
			kind := "CLARIFICATION"
			if code == "NO_ACTION" || code == "BUDGET_SETTINGS_ONLY" {
				kind = "NOTICE"
			}
			messages = append(messages, d.FollowUp{Kind: kind, Content: d.FollowUpContent{Code: code}})
		} else {
			// A reply that could not be written is not a research failure (ADR-0086): its Job
			// stays FAILED for operators, but it neither reports an error here nor withholds a proposal.
			rows, e := q.QueryContext(tx, `SELECT value->>'id',value->>'status',value->>'reason',(value->>'retryable')::boolean,value->>'title',value->>'round' FROM curation_conversation_requests cr CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cr.terminal_jobs,(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',j.id,'status',j.status,'reason',COALESCE(j.failure_code,''),'retryable',j.retryable,'title',COALESCE(t.title,''),'round',j.research_round_id)),'[]'::jsonb) FROM intelligence_jobs j LEFT JOIN research_rounds rr ON rr.id=j.research_round_id LEFT JOIN shopping_sessions ss ON ss.id=rr.shopping_session_id LEFT JOIN plan_targets t ON t.id=ss.plan_target_id WHERE j.curation_id=$1 AND (j.created_at>=$2 OR (j.curation_action_id=cr.action_id AND j.updated_at>=$2))))) snapshot WHERE cr.id=$3 AND NOT EXISTS(SELECT 1 FROM intelligence_jobs rj JOIN curation_actions ra ON ra.id=rj.interpretation_action_id WHERE rj.id=(value->>'id')::uuid AND ra.action_type='RESPONSE')`, cid, created, id)
			if e != nil {
				return e
			}
			failed, cancelled := false, false
			rounds := map[string]bool{}
			for rows.Next() {
				var job, js, reason, title string
				var retry bool
				var round sql.NullString
				if e = rows.Scan(&job, &js, &reason, &retry, &title, &round); e != nil {
					rows.Close()
					return e
				}
				if round.Valid {
					rounds[round.String] = true
				}
				if js == "FAILED" {
					failed = true
					messages = append(messages, d.FollowUp{Kind: "ERROR", Content: d.FollowUpContent{Code: "RESEARCH_FAILED", JobID: job, ReasonCode: reason, Retryable: retry, TargetTitle: title}})
				}
				if js == "CANCELLED" {
					cancelled = true
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			added := 0
			// Persisted assessments include the exact research round; visibility and
			// current hydration never influence response facts.
			rows, e = q.QueryContext(tx, `SELECT t.id,t.title,tc.criteria,ss.id,ss.version,COALESCE(curation_follow_up_context(t.curation_id,t.id),''),candidate.axis_assessment,(SELECT count(*) FROM phase8_research_candidates pc WHERE pc.curation_id=t.curation_id AND pc.plan_target_id=t.id) FROM plan_targets t JOIN curation_target_criteria tc ON tc.target_id=t.id JOIN shopping_sessions ss ON ss.plan_target_id=t.id JOIN phase8_research_candidates candidate ON candidate.plan_target_id=t.id WHERE t.curation_id=$1 AND t.removed_at IS NULL AND candidate.axis_assessment IS NOT NULL ORDER BY t.order_index,candidate.candidate_id`, cid)
			if e != nil {
				return e
			}
			type targetFacts struct {
				title, tid, sid, fingerprint string
				sv                           int64
				criteria                     d.TargetCriteriaSetV1
				count                        int
				low, good                    map[string]int
				total                        int
				// Outcomes of the Rounds this response completed for the Target.
				researched                     bool
				observed, duplicates, admitted int
				limited                        []d.MallName
				query                          string
			}
			targets := map[string]*targetFacts{}
			order := []string{}
			for rows.Next() {
				var tid, title, sid, fp string
				var version int64
				var count int
				var criteria, assessment []byte
				if e = rows.Scan(&tid, &title, &criteria, &sid, &version, &fp, &assessment, &count); e != nil {
					rows.Close()
					return e
				}
				var c d.TargetCriteriaSetV1
				var a rd.AxisAssessmentV1
				if e = json.Unmarshal(criteria, &c); e != nil {
					rows.Close()
					return e
				}
				if e = json.Unmarshal(assessment, &a); e != nil {
					rows.Close()
					return e
				}
				if !rounds[a.RoundID] {
					continue
				}
				added++
				t := targets[tid]
				if t == nil {
					t = &targetFacts{title: c.Subject.Label, tid: tid, sid: sid, fingerprint: fp, sv: version, criteria: c, count: count, low: map[string]int{}, good: map[string]int{}}
					targets[tid] = t
					order = append(order, tid)
				}
				// Compare only evaluations made under the same immutable criteria.
				cb, _ := json.Marshal(c)
				ab, _ := json.Marshal(a.Criteria)
				if string(cb) != string(ab) {
					continue
				}
				t.total++
				for _, score := range a.Scores {
					if score.ScorePercent < 50 {
						t.low[score.AxisID]++
					}
					if score.ScorePercent >= 60 {
						t.good[score.AxisID]++
					}
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if latest == id {
				coverageRows, e := q.QueryContext(tx, `SELECT t.title,p.source_coverage FROM phase8_research_pools p JOIN plan_targets t ON t.id=p.plan_target_id WHERE p.curation_id=$1 AND p.updated_at>=$2`, cid, created)
				if e != nil {
					return e
				}
				for coverageRows.Next() {
					var title string
					var raw []byte
					if e = coverageRows.Scan(&title, &raw); e != nil {
						coverageRows.Close()
						return e
					}
					var sources []struct {
						Status         string `json:"status"`
						ReasonCode     string `json:"reasonCode"`
						CandidateCount int    `json:"candidateCount"`
					}
					if len(raw) > 0 {
						if e = json.Unmarshal(raw, &sources); e != nil {
							coverageRows.Close()
							return e
						}
					}
					for _, source := range sources {
						if source.Status != "FAILED" && !(source.Status == "PARTIAL" && source.CandidateCount == 0) {
							continue
						}
						switch source.ReasonCode {
						case "CATALOG_API_DISABLED", "CATALOG_API_NOT_CONFIGURED", "CATALOG_API_RATE_LIMITED", "CATALOG_LOCAL_DAILY_LIMIT", "CATALOG_QUOTA_EXHAUSTED", "CATALOG_QUOTA_UNCONFIRMED", "AMAZON_API_DISABLED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_QUOTA_UNCONFIRMED":
							continue
						}
						messages = append(messages, d.FollowUp{Kind: "ERROR", Content: d.FollowUpContent{Code: "RESEARCH_SOURCE_INCOMPLETE", TargetTitle: title, ReasonCode: source.ReasonCode}})
						break
					}
				}
				e = coverageRows.Err()
				coverageRows.Close()
				if e != nil {
					return e
				}
			}
			resultCode := "RESEARCH_COMPLETED"
			if cancelled {
				resultCode = "RESEARCH_CANCELLED"
			}
			if failed {
				resultCode = "RESEARCH_PARTIAL_FAILURE"
			}
			messages = append([]d.FollowUp{{Kind: "RESULT", Content: d.FollowUpContent{Code: resultCode, Added: added}}}, messages...)
			g.BackgroundEligible = !failed && !cancelled && mode != "RECOVERED" && latest == id
			if !failed && !cancelled && added > 0 && mode != "RECOVERED" && latest == id {
				// What each researched Target's sources returned this response: the
				// few-new and call-limit proposals read it, and the previous query
				// steers a different search away from repeating itself.
				roundIDs := make([]string, 0, len(rounds))
				for round := range rounds {
					roundIDs = append(roundIDs, round)
				}
				outcomeRows, e := q.QueryContext(tx, `SELECT t.id::text,tc.criteria,ss.id::text,ss.version,COALESCE(curation_follow_up_context(t.curation_id,t.id),''),(SELECT count(*) FROM phase8_research_candidates pc WHERE pc.curation_id=t.curation_id AND pc.plan_target_id=t.id),COALESCE(p.provider_query,''),o.observed,o.duplicates,o.admitted,o.coverage FROM (SELECT plan_target_id,sum(observed_count)::int observed,sum(duplicate_count)::int duplicates,sum(admitted_count)::int admitted,jsonb_agg(source_coverage) coverage FROM research_round_outcomes WHERE curation_id=$1 AND round_id=ANY($2::uuid[]) GROUP BY plan_target_id) o JOIN plan_targets t ON t.id=o.plan_target_id AND t.curation_id=$1 AND t.removed_at IS NULL JOIN curation_target_criteria tc ON tc.target_id=t.id JOIN shopping_sessions ss ON ss.plan_target_id=t.id LEFT JOIN phase8_research_pools p ON p.curation_id=t.curation_id AND p.plan_target_id=t.id ORDER BY t.order_index`, cid, roundIDs)
				if e != nil {
					return e
				}
				for outcomeRows.Next() {
					var tid, sid, fp, query string
					var version int64
					var count, observed, duplicates, admitted int
					var criteria, coverage []byte
					if e = outcomeRows.Scan(&tid, &criteria, &sid, &version, &fp, &count, &query, &observed, &duplicates, &admitted, &coverage); e != nil {
						outcomeRows.Close()
						return e
					}
					t := targets[tid]
					if t == nil {
						var c d.TargetCriteriaSetV1
						if e = json.Unmarshal(criteria, &c); e != nil {
							outcomeRows.Close()
							return e
						}
						t = &targetFacts{title: c.Subject.Label, tid: tid, sid: sid, fingerprint: fp, sv: version, criteria: c, count: count, low: map[string]int{}, good: map[string]int{}}
						targets[tid] = t
						order = append(order, tid)
					}
					var sources [][]struct {
						Source         string `json:"source"`
						Status         string `json:"status"`
						ReasonCode     string `json:"reasonCode"`
						CandidateCount int    `json:"candidateCount"`
					}
					if e = json.Unmarshal(coverage, &sources); e != nil {
						outcomeRows.Close()
						return e
					}
					seen := map[string]bool{}
					for _, round := range sources {
						for _, source := range round {
							if (source.Status != "SKIPPED" && source.Status != "FAILED") || source.CandidateCount > 0 || seen[source.Source] ||
								(source.ReasonCode != "CATALOG_API_RATE_LIMITED" && source.ReasonCode != "CATALOG_UPSTREAM_RATE_LIMITED") {
								continue
							}
							seen[source.Source] = true
							name := d.MallName{Korean: source.Source, English: source.Source}
							if mall, ok := rd.KoreanMall(rd.Source(source.Source)); ok {
								name = d.MallName{Korean: mall.LabelKO, English: mall.LabelEN}
							}
							t.limited = append(t.limited, name)
						}
					}
					t.researched, t.observed, t.duplicates, t.admitted, t.query = true, observed, duplicates, admitted, query
				}
				e = outcomeRows.Err()
				outcomeRows.Close()
				if e != nil {
					return e
				}
				now := time.Now()
				for _, tid := range order {
					t := targets[tid]
					proposals := d.ResearchAgainProposals(d.ProposalFacts{Criteria: t.criteria, Compared: t.total, LowByAxis: t.low, GoodByAxis: t.good, PoolSize: t.count, Researched: t.researched, Observed: t.observed, Duplicates: t.duplicates, Admitted: t.admitted, RateLimitedMalls: t.limited, PreviousQuery: t.query}, g.Locale, d.FollowUpAction{Kind: "RESEARCH_AGAIN", TargetID: tid, SessionID: t.sid, SessionVersion: t.sv, CriteriaVersion: t.criteria.Version}, t.fingerprint, now)
					if len(proposals) == 0 {
						continue
					}
					var dismissed bool
					if e = q.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM curation_follow_ups WHERE curation_id=$1 AND kind='PROPOSAL' AND status='DISMISSED' AND fingerprint=$2 AND payload->>'targetId'=$3)`, cid, t.fingerprint, tid).Scan(&dismissed); e != nil {
						return e
					}
					if !dismissed {
						g.Choices = append(g.Choices, proposals...)
					}
				}
			}
		}
		for _, m := range messages {
			m.Status = "PENDING"
			if m.Kind == "RESULT" || latest != id {
				m.Status = "SUPERSEDED"
			}
			if err = r.insertFollowUp(tx, g, m); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(tx, `UPDATE curation_conversation_requests SET response_ready=true,generating=$2,generation_started_at=now(),status='COMPLETE' WHERE id=$1`, id, len(g.Choices) > 0 || g.BackgroundEligible)
		if err != nil {
			return err
		}
		generation = g
		return nil
	})
	return generation, err
}
func (r *Repository) insertFollowUp(ctx context.Context, g *cv.Generation, m d.FollowUp) error {
	content, err := json.Marshal(m.Content)
	if err != nil {
		return err
	}
	var payload any
	if m.Payload != nil {
		b, e := json.Marshal(m.Payload)
		if e != nil {
			return e
		}
		payload = string(b)
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content,payload,fingerprint) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8)`, g.UserID, g.CurationID, g.ResponseID, m.Kind, m.Status, string(content), payload, m.Fingerprint)
	return err
}
func (r *Repository) FinishGeneration(ctx context.Context, g *cv.Generation, m *d.FollowUp) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var latest string
		if err := q.QueryRowContext(tx, `SELECT COALESCE(cc.latest_request_id::text,'') FROM curations c JOIN curation_conversations cc ON cc.curation_id=c.id WHERE c.id=$1 FOR UPDATE OF c`, g.CurationID).Scan(&latest); err != nil {
			return err
		}
		var active bool
		if err := q.QueryRowContext(tx, `SELECT generating FROM curation_conversation_requests WHERE id=$1`, g.ResponseID).Scan(&active); err != nil {
			return err
		}
		if m != nil && active && latest == g.ResponseID {
			var fp sql.NullString
			if err := q.QueryRowContext(tx, `SELECT curation_follow_up_context($1,$2)`, g.CurationID, m.Payload.TargetID).Scan(&fp); err != nil {
				return err
			}
			if fp.Valid && fp.String == m.Fingerprint {
				m.Status = "PENDING"
				if err := r.insertFollowUp(tx, g, *m); err != nil {
					return err
				}
			}
		}
		_, err := q.ExecContext(tx, `UPDATE curation_conversation_requests SET generating=false WHERE id=$1`, g.ResponseID)
		return err
	})
}

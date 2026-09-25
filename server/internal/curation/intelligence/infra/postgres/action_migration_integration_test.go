package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	cp "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"os"
	"testing"
	"time"
)

func TestActionMigrationPreservesChoicesAndDoesNotRepeatInflightAI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	down, e := os.ReadFile("../../../../../migrations/000115_curation_action_execution.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	up, e := os.ReadFile("../../../../../migrations/000115_curation_action_execution.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	rollback := errors.New("test rollback")
	for _, waiting := range []bool{true, false} {
		e = f.database.WithinTransaction(ctx, func(tx context.Context) error {
			q := f.database.Queryer(tx)
			if _, e := q.ExecContext(tx, string(down)); e != nil {
				return e
			}
			id := f.repository.ids.NewID()
			stepID := f.repository.ids.NewID()
			status := "WAITING_SELECTION"
			raw := map[string]any{"schemaVersion": "vitlane.curation-thread.v1", "id": id, "mode": "AUTO", "origin": "REQUEST", "request": "synthetic budget request", "revision": int64(2), "expectedCurationVersion": int64(1), "steps": []any{}, "decisions": []any{}}
			if waiting {
				raw["question"] = map[string]any{"id": f.repository.ids.NewID(), "prompt": "Choose scope", "options": []any{map[string]any{"id": f.repository.ids.NewID(), "label": "total", "steps": []any{map[string]any{"id": stepID, "kind": "BUDGET", "status": "PENDING", "budget": map[string]any{"kind": "SET_TOTAL", "amount": "100.00"}}}, "decisions": []any{map[string]any{"kind": "BUDGET", "stepIds": []string{stepID}}}}}}
			} else {
				status = "INTERPRETING"
				raw["interpretationStartedAt"] = time.Now().UTC()
			}
			b, _ := json.Marshal(raw)
			if _, e := q.ExecContext(tx, `INSERT INTO curation_threads(id,user_id,curation_id,plan_id,status,request_hash,revision,data) VALUES($1::uuid,$2,$3,$4,$5,$1::text,2,$6)`, id, f.userID, f.job.CurationID, f.job.PlanID, status, b); e != nil {
				return e
			}
			if _, e := q.ExecContext(tx, string(up)); e != nil {
				return e
			}
			got, e := cp.NewRepository(f.database, nil).ReadThread(tx, f.userID, id)
			if e != nil {
				return e
			}
			if len(got.Actions) != 1 || got.Actions[0].Type != d.CurationActionAutoStart {
				return fmt.Errorf("missing AUTO_START: %+v", got)
			}
			a := got.Actions[0]
			if waiting {
				if a.Question == nil || len(a.Question.Options) != 1 || len(a.Question.Options[0].Actions) != 1 || a.Question.Options[0].Actions[0].Type != d.CurationActionBudgetChange || a.Question.Options[0].Decisions[0].ActionIDs[0] != stepID {
					return fmt.Errorf("choice lost: %+v", a)
				}
			} else if got.Status != "FAILED" || a.Status != "FAILED" || a.ReasonCode != "AUTO_INTERPRETATION_INTERRUPTED" {
				return fmt.Errorf("inflight call would repeat: %+v", got)
			}
			var duplicate bool
			if e = q.QueryRowContext(tx, `SELECT data ? 'actions' OR data ? 'steps' FROM curation_threads WHERE id=$1`, id).Scan(&duplicate); e != nil {
				return e
			}
			if duplicate {
				return errors.New("duplicate execution state in Thread")
			}
			return rollback
		})
		if !errors.Is(e, rollback) {
			t.Fatal(e)
		}
	}
}
func TestInterpretationRetryKeepsInputAndFencesOldAttempt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, ctx)
	f.repository.EnableThreads()
	repo := cp.NewRepository(f.database, nil)
	id, aid := f.repository.ids.NewID(), f.repository.ids.NewID()
	thread := d.CurationThread{SchemaVersion: d.ThreadSchema, ID: id, UserID: f.userID, CurationID: f.job.CurationID, PlanID: f.job.PlanID, Mode: "AUTO", Origin: "REQUEST", Request: "synthetic budget change", RequestHash: id, Revision: 1, ExpectedCurationVersion: 1, Status: "INTERPRETING", Actions: []d.CurationAction{{ID: aid, Type: d.CurationActionAutoStart, Instruction: "synthetic budget change", Status: "RUNNING", InputRevision: 2, Answers: []d.ThreadAnswer{{Text: "total", Revision: 1}}}}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	job := f.job
	job.CurationActionID = aid
	job.ExecutionActionID = aid
	job.Target = intelligence.JobTarget{Kind: intelligence.TargetActionInterpretation, ID: aid, Revision: 2}
	if e := f.database.WithinTransaction(ctx, func(tx context.Context) error {
		if e := repo.InsertThread(tx, thread); e != nil {
			return e
		}
		return f.repository.InsertJob(c.WithThreadExecution(tx, id, aid), job)
	}); e != nil {
		t.Fatal(e)
	}
	claimed, e := f.repository.ClaimPending(ctx, intelligence.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, time.Now())
	if e != nil || len(claimed) != 1 {
		t.Fatalf("claim %+v %v", claimed, e)
	}
	oldAttempt := claimed[0].Attempt
	closed := oldAttempt
	closed.Status = intelligence.AttemptFailed
	now := time.Now().UTC()
	closed.CompletedAt = &now
	if e = f.repository.CloseAttempt(ctx, closed); e != nil {
		t.Fatal(e)
	}
	if e = f.repository.CloseJob(ctx, job.ID, intelligence.JobFailed, "PROVIDER_RESPONSE_INVALID", true, time.Now()); e != nil {
		t.Fatal(e)
	}
	thread.Actions[0].Status = "FAILED"
	thread.Status = "FAILED"
	thread.Actions[0].Jobs, e = repo.ActionJobs(ctx, id, aid)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.SaveThread(ctx, thread); e != nil {
		t.Fatal(e)
	}
	if e = f.repository.ReopenJob(ctx, job.ID, nil, time.Now()); e != nil {
		t.Fatal(e)
	}
	execution, e := f.repository.ThreadExecutionContext(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := repo.ReadThread(ctx, f.userID, c.ThreadExecutionFrom(execution).ThreadID)
	if e != nil {
		t.Fatal(e)
	}
	a := fresh.Actions[0]
	if a.Type != d.CurationActionAutoStart || a.InputRevision != 2 || len(a.Answers) != 1 || a.RetryOfActionID != aid {
		t.Fatalf("lost interpretation input %+v", a)
	}
	claimed, e = f.repository.ClaimPending(ctx, intelligence.ProviderManaged, intelligenceapp.DefaultClaimPolicy(), time.Minute, time.Now())
	if e != nil || len(claimed) != 1 {
		t.Fatalf("retry claim %+v %v", claimed, e)
	}
	current := claimed[0]
	valid, e := f.repository.ThreadExecutionContext(ctx, job.ID, current.Attempt.ID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.GuardThreadMutation(valid, f.userID, job.CurationID); e != nil {
		t.Fatal(e)
	}
	late := c.WithActionAttempt(c.WithThreadExecution(ctx, fresh.ID, a.ID), job.ID, oldAttempt.ID)
	if e = repo.GuardThreadMutation(late, f.userID, job.CurationID); e == nil {
		t.Fatal("old attempt admitted into new Action")
	}
	reread, e := f.repository.GetJob(ctx, f.userID, job.ID)
	if e != nil || reread.CurationActionID != aid || reread.ExecutionActionID != a.ID {
		t.Fatalf("provenance %+v %v", reread, e)
	}
}

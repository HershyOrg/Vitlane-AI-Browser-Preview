package postgres_test

import (
	"context"
	"encoding/json"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountpg "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	cv "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	pg "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	planningpg "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	sessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sessionpg "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbpg "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestConversationPostgresAdmissionAndMessageLifetime(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := dbpg.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	// Shared integration fixtures truncate users; hold their standard lock.
	lock, err := db.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = lock.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests',0))`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests',0))`)
	ids := shared.UUIDGenerator{}
	clock := shared.SystemClock{}
	user, err := accountapp.NewService(accountpg.NewRepository(db), clock, ids).CreateDevelopmentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := pg.NewRepository(db, planningpg.NewRepository(db))
	plans := c.NewService(repo, sessionapp.NewService(sessionpg.NewRepository(db), clock, ids), db, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created, err := plans.CreatePlan(ctx, c.CreatePlanInput{UserID: string(user.ID), AuthSessionID: ids.NewID(), OriginalIntent: "독서용 조명", PlanningMode: "SINGLE", ExecutionMode: "EXPERIMENT", TotalBudget: c.MoneyInput{Amount: "100", Currency: "USD"}, Country: "KR", City: "서울", Category: "lighting", URLMode: "NONE", AgentMode: "MANAGED", ModelKey: "gpt-5-nano", IdempotencyKey: ids.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	uid, cid := string(user.ID), string(created.Curation.ID)
	read := func() cv.Conversation {
		out, e := repo.Read(ctx, uid, cid)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	initial := read()
	if len(initial.Requests) != 1 {
		t.Fatalf("initial requests %+v", initial)
	}
	responseID := initial.Requests[0].ID
	seed := func(kind string) string {
		mid := ids.NewID()
		body, _ := json.Marshal(d.FollowUpContent{Code: "RESEARCH_FAILED"})
		_, e := db.DB.ExecContext(ctx, `INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content) VALUES($1,$2,$3,$4,$5,'PENDING',$6::jsonb)`, mid, uid, cid, responseID, kind, string(body))
		if e != nil {
			t.Fatal(e)
		}
		return mid
	}
	first, second := seed("ERROR"), seed("NOTICE")
	service := &cv.Service{Repository: repo, Tx: db}
	ack := cv.ResponseInput{UserID: uid, CurationID: cid, MessageID: first, Response: "ACKNOWLEDGE", ExpectedVersion: 1, ClientRequestID: ids.NewID()}
	if err = service.Respond(ctx, ack); err != nil {
		t.Fatal(err)
	}
	if err = service.Respond(ctx, ack); err != nil {
		t.Fatal("idempotent ack", err)
	}
	out := read()
	pending := 0
	for _, m := range out.Messages {
		if m.Status == "PENDING" {
			pending++
			if m.ID != second {
				t.Fatal("wrong pending")
			}
		}
	}
	if pending != 1 {
		t.Fatal("ack closed other messages")
	}
	changed := ack
	changed.Response = "DISMISS"
	if err = service.Respond(ctx, changed); err == nil {
		t.Fatal("replayed a different response")
	}
	// Invalid admission rolls back superseding and request insertion together.
	rollback := ids.NewID()
	_ = db.WithinTransaction(ctx, func(tx context.Context) error {
		_, e := repo.AdmitManual(tx, cv.RequestInput{UserID: uid, CurationID: cid, ClientRequestID: rollback, Mode: "RESEARCH_AGAIN"})
		if e != nil {
			t.Fatal(e)
		}
		return io.EOF
	})
	if len(read().Requests) != 1 {
		t.Fatal("rolled-back intake survived")
	}
	// Two devices resolve concurrently: exactly one reservation is admitted.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- db.WithinTransaction(ctx, func(tx context.Context) error {
				_, e := repo.AdmitManual(tx, cv.RequestInput{UserID: uid, CurationID: cid, ClientRequestID: ids.NewID(), Mode: "AUTO", Request: "new request"})
				return e
			})
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("admitted %d concurrent requests", success)
	}
	out = read()
	for _, m := range out.Messages {
		if m.Status == "PENDING" {
			t.Fatal("pending survived intake")
		}
	}
	if err = service.Respond(ctx, cv.ResponseInput{UserID: uid, CurationID: cid, MessageID: second, Response: "ACKNOWLEDGE", ExpectedVersion: 1, ClientRequestID: ids.NewID()}); err == nil {
		t.Fatal("old device action accepted")
	}
	// A completed no-action request ID must never become a legacy exact action.
	last := out.Requests[len(out.Requests)-1].ID
	if _, err = db.DB.ExecContext(ctx, `UPDATE curation_conversation_requests SET status='COMPLETE',result_code='NO_ACTION' WHERE id=$1`, last); err != nil {
		t.Fatal(err)
	}
	if err = repo.PrepareConversationAction(ctx, c.RecordCurationActionInput{ActionID: last, UserID: uid, CurationID: cid, Type: d.CurationActionCurationAddTargets}); err == nil {
		t.Fatal("completed request ID was reused for an action")
	}

	if _, err = repo.Read(ctx, ids.NewID(), cid); err == nil {
		t.Fatal("foreign owner read")
	}
	// Response publication and a new intake both touch the previous request.
	// Their shared Curation-first lock order must preserve one response per request.
	for n := 0; n < 5; n++ {
		results := make(chan error, 2)
		go func() { _, e := repo.ClaimResponse(ctx); results <- e }()
		go func() {
			results <- db.WithinTransaction(ctx, func(tx context.Context) error {
				rid := ids.NewID()
				if _, e := repo.AdmitManual(tx, cv.RequestInput{UserID: uid, CurationID: cid, ClientRequestID: rid, Mode: "AUTO", Request: "synthetic input"}); e != nil {
					return e
				}
				_, e := db.Queryer(tx).ExecContext(tx, `UPDATE curation_conversation_requests SET status='COMPLETE',result_code='NO_ACTION' WHERE id=$1`, rid)
				return e
			})
		}()
		for k := 0; k < 2; k++ {
			if e := <-results; e != nil {
				t.Fatal("publication/intake race", e)
			}
		}
	}

}

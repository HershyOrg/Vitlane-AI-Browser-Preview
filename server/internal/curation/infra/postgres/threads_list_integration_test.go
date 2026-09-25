package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	pp "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	sa "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sp "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbp "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// threadStatementCounter records every statement pgx sends on the connections
// of one test database handle.
type threadStatementCounter struct {
	mu         sync.Mutex
	statements []string
}

func (s *threadStatementCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, data.SQL)
	return ctx
}

func (s *threadStatementCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (s *threadStatementCounter) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.statements
	s.statements = nil
	return out
}

// The Web restores a Curation's mode and request Threads from one list read,
// and the Thread worker polls pending Threads every second. Both reads must cost
// the same number of statements for one Thread or many (ADR-0081), and return
// exactly what a single Thread read returns.
func TestCurationThreadListReadsAllActionsInOneStatement(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database, err := dbp.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	conn, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests',0))`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests',0))`)
	if _, err = database.DB.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}
	seedCurationThreadFixtureRows(t, ctx, database)
	user, curation, archived, target := "50000000-0000-4000-8000-000000000001", "52000000-0000-4000-8000-000000000002", "52000000-0000-4000-8000-000000000003", "53000000-0000-4000-8000-000000000001"
	if _, err = database.DB.ExecContext(ctx, `INSERT INTO curation_budgets(curation_id,enabled,currency,version,research_version,allocations,initial_materialized) VALUES($1,true,'USD',1,1,jsonb_build_array(jsonb_build_object('targetId',$2::text,'quantity',1,'amount','200.00')),true)`, curation, target); err != nil {
		t.Fatal(err)
	}

	ids := shared.UUIDGenerator{}
	clock := shared.SystemClock{}
	repo := NewRepository(database, pp.NewRepository(database))
	core := c.NewService(repo, sa.NewService(sp.NewRepository(database), clock, ids), database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service := c.NewThreadService(core, repo, &threadTestInterpreter{}, &threadTestPrimitives{})

	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	counter := &threadStatementCounter{}
	config.Tracer = counter
	name := stdlib.RegisterConnConfig(config)
	defer stdlib.UnregisterConnConfig(name)
	counted, err := dbp.Open(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	defer counted.Close()
	countedRepo := NewRepository(counted, pp.NewRepository(counted))

	list := func(id string) (d.ControlMode, []d.CurationThread, []string) {
		t.Helper()
		counter.take()
		mode, threads, e := countedRepo.ListThreads(ctx, user, id)
		if e != nil {
			t.Fatal(e)
		}
		return mode, threads, counter.take()
	}
	pending := func() ([]d.CurationThread, []string) {
		t.Helper()
		counter.take()
		threads, e := countedRepo.PendingThreads(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return threads, counter.take()
	}
	expectStatements := func(label string, got []string, fragments ...string) {
		t.Helper()
		if len(got) != len(fragments) {
			t.Fatalf("%s: %d statements, want %d: %q", label, len(got), len(fragments), got)
		}
		for n, fragment := range fragments {
			if !strings.Contains(got[n], fragment) {
				t.Fatalf("%s: statement %d = %q, want it to read %s", label, n, got[n], fragment)
			}
		}
	}
	sameAsSingleRead := func(label string, got d.CurationThread) {
		t.Helper()
		want, e := repo.ReadThread(ctx, user, got.ID)
		if e != nil {
			t.Fatal(e)
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) || got.AuthSessionID != want.AuthSessionID {
			t.Fatalf("%s: list read differs from single read\nlist:   %s\nsingle: %s", label, gotJSON, wantJSON)
		}
	}
	submit := func(input c.SubmitThreadInput) d.CurationThread {
		t.Helper()
		input.ID = ids.NewID()
		input.ExpectedCurationVersion = 2
		out, e := service.Submit(ctx, user, "auth-session-1", curation, input)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	stop := func(thread d.CurationThread) {
		t.Helper()
		if _, e := service.Cancel(ctx, user, curation, thread.ID); e != nil {
			t.Fatal(e)
		}
	}

	mode, threads, statements := list(curation)
	if mode != (d.ControlMode{Mode: "AUTO", Version: 1}) || len(threads) != 0 {
		t.Fatalf("empty list mode=%+v threads=%d", mode, len(threads))
	}
	expectStatements("empty list", statements, "curation_control_modes", "FROM curation_threads")

	first := submit(c.SubmitThreadInput{Request: "의자 예산을 바꿔줘"})
	_, threads, statements = list(curation)
	if len(threads) != 1 || threads[0].ID != first.ID || len(threads[0].Actions) != 1 {
		t.Fatalf("one thread list=%+v", threads)
	}
	expectStatements("one thread", statements, "curation_control_modes", "FROM curation_threads", "FROM curation_actions")
	sameAsSingleRead("one thread", threads[0])
	waiting, statements := pending()
	if len(waiting) != 1 || waiting[0].ID != first.ID {
		t.Fatalf("pending=%+v", waiting)
	}
	expectStatements("pending", statements, "FROM curation_threads", "FROM curation_actions")
	sameAsSingleRead("pending", waiting[0])
	stop(first)

	// A manual research-again with feedback owns two Actions, so grouping by
	// thread and ordering by sequence are both exercised.
	second := submit(c.SubmitThreadInput{Kind: "RESEARCH_AGAIN", TargetID: target, Request: "더 조용한 의자"})
	if len(second.Actions) != 2 {
		t.Fatalf("manual research-again actions=%d", len(second.Actions))
	}
	stop(second)
	third := submit(c.SubmitThreadInput{Request: "다른 의자도 찾아줘"})

	_, threads, statements = list(curation)
	if len(threads) != 3 || threads[0].ID != third.ID || threads[1].ID != second.ID || threads[2].ID != first.ID {
		t.Fatalf("three threads out of order: %+v", threads)
	}
	expectStatements("three threads", statements, "curation_control_modes", "FROM curation_threads", "FROM curation_actions")
	for _, thread := range threads {
		sameAsSingleRead("three threads", thread)
	}
	if threads[1].Actions[0].Type != d.CurationActionCriteriaChange || threads[1].Status != "CANCELLED" {
		t.Fatalf("manual thread=%+v", threads[1])
	}

	waiting, statements = pending()
	if len(waiting) != 1 || waiting[0].ID != third.ID {
		t.Fatalf("pending after cancels=%+v", waiting)
	}
	expectStatements("pending after cancels", statements, "FROM curation_threads", "FROM curation_actions")
	stop(third)
	waiting, statements = pending()
	if len(waiting) != 0 {
		t.Fatalf("nothing should be pending: %+v", waiting)
	}
	expectStatements("nothing pending", statements, "FROM curation_threads")

	if _, e := service.ChangeMode(ctx, user, curation, d.ControlMode{Mode: "MANUAL", Version: 1}); e != nil {
		t.Fatal(e)
	}
	if mode, _, _ = list(curation); mode != (d.ControlMode{Mode: "MANUAL", Version: 2}) {
		t.Fatalf("list mode after change=%+v", mode)
	}

	counter.take()
	if _, _, e := countedRepo.ListThreads(ctx, user, archived); !errors.Is(e, d.ErrCurationNotFound) {
		t.Fatalf("archived curation list error=%v", e)
	}
	expectStatements("archived curation", counter.take(), "curation_control_modes")
}

package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestCurationListStatementBoundsFollowTheDirection(t *testing.T) {
	t.Parallel()

	cursor := &curationapp.CurationListCursor{
		CreatedAt:  time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC),
		CurationID: "52000000-0000-4000-8000-000000000001",
	}
	for _, test := range []struct {
		name       string
		query      curationapp.CurationListQuery
		bound      string
		argsLength int
	}{
		{name: "first page", query: curationapp.CurationListQuery{}, argsLength: 2},
		{name: "before", query: curationapp.CurationListQuery{Before: cursor}, bound: "(curation.created_at, curation.id) < ($2, $3::uuid)", argsLength: 4},
		{name: "after", query: curationapp.CurationListQuery{After: cursor}, bound: "(curation.created_at, curation.id) > ($2, $3::uuid)", argsLength: 4},
	} {
		statement, args := curationListStatement("user-1", test.query, 20)
		if len(args) != test.argsLength || args[len(args)-1] != 21 ||
			!strings.Contains(statement, "ORDER BY curation.created_at DESC, curation.id DESC") ||
			!strings.Contains(statement, "curation.archived_at IS NULL") {
			t.Fatalf("%s: statement=%s args=%#v", test.name, statement, args)
		}
		if (test.bound == "") != !strings.Contains(statement, "(curation.created_at, curation.id)") ||
			(test.bound != "" && !strings.Contains(statement, test.bound)) {
			t.Fatalf("%s: bound missing or unexpected in %s", test.name, statement)
		}
		// The sidebar reads the title only; Targets, Sessions and Jobs stay untouched.
		for _, table := range []string{"plan_targets", "shopping_sessions", "intelligence_jobs"} {
			if strings.Contains(statement, table) {
				t.Fatalf("%s: statement joins %s", test.name, table)
			}
		}
	}
}

func TestPostgresCurationListPagesByCreationTimeWithOneIndex(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_lock(
		hashtextextended('vitlane.integration_tests', 0)
	)`); err != nil {
		t.Fatal(err)
	}
	defer lockConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(
		hashtextextended('vitlane.integration_tests', 0)
	)`)
	if _, err := database.DB.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	seedCurationListUser(t, ctx, database, curationListUserID)
	seedCurationListUser(t, ctx, database, curationListOtherUserID)
	// Rows 3 and 4 share one creation instant, so only the ID orders them, and
	// with two rows a page that pair straddles the first page boundary.
	seedCurationListRow(t, ctx, database, curationListUserID, 1, base.Add(1*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 2, base.Add(2*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 3, base.Add(3*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 4, base.Add(3*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 5, base.Add(4*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 6, base.Add(5*time.Hour), true)
	seedCurationListRow(t, ctx, database, curationListOtherUserID, 7, base.Add(6*time.Hour), false)

	repository := NewRepository(database, nil)
	read := func(query curationapp.CurationListQuery) curationapp.CurationListRepositoryPage {
		t.Helper()
		page, err := repository.ListCurations(ctx, curationListUserID, query, 2)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	cursorOf := func(item curationapp.CurationListItem) *curationapp.CurationListCursor {
		return &curationapp.CurationListCursor{CreatedAt: item.CreatedAt, CurationID: item.CurationID}
	}

	// Walking "more" visits every active row of this user once, newest first,
	// splitting the tied pair across a page boundary without losing either.
	var walked []string
	page := read(curationapp.CurationListQuery{})
	for {
		for _, item := range page.Items {
			walked = append(walked, item.CurationID)
		}
		if !page.More {
			break
		}
		page = read(curationapp.CurationListQuery{Before: cursorOf(page.Items[len(page.Items)-1])})
	}
	want := []string{curationListID(5), curationListID(4), curationListID(3), curationListID(2), curationListID(1)}
	if strings.Join(walked, ",") != strings.Join(want, ",") {
		t.Fatalf("walked=%v want=%v", walked, want)
	}
	first := read(curationapp.CurationListQuery{})
	if first.Items[0].IntentSummary != "사이드바 제목 5" {
		t.Fatalf("title=%q", first.Items[0].IntentSummary)
	}
	newest := cursorOf(first.Items[0])

	// Returning to the tab with nothing new costs an empty page.
	if nothing := read(curationapp.CurationListQuery{After: newest}); len(nothing.Items) != 0 || nothing.More {
		t.Fatalf("nothing new=%#v", nothing)
	}
	// A Curation made in another tab comes back alone.
	seedCurationListRow(t, ctx, database, curationListUserID, 8, base.Add(7*time.Hour), false)
	if one := read(curationapp.CurationListQuery{After: newest}); len(one.Items) != 1 || one.More ||
		one.Items[0].CurationID != curationListID(8) {
		t.Fatalf("one new=%#v", one)
	}
	// More new Curations than a page holds come back as the newest page with More set.
	seedCurationListRow(t, ctx, database, curationListUserID, 9, base.Add(8*time.Hour), false)
	seedCurationListRow(t, ctx, database, curationListUserID, 10, base.Add(9*time.Hour), false)
	if many := read(curationapp.CurationListQuery{After: newest}); len(many.Items) != 2 || !many.More ||
		many.Items[0].CurationID != curationListID(10) || many.Items[1].CurationID != curationListID(9) {
		t.Fatalf("many new=%#v", many)
	}

	// Every direction walks the partial index instead of reading and sorting all rows.
	for name, query := range map[string]curationapp.CurationListQuery{
		"first": {}, "before": {Before: newest}, "after": {After: newest},
	} {
		statement, args := curationListStatement(curationListUserID, query, 20)
		plan := explainWithoutSequentialScans(t, ctx, database, statement, args)
		if !strings.Contains(plan, "curations_user_active_created_idx") || strings.Contains(plan, "Sort Key") ||
			strings.Contains(plan, "Filter:") {
			t.Fatalf("%s plan does not walk the sidebar index:\n%s", name, plan)
		}
		// The cursor bound must start the index scan, not filter rows after it,
		// or a deep "more" page would still read every newer row first.
		if name != "first" && !strings.Contains(plan, "Index Cond: ((user_id = ") {
			t.Fatalf("%s cursor is not an index condition:\n%s", name, plan)
		}
	}
}

const (
	curationListUserID      = "50000000-0000-4000-8000-000000000001"
	curationListOtherUserID = "50000000-0000-4000-8000-000000000099"
)

func curationListID(number int) string {
	return fmt.Sprintf("52000000-0000-4000-8000-%012d", number)
}

func seedCurationListUser(t *testing.T, ctx context.Context, database *sharedpostgres.Database, userID string) {
	t.Helper()
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO users(id,status,created_at,updated_at)
		VALUES ($1,'ACTIVE','2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, userID); err != nil {
		t.Fatal(err)
	}
}

func seedCurationListRow(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	userID string,
	number int,
	createdAt time.Time,
	archived bool,
) {
	t.Helper()
	planID := fmt.Sprintf("51000000-0000-4000-8000-%012d", number)
	var archivedAt any
	var archivedBy any
	if archived {
		archivedAt, archivedBy = createdAt.Add(time.Minute), userID
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,created_at,
			agent_mode,model_key
		) VALUES ($1,$2,$3,'SINGLE','EXPERIMENT',100,'USD','KR','서울',$4,'MANAGED','gpt-5.6-luna')`,
		planID, userID, fmt.Sprintf("사이드바 제목 %d", number), createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO curations(
			user_id,version,created_at,updated_at,id,shopping_plan_id,
			phase,archived_at,archived_by_user_id
		) VALUES ($1,1,$2,$2,$3,$4,'PLANNING',$5,$6)`,
		userID, createdAt, curationListID(number), planID, archivedAt, archivedBy,
	); err != nil {
		t.Fatal(err)
	}
}

// explainWithoutSequentialScans shows whether the planner can serve a
// statement from an index at all. A handful of seeded rows would otherwise make
// a sequential scan the cheaper plan and hide a missing or unusable index.
func explainWithoutSequentialScans(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	statement string,
	args []any,
) string {
	t.Helper()
	transaction, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := transaction.QueryContext(ctx, "EXPLAIN "+statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

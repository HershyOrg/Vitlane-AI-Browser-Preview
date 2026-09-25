package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestPostgresCurationTimelineProjectionQuery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(
		ctx,
		"../../../../migrations",
	); err != nil {
		t.Fatal(err)
	}
	err = database.WithinTransaction(
		ctx,
		func(txContext context.Context) error {
			createCurationTimelineProjectionTables(
				t,
				txContext,
				database,
			)
			seedCurationTimelineProjection(
				t,
				txContext,
				database,
			)
			items, readErr := curationpostgres.NewRepository(
				database, nil,
			).ListCurationTimeline(
				txContext,
				"e1000000-0000-4000-8000-000000000001",
				"e2000000-0000-4000-8000-000000000001",
				10,
			)
			if readErr != nil {
				return readErr
			}
			assertCurationTimelineProjection(t, items)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func createCurationTimelineProjectionTables(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
) {
	t.Helper()
	statements := []string{
		`CREATE TEMP TABLE curation_actions (
			id uuid, curation_id uuid, actor_user_id uuid,
			action_type text, phase_at_request text,
			requested_transition_to text, subject_type text,
			subject_id text, effect_kind text, source_ref_type text,
			source_ref_id text, expected_curation_version bigint,
			created_at timestamptz
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE shopping_plans (
			id uuid, user_id uuid, original_intent text,
			created_at timestamptz
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE curation_runs (
			id uuid, curation_id uuid, user_id uuid,
			instruction text, status text, completed_at timestamptz,
			updated_at timestamptz, created_at timestamptz,
			idempotency_key uuid
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE plan_targets (
			id uuid, user_id uuid, curation_id uuid,
			created_by_curation_run_id uuid, title text,
			order_index integer
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE intelligence_jobs (
			id uuid, user_id uuid, curation_action_id uuid,
			research_round_id uuid
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE research_rounds (
			id uuid, user_id uuid, shopping_session_id uuid,
			status text, result_submission_id uuid,
			completed_at timestamptz, created_at timestamptz
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE shopping_sessions (
			id uuid, user_id uuid, plan_target_id uuid
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE research_feedback (
			feedback text, next_round_id uuid,
			shopping_session_id uuid, user_id uuid,
			client_request_id uuid, created_at timestamptz
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE candidates (
			research_submission_id uuid, name text,
			order_index integer
		) ON COMMIT DROP`,
	}
	for _, statement := range statements {
		if _, err := database.Queryer(ctx).ExecContext(
			ctx,
			statement,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func seedCurationTimelineProjection(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
) {
	t.Helper()
	if _, err := database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO shopping_plans
			(id,user_id,original_intent,created_at)
		VALUES (
			'f1000000-0000-4000-8000-000000000001',
			'e1000000-0000-4000-8000-000000000001',
			'집중할 수 있는 업무 공간',
			'2026-07-31T00:00:00Z'
		);
		INSERT INTO curation_runs
			(id,curation_id,user_id,instruction,status,
			 completed_at,updated_at,created_at,idempotency_key)
		VALUES (
			'f2000000-0000-4000-8000-000000000001',
			'e2000000-0000-4000-8000-000000000001',
			'e1000000-0000-4000-8000-000000000001',
			'업무용 조명도 추가해줘','COMPLETED',
			'2026-07-31T00:02:00Z','2026-07-31T00:02:00Z',
			'2026-07-31T00:01:00Z',
			'a2000000-0000-4000-8000-000000000001'
		);
		INSERT INTO plan_targets
			(id,user_id,curation_id,created_by_curation_run_id,
			 title,order_index)
		VALUES
			('b1000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 'f2000000-0000-4000-8000-000000000001',
			 '업무용 조명',1),
			('b2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 NULL,'업무용 의자',2);
		INSERT INTO shopping_sessions(id,user_id,plan_target_id)
		VALUES
			('c1000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'b1000000-0000-4000-8000-000000000001'),
			('c2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'b2000000-0000-4000-8000-000000000001');
		INSERT INTO research_rounds
			(id,user_id,shopping_session_id,status,
			 result_submission_id,completed_at,created_at)
		VALUES
			('d1000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'c1000000-0000-4000-8000-000000000001',
			 'RESULTS_READY',NULL,'2026-07-31T00:04:00Z',
			 '2026-07-31T00:03:00Z'),
			('d2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'c2000000-0000-4000-8000-000000000001',
			 'RESULTS_READY',
			 'd3000000-0000-4000-8000-000000000001',
			 '2026-07-31T00:06:00Z','2026-07-31T00:05:00Z');
		INSERT INTO intelligence_jobs
			(id,user_id,curation_action_id,research_round_id)
		VALUES (
			'f3000000-0000-4000-8000-000000000001',
			'e1000000-0000-4000-8000-000000000001',
			'a3000000-0000-4000-8000-000000000001',
			'd1000000-0000-4000-8000-000000000001'
		);
		INSERT INTO research_feedback
			(feedback,next_round_id,shopping_session_id,user_id,
			 client_request_id,created_at)
		VALUES (
			'더 가볍고 등받이가 낮은 후보',
			'd2000000-0000-4000-8000-000000000001',
			'c2000000-0000-4000-8000-000000000001',
			'e1000000-0000-4000-8000-000000000001',
			'a4000000-0000-4000-8000-000000000001',
			'2026-07-31T00:05:00Z'
		);
		INSERT INTO candidates
			(research_submission_id,name,order_index)
		VALUES
			('d3000000-0000-4000-8000-000000000001','후보 A',0),
			('d3000000-0000-4000-8000-000000000001','후보 B',1);
		INSERT INTO curation_actions
			(id,curation_id,actor_user_id,action_type,
			 phase_at_request,requested_transition_to,
			 subject_type,subject_id,effect_kind,
			 source_ref_type,source_ref_id,
			 expected_curation_version,created_at)
		VALUES
			('a1000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'INTENT_NEXT_STEP','HAVING_INTENT','PLANNING',
			 'INTENT',NULL,'NONE','SHOPPING_PLAN',
			 'f1000000-0000-4000-8000-000000000001',1,
			 '2026-07-31T00:00:00Z'),
			('a2000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'PLANNING_ADD_TARGETS','PLANNING',NULL,
			 'TARGET_LIST',NULL,'INTELLIGENCE',
			 'CURATION_RUN_REQUEST',
			 'a2000000-0000-4000-8000-000000000001',1,
			 '2026-07-31T00:01:00Z'),
			('a3000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'PLANNING_START_CURATING','PLANNING','CURATING',
			 'CURATION','e2000000-0000-4000-8000-000000000001',
			 'INTELLIGENCE','RESEARCH_START_REQUEST',
			 'a3000000-0000-4000-8000-000000000001',2,
			 '2026-07-31T00:03:00Z'),
			('a4000000-0000-4000-8000-000000000001',
			 'e2000000-0000-4000-8000-000000000001',
			 'e1000000-0000-4000-8000-000000000001',
			 'TARGET_RESEARCH_AGAIN','CURATING',NULL,
			 'TARGET','b2000000-0000-4000-8000-000000000001',
			 'INTELLIGENCE','RESEARCH_AGAIN_REQUEST',
			 'a4000000-0000-4000-8000-000000000001',3,
			 '2026-07-31T00:05:00Z');
	`); err != nil {
		t.Fatal(err)
	}
}

func assertCurationTimelineProjection(
	t *testing.T,
	items []curationapp.CurationTimelineItem,
) {
	t.Helper()
	if len(items) != 4 {
		t.Fatalf("timeline length=%d items=%#v", len(items), items)
	}
	if items[0].DisplayBody != "집중할 수 있는 업무 공간" ||
		items[0].Result == nil ||
		items[0].Result.Kind !=
			curationapp.CurationTimelineResultIntentAccepted {
		t.Fatalf("intent item=%#v", items[0])
	}
	if items[1].DisplayBody != "업무용 조명도 추가해줘" ||
		items[1].Result == nil ||
		len(items[1].Result.Diff.Added) != 1 {
		t.Fatalf("expansion item=%#v", items[1])
	}
	if items[2].DisplayBody != "" ||
		items[2].Result == nil ||
		len(items[2].Result.Diff.Changed) != 1 ||
		!strings.Contains(items[2].Result.Summary, "ResearchRound 1개") {
		t.Fatalf("research start item=%#v", items[2])
	}
	if items[3].DisplayBody !=
		"더 가볍고 등받이가 낮은 후보" ||
		items[3].Result == nil ||
		len(items[3].Result.Diff.Added) != 2 ||
		!strings.Contains(items[3].Result.Summary, "후보 2개") {
		t.Fatalf("research again item=%#v", items[3])
	}
}

package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	planningpostgres "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessionpostgres "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

var errTargetRemoveCurationSave = errors.New(
	"forced curation save failure after selection removal",
)

type failingTargetRemoveRepository struct {
	*curationpostgres.Repository
}

func (failingTargetRemoveRepository) SaveCuration(
	context.Context,
	int64,
	curationdomain.Curation,
) error {
	return errTargetRemoveCurationSave
}

func TestPostgresTargetRemoveActionIsAtomicAndReplaySafe(t *testing.T) {
	for _, inferred := range []bool{false, true} {
		name := "explicit"
		if inferred {
			name = "inferred"
		}
		t.Run(name, func(t *testing.T) { testTargetRemoveBudget(t, inferred) })
	}
}

func testTargetRemoveBudget(t *testing.T, inferred bool) {
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
	if _, err := lockConnection.ExecContext(
		ctx,
		`SELECT pg_advisory_lock(
			hashtextextended('vitlane.integration_tests', 0)
		)`,
	); err != nil {
		t.Fatal(err)
	}
	defer lockConnection.ExecContext(
		context.Background(),
		`SELECT pg_advisory_unlock(
			hashtextextended('vitlane.integration_tests', 0)
		)`,
	)
	if _, err := database.DB.ExecContext(
		ctx,
		`TRUNCATE users CASCADE`,
	); err != nil {
		t.Fatal(err)
	}

	clock := sharedapp.SystemClock{}
	ids := sharedapp.UUIDGenerator{}
	user, err := accountapp.NewService(
		accountpostgres.NewRepository(database),
		clock,
		ids,
	).CreateDevelopmentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionService := shoppingsessionapp.NewService(
		shoppingsessionpostgres.NewRepository(database),
		clock,
		ids,
	)
	planningRepository := planningpostgres.NewRepository(database)
	curationRepository := curationpostgres.NewRepository(database, planningRepository)
	service := curationapp.NewService(
		curationRepository,
		sessionService,
		database,
		clock,
		ids,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	selectionService := curationapp.NewSelectionService(
		curationRepository, clock, ids,
	)
	selectionService.EnableTransactor(database)
	selectionService.EnableSelectionMutationActions(service)
	service.EnableTargetSelectionRemover(selectionService)
	initialBudget := "100.00"
	initialRequest := &curationdomain.InitialBudgetRequest{SchemaVersion: curationdomain.BudgetSchema, Currency: "USD", TotalAmount: &initialBudget, AllocationMode: "EQUAL"}
	var resolved *curationdomain.InitialBudgetRequest
	if inferred {
		initialRequest = &curationdomain.InitialBudgetRequest{SchemaVersion: curationdomain.BudgetSchema, InputMode: "AUTO", Currency: "KRW", AllocationMode: "EQUAL"}
		resolved = &curationdomain.InitialBudgetRequest{SchemaVersion: curationdomain.BudgetSchema, InputMode: "EXPLICIT", Currency: "USD", TotalAmount: &initialBudget, AllocationMode: "EQUAL"}
	}
	planActionID := ids.NewID()
	created, err := service.CreatePlan(
		ctx,
		curationapp.CreatePlanInput{
			BudgetRequest:  initialRequest,
			UserID:         string(user.ID),
			AuthSessionID:  ids.NewID(),
			OriginalIntent: "원자적으로 제거할 독서용 조명",
			PlanningMode:   "SINGLE",
			ExecutionMode:  "EXPERIMENT",
			TotalBudget: curationapp.MoneyInput{
				Amount:   "100",
				Currency: "USD",
			},
			Country:   "KR",
			City:      "서울",
			Category:  "lighting",
			URLMode:   "NONE",
			AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
			IdempotencyKey: planActionID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The proposal needs a job as its provenance. This test is about the
	// removal transaction, not about dispatch, so the job row is seeded
	// directly rather than by standing up the intelligence service.
	planningJobID := ids.NewID()
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO intelligence_jobs(
			id, user_id, curation_id, curation_action_id, plan_id,
			target_kind, planning_task_id, provider, model_key, status,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,'PLANNING_TASK',$6,'MANAGED','gpt-5-nano',
		          'RUNNING', now(), now())
	`,
		planningJobID, string(user.ID), string(created.Curation.ID),
		planActionID, string(created.Plan.ID),
		string(created.PlanningTask.ID),
	); err != nil {
		t.Fatal(err)
	}
	// ADR-0026: the plan starts with zero Targets; the accepted INITIAL
	// proposal is what materializes the Target and its READY ShoppingSession.
	if len(created.Targets) != 0 {
		t.Fatalf("create must not materialize targets: %d", len(created.Targets))
	}
	proposalInput := curationapp.SubmitPlanningProposalInput{
		ResolvedBudget: resolved,
		UserID:         string(user.ID), PlanID: string(created.Plan.ID),
		TaskID:           string(created.PlanningTask.ID),
		ClientProposalID: ids.NewID(),
		ContextVersion:   created.PlanningTask.ContextVersion,
		ContextHash:      created.PlanningTask.ContextHash,
		SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
		Targets: []curationapp.TargetInput{{
			Quantity: 2, Title: "독서용 조명", NormalizedIntent: "원자적으로 제거할 독서용 조명",
			Category:        "lighting",
			AllocatedBudget: curationapp.MoneyInput{Amount: "60", Currency: "USD"},
			URLMode:         "NONE",
		}},
	}
	// A failure after Target + budget persistence must roll both back, including
	// the resolved denomination and requested quantity. Retry the same proposal.
	failingInitial := curationapp.NewService(failingTargetRemoveRepository{curationRepository}, sessionService, database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := failingInitial.SubmitPlanningProposalForIntelligence(ctx, string(user.ID), planningJobID, proposalInput); !errors.Is(err, errTargetRemoveCurationSave) {
		t.Fatalf("initial rollback error=%v", err)
	}
	emptyBudget, err := service.Budget(ctx, string(user.ID), string(created.Curation.ID))
	if err != nil || emptyBudget.Enabled || len(emptyBudget.Allocations) != 0 || emptyBudget.Version != 0 {
		t.Fatalf("partial initial budget=%+v err=%v", emptyBudget, err)
	}
	var partialTargets int
	if err := database.DB.QueryRowContext(ctx, `SELECT count(*) FROM plan_targets WHERE plan_id=$1`, created.Plan.ID).Scan(&partialTargets); err != nil || partialTargets != 0 {
		t.Fatalf("partial targets=%d err=%v", partialTargets, err)
	}
	proposal, err := service.SubmitPlanningProposalForIntelligence(ctx, string(user.ID), planningJobID, proposalInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.CreatedTargetIDs) != 1 ||
		len(proposal.CreatedSessionIDs) != 1 {
		t.Fatalf("INITIAL proposal did not materialize one target: %#v", proposal)
	}
	var acceptedCurationVersion int64
	if err := database.DB.QueryRowContext(
		ctx, `SELECT version FROM curations WHERE id=$1`, created.Curation.ID,
	).Scan(&acceptedCurationVersion); err != nil {
		t.Fatal(err)
	}
	curating, err := service.StartCurating(
		ctx,
		string(user.ID),
		string(created.Plan.ID),
		acceptedCurationVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	created.Plan = curating.Plan
	created.Curation = curating.Curation
	sessionID := proposal.CreatedSessionIDs[0]
	configurationID := seedTargetRemovalSelectionConfiguration(
		t,
		ctx,
		database,
		ids,
		string(user.ID),
		string(created.Plan.ID),
		string(created.Curation.ID),
		planActionID,
		sessionID,
		clock.Now(),
	)
	firstSelection, err := selectionService.CreateSelection(
		ctx,
		curationapp.CreateSelectionInput{
			UserID:                  string(user.ID),
			CurationID:              string(created.Curation.ID),
			ConfigurationID:         configurationID,
			Quantity:                1,
			ClientCommandID:         ids.NewID(),
			ExpectedCurationVersion: created.Curation.Version,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondSelection, err := selectionService.CreateSelection(
		ctx,
		curationapp.CreateSelectionInput{
			UserID:                  string(user.ID),
			CurationID:              string(created.Curation.ID),
			ConfigurationID:         configurationID,
			Quantity:                2,
			ClientCommandID:         ids.NewID(),
			ExpectedCurationVersion: created.Curation.Version,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstSelection, err = selectionService.UpdateSelection(
		ctx,
		curationapp.UpdateSelectionInput{
			UserID:                  string(user.ID),
			CurationID:              string(created.Curation.ID),
			SelectionID:             firstSelection.Selection.ID,
			ExpectedVersion:         firstSelection.Selection.Version,
			Quantity:                3,
			ConfigurationID:         configurationID,
			ClientCommandID:         ids.NewID(),
			ExpectedCurationVersion: created.Curation.Version,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeRemoval, err := selectionService.GetCurationCart(
		ctx,
		string(user.ID),
		string(created.Curation.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeRemoval.Selections) != 2 {
		t.Fatalf("cart before target removal=%#v", beforeRemoval)
	}
	var selectionCommandCountBefore int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM curation_selection_commands
		WHERE curation_id=$1
	`, created.Curation.ID).Scan(&selectionCommandCountBefore); err != nil {
		t.Fatal(err)
	}
	if selectionCommandCountBefore != 3 {
		t.Fatalf(
			"selection command count before removal=%d",
			selectionCommandCountBefore,
		)
	}
	var selectionActionCount, selectionLineageCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE source_ref_type='CURATION_SELECTION_COMMAND'
				  AND source_ref_id=id::text
			)
		FROM curation_actions
		WHERE curation_id=$1
		  AND action_type='SELECTION_MUTATION'
	`, created.Curation.ID).Scan(
		&selectionActionCount,
		&selectionLineageCount,
	); err != nil {
		t.Fatal(err)
	}
	if selectionActionCount != 3 || selectionLineageCount != 3 {
		t.Fatalf(
			"selection action count=%d lineage=%d",
			selectionActionCount,
			selectionLineageCount,
		)
	}
	targetID := proposal.CreatedTargetIDs[0]

	beforeBudget, err := service.Budget(ctx, string(user.ID), string(created.Curation.ID))
	if err != nil || !beforeBudget.Enabled || *beforeBudget.TotalAmount != "100.00" || beforeBudget.Allocations[0].Quantity != 2 {
		t.Fatalf("initial budget: %+v %v", beforeBudget, err)
	}
	editedAmount := "120.00"
	budgetCommand := curationdomain.BudgetCommand{SchemaVersion: curationdomain.BudgetSchema, CommandID: ids.NewID(), ExpectedVersion: beforeBudget.Version, Kind: "SET_TARGET", TargetID: targetID, Amount: &editedAmount, Quantity: 2}
	if _, e := service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), budgetCommand); e == nil {
		t.Fatal("active legacy job allowed budget mutation")
	}
	// Finish the manually seeded planning job before testing independent budget edits.
	if _, e := database.DB.ExecContext(ctx, `UPDATE intelligence_jobs SET status='SUCCEEDED',completed_at=now() WHERE id=$1`, planningJobID); e != nil {
		t.Fatal(e)
	}
	edited, err := service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), budgetCommand)
	if err != nil || *edited.TotalAmount != "120.00" || edited.Allocations[0].Quantity != 2 {
		t.Fatalf("budget edit %+v %v", edited, err)
	}
	replayBudget, err := service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), budgetCommand)
	if err != nil || replayBudget.Version != edited.Version {
		t.Fatal("budget replay", err)
	}
	budgetCommand.CommandID = ids.NewID()
	if _, err = service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), budgetCommand); err == nil {
		t.Fatal("stale budget edit accepted")
	}
	mismatch := "121.00"
	bad := curationdomain.BudgetCommand{SchemaVersion: curationdomain.BudgetSchema, CommandID: ids.NewID(), ExpectedVersion: edited.Version, Kind: "SET_TOTAL", TotalAmount: &mismatch, AllocationMode: "MANUAL", Allocations: []curationdomain.TargetBudget{{TargetID: targetID, Amount: &editedAmount}}}
	if _, err = service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), bad); err == nil {
		t.Fatal("partial allocation committed")
	}
	for _, currency := range []string{"KRW", "USD"} {
		amount := "156000"
		if currency == "USD" {
			amount = "120.00"
		}
		command := curationdomain.BudgetCommand{SchemaVersion: curationdomain.BudgetSchema, CommandID: ids.NewID(), ExpectedVersion: edited.Version, Kind: "SET_TOTAL", Currency: currency, TotalAmount: &amount, AllocationMode: "MANUAL", Allocations: []curationdomain.TargetBudget{{TargetID: targetID, Amount: &amount, Quantity: 3}}}
		edited, err = service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), command)
		if err != nil || edited.Currency != currency || *edited.TotalAmount != amount || edited.Allocations[0].Quantity != 3 {
			t.Fatalf("currency and quantity transaction: %+v %v", edited, err)
		}
		replayed, err := service.ChangeBudget(ctx, string(user.ID), string(created.Curation.ID), command)
		if err != nil || replayed.Version != edited.Version || replayed.Currency != currency {
			t.Fatal("currency replay", err)
		}
	}
	if _, err = service.Budget(ctx, ids.NewID(), string(created.Curation.ID)); !errors.Is(err, curationdomain.ErrPlanNotFound) {
		t.Fatal("budget ownership", err)
	}
	var targetVersion int64
	if err := database.DB.QueryRowContext(
		ctx, `SELECT version FROM plan_targets WHERE id=$1`, targetID,
	).Scan(&targetVersion); err != nil {
		t.Fatal(err)
	}

	failingService := curationapp.NewService(
		failingTargetRemoveRepository{
			Repository: curationpostgres.NewRepository(database, planningRepository),
		},
		sessionService,
		database,
		clock,
		ids,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	failingService.EnableTargetSelectionRemover(selectionService)
	failedActionID := ids.NewID()
	if _, err := failingService.ExecuteTargetRemoveAction(
		ctx,
		curationapp.ExecuteTargetRemoveActionInput{
			ActionID:                failedActionID,
			UserID:                  string(user.ID),
			CurationID:              string(created.Curation.ID),
			TargetID:                targetID,
			ExpectedCurationVersion: created.Curation.Version,
		},
	); !errors.Is(err, errTargetRemoveCurationSave) {
		t.Fatalf("forced transaction failure=%v", err)
	}
	var (
		activeSelectionsAfterFailure int
		failedActionCount            int
		targetRemovedAfterFailure    bool
	)
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM curation_selections
			 WHERE plan_target_id=$1 AND removed_at IS NULL),
			(SELECT count(*) FROM curation_actions WHERE id=$2),
			(SELECT removed_at IS NOT NULL FROM plan_targets WHERE id=$1)
	`, targetID, failedActionID).Scan(
		&activeSelectionsAfterFailure,
		&failedActionCount,
		&targetRemovedAfterFailure,
	); err != nil {
		t.Fatal(err)
	}
	if activeSelectionsAfterFailure != 2 ||
		failedActionCount != 0 ||
		targetRemovedAfterFailure {
		t.Fatalf(
			"failed TARGET_REMOVE escaped transaction: selections=%d action=%d target_removed=%v",
			activeSelectionsAfterFailure,
			failedActionCount,
			targetRemovedAfterFailure,
		)
	}

	afterFailureBudget, err := service.Budget(ctx, string(user.ID), string(created.Curation.ID))
	if err != nil || afterFailureBudget.Version != edited.Version || *afterFailureBudget.TotalAmount != "120.00" {
		t.Fatal("budget escaped target removal rollback", err)
	}
	actionID := ids.NewID()
	input := curationapp.ExecuteTargetRemoveActionInput{
		ActionID:                actionID,
		UserID:                  string(user.ID),
		CurationID:              string(created.Curation.ID),
		TargetID:                targetID,
		ExpectedCurationVersion: created.Curation.Version,
	}

	first, err := service.ExecuteTargetRemoveAction(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	afterRemovalBudget, err := service.Budget(ctx, string(user.ID), string(created.Curation.ID))
	if err != nil || !afterRemovalBudget.Enabled || *afterRemovalBudget.TotalAmount != "0.00" || len(afterRemovalBudget.Allocations) != 0 {
		t.Fatal("removed target budget remains", err)
	}
	if first.Replay ||
		first.Action.Type != curationdomain.CurationActionTargetRemove ||
		first.CurationVersion != created.Curation.Version+1 {
		t.Fatalf("first=%#v", first)
	}

	var (
		actionCount     int
		curationVersion int64
		storedTargetVer int64
		removedBy       string
		removedAt       time.Time
	)
	if err := database.DB.QueryRowContext(ctx, `
			SELECT
				(SELECT count(*) FROM curation_actions
				 WHERE id=$1::uuid
				   AND curation_id=$2::uuid
				   AND actor_user_id=$3::uuid
				   AND action_type='TARGET_REMOVE'
				   AND subject_type='TARGET'
				   AND subject_id=$4::text
				   AND source_ref_type='PLAN_TARGET'
				   AND source_ref_id=$4::text),
				(SELECT version FROM curations WHERE id=$2::uuid),
				(SELECT version FROM plan_targets WHERE id=$4::uuid),
				(SELECT removed_by_user_id::text
				 FROM plan_targets WHERE id=$4::uuid),
				(SELECT removed_at FROM plan_targets WHERE id=$4::uuid)
		`, actionID, created.Curation.ID, user.ID, targetID).Scan(
		&actionCount,
		&curationVersion,
		&storedTargetVer,
		&removedBy,
		&removedAt,
	); err != nil {
		t.Fatal(err)
	}
	if actionCount != 1 ||
		curationVersion != created.Curation.Version+1 ||
		storedTargetVer != targetVersion+1 ||
		removedBy != string(user.ID) ||
		!removedAt.Equal(first.RemovedAt) {
		t.Fatalf(
			"stored action/curation/target/remover/time=%d/%d/%d/%s/%s",
			actionCount,
			curationVersion,
			storedTargetVer,
			removedBy,
			removedAt,
		)
	}
	assertTargetRemovedSelection := func(
		selectionID string,
		expectedVersion int64,
	) {
		t.Helper()
		var (
			version          int64
			selectionRemoved time.Time
			selectionRemover string
		)
		if err := database.DB.QueryRowContext(ctx, `
			SELECT version, removed_at, removed_by_user_id::text
			FROM curation_selections
			WHERE id=$1
		`, selectionID).Scan(
			&version,
			&selectionRemoved,
			&selectionRemover,
		); err != nil {
			t.Fatal(err)
		}
		if version != expectedVersion ||
			!selectionRemoved.Equal(first.RemovedAt) ||
			selectionRemover != string(user.ID) {
			t.Fatalf(
				"selection %s version/removal/remover=%d/%s/%s",
				selectionID,
				version,
				selectionRemoved,
				selectionRemover,
			)
		}
	}
	assertTargetRemovedSelection(
		firstSelection.Selection.ID,
		firstSelection.Selection.Version+1,
	)
	assertTargetRemovedSelection(
		secondSelection.Selection.ID,
		secondSelection.Selection.Version+1,
	)
	afterRemoval, err := selectionService.GetCurationCart(
		ctx,
		string(user.ID),
		string(created.Curation.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRemoval.Selections) != 0 {
		t.Fatalf("removed selections leaked into CartView=%#v", afterRemoval)
	}
	var selectionCommandCountAfter int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM curation_selection_commands
		WHERE curation_id=$1
	`, created.Curation.ID).Scan(&selectionCommandCountAfter); err != nil {
		t.Fatal(err)
	}
	if selectionCommandCountAfter != selectionCommandCountBefore {
		t.Fatalf(
			"TARGET_REMOVE created Selection commands: before=%d after=%d",
			selectionCommandCountBefore,
			selectionCommandCountAfter,
		)
	}

	replay, err := service.ExecuteTargetRemoveAction(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay ||
		replay.Action.ID != first.Action.ID ||
		replay.CurationVersion != first.CurationVersion ||
		!replay.RemovedAt.Equal(first.RemovedAt) {
		t.Fatalf("replay=%#v first=%#v", replay, first)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM curation_actions WHERE id=$1
	`, actionID).Scan(&actionCount); err != nil {
		t.Fatal(err)
	}
	if actionCount != 1 {
		t.Fatalf("replay action count=%d", actionCount)
	}
	assertTargetRemovedSelection(
		firstSelection.Selection.ID,
		firstSelection.Selection.Version+1,
	)
	assertTargetRemovedSelection(
		secondSelection.Selection.ID,
		secondSelection.Selection.Version+1,
	)
}

func seedTargetRemovalSelectionConfiguration(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	ids sharedapp.IDGenerator,
	userID, planID, curationID, actionID, sessionID string,
	now time.Time,
) string {
	t.Helper()
	roundID := ids.NewID()
	jobID := ids.NewID()
	submissionID := ids.NewID()
	candidateID := ids.NewID()
	configurationID := ids.NewID()
	err := database.WithinTransaction(
		ctx,
		func(txContext context.Context) error {
			queryer := database.Queryer(txContext)
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO research_rounds(
					id,shopping_session_id,user_id,round_number,context_schema,
					context_version,context_hash,context_snapshot,status,
					created_at,completed_at
				) VALUES (
					$1,$2,$3,1,'vitlane.research-context.v1',1,
					'target-removal-context','{}'::jsonb,
					'RESULTS_READY',$4,$4
				)
			`, roundID, sessionID, userID, now); err != nil {
				return err
			}
			// The submission needs a job as its provenance; this helper seeds
			// a finished research result, not a dispatch.
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO intelligence_jobs(
					id,user_id,curation_id,curation_action_id,plan_id,
					target_kind,research_round_id,provider,model_key,status,
					created_at,updated_at,completed_at
				) VALUES (
					$1,$2,$3,$4,$5,'RESEARCH_ROUND',$6,'MANAGED','gpt-5-nano',
					'SUCCEEDED',$7,$7,$7
				)
			`, jobID, userID, curationID, actionID, planID, roundID, now,
			); err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO research_submissions(
					id,research_round_id,intelligence_job_id,client_submission_id,
					schema_version,context_version,context_hash,
					submission_hash,payload,outcome,validation_status,
					submitted_at
				) VALUES (
					$1,$2,$3,$4,'vitlane.research-submission.v3',1,
					'target-removal-context','target-removal-submission',
					'{}'::jsonb,'RESULTS','VALID',$5
				)
			`, submissionID, roundID, jobID, ids.NewID(), now); err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				UPDATE research_rounds
				SET result_submission_id=$1
				WHERE id=$2
			`, submissionID, roundID); err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				UPDATE shopping_sessions
				SET current_research_round_id=$1,
				    status='REVIEWING',
				    version=version+1,
				    updated_at=$2
				WHERE id=$3
			`, roundID, now, sessionID); err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO candidates(
					id,research_submission_id,shopping_session_id,
					product_url,merchant_domain,category,name,description,
					image_url,price_amount,price_currency,variant_discovery,
					evidence,observed_at,order_support,orderability,
					eligibility,candidate_hash_schema,candidate_hash,
					order_index,created_at
				) VALUES (
					$1,$2,$3,
					'https://shop.example.com/target-removal',
					'shop.example.com','lighting',
					'Target removal candidate','','',10,'USD',
					'{"schemaVersion":"vitlane.variant-discovery.v1","status":"NOT_APPLICABLE","fields":[],"providerVariantRefs":[],"observedAt":"2026-07-31T00:00:00Z","evidence":{"summary":"no options","sourceUrls":["https://shop.example.com/target-removal"]}}'::jsonb,
					'{"summary":"target removal integration","matchedCriteria":[],"tradeoffs":[],"sourceUrls":["https://shop.example.com/target-removal"]}'::jsonb,
					$4,'UNKNOWN',
					'{"schemaVersion":"vitlane.orderability.v1","providerKind":"GENERIC_WEB","executionMode":"MANUAL_MERCHANT_ORDER","externalEffect":"SIMULATED","liveOrderability":"UNVERIFIED","settlementStatus":"SUPPORTED","status":"TEST_ORDER_FLOW_AVAILABLE","reasonCodes":[]}'::jsonb,
					'{"hardChecks":"PASS","semanticReview":"REQUIRED","reasonCodes":[],"policyVersion":"test"}'::jsonb,
					'vitlane.candidate.v3',
					'target-removal-candidate',0,$4
				)
			`, candidateID, submissionID, sessionID, now); err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO candidate_configurations(
					id,shopping_session_id,candidate_id,user_id,
					schema_version,fields,selections,confirms_no_options,
					configuration_hash,created_at
				) VALUES (
					$1,$2,$3,$4,
					'vitlane.candidate-configuration.v1',
					'[]'::jsonb,'[]'::jsonb,true,
					'target-removal-configuration',$5
				)
			`, configurationID, sessionID, candidateID, userID, now); err != nil {
				return err
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return configurationID
}

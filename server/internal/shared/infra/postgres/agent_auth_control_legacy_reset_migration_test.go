package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const agentAuthControlLegacyResetMigration = "000030_agent_auth_control_legacy_reset.up.sql"

func TestAgentAuthControlLegacyResetMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(t, directory, agentAuthControlLegacyResetMigration)
	down := readMigrationContract(
		t, directory, "000030_agent_auth_control_legacy_reset.down.sql",
	)

	for _, fragment := range []string{
		"CREATE TABLE agent_auth_control_cutover_audits",
		"AGENT_AUTH_CONTROL_CUTOVER_NEW_STATE_EXISTS",
		"EXISTS (SELECT 1 FROM agent_auth_attempts)",
		"EXISTS (SELECT 1 FROM agent_work_batches)",
		"AGENT_AUTH_CONTROL_CUTOVER_RESEARCH_SESSION_MISMATCH",
		"SET status='CANCELLED'",
		"SET status=open_round.previous_round_status",
		"WHEN open_round.previous_round_id IS NULL THEN 'READY'",
		"ELSE 'REVIEWING'",
		"DELETE FROM agent_grant_requests",
		"activation_ref='revoked:' || id::text",
		"DELETE FROM oauth_access_tokens",
		"DELETE FROM oauth_refresh_tokens",
		"agent_connections_cutover_managed_or_terminal_check",
		"agent_grants_cutover_terminal_provenance_check",
		"agent_grant_sessions_cutover_released_check",
		"agent_grant_requests_cutover_empty_check CHECK (FALSE)",
		"planning_tasks_cutover_no_agent_grant_check",
		"research_rounds_cutover_no_agent_grant_check",
		"ALTER COLUMN credential_family_id SET NOT NULL",
		"ALTER COLUMN credential_epoch SET NOT NULL",
		"AGENT_AUTH_CONTROL_CUTOVER_RESULT_PRESERVATION_FAILED",
		"AGENT_AUTH_CONTROL_CUTOVER_POSTCONDITION_FAILED",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("legacy reset up migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"TRUNCATE TABLE shopping_plans",
		"DELETE FROM planning_proposals",
		"DELETE FROM research_submissions",
		"DELETE FROM candidates",
		"DELETE FROM candidate_interactions",
		"DELETE FROM candidate_interaction_events",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("legacy reset destroys preserved product data with %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"is irreversible",
		"restore the approved pre-cutover database backup",
		"RAISE EXCEPTION",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("legacy reset down migration is missing %q", fragment)
		}
	}
}

// TestAgentAuthControlLegacyResetMigration uses an isolated PostgreSQL 16
// database because the migration intentionally destroys legacy Agent
// credentials and terminalizes in-flight Product work.
func TestAgentAuthControlLegacyResetMigration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDatabase, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := isolatedCutoverDatabaseName(t)
	if _, err := adminDatabase.DB.ExecContext(
		ctx,
		"CREATE DATABASE "+quotePostgresIdentifier(databaseName)+" TEMPLATE template0",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated migration database: %v", err)
	}

	var isolatedDatabase *Database
	t.Cleanup(func() {
		if isolatedDatabase != nil {
			_ = isolatedDatabase.Close()
		}
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(), 15*time.Second,
		)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated migration database: %v", err)
		}
		_ = adminDatabase.Close()
	})

	isolatedDatabase, err = Open(
		ctx, databaseURLWithName(t, databaseURL, databaseName),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertPostgres16(t, ctx, isolatedDatabase.DB)

	directory := migrationTestDirectory(t)
	applyMigrationsOneThroughTwentyTwo(t, ctx, isolatedDatabase.DB, directory)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, directory, hardCutoverMigration,
	)
	insertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)
	insertAgentAuthControlLegacyResetFixture(t, ctx, isolatedDatabase.DB)
	for _, migration := range []string{
		"000024_agent_auth_control_prepare.up.sql",
		"000025_agent_auth_control_research_cutover.up.sql",
		"000026_agent_auth_control_planning_cutover.up.sql",
		"000027_agent_auth_attempt_cutover.up.sql",
		"000028_agent_connection_revoke_command.up.sql",
		"000029_research_rejected_schema_audit.up.sql",
	} {
		applyRecordedMigration(
			t, ctx, isolatedDatabase.DB, directory, migration,
		)
	}

	insertNewAuthAttemptCutoverBlocker(t, ctx, isolatedDatabase.DB)
	applyAgentAuthControlLegacyResetExpectFailure(
		t, ctx, isolatedDatabase.DB, directory,
		"AGENT_AUTH_CONTROL_CUTOVER_NEW_STATE_EXISTS",
	)
	assertTableExists(
		t, ctx, isolatedDatabase.DB,
		"agent_auth_control_cutover_audits", false,
	)
	assertAgentAuthControlResetNotApplied(t, ctx, isolatedDatabase.DB)
	if _, err := isolatedDatabase.DB.ExecContext(
		ctx,
		`DELETE FROM agent_auth_attempts
		 WHERE id='30000000-0000-4000-8000-000000000001'`,
	); err != nil {
		t.Fatalf("remove new AuthAttempt blocker: %v", err)
	}

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, directory,
		agentAuthControlLegacyResetMigration,
	)
	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB,
		agentAuthControlLegacyResetMigration, true,
	)
	assertAgentAuthControlLegacyReset(t, ctx, isolatedDatabase.DB)
	assertAgentAuthControlLegacyWritesRejected(
		t, ctx, isolatedDatabase.DB,
	)
	assertAgentAuthControlLegacyResetDownFails(
		t, ctx, isolatedDatabase.DB, directory,
	)
}

func insertAgentAuthControlLegacyResetFixture(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	requestHash := bytes.Repeat([]byte{0x41}, 32)
	const (
		userID            = "24000000-0000-4000-8000-000000000001"
		connectionID      = "24000000-0000-4000-8000-000000000003"
		openPlanID        = "24000000-0000-4000-8000-000000000005"
		openTaskID        = "24000000-0000-4000-8000-000000000006"
		openGrantID       = "24000000-0000-4000-8000-000000000007"
		researchPlanID    = "30000000-0000-4000-8000-000000000010"
		completedTaskID   = "30000000-0000-4000-8000-000000000011"
		completedGrantID  = "30000000-0000-4000-8000-000000000012"
		planningProposal  = "30000000-0000-4000-8000-000000000013"
		curationRunID     = "30000000-0000-4000-8000-000000000014"
		initialTargetID   = "30000000-0000-4000-8000-000000000015"
		retryTargetID     = "30000000-0000-4000-8000-000000000016"
		initialSessionID  = "30000000-0000-4000-8000-000000000017"
		retrySessionID    = "30000000-0000-4000-8000-000000000018"
		researchGrantID   = "30000000-0000-4000-8000-000000000019"
		initialRoundID    = "30000000-0000-4000-8000-000000000020"
		previousRoundID   = "30000000-0000-4000-8000-000000000021"
		retryRoundID      = "30000000-0000-4000-8000-000000000022"
		submissionID      = "30000000-0000-4000-8000-000000000023"
		candidateID       = "30000000-0000-4000-8000-000000000024"
		feedbackID        = "30000000-0000-4000-8000-000000000025"
		interactionEvent  = "30000000-0000-4000-8000-000000000026"
		openCurationRunID = "30000000-0000-4000-8000-000000000027"
	)

	if _, err := database.ExecContext(ctx, `
		INSERT INTO curations(
			plan_id,user_id,status,version,created_at,updated_at
		) VALUES ($1,$2,'OPEN',1,$3,$3);

		INSERT INTO curation_runs(
			id,curation_plan_id,user_id,kind,instruction,planning_task_id,
			status,idempotency_key,request_hash,created_at,updated_at
		) VALUES (
			$4,$1,$2,'INITIAL','legacy open planning',$5,'REQUESTED',
			'30000000-0000-4000-8000-000000000028',$6,$3,$3
		);

		INSERT INTO agent_grant_requests(
			user_id,idempotency_key,request_hash,agent_grant_id,
			created_at,completed_at
		) VALUES (
			$2,'30000000-0000-4000-8000-000000000029',$6,$7,$3,$3
		);
	`, openPlanID, userID, now, openCurationRunID, openTaskID, requestHash, openGrantID); err != nil {
		t.Fatalf("insert open Planning reset fixture: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,status,version,
			created_at,updated_at,planning_context_version,
			planning_context_hash
		) VALUES (
			$1,$2,'Preserve completed Agent results','AUTO','EXPERIMENT',
			200,'USD','US','Test City','CONFIRMED',1,$3,$3,1,
			'completed-context'
		);

		INSERT INTO planning_tasks(
			id,plan_id,user_id,context_version,context_hash,status,
			expires_at,completed_at,created_at,updated_at
		) VALUES (
			$4,$1,$2,1,'completed-context','COMPLETED',
			$3::timestamptz + INTERVAL '1 hour',$3,$3,$3
		);

		INSERT INTO agent_grants(
			id,connection_id,user_id,kind,plan_id,planning_task_id,
			scopes,activation_ref,expires_at,created_at
		) VALUES (
			$5,$6,$2,'PLANNING',$1,$4,
			ARRAY['planning:read','planning:write'],
			'legacy-completed-planning',
			$3::timestamptz + INTERVAL '1 day',$3
		);

		INSERT INTO planning_proposals(
			id,task_id,plan_id,user_id,agent_grant_id,client_proposal_id,
			context_version,context_hash,schema_version,proposal_hash,payload,
			validation_status,submitted_at
		) VALUES (
			$7,$4,$1,$2,$5,
			'30000000-0000-4000-8000-000000000030',
			1,'completed-context','vitlane.planning-proposal.v2',
			'preserved-planning-proposal','{}'::jsonb,'ACCEPTED',$3
		);

		INSERT INTO curations(
			plan_id,user_id,status,version,created_at,updated_at
		) VALUES ($1,$2,'OPEN',1,$3,$3);

		INSERT INTO curation_runs(
			id,curation_plan_id,user_id,kind,instruction,planning_task_id,
			status,idempotency_key,request_hash,created_at,updated_at
		) VALUES (
			$8,$1,$2,'INITIAL','preserved research',$4,'RESEARCHING',
			'30000000-0000-4000-8000-000000000031',$9,$3,$3
		);
	`, researchPlanID, userID, now, completedTaskID, completedGrantID,
		connectionID, planningProposal, curationRunID, requestHash); err != nil {
		t.Fatalf("insert completed Planning reset fixture: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO plan_targets(
			id,plan_id,title,normalized_intent,category,allocated_amount,
			allocated_currency,country,city,url_mode,order_index,confirmed_at,
			target_hash,target_hash_schema,version,created_at,updated_at,
			created_by_curation_run_id
		) VALUES
			(
				$1,$2,'Initial research','initial research','test',100,'USD',
				'US','Test City','NONE',0,$3,'initial-target-hash',
				'vitlane.plan-target.v1',1,$3,$3,$4
			),
			(
				$5,$2,'Research again','research again','test',100,'USD',
				'US','Test City','NONE',1,$3,'retry-target-hash',
				'vitlane.plan-target.v1',1,$3,$3,$4
			);

		INSERT INTO shopping_sessions(
			id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,
			status,version,created_at,updated_at
		) VALUES
			($6,$1,$7,'{}','{}','RESEARCHING',1,$3,$3),
			($8,$5,$7,'{}','{}','RESEARCHING',1,$3,$3);
	`, initialTargetID, researchPlanID, now, curationRunID, retryTargetID,
		initialSessionID, userID, retrySessionID); err != nil {
		t.Fatalf("insert Session reset fixture: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_grants(
			id,connection_id,user_id,kind,plan_id,scopes,activation_ref,
			expires_at,created_at
		) VALUES (
			$1,$2,$3,'RESEARCH',$4,
			ARRAY['context:read','feedback:read','research:submit'],
			'legacy-research-activation',
			$5::timestamptz + INTERVAL '1 day',$5
		);

		INSERT INTO agent_grant_sessions(
			agent_grant_id,shopping_session_id,created_at
		) VALUES ($1,$6,$5),($1,$7,$5);

		INSERT INTO agent_grant_requests(
			user_id,idempotency_key,request_hash,agent_grant_id,
			created_at,completed_at
		) VALUES (
			$3,'30000000-0000-4000-8000-000000000032',$8,$1,$5,$5
		);

		INSERT INTO research_rounds(
			id,shopping_session_id,user_id,round_number,context_schema,
			context_version,context_hash,context_snapshot,status,
			current_agent_grant_id,created_at
		) VALUES (
			$9,$6,$3,1,'vitlane.research-context.v1',1,
			'initial-context','{}'::jsonb,'REQUESTED',$1,$5
		);

		INSERT INTO research_rounds(
			id,shopping_session_id,user_id,round_number,context_schema,
			context_version,context_hash,context_snapshot,status,
			current_agent_grant_id,created_at,completed_at
		) VALUES (
			$10,$7,$3,1,'vitlane.research-context.v1',1,
			'previous-context','{}'::jsonb,'SUPERSEDED',$1,$5,$5
		);

		INSERT INTO research_rounds(
			id,shopping_session_id,user_id,round_number,context_schema,
			context_version,context_hash,context_snapshot,status,
			current_agent_grant_id,created_at
		) VALUES (
			$11,$7,$3,2,'vitlane.research-context.v1',1,
			'retry-context','{}'::jsonb,'REQUESTED',$1,$5
		);

		UPDATE shopping_sessions
		SET current_research_round_id=CASE id
			WHEN $6::uuid THEN $9::uuid
			WHEN $7::uuid THEN $11::uuid
		END
		WHERE id IN ($6::uuid,$7::uuid);
	`, researchGrantID, connectionID, userID, researchPlanID, now,
		initialSessionID, retrySessionID, requestHash, initialRoundID,
		previousRoundID, retryRoundID); err != nil {
		t.Fatalf("insert legacy Research authority fixture: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO research_submissions(
			id,research_round_id,agent_grant_id,client_submission_id,
			schema_version,context_version,context_hash,submission_hash,payload,
			outcome,validation_status,submitted_at
		) VALUES (
			$1,$2,$3,'preserved-submission-client',
			'vitlane.research-submission.v3',1,'previous-context',
			'preserved-research-submission','{}'::jsonb,
			'RESULTS','VALID',$4
		);

		UPDATE research_rounds
		SET result_submission_id=$1
		WHERE id=$2;

		INSERT INTO candidates(
			id,research_submission_id,shopping_session_id,product_url,
			merchant_domain,category,name,description,image_url,price_amount,
			price_currency,variant_discovery,evidence,observed_at,
			purchase_support,purchase_path,eligibility,candidate_hash_schema,
			candidate_hash,order_index,created_at
		) VALUES (
			$5,$1,$6,'https://example.test/preserved','example.test',
			'test','Preserved candidate','','',25,'USD',
			'{"schemaVersion":"vitlane.variant-discovery.v1","status":"NOT_APPLICABLE","fields":[],"providerVariantRefs":[],"evidence":{"summary":"none","sourceUrls":["https://example.test/preserved"]}}'::jsonb,
			'{"summary":"preserved","matchedCriteria":[],"tradeoffs":[],"sourceUrls":["https://example.test/preserved"]}'::jsonb,
			$4,'UNKNOWN',
			'{"schemaVersion":"vitlane.purchase-path.v1","providerKind":"GENERIC_WEB","executionMode":"MANUAL_MERCHANT_ORDER","externalEffect":"SIMULATED","liveOrderability":"UNVERIFIED","settlementStatus":"SUPPORTED","status":"TEST_ORDER_FLOW_AVAILABLE","reasonCodes":[]}'::jsonb,
			'{"hardChecks":"PASS","semanticReview":"REQUIRED","reasonCodes":[],"policyVersion":"test"}'::jsonb,
			'vitlane.candidate.v2','preserved-candidate-hash',0,$4
		);

		INSERT INTO candidate_interactions(
			shopping_session_id,candidate_id,user_id,pinned,sentiment,
			version,updated_at
		) VALUES ($6,$5,$7,TRUE,'LIKE',2,$4);

		INSERT INTO candidate_interaction_events(
			id,shopping_session_id,candidate_id,user_id,action,
			client_command_id,command_hash,created_at
		) VALUES (
			$8,$6,$5,$7,'LIKE',
			'30000000-0000-4000-8000-000000000033',
			'preserved-interaction-command',$4
		);

		INSERT INTO research_feedback(
			id,shopping_session_id,previous_round_id,next_round_id,user_id,
			feedback,interaction_snapshot,schema_version,feedback_version,
			feedback_hash,previous_round_status,status,client_request_id,
			request_hash,created_at
		) VALUES (
			$9,$6,$2,$10,$7,'research again',
			'{}'::jsonb,'vitlane.research-feedback.v1',1,
			'preserved-feedback','RESULTS_READY','ACTIVE',
			'30000000-0000-4000-8000-000000000034',
			'preserved-feedback-request',$4
		);
	`, submissionID, previousRoundID, researchGrantID, now, candidateID,
		retrySessionID, userID, interactionEvent, feedbackID, retryRoundID); err != nil {
		t.Fatalf("insert preserved Research result fixture: %v", err)
	}
}

func insertNewAuthAttemptCutoverBlocker(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_auth_attempts(
			id,oauth_client_id,requested_scopes,resource,issuer,state_hash,
			request_fingerprint,status,expires_at,version,created_at,updated_at
		) VALUES (
			'30000000-0000-4000-8000-000000000001',
			'24000000-0000-4000-8000-000000000002',
			ARRAY['planning:read'],'http://localhost:8080/mcp',
			'http://localhost:8080',
			decode(repeat('51',32),'hex'),decode(repeat('52',32),'hex'),
			'REQUESTED',$1::timestamptz + INTERVAL '5 minutes',1,$1,$1
		)
	`, now); err != nil {
		t.Fatalf("insert new AuthAttempt cutover blocker: %v", err)
	}
}

func applyAgentAuthControlLegacyResetExpectFailure(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory, expected string,
) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(
		directory, agentAuthControlLegacyResetMigration,
	))
	if err != nil {
		t.Fatalf("read legacy reset migration: %v", err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin rejected legacy reset migration: %v", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, string(body)); err == nil {
		t.Fatal("legacy reset unexpectedly accepted new AgentControl/AuthAttempt state")
	} else if !strings.Contains(err.Error(), expected) {
		t.Fatalf("legacy reset error=%q, want %s", err, expected)
	}
}

func assertAgentAuthControlResetNotApplied(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	var connectionStatus, taskStatus, activationRef string
	if err := database.QueryRowContext(ctx, `
		SELECT
			(SELECT status FROM agent_connections
			 WHERE id='24000000-0000-4000-8000-000000000003'),
			(SELECT status FROM planning_tasks
			 WHERE id='24000000-0000-4000-8000-000000000006'),
			(SELECT activation_ref FROM agent_grants
			 WHERE id='24000000-0000-4000-8000-000000000007')
	`).Scan(&connectionStatus, &taskStatus, &activationRef); err != nil {
		t.Fatalf("read state after rejected reset: %v", err)
	}
	if connectionStatus != "ACTIVE" || taskStatus != "REQUESTED" ||
		activationRef != "legacy-activation-ref" {
		t.Fatalf(
			"rejected reset changed state: connection=%s task=%s activation=%s",
			connectionStatus, taskStatus, activationRef,
		)
	}
}

func assertAgentAuthControlLegacyReset(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for table, query := range map[string]string{
		"active connections": `
			SELECT COUNT(*) FROM agent_connections
			WHERE status='ACTIVE' OR revoked_at IS NULL`,
		"access tokens":         `SELECT COUNT(*) FROM oauth_access_tokens`,
		"refresh tokens":        `SELECT COUNT(*) FROM oauth_refresh_tokens`,
		"active grants":         `SELECT COUNT(*) FROM agent_grants WHERE revoked_at IS NULL`,
		"active grant sessions": `SELECT COUNT(*) FROM agent_grant_sessions WHERE released_at IS NULL`,
		"grant requests":        `SELECT COUNT(*) FROM agent_grant_requests`,
		"open planning":         `SELECT COUNT(*) FROM planning_tasks WHERE status='REQUESTED'`,
		"open research":         `SELECT COUNT(*) FROM research_rounds WHERE status='REQUESTED'`,
		"planning pointers":     `SELECT COUNT(*) FROM planning_tasks WHERE current_agent_grant_id IS NOT NULL`,
		"research pointers":     `SELECT COUNT(*) FROM research_rounds WHERE current_agent_grant_id IS NOT NULL`,
	} {
		var count int
		if err := database.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s=%d want 0", table, count)
		}
	}

	var connectionStatus string
	var credentialStatus sql.NullString
	var connectionVersion int64
	if err := database.QueryRowContext(ctx, `
		SELECT status,credential_status,version
		FROM agent_connections
		WHERE id='24000000-0000-4000-8000-000000000003'
	`).Scan(&connectionStatus, &credentialStatus, &connectionVersion); err != nil {
		t.Fatalf("read reset legacy Connection: %v", err)
	}
	if connectionStatus != "REVOKED" || credentialStatus.Valid ||
		connectionVersion != 2 {
		t.Errorf(
			"legacy Connection=%s credential=%v version=%d",
			connectionStatus, credentialStatus, connectionVersion,
		)
	}

	rows, err := database.QueryContext(ctx, `
		SELECT activation_ref
		FROM agent_grants
		ORDER BY id
	`)
	if err != nil {
		t.Fatalf("list reset activation refs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var activationRef string
		if err := rows.Scan(&activationRef); err != nil {
			t.Fatalf("scan reset activation ref: %v", err)
		}
		if !strings.HasPrefix(activationRef, "revoked:") {
			t.Errorf("legacy activation ref was not tombstoned: %q", activationRef)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reset activation refs: %v", err)
	}

	var openTaskStatus, openRunStatus string
	if err := database.QueryRowContext(ctx, `
		SELECT
			(SELECT status FROM planning_tasks
			 WHERE id='24000000-0000-4000-8000-000000000006'),
			(SELECT status FROM curation_runs
			 WHERE id='30000000-0000-4000-8000-000000000027')
	`).Scan(&openTaskStatus, &openRunStatus); err != nil {
		t.Fatalf("read cancelled Planning state: %v", err)
	}
	if openTaskStatus != "CANCELLED" || openRunStatus != "CANCELLED" {
		t.Errorf("Planning reset task=%s run=%s", openTaskStatus, openRunStatus)
	}

	var initialRoundStatus, initialSessionStatus string
	var initialCurrentRound sql.NullString
	if err := database.QueryRowContext(ctx, `
		SELECT round.status,session.status,session.current_research_round_id
		FROM research_rounds round
		JOIN shopping_sessions session
		  ON session.id=round.shopping_session_id
		WHERE round.id='30000000-0000-4000-8000-000000000020'
	`).Scan(
		&initialRoundStatus, &initialSessionStatus, &initialCurrentRound,
	); err != nil {
		t.Fatalf("read cancelled initial Research state: %v", err)
	}
	if initialRoundStatus != "CANCELLED" ||
		initialSessionStatus != "READY" || initialCurrentRound.Valid {
		t.Errorf(
			"initial Research reset round=%s session=%s current=%v",
			initialRoundStatus, initialSessionStatus, initialCurrentRound,
		)
	}

	var retryRoundStatus, previousRoundStatus, retrySessionStatus string
	var retryCurrentRound, feedbackStatus, runStatus string
	if err := database.QueryRowContext(ctx, `
		SELECT
			(SELECT status FROM research_rounds
			 WHERE id='30000000-0000-4000-8000-000000000022'),
			(SELECT status FROM research_rounds
			 WHERE id='30000000-0000-4000-8000-000000000021'),
			(SELECT status FROM shopping_sessions
			 WHERE id='30000000-0000-4000-8000-000000000018'),
			(SELECT current_research_round_id::text FROM shopping_sessions
			 WHERE id='30000000-0000-4000-8000-000000000018'),
			(SELECT status FROM research_feedback
			 WHERE id='30000000-0000-4000-8000-000000000025'),
			(SELECT status FROM curation_runs
			 WHERE id='30000000-0000-4000-8000-000000000014')
	`).Scan(
		&retryRoundStatus, &previousRoundStatus, &retrySessionStatus,
		&retryCurrentRound, &feedbackStatus, &runStatus,
	); err != nil {
		t.Fatalf("read cancelled retry Research state: %v", err)
	}
	if retryRoundStatus != "CANCELLED" ||
		previousRoundStatus != "RESULTS_READY" ||
		retrySessionStatus != "REVIEWING" ||
		retryCurrentRound != "30000000-0000-4000-8000-000000000021" ||
		feedbackStatus != "CANCELLED" || runStatus != "RESEARCH_READY" {
		t.Errorf(
			"retry Research reset next=%s previous=%s session=%s current=%s feedback=%s run=%s",
			retryRoundStatus, previousRoundStatus, retrySessionStatus,
			retryCurrentRound, feedbackStatus, runStatus,
		)
	}

	for label, query := range map[string]string{
		"planning proposal": `
			SELECT COUNT(*) FROM planning_proposals
			WHERE id='30000000-0000-4000-8000-000000000013'
			  AND proposal_hash='preserved-planning-proposal'`,
		"research submission": `
			SELECT COUNT(*) FROM research_submissions
			WHERE id='30000000-0000-4000-8000-000000000023'
			  AND submission_hash='preserved-research-submission'`,
		"candidate": `
			SELECT COUNT(*) FROM candidates
			WHERE id='30000000-0000-4000-8000-000000000024'
			  AND candidate_hash='preserved-candidate-hash'`,
		"candidate interaction": `
			SELECT COUNT(*) FROM candidate_interactions
			WHERE candidate_id='30000000-0000-4000-8000-000000000024'
			  AND pinned AND sentiment='LIKE' AND version=2`,
		"candidate interaction event": `
			SELECT COUNT(*) FROM candidate_interaction_events
			WHERE id='30000000-0000-4000-8000-000000000026'
			  AND command_hash='preserved-interaction-command'`,
	} {
		var count int
		if err := database.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("read preserved %s: %v", label, err)
		}
		if count != 1 {
			t.Errorf("preserved %s count=%d want 1", label, count)
		}
	}

	var rawPreCounts, rawPostCounts, rawHashes []byte
	if err := database.QueryRowContext(ctx, `
		SELECT pre_cutover_counts,post_cutover_counts,preservation_hashes
		FROM agent_auth_control_cutover_audits
		WHERE migration_version='000030_agent_auth_control_legacy_reset'
	`).Scan(&rawPreCounts, &rawPostCounts, &rawHashes); err != nil {
		t.Fatalf("read Agent cutover audit: %v", err)
	}
	var preCounts, postCounts map[string]any
	var hashes map[string]string
	if err := json.Unmarshal(rawPreCounts, &preCounts); err != nil {
		t.Fatalf("decode Agent pre-cutover counts: %v", err)
	}
	if err := json.Unmarshal(rawPostCounts, &postCounts); err != nil {
		t.Fatalf("decode Agent post-cutover counts: %v", err)
	}
	if err := json.Unmarshal(rawHashes, &hashes); err != nil {
		t.Fatalf("decode Agent preservation hashes: %v", err)
	}
	for key, expected := range map[string]float64{
		"agentConnections":           1,
		"oauthAccessTokens":          1,
		"oauthRefreshTokens":         1,
		"agentGrants":                3,
		"activeAgentGrantSessions":   2,
		"agentGrantRequests":         2,
		"openPlanningTasks":          1,
		"openResearchRounds":         2,
		"planningProposals":          1,
		"researchSubmissions":        1,
		"candidates":                 1,
		"candidateInteractions":      1,
		"candidateInteractionEvents": 1,
	} {
		if preCounts[key] != expected {
			t.Errorf("pre-cutover %s=%v want %v", key, preCounts[key], expected)
		}
	}
	for key, value := range postCounts {
		if value != float64(0) {
			t.Errorf("post-cutover %s=%v want 0", key, value)
		}
	}
	for _, key := range []string{
		"planningProposals",
		"researchSubmissions",
		"candidates",
		"candidateInteractions",
		"candidateInteractionEvents",
	} {
		if hashes[key] == "" {
			t.Errorf("empty preservation hash for %s", key)
		}
	}
}

func assertAgentAuthControlLegacyWritesRejected(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		statement string
		arguments []any
	}{
		{
			name: "active legacy Connection",
			statement: `
				INSERT INTO agent_connections(
					id,user_id,oauth_client_id,label,authorized_scopes,status,
					authorized_at,created_at,updated_at
				) VALUES (
					'30000000-0000-4000-8000-000000000040',
					'24000000-0000-4000-8000-000000000001',
					'24000000-0000-4000-8000-000000000002',
					'forbidden legacy Connection',
					ARRAY['planning:read'],'ACTIVE',$1,$1,$1
				)`,
			arguments: []any{now},
		},
		{
			name: "active legacy AgentGrant",
			statement: `
				INSERT INTO agent_grants(
					id,connection_id,user_id,kind,plan_id,scopes,
					activation_ref,expires_at,created_at
				) VALUES (
					'30000000-0000-4000-8000-000000000041',
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000001',
					'RESEARCH',
					'30000000-0000-4000-8000-000000000010',
					ARRAY['research:submit'],'forbidden-legacy-grant',
					$1::timestamptz + INTERVAL '1 day',$1
				)`,
			arguments: []any{now},
		},
		{
			name: "unreleased legacy AgentGrantSession",
			statement: `
				INSERT INTO agent_grant_sessions(
					agent_grant_id,shopping_session_id,created_at
				) VALUES (
					'30000000-0000-4000-8000-000000000012',
					'30000000-0000-4000-8000-000000000017',$1
				)`,
			arguments: []any{now},
		},
		{
			name: "legacy AgentGrantRequest",
			statement: `
				INSERT INTO agent_grant_requests(
					user_id,idempotency_key,request_hash,agent_grant_id,
					created_at,completed_at
				) VALUES (
					'24000000-0000-4000-8000-000000000001',
					'30000000-0000-4000-8000-000000000042',
					decode(repeat('71',32),'hex'),
					'30000000-0000-4000-8000-000000000012',$1,$1
				)`,
			arguments: []any{now},
		},
		{
			name: "Planning AgentGrant pointer",
			statement: `
				UPDATE planning_tasks
				SET current_agent_grant_id=
					'30000000-0000-4000-8000-000000000012'
				WHERE id='30000000-0000-4000-8000-000000000011'`,
		},
		{
			name: "Research AgentGrant pointer",
			statement: `
				UPDATE research_rounds
				SET current_agent_grant_id=
					'30000000-0000-4000-8000-000000000019'
				WHERE id='30000000-0000-4000-8000-000000000021'`,
		},
		{
			name: "unmanaged OAuth access token",
			statement: `
				INSERT INTO oauth_access_tokens(
					token_hash,connection_id,oauth_client_id,resource,scopes,
					expires_at,created_at
				) VALUES (
					decode(repeat('72',32),'hex'),
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000002',
					'http://localhost:8080/mcp',ARRAY['planning:read'],
					$1::timestamptz + INTERVAL '1 hour',$1
				)`,
			arguments: []any{now},
		},
		{
			name: "unmanaged OAuth refresh token",
			statement: `
				INSERT INTO oauth_refresh_tokens(
					token_hash,token_family_id,connection_id,oauth_client_id,
					resource,scopes,expires_at,absolute_expires_at,created_at
				) VALUES (
					decode(repeat('73',32),'hex'),
					'30000000-0000-4000-8000-000000000043',
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000002',
					'http://localhost:8080/mcp',ARRAY['planning:read'],
					$1::timestamptz + INTERVAL '1 hour',
					$1::timestamptz + INTERVAL '1 day',$1
				)`,
			arguments: []any{now},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := database.ExecContext(
				ctx, testCase.statement, testCase.arguments...,
			); err == nil {
				t.Fatalf("cutover accepted %s", testCase.name)
			}
		})
	}
}

func assertAgentAuthControlLegacyResetDownFails(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(
		directory, "000030_agent_auth_control_legacy_reset.down.sql",
	))
	if err != nil {
		t.Fatalf("read legacy reset down migration: %v", err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin legacy reset down migration: %v", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, string(body)); err == nil {
		t.Fatal("irreversible Agent authority reset down migration succeeded")
	} else if !strings.Contains(err.Error(), "restore the approved pre-cutover") {
		t.Fatalf("unexpected Agent authority reset down error: %v", err)
	}
}

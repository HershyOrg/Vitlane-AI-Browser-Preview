package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const agentAuthControlPrepareMigration = "000024_agent_auth_control_prepare.up.sql"

func TestAgentAuthControlPrepareMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(t, directory, agentAuthControlPrepareMigration)
	down := readMigrationContract(
		t,
		directory,
		"000024_agent_auth_control_prepare.down.sql",
	)

	for _, fragment := range []string{
		"CREATE TABLE agent_credential_families",
		"CREATE TABLE agent_command_requests",
		"CREATE TABLE agent_work_batches",
		"CREATE TABLE agent_work_orders",
		"CREATE TABLE agent_delegations",
		"CREATE TABLE agent_work_attempts",
		"CREATE TABLE agent_connection_acquisition_intents",
		"DEFERRABLE INITIALLY DEFERRED",
		"agent_command_requests_web_idempotency_idx",
		"agent_command_requests_agent_idempotency_idx",
		"agent_command_requests_completion_guard",
		"agent_work_orders_open_planning_target_idx",
		"agent_work_orders_open_research_target_idx",
		"agent_delegations_current_idx",
		"agent_work_attempts_current_idx",
		"ADD COLUMN credential_family_id UUID",
		"ADD COLUMN credential_epoch BIGINT",
		"DROP CONSTRAINT agent_connections_user_id_oauth_client_id_key",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"DROP TABLE agent_grants",
		"DROP TABLE agent_grant_sessions",
		"DROP TABLE agent_grant_requests",
		"DROP COLUMN agent_grant_id",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("additive up migration contains legacy cutover operation %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"DROP TABLE IF EXISTS agent_connection_acquisition_intents",
		"DROP TABLE IF EXISTS agent_work_attempts",
		"DROP TABLE IF EXISTS agent_delegations",
		"DROP TABLE IF EXISTS agent_work_orders",
		"DROP TABLE IF EXISTS agent_work_batches",
		"DROP TABLE IF EXISTS agent_command_requests",
		"DROP TABLE IF EXISTS agent_credential_families",
		"DROP COLUMN IF EXISTS connection_generation",
		"ADD CONSTRAINT agent_connections_user_id_oauth_client_id_key",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
}

// TestAgentAuthControlPrepareUpgradeRollback uses an isolated database because
// its rollback contract drops the newly prepared control-plane tables. It
// intentionally applies migration 000023 by exact name instead of calling
// Database.Migrate, so this test keeps proving the 000023 -> 000024 boundary
// when later migrations are added.
func TestAgentAuthControlPrepareUpgradeRollback(t *testing.T) {
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
			if err := isolatedDatabase.Close(); err != nil {
				t.Errorf("close isolated migration database: %v", err)
			}
		}
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(),
			15*time.Second,
		)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated migration database: %v", err)
		}
		if err := adminDatabase.Close(); err != nil {
			t.Errorf("close migration admin database: %v", err)
		}
	})

	isolatedURL := databaseURLWithName(t, databaseURL, databaseName)
	isolatedDatabase, err = Open(ctx, isolatedURL)
	if err != nil {
		t.Fatalf("open isolated migration database: %v", err)
	}
	assertPostgres16(t, ctx, isolatedDatabase.DB)

	migrationDirectory := migrationTestDirectory(t)
	applyMigrationsOneThroughTwentyTwo(
		t,
		ctx,
		isolatedDatabase.DB,
		migrationDirectory,
	)
	applyRecordedMigration(
		t,
		ctx,
		isolatedDatabase.DB,
		migrationDirectory,
		hardCutoverMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 23)

	insertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)
	assertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)

	applyRecordedMigration(
		t,
		ctx,
		isolatedDatabase.DB,
		migrationDirectory,
		agentAuthControlPrepareMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 24)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		agentAuthControlPrepareMigration,
		true,
	)
	assertAgentAuthControlPrepareSchema(t, ctx, isolatedDatabase.DB)
	assertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)
	exerciseAgentAuthControlPrepareConstraints(t, ctx, isolatedDatabase.DB)

	applyAgentAuthControlPrepareDown(
		t,
		ctx,
		isolatedDatabase.DB,
		migrationDirectory,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 23)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		agentAuthControlPrepareMigration,
		false,
	)
	assertAgentAuthControlPrepareRolledBack(t, ctx, isolatedDatabase.DB)
	assertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)

	applyRecordedMigration(
		t,
		ctx,
		isolatedDatabase.DB,
		migrationDirectory,
		agentAuthControlPrepareMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 24)
	assertAgentAuthControlPrepareSchema(t, ctx, isolatedDatabase.DB)
	assertAgentAuthControlLegacyFixture(t, ctx, isolatedDatabase.DB)
}

func readMigrationContract(t *testing.T, directory, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}

func insertAgentAuthControlLegacyFixture(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	codeHash := bytes.Repeat([]byte{0x10}, 32)
	hash := bytes.Repeat([]byte{0x11}, 32)
	refreshHash := bytes.Repeat([]byte{0x12}, 32)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			query: `
				INSERT INTO users(
					id,status,email,display_name,created_at,updated_at
				) VALUES (
					'24000000-0000-4000-8000-000000000001',
					'ACTIVE','prepare@example.test','Prepare Legacy',$1,$1
				)
			`,
			args: []any{now},
		},
		{
			query: `
				INSERT INTO agent_oauth_clients(
					id,client_id,registration_method,client_name,redirect_uris,
					token_endpoint_auth_method,client_profile,status,created_at,updated_at
				) VALUES (
					'24000000-0000-4000-8000-000000000002',
					'prepare-client','PREDEFINED','Prepare Client',
					ARRAY['http://127.0.0.1/callback'],'none','GENERIC_MCP',
					'ACTIVE',$1,$1
				)
			`,
			args: []any{now},
		},
		{
			query: `
				INSERT INTO agent_connections(
					id,user_id,oauth_client_id,label,authorized_scopes,status,
					authorized_at,created_at,updated_at
				) VALUES (
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000001',
					'24000000-0000-4000-8000-000000000002',
					'Legacy Codex',ARRAY['planning:read','planning:write'],
					'ACTIVE',$1,$1,$1
				)
			`,
			args: []any{now},
		},
		{
			query: `
				INSERT INTO oauth_authorization_codes(
					code_hash,connection_id,oauth_client_id,redirect_uri,
					resource,scopes,pkce_challenge,expires_at,created_at
				) VALUES (
					$1,
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000002',
					'http://127.0.0.1/callback',
					'http://localhost:8080/mcp',ARRAY['planning:read'],
					'prepare-pkce-challenge',$2,$3
				)
			`,
			args: []any{codeHash, now.Add(5 * time.Minute), now},
		},
		{
			query: `
				INSERT INTO oauth_access_tokens(
					token_hash,connection_id,oauth_client_id,resource,scopes,
					expires_at,created_at
				) VALUES (
					$1,
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000002',
					'http://localhost:8080/mcp',ARRAY['planning:read'],
					$2,$3
				)
			`,
			args: []any{hash, now.Add(time.Hour), now},
		},
		{
			query: `
				INSERT INTO oauth_refresh_tokens(
					token_hash,token_family_id,connection_id,oauth_client_id,
					resource,scopes,expires_at,absolute_expires_at,created_at
				) VALUES (
					$1,
					'24000000-0000-4000-8000-000000000004',
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000002',
					'http://localhost:8080/mcp',ARRAY['planning:read'],
					$2,$3,$4
				)
			`,
			args: []any{
				refreshHash,
				now.Add(24 * time.Hour),
				now.Add(30 * 24 * time.Hour),
				now,
			},
		},
		{
			query: `
				INSERT INTO shopping_plans(
					id,user_id,original_intent,plan_mode,execution_mode,
					budget_amount,budget_currency,country,city,status,version,
					created_at,updated_at,planning_context_version,
					planning_context_hash
				) VALUES (
					'24000000-0000-4000-8000-000000000005',
					'24000000-0000-4000-8000-000000000001',
					'AgentControl prepare migration','SINGLE','EXPERIMENT',
					100,'USD','KR','Seoul','PLANNING',1,$1,$1,1,'context'
				)
			`,
			args: []any{now},
		},
		{
			query: `
				INSERT INTO planning_tasks(
					id,plan_id,user_id,context_version,context_hash,status,
					expires_at,created_at,updated_at
				) VALUES (
					'24000000-0000-4000-8000-000000000006',
					'24000000-0000-4000-8000-000000000005',
					'24000000-0000-4000-8000-000000000001',
					1,'context','REQUESTED',$1,$2,$2
				)
			`,
			args: []any{now.Add(time.Hour), now},
		},
		{
			query: `
				INSERT INTO agent_grants(
					id,connection_id,user_id,kind,plan_id,planning_task_id,
					scopes,activation_ref,expires_at,created_at
				) VALUES (
					'24000000-0000-4000-8000-000000000007',
					'24000000-0000-4000-8000-000000000003',
					'24000000-0000-4000-8000-000000000001',
					'PLANNING',
					'24000000-0000-4000-8000-000000000005',
					'24000000-0000-4000-8000-000000000006',
					ARRAY['planning:read','planning:write'],
					'legacy-activation-ref',$1,$2
				)
			`,
			args: []any{now.Add(time.Hour), now},
		},
	} {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert legacy AgentControl fixture: %v", err)
		}
	}
}

func assertAgentAuthControlLegacyFixture(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for table, expected := range map[string]int{
		"agent_connections":         1,
		"oauth_authorization_codes": 1,
		"oauth_access_tokens":       1,
		"oauth_refresh_tokens":      1,
		"agent_grants":              1,
	} {
		assertTableCount(t, ctx, database, table, expected)
	}
	assertTableExists(t, ctx, database, "agent_grant_sessions", true)
	assertTableExists(t, ctx, database, "agent_grant_requests", true)
}

func assertAgentAuthControlPrepareSchema(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"agent_credential_families",
		"agent_command_requests",
		"agent_work_batches",
		"agent_work_orders",
		"agent_delegations",
		"agent_work_attempts",
		"agent_connection_acquisition_intents",
	} {
		assertTableExists(t, ctx, database, table, true)
	}
	for _, column := range []string{
		"connection_generation",
		"resource",
		"issuer",
		"credential_status",
		"current_credential_family_id",
		"current_credential_epoch",
	} {
		assertColumnExists(t, ctx, database, "agent_connections", column, true)
		assertColumnNullable(t, ctx, database, "agent_connections", column, true)
	}
	assertColumnExists(t, ctx, database, "agent_connections", "version", true)
	assertColumnNullable(t, ctx, database, "agent_connections", "version", false)
	for _, table := range []string{
		"oauth_authorization_codes",
		"oauth_access_tokens",
		"oauth_refresh_tokens",
	} {
		assertColumnExists(t, ctx, database, table, "credential_family_id", true)
		assertColumnExists(t, ctx, database, table, "credential_epoch", true)
		assertColumnNullable(t, ctx, database, table, "credential_family_id", true)
		assertColumnNullable(t, ctx, database, table, "credential_epoch", true)
	}
	assertColumnExists(
		t,
		ctx,
		database,
		"oauth_refresh_tokens",
		"refresh_generation",
		true,
	)
	assertColumnNullable(
		t,
		ctx,
		database,
		"oauth_refresh_tokens",
		"refresh_generation",
		true,
	)
	for _, column := range []string{
		"subject_kind",
		"subject_id",
		"subject_epoch",
		"subject_generation",
	} {
		assertColumnExists(t, ctx, database, "agent_command_requests", column, true)
	}

	var generation sql.NullInt64
	var resource, issuer, credentialStatus sql.NullString
	var version int64
	if err := database.QueryRowContext(ctx, `
		SELECT connection_generation,resource,issuer,credential_status,version
		FROM agent_connections
		WHERE id='24000000-0000-4000-8000-000000000003'
	`).Scan(&generation, &resource, &issuer, &credentialStatus, &version); err != nil {
		t.Fatalf("read prepared legacy connection: %v", err)
	}
	if generation.Valid || resource.Valid || issuer.Valid || credentialStatus.Valid {
		t.Fatalf(
			"legacy connection unexpectedly backfilled: generation=%v resource=%v issuer=%v status=%v",
			generation,
			resource,
			issuer,
			credentialStatus,
		)
	}
	if version != 1 {
		t.Fatalf("legacy connection version=%d want 1", version)
	}

	var deferrable, deferred bool
	if err := database.QueryRowContext(ctx, `
		SELECT condeferrable, condeferred
		FROM pg_constraint
		WHERE conname='agent_work_orders_active_attempt_fkey'
	`).Scan(&deferrable, &deferred); err != nil {
		t.Fatalf("read active-attempt constraint: %v", err)
	}
	if !deferrable || !deferred {
		t.Fatalf(
			"active-attempt constraint deferrable/deferred=%t/%t want true/true",
			deferrable,
			deferred,
		)
	}
	assertPostgresConstraint(
		t,
		ctx,
		database,
		"agent_connections_user_id_oauth_client_id_key",
		false,
	)
}

func exerciseAgentAuthControlPrepareConstraints(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	now := time.Date(2026, 7, 30, 9, 5, 0, 0, time.UTC)
	requestHash := bytes.Repeat([]byte{0x21}, 32)
	scopeHash := strings.Repeat("22", 32)
	activationHash := bytes.Repeat([]byte{0x23}, 32)

	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_connections(
			id,user_id,oauth_client_id,label,authorized_scopes,status,
			authorized_at,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000098',
			'24000000-0000-4000-8000-000000000001',
			'24000000-0000-4000-8000-000000000002',
			'Second generation',ARRAY['planning:read'],'ACTIVE',$1,$1,$1
		)
	`, now); err != nil {
		t.Fatalf("insert second Connection generation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		DELETE FROM agent_connections
		WHERE id='24000000-0000-4000-8000-000000000098'
	`); err != nil {
		t.Fatalf("remove second Connection generation before rollback: %v", err)
	}

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin credential-family fixture: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO agent_credential_families(
			id,connection_id,epoch,status,refresh_generation,
			confirmation_deadline_at,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000004',
			'24000000-0000-4000-8000-000000000003',
			1,'UNCONFIRMED',1,$1,$2,$2
		)
	`, now.Add(30*time.Minute), now); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("insert credential family: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE agent_connections
		SET connection_generation=1,
		    resource='http://localhost:8080/mcp',
		    issuer='https://vitlane.example',
		    credential_status='UNCONFIRMED',
		    current_credential_family_id=
		        '24000000-0000-4000-8000-000000000004',
		    current_credential_epoch=1,
		    version=2,
		    updated_at=$1
		WHERE id='24000000-0000-4000-8000-000000000003'
	`, now); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("link current credential family: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE oauth_authorization_codes
		SET credential_family_id='24000000-0000-4000-8000-000000000004',
		    credential_epoch=1
		WHERE connection_id='24000000-0000-4000-8000-000000000003'
	`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("link authorization code family: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE oauth_access_tokens
		SET credential_family_id='24000000-0000-4000-8000-000000000004',
		    credential_epoch=1
		WHERE connection_id='24000000-0000-4000-8000-000000000003'
	`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("link access token family: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE oauth_refresh_tokens
		SET credential_family_id='24000000-0000-4000-8000-000000000004',
		    credential_epoch=1,
		    refresh_generation=1
		WHERE connection_id='24000000-0000-4000-8000-000000000003'
	`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("link refresh token family: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit credential family fixture: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_command_requests(
			id,actor_kind,actor_user_id,actor_auth_session_id,
			command,idempotency_key,request_hash,
			subject_kind,subject_id,subject_generation,
			response_status,response_headers_snapshot,response_ciphertext,
			response_hash,response_key_version,created_at,completed_at
		) VALUES (
			'24000000-0000-4000-8000-000000000008',
			'WEB_USER',
			'24000000-0000-4000-8000-000000000001',
			'24000000-0000-4000-8000-000000000009',
			'CREATE_AGENT_WORK','delegate-1',$1,
			'WORK_BATCH',
			'24000000-0000-4000-8000-000000000010',
			1,201,'{}'::jsonb,$2,$3,1,$4,$4
		)
	`, requestHash, []byte("encrypted-web-response"), requestHash, now); err != nil {
		t.Fatalf("insert web command reservation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_work_batches(
			id,user_id,work_kind,authorized_by_action_id,
			scope_snapshot_hash,status,activation_epoch,
			activation_ref_ciphertext,activation_ref_hash,
			secret_key_version,activation_expires_at,version,
			created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000010',
			'24000000-0000-4000-8000-000000000001',
			'PLANNING',
			'24000000-0000-4000-8000-000000000008',
			$1,'OPEN',1,$2,$3,1,$4,1,$5,$5
		)
	`,
		scopeHash,
		[]byte("encrypted-activation-ref"),
		activationHash,
		now.Add(30*time.Minute),
		now,
	); err != nil {
		t.Fatalf("insert work batch: %v", err)
	}

	transaction, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin work aggregate fixture: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO agent_work_orders(
			id,batch_id,user_id,work_kind,planning_task_id,
			scope_snapshot_hash,status,active_attempt_id,generation,
			deadline_at,version,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000011',
			'24000000-0000-4000-8000-000000000010',
			'24000000-0000-4000-8000-000000000001',
			'PLANNING',
			'24000000-0000-4000-8000-000000000006',
			$1,'OPEN',
			'24000000-0000-4000-8000-000000000013',
			1,$2,1,$3,$3
		)
	`, scopeHash, now.Add(2*time.Hour), now); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("insert work order: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO agent_delegations(
			id,work_order_id,user_id,scopes,scope_snapshot_hash,
			assignee_mode,epoch,status,expires_at,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000012',
			'24000000-0000-4000-8000-000000000011',
			'24000000-0000-4000-8000-000000000001',
			ARRAY['planning:read','planning:write'],$1,
			'CLAIMING_CONNECTION',1,'PENDING_CLAIM',$2,$3
		)
	`, scopeHash, now.Add(24*time.Hour), now); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("insert delegation: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO agent_work_attempts(
			id,work_order_id,delegation_id,generation,status,
			execution_deadline_at,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000013',
			'24000000-0000-4000-8000-000000000011',
			'24000000-0000-4000-8000-000000000012',
			1,'OFFERED',$1,$2,$2
		)
	`, now.Add(2*time.Hour), now); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("insert work attempt: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit work aggregate fixture: %v", err)
	}

	transaction, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin invalid active-attempt transaction: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE agent_work_orders
		SET active_attempt_id='24000000-0000-4000-8000-000000000099'
		WHERE id='24000000-0000-4000-8000-000000000011'
	`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("deferred active-attempt update failed before commit: %v", err)
	}
	if err := transaction.Commit(); err == nil {
		t.Fatal("invalid deferred active-attempt reference unexpectedly committed")
	}
	var activeAttemptID string
	if err := database.QueryRowContext(ctx, `
		SELECT active_attempt_id
		FROM agent_work_orders
		WHERE id='24000000-0000-4000-8000-000000000011'
	`).Scan(&activeAttemptID); err != nil {
		t.Fatalf("read active attempt after rejected commit: %v", err)
	}
	if activeAttemptID != "24000000-0000-4000-8000-000000000013" {
		t.Fatalf("active attempt changed after rejected commit: %s", activeAttemptID)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_command_requests(
			id,actor_kind,actor_user_id,command,idempotency_key,
			request_hash,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000014',
			'WEB_USER',
			'24000000-0000-4000-8000-000000000001',
			'CREATE_AGENT_WORK','delegate-1',$1,$2
		)
	`, requestHash, now); err == nil {
		t.Fatal("duplicate WEB_USER idempotency key unexpectedly succeeded")
	}

	if _, err := database.ExecContext(ctx, `
		UPDATE agent_command_requests
		SET request_hash=$1
		WHERE id='24000000-0000-4000-8000-000000000008'
	`, bytes.Repeat([]byte{0x31}, 32)); err == nil {
		t.Fatal("immutable command request hash update unexpectedly succeeded")
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_command_requests(
			id,actor_kind,actor_user_id,actor_connection_id,
			actor_credential_family_id,actor_credential_epoch,
			command,idempotency_key,request_hash,
			subject_kind,subject_id,subject_epoch,subject_generation,
			response_status,response_headers_snapshot,response_ciphertext,
			response_hash,response_key_version,created_at,completed_at
		) VALUES (
			'24000000-0000-4000-8000-000000000015',
			'AGENT_PRINCIPAL',
			'24000000-0000-4000-8000-000000000001',
			'24000000-0000-4000-8000-000000000003',
			'24000000-0000-4000-8000-000000000004',
			1,'CLAIM_AGENT_WORK','claim-1',$1,
			'WORK_ORDER',
			'24000000-0000-4000-8000-000000000011',
			1,1,200,'{}'::jsonb,$2,$3,1,$4,$4
		)
	`, requestHash, []byte("encrypted-agent-response"), requestHash, now); err != nil {
		t.Fatalf("insert AgentPrincipal command reservation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_command_requests(
			id,actor_kind,actor_user_id,actor_connection_id,
			actor_credential_family_id,actor_credential_epoch,
			command,idempotency_key,request_hash,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000016',
			'AGENT_PRINCIPAL',
			'24000000-0000-4000-8000-000000000001',
			'24000000-0000-4000-8000-000000000003',
			'24000000-0000-4000-8000-000000000004',
			1,'CLAIM_AGENT_WORK','claim-1',$1,$2
		)
	`, requestHash, now); err == nil {
		t.Fatal("duplicate AgentPrincipal idempotency key unexpectedly succeeded")
	}
}

func applyAgentAuthControlPrepareDown(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
) {
	t.Helper()
	body := readMigrationContract(
		t,
		directory,
		"000024_agent_auth_control_prepare.down.sql",
	)
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin AgentControl prepare rollback: %v", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, body); err != nil {
		t.Fatalf("apply AgentControl prepare down migration: %v", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`DELETE FROM schema_migrations WHERE version=$1`,
		agentAuthControlPrepareMigration,
	); err != nil {
		t.Fatalf("unrecord AgentControl prepare migration: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit AgentControl prepare rollback: %v", err)
	}
}

func assertAgentAuthControlPrepareRolledBack(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"agent_credential_families",
		"agent_command_requests",
		"agent_work_batches",
		"agent_work_orders",
		"agent_delegations",
		"agent_work_attempts",
		"agent_connection_acquisition_intents",
	} {
		assertTableExists(t, ctx, database, table, false)
	}
	for _, column := range []string{
		"connection_generation",
		"resource",
		"issuer",
		"credential_status",
		"current_credential_family_id",
		"current_credential_epoch",
		"version",
	} {
		assertColumnExists(t, ctx, database, "agent_connections", column, false)
	}
	for _, table := range []string{
		"oauth_authorization_codes",
		"oauth_access_tokens",
		"oauth_refresh_tokens",
	} {
		assertColumnExists(t, ctx, database, table, "credential_family_id", false)
		assertColumnExists(t, ctx, database, table, "credential_epoch", false)
	}
	assertColumnExists(
		t,
		ctx,
		database,
		"oauth_refresh_tokens",
		"refresh_generation",
		false,
	)
	assertPostgresConstraint(
		t,
		ctx,
		database,
		"agent_connections_user_id_oauth_client_id_key",
		true,
	)
}

func assertPostgresConstraint(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	name string,
	expected bool,
) {
	t.Helper()
	var actual bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM pg_constraint
			WHERE connamespace='public'::regnamespace
			  AND conname=$1
		)
	`, name).Scan(&actual); err != nil {
		t.Fatalf("read PostgreSQL constraint %s: %v", name, err)
	}
	if actual != expected {
		t.Fatalf("constraint %s exists=%t want %t", name, actual, expected)
	}
}

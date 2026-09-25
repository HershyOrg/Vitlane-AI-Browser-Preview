package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const hardCutoverMigration = "000023_intent_shopping_hard_cutover.up.sql"

// TestIntentShoppingHardCutoverUpgradeRollback runs in a database created only
// for this test. In particular, it must never truncate or otherwise mutate the
// shared database named by TEST_DATABASE_URL.
func TestIntentShoppingHardCutoverUpgradeRollback(t *testing.T) {
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
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 22)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		hardCutoverMigration,
		false,
	)

	insertLegacyCutoverFixture(t, ctx, isolatedDatabase.DB)
	assertLegacyShoppingFixturePresent(t, ctx, isolatedDatabase.DB)
	assertAccountFixturePreserved(t, ctx, isolatedDatabase.DB)

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, hardCutoverMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 23)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		hardCutoverMigration,
		true,
	)
	assertShoppingDataEmpty(t, ctx, isolatedDatabase.DB)
	assertAccountFixturePreserved(t, ctx, isolatedDatabase.DB)
	assertHardCutoverSchema(t, ctx, isolatedDatabase.DB)

	applyHardCutoverDown(t, ctx, isolatedDatabase.DB, migrationDirectory)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 22)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		hardCutoverMigration,
		false,
	)
	assertShoppingDataEmpty(t, ctx, isolatedDatabase.DB)
	assertAccountFixturePreserved(t, ctx, isolatedDatabase.DB)
	assertVersionTwentyTwoSchema(t, ctx, isolatedDatabase.DB)

	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, migrationDirectory, hardCutoverMigration,
	)
	assertRecordedMigrationCount(t, ctx, isolatedDatabase.DB, 23)
	assertMigrationRecorded(
		t,
		ctx,
		isolatedDatabase.DB,
		hardCutoverMigration,
		true,
	)
	assertShoppingDataEmpty(t, ctx, isolatedDatabase.DB)
	assertAccountFixturePreserved(t, ctx, isolatedDatabase.DB)
	assertHardCutoverSchema(t, ctx, isolatedDatabase.DB)
}

func isolatedCutoverDatabaseName(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate isolated database name: %v", err)
	}
	return "vitlane_cutover_" + hex.EncodeToString(random)
}

func databaseURLWithName(t *testing.T, databaseURL, databaseName string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("TEST_DATABASE_URL must use postgres:// or postgresql://")
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	query := parsed.Query()
	// Cutover fixtures deliberately seed a complete legacy graph with
	// parameterized multi-statement batches. pgx extended protocol rejects
	// those batches before PostgreSQL can execute them, while production
	// migrations themselves contain no parameters. Keep the fixture connection
	// on the simple protocol so the test exercises the destructive migration
	// instead of the driver's prepared-statement limitation.
	query.Set("default_query_exec_mode", "simple_protocol")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func assertPostgres16(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var version int
	if err := database.QueryRowContext(
		ctx,
		`SELECT current_setting('server_version_num')::integer`,
	).Scan(&version); err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}
	if version < 160000 || version >= 170000 {
		t.Fatalf("migration contract requires PostgreSQL 16, got server_version_num=%d", version)
	}
}

func migrationTestDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration integration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../migrations"))
}

func applyMigrationsOneThroughTwentyTwo(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create isolated schema_migrations: %v", err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var migrationNames []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		if name < hardCutoverMigration {
			migrationNames = append(migrationNames, name)
		}
	}
	sort.Strings(migrationNames)
	if len(migrationNames) != 22 {
		t.Fatalf("expected migrations 000001 through 000022, got %d", len(migrationNames))
	}
	for index, name := range migrationNames {
		expectedPrefix := fmt.Sprintf("%06d_", index+1)
		if !strings.HasPrefix(name, expectedPrefix) {
			t.Fatalf(
				"expected migration %s at position %d, got %s",
				expectedPrefix,
				index+1,
				name,
			)
		}
		applyRecordedMigration(t, ctx, database, directory, name)
	}
}

func applyRecordedMigration(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
	name string,
) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin migration %s: %v", name, err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("apply migration %s: %v", name, err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version) VALUES ($1)`,
		name,
	); err != nil {
		t.Fatalf("record migration %s: %v", name, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit migration %s: %v", name, err)
	}
}

func applyHardCutoverDown(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	directory string,
) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(
		directory,
		"000023_intent_shopping_hard_cutover.down.sql",
	))
	if err != nil {
		t.Fatalf("read hard-cutover down migration: %v", err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin hard-cutover rollback: %v", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("apply hard-cutover down migration: %v", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`DELETE FROM schema_migrations WHERE version=$1`,
		hardCutoverMigration,
	); err != nil {
		t.Fatalf("unrecord hard-cutover migration: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit hard-cutover rollback: %v", err)
	}
}

func insertLegacyCutoverFixture(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	const (
		userID            = "11000000-0000-4000-8000-000000000001"
		identityID        = "11000000-0000-4000-8000-000000000002"
		walletID          = "11000000-0000-4000-8000-000000000003"
		shippingProfileID = "11000000-0000-4000-8000-000000000004"
		planID            = "22000000-0000-4000-8000-000000000001"
		targetID          = "22000000-0000-4000-8000-000000000002"
		sessionID         = "22000000-0000-4000-8000-000000000003"
	)
	fixtureTime := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			query: `
				INSERT INTO users(
					id,status,email,display_name,created_at,updated_at
				) VALUES ($1,'ACTIVE','cutover@example.test','Cutover Account',$2,$2)
			`,
			args: []any{userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO external_identities(
					id,user_id,provider,provider_subject,email_snapshot,
					email_verified,display_name_snapshot,created_at,updated_at
				) VALUES (
					$1,$2,'GOOGLE','cutover-subject','cutover@example.test',
					true,'Cutover Account',$3,$3
				)
			`,
			args: []any{identityID, userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO wallets(
					id,user_id,address,chain_id,verified_at,created_at,is_default
				) VALUES (
					$1,$2,'0x1111111111111111111111111111111111111111',
					'91342',$3,$3,true
				)
			`,
			args: []any{walletID, userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO shipping_profiles(
					id,user_id,label,country,masked_summary,encrypted_payload,
					payload_nonce,key_version,payload_hmac,profile_version,
					is_default,created_at,updated_at
				) VALUES (
					$1,$2,'Cutover shipping','KR','서울 · ***',
					decode('01','hex'),decode('02','hex'),'test-key',
					'cutover-shipping-hmac',1,true,$3,$3
				)
			`,
			args: []any{shippingProfileID, userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO shopping_plans(
					id,user_id,original_intent,plan_mode,execution_mode,
					budget_amount,budget_currency,country,city,status,version,
					created_at,updated_at,planning_context_version,
					planning_context_hash
				) VALUES (
					$1,$2,'legacy preview shopping plan','AUTO','EXPERIMENT',
					100000,'KRW','KR','서울','CONFIRMED',1,$3,$3,1,
					'legacy-context'
				)
			`,
			args: []any{planID, userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO curations(
					plan_id,user_id,status,version,created_at,updated_at
				) VALUES ($1,$2,'OPEN',1,$3,$3)
			`,
			args: []any{planID, userID, fixtureTime},
		},
		{
			query: `
				INSERT INTO plan_targets(
					id,plan_id,title,normalized_intent,category,
					allocated_amount,allocated_currency,country,city,url_mode,
					order_index,confirmed_at,target_hash,version,
					created_at,updated_at
				) VALUES (
					$1,$2,'legacy target','legacy target','test',100000,'KRW',
					'KR','서울','NONE',0,$3,'legacy-target-hash',1,$3,$3
				)
			`,
			args: []any{targetID, planID, fixtureTime},
		},
		{
			query: `
				INSERT INTO shopping_sessions(
					id,plan_target_id,user_id,target_snapshot,
					research_scope_snapshot,status,version,created_at,updated_at
				) VALUES ($1,$2,$3,'{}','{}','READY',1,$4,$4)
			`,
			args: []any{sessionID, targetID, userID, fixtureTime},
		},
	} {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert legacy cutover fixture: %v", err)
		}
	}
}

func assertLegacyShoppingFixturePresent(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"shopping_plans",
		"curations",
		"plan_targets",
		"shopping_sessions",
	} {
		assertTableCount(t, ctx, database, table, 1)
	}
}

func assertShoppingDataEmpty(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"shopping_plans",
		"curations",
		"plan_targets",
		"shopping_sessions",
		"planning_tasks",
	} {
		assertTableCount(t, ctx, database, table, 0)
	}
	if tableExists(t, ctx, database, "shopping_carts") {
		assertTableCount(t, ctx, database, "shopping_carts", 0)
	}
}

func assertAccountFixturePreserved(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	var (
		email             string
		displayName       string
		identitySubject   string
		walletAddress     string
		walletIsDefault   bool
		shippingLabel     string
		shippingIsDefault bool
	)
	if err := database.QueryRowContext(ctx, `
		SELECT
			u.email,
			u.display_name,
			identity.provider_subject,
			wallet.address,
			wallet.is_default,
			shipping.label,
			shipping.is_default
		FROM users AS u
		JOIN external_identities AS identity ON identity.user_id=u.id
		JOIN wallets AS wallet ON wallet.user_id=u.id
		JOIN shipping_profiles AS shipping ON shipping.user_id=u.id
		WHERE u.id='11000000-0000-4000-8000-000000000001'
	`).Scan(
		&email,
		&displayName,
		&identitySubject,
		&walletAddress,
		&walletIsDefault,
		&shippingLabel,
		&shippingIsDefault,
	); err != nil {
		t.Fatalf("read preserved account fixture: %v", err)
	}
	if email != "cutover@example.test" ||
		displayName != "Cutover Account" ||
		identitySubject != "cutover-subject" ||
		walletAddress != "0x1111111111111111111111111111111111111111" ||
		!walletIsDefault ||
		shippingLabel != "Cutover shipping" ||
		!shippingIsDefault {
		t.Fatalf(
			"account fixture changed: email=%q displayName=%q identity=%q "+
				"wallet=%q walletDefault=%t shipping=%q shippingDefault=%t",
			email,
			displayName,
			identitySubject,
			walletAddress,
			walletIsDefault,
			shippingLabel,
			shippingIsDefault,
		)
	}
}

func assertHardCutoverSchema(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"agent_capabilities",
		"agent_capability_sessions",
		"candidate_decisions",
		"plan_purchase_item_decisions",
		"plan_purchase_closures",
	} {
		assertTableExists(t, ctx, database, table, false)
	}
	for _, table := range []string{
		"candidate_interactions",
		"candidate_interaction_events",
		"shopping_carts",
		"shopping_cart_items",
		"shopping_cart_commands",
	} {
		assertTableExists(t, ctx, database, table, true)
	}
	assertColumnExists(t, ctx, database, "plan_targets", "removed_from_plan_id", false)
	assertColumnNullable(t, ctx, database, "plan_targets", "plan_id", false)
	assertColumnExists(t, ctx, database, "candidates", "variant_options", false)
	assertColumnExists(
		t,
		ctx,
		database,
		"shopping_sessions",
		"selected_candidate_id",
		false,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"research_feedback",
		"interaction_snapshot",
		true,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"research_feedback",
		"decision_snapshot",
		false,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"planning_proposals",
		"agent_capability_id",
		false,
	)
	assertColumnNullable(t, ctx, database, "planning_proposals", "agent_grant_id", false)
}

func assertVersionTwentyTwoSchema(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
) {
	t.Helper()
	for _, table := range []string{
		"agent_capabilities",
		"agent_capability_sessions",
		"candidate_decisions",
		"plan_purchase_item_decisions",
		"plan_purchase_closures",
	} {
		assertTableExists(t, ctx, database, table, true)
	}
	for _, table := range []string{
		"candidate_interactions",
		"candidate_interaction_events",
		"shopping_carts",
		"shopping_cart_items",
		"shopping_cart_commands",
	} {
		assertTableExists(t, ctx, database, table, false)
	}
	assertColumnExists(t, ctx, database, "plan_targets", "removed_from_plan_id", true)
	assertColumnNullable(t, ctx, database, "plan_targets", "plan_id", true)
	assertColumnExists(t, ctx, database, "candidates", "variant_options", true)
	assertColumnExists(
		t,
		ctx,
		database,
		"shopping_sessions",
		"selected_candidate_id",
		true,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"research_feedback",
		"decision_snapshot",
		true,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"research_feedback",
		"interaction_snapshot",
		false,
	)
	assertColumnExists(
		t,
		ctx,
		database,
		"planning_proposals",
		"agent_capability_id",
		true,
	)
	assertColumnNullable(t, ctx, database, "planning_proposals", "agent_grant_id", true)
}

func assertRecordedMigrationCount(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	expected int,
) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		ctx,
		`SELECT count(*) FROM schema_migrations`,
	).Scan(&count); err != nil {
		t.Fatalf("count recorded migrations: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d recorded migrations, got %d", expected, count)
	}
}

func assertMigrationRecorded(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	migration string,
	expected bool,
) {
	t.Helper()
	var actual bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM schema_migrations WHERE version=$1
		)
	`, migration).Scan(&actual); err != nil {
		t.Fatalf("read migration record %s: %v", migration, err)
	}
	if actual != expected {
		t.Fatalf("migration %s recorded=%t, want %t", migration, actual, expected)
	}
}

func assertTableCount(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
	expected int,
) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		ctx,
		"SELECT count(*) FROM "+quotePostgresIdentifier(table),
	).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != expected {
		t.Fatalf("expected %s count=%d, got %d", table, expected, count)
	}
}

func tableExists(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
) bool {
	t.Helper()
	var exists bool
	if err := database.QueryRowContext(ctx, `
		SELECT to_regclass('public.' || $1) IS NOT NULL
	`, table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}

func assertTableExists(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
	expected bool,
) {
	t.Helper()
	actual := tableExists(t, ctx, database, table)
	if actual != expected {
		t.Fatalf("table %s exists=%t, want %t", table, actual, expected)
	}
}

func assertColumnExists(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
	column string,
	expected bool,
) {
	t.Helper()
	var actual bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema='public'
			  AND table_name=$1
			  AND column_name=$2
		)
	`, table, column).Scan(&actual); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	if actual != expected {
		t.Fatalf("column %s.%s exists=%t, want %t", table, column, actual, expected)
	}
}

func assertColumnNullable(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table string,
	column string,
	expected bool,
) {
	t.Helper()
	var nullable string
	if err := database.QueryRowContext(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name=$1
		  AND column_name=$2
	`, table, column).Scan(&nullable); err != nil {
		t.Fatalf("read nullability for %s.%s: %v", table, column, err)
	}
	actual := nullable == "YES"
	if actual != expected {
		t.Fatalf("%s.%s nullable=%t, want %t", table, column, actual, expected)
	}
}

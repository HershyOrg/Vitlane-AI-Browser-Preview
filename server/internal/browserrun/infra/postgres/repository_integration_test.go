package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	browserapp "github.com/vitlane/vitlane/server/internal/browserrun/app"
	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestRepositoryPersistsOwnerBoundRunJournal(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	directory, err := filepath.Abs("../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := openBrowserRunIntegrationDatabase(t, ctx, databaseURL, directory)
	seedBrowserRunCandidate(t, ctx, database)
	repository := NewRepository(database)

	candidate, err := repository.ResolveCandidate(
		ctx, browserRunUserID, browserRunCurationID, "candidate-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ProductURL != "https://shop.example/products/1" ||
		candidate.MerchantOrigin != "https://shop.example" {
		t.Fatalf("candidate=%+v", candidate)
	}
	now := time.Now().UTC()
	run, err := browserdomain.NewRun(browserRunID, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	err = database.WithinTransaction(ctx, func(tx context.Context) error {
		if err := repository.InsertRun(tx, run); err != nil {
			return err
		}
		if err := repository.AppendEvent(tx, browserapp.Event{
			ID: browserRunEventID, UserID: browserRunUserID, RunID: browserRunID,
			Sequence: 1, Type: "RUN_CREATED", Payload: []byte(`{"candidateId":"candidate-1"}`),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		return repository.InsertCommand(tx, browserapp.CommandRecord{
			UserID: browserRunUserID, RunID: browserRunID, CommandType: "CREATE",
			IdempotencyKey:  "create-1",
			RequestHash:     "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ResponseVersion: 1, CreatedAt: now,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := repository.GetRun(ctx, browserRunUserID, browserRunID, false)
	if err != nil || stored.State != browserdomain.StateAwaitingNavigationApproval {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if err := stored.ApproveNavigation(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.WithinTransaction(ctx, func(tx context.Context) error {
		if err := repository.UpdateRun(tx, stored, 1); err != nil {
			return err
		}
		return repository.AppendEvent(tx, browserapp.Event{
			ID: browserRunEvent2ID, UserID: browserRunUserID, RunID: browserRunID,
			Sequence: 2, Type: "NAVIGATION_APPROVED", Payload: []byte(`{}`), CreatedAt: now.Add(time.Second),
		})
	}); err != nil {
		t.Fatal(err)
	}
	stored, err = repository.GetRun(ctx, browserRunUserID, browserRunID, false)
	if err != nil || stored.State != browserdomain.StateNavigationApproved || stored.Version != 2 {
		t.Fatalf("updated=%+v err=%v", stored, err)
	}
	command, found, err := repository.FindCommand(ctx, browserRunUserID, "CREATE", "create-1")
	if err != nil || !found || command.RunID != browserRunID {
		t.Fatalf("command=%+v found=%v err=%v", command, found, err)
	}
	if _, err := repository.GetRun(ctx, browserRunOtherUserID, browserRunID, false); err != browserdomain.ErrRunNotFound {
		t.Fatalf("cross-owner read must look absent: %v", err)
	}
}

// A dedicated PostgreSQL schema gives this package full migration and
// repository coverage even when the application role cannot CREATE DATABASE.
// search_path applies to every pooled connection and cleanup drops only the
// generated schema.
func openBrowserRunIntegrationDatabase(
	t *testing.T,
	ctx context.Context,
	databaseURL, migrations string,
) *sharedpostgres.Database {
	t.Helper()
	admin, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("vitlane_browserrun_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	database, err := sharedpostgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.DB.ExecContext(cleanup, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop browser run test schema: %v", err)
		}
		_ = admin.Close()
	})
	if err := database.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}
	return database
}

const (
	browserRunUserID      = "97000000-0000-4000-8000-000000000001"
	browserRunOtherUserID = "97000000-0000-4000-8000-000000000009"
	browserRunPlanID      = "97000000-0000-4000-8000-000000000002"
	browserRunCurationID  = "97000000-0000-4000-8000-000000000003"
	browserRunTargetID    = "97000000-0000-4000-8000-000000000004"
	browserRunID          = "97000000-0000-4000-8000-000000000005"
	browserRunEventID     = "97000000-0000-4000-8000-000000000006"
	browserRunEvent2ID    = "97000000-0000-4000-8000-000000000007"
)

func seedBrowserRunCandidate(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
) {
	t.Helper()
	now := time.Now().UTC()
	statements := []string{
		`INSERT INTO users(id,status,created_at,updated_at) VALUES
		 ($1,'ACTIVE',$3,$3),($2,'ACTIVE',$3,$3)`,
		`INSERT INTO shopping_plans(
		 id,user_id,original_intent,plan_mode,execution_mode,budget_amount,
		 budget_currency,country,city,created_at
		) VALUES ($1,$2,'browser product','SINGLE','EXPERIMENT',100,'USD','US','',$3)`,
		`INSERT INTO curations(id,shopping_plan_id,user_id,phase,version,created_at,updated_at)
		 VALUES ($1,$2,$3,'CURATING',1,$4,$4)`,
		`INSERT INTO plan_targets(
		 id,curation_id,user_id,plan_id,title,normalized_intent,category,
		 allocated_amount,allocated_currency,country,city,url_mode,order_index,
		 version,created_at,updated_at
		) VALUES ($1,$2,$3,$4,'browser product','browser product','test',100,'USD','US','',
		 'NONE',0,1,$5,$5)`,
		`INSERT INTO phase8_research_pools(
		 user_id,curation_id,plan_target_id,version,expand_ordinal,latest_mode,created_at,updated_at
		) VALUES ($1,$2,$3,1,0,'REPLACE',$4,$4)`,
		`INSERT INTO phase8_research_candidates(
		 user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,
		 identity_key,locator_kind,product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES ($1,$2,$3,'candidate-1','product-1','SHOPIFY_LIVE','identity-1',
		 'PRODUCT_URL','https://SHOP.example/products/1#tracking',true,0,$4,$4)`,
	}
	arguments := [][]any{
		{browserRunUserID, browserRunOtherUserID, now},
		{browserRunPlanID, browserRunUserID, now},
		{browserRunCurationID, browserRunPlanID, browserRunUserID, now},
		{browserRunTargetID, browserRunCurationID, browserRunUserID, browserRunPlanID, now},
		{browserRunUserID, browserRunCurationID, browserRunTargetID, now},
		{browserRunUserID, browserRunCurationID, browserRunTargetID, now},
	}
	for index, statement := range statements {
		if _, err := database.DB.ExecContext(ctx, statement, arguments[index]...); err != nil {
			t.Fatal(err)
		}
	}
}

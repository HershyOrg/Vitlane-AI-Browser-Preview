package postgres

import (
	"context"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"os"
	"sync"
	"testing"
	"time"
)

func TestAmazonControlPersistsAuditsAndFencesAdmission(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	const operator = "98000000-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	initial, err := repo.ReadAmazonControl(ctx)
	if err != nil || !initial.Enabled || initial.Version != 1 {
		t.Fatalf("default: %#v %v", initial, err)
	}
	if err = repo.SaveAmazonQuota(ctx, researchapp.CatalogAPIQuota{Limit: 100, Remaining: 100, ResetAt: now.Add(time.Hour), ObservedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewRepository(db).UpdateAmazonControl(ctx, operator, false, 1, now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	disabled, err := NewRepository(db).ReadAmazonControl(ctx)
	if err != nil || disabled.Enabled || disabled.Version != 2 {
		t.Fatalf("reload: %#v %v", disabled, err)
	}
	if _, err = repo.ReserveAmazonCall(ctx, "SEARCH", now); err == nil {
		t.Fatal("Off admitted a call")
	}
	if _, err = repo.UpdateAmazonControl(ctx, operator, true, 1, now); err == nil {
		t.Fatal("stale tab overwrote Off")
	}
	var audits, calls int
	if err = db.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM research_catalog_source_control_audit),(SELECT count(*) FROM research_catalog_api_calls)`).Scan(&audits, &calls); err != nil || audits != 1 || calls != 0 {
		t.Fatalf("audit/calls %d/%d %v", audits, calls, err)
	}
	if _, err = repo.UpdateAmazonControl(ctx, operator, true, 2, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveAmazonCall(ctx, "SEARCH", now); err != nil {
		t.Fatal(err)
	}
	// Failed audit insertion rolls back the setting too.
	if _, err = repo.UpdateAmazonControl(ctx, "99999999-0000-4000-8000-000000000000", false, 3, now); err == nil {
		t.Fatal("missing operator audit accepted")
	}
	current, err := repo.ReadAmazonControl(ctx)
	if err != nil || !current.Enabled || current.Version != 3 {
		t.Fatalf("non-atomic audit: %#v %v", current, err)
	}
}

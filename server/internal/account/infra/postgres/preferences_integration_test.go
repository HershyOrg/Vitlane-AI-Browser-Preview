package postgres_test

import (
	"context"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestPreferencesPostgresIsolationCASAndRollback(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base, err := sharedpostgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "step2_preferences_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err = base.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sharedpostgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	alice, bob := sharedapp.UUIDGenerator{}.NewID(), sharedapp.UUIDGenerator{}.NewID()
	for _, id := range []string{alice, bob} {
		if _, err = db.DB.ExecContext(ctx, `INSERT INTO users(id,status,created_at,updated_at) VALUES($1,'ACTIVE',now(),now())`, id); err != nil {
			t.Fatal(err)
		}
	}
	repo := accountpostgres.NewRepository(db)
	initial, err := repo.ReadPreferences(ctx, alice)
	if err != nil || initial.Version != 0 || initial.ResearchCountry != "" || initial.Effective(accountdomain.DefaultsFor("ko-KR", "ko-KR")).ResearchCountry != "KR" {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	zero := int64(0)
	for _, country := range []string{"KR", "US"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			_, err := repo.PatchPreferences(ctx, alice, accountdomain.PreferencesPatch{ResearchCountry: &value}, &zero)
			errors <- err
		}(country)
	}
	wg.Wait()
	close(errors)
	success, conflict := 0, 0
	for err := range errors {
		if err == nil {
			success++
		} else if fault.CodeOf(err) == fault.Conflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS results: success=%d conflict=%d", success, conflict)
	}
	usd := "USD"
	en := "en-US"
	for _, patch := range []accountdomain.PreferencesPatch{{PreferredCurrency: &usd}, {UILocale: &en}} {
		if _, err = repo.PatchPreferences(ctx, alice, patch, nil); err != nil {
			t.Fatal(err)
		}
	}
	stored, _ := repo.ReadPreferences(ctx, alice)
	if stored.Version != 3 || stored.UILocale != en || stored.PreferredCurrency != usd {
		t.Fatalf("partial merge=%+v", stored)
	}
	rollback := fault.New(fault.Conflict, "TEST_ROLLBACK", false)
	err = db.WithinTransaction(ctx, func(tx context.Context) error {
		kr := "KRW"
		if _, err := repo.PatchPreferences(tx, alice, accountdomain.PreferencesPatch{PreferredCurrency: &kr}, nil); err != nil {
			return err
		}
		return rollback
	})
	if err != rollback {
		t.Fatal(err)
	}
	after, _ := repo.ReadPreferences(ctx, alice)
	if after != stored {
		t.Fatalf("rollback escaped transaction: %+v", after)
	}
	other, _ := repo.ReadPreferences(ctx, bob)
	if other.Version != 0 || other.UILocale != "" {
		t.Fatalf("cross-user write: %+v", other)
	}
}

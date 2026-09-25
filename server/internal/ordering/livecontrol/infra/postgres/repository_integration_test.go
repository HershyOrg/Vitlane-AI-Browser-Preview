package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livedomain "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type integrationClock struct{ now time.Time }

func (c integrationClock) Now() time.Time { return c.now }

type integrationIDs struct{ values []string }

func (i *integrationIDs) NewID() string {
	value := i.values[0]
	i.values = i.values[1:]
	return value
}

func TestPostgresRuntimeControlPersistsVersionedStateAndRejectedAudits(t *testing.T) {
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
	if err := database.Migrate(ctx, "../../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lock, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	if _, err := database.DB.ExecContext(ctx, `
		TRUNCATE ordering_live_control_audits;
		UPDATE ordering_live_control
		SET version=1, order_issue_killed=TRUE, paypal_money_killed=TRUE,
		    merchant_effect_killed=TRUE, changed_at=now(),
		    changed_by='SYSTEM_MIGRATION', reason='FAIL_CLOSED_INITIAL_STATE'
		WHERE singleton=TRUE
	`); err != nil {
		t.Fatal(err)
	}

	repository := NewRepository(database)
	ids := &integrationIDs{values: []string{
		"00000000-0000-4000-8000-000000000921",
		"00000000-0000-4000-8000-000000000922",
		"00000000-0000-4000-8000-000000000923",
		"00000000-0000-4000-8000-000000000924",
	}}
	service := liveapp.NewService(repository, liveapp.StaticPolicy{
		OrderIssue: true, PayPalMoney: true, MerchantEffect: true,
	}, integrationClock{now: time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)}, ids)

	initial, err := service.State(ctx)
	if err != nil || initial.Version != 1 || !initial.OrderIssue.RuntimeKilled ||
		!initial.PayPalMoney.RuntimeKilled || !initial.MerchantEffect.RuntimeKilled {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	opened, err := service.Reactivate(ctx, liveapp.ChangeRequest{
		Scope: string(livedomain.ScopeAll), Confirmation: "REACTIVATE PAYPAL LIVE ALL",
		Reason: "pilot activation integration", ExpectedVersion: 1, Actor: "operator-1",
	})
	if err != nil || opened.Version != 2 || !opened.OrderIssue.Effective ||
		!opened.PayPalMoney.Effective || !opened.MerchantEffect.Effective {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	killed, err := service.Kill(ctx, liveapp.ChangeRequest{
		Scope: string(livedomain.ScopePayPalMoney), Confirmation: "KILL PAYPAL LIVE MONEY",
		Reason: "provider incident integration", ExpectedVersion: 2, Actor: "operator-2",
	})
	if err != nil || killed.Version != 3 || killed.OrderIssue.RuntimeKilled ||
		!killed.PayPalMoney.RuntimeKilled || killed.MerchantEffect.RuntimeKilled {
		t.Fatalf("killed=%+v err=%v", killed, err)
	}
	if _, err := service.Kill(ctx, liveapp.ChangeRequest{
		Scope: string(livedomain.ScopeAll), Confirmation: "KILL PAYPAL LIVE ALL",
		Reason: "stale operator integration", ExpectedVersion: 2, Actor: "operator-3",
	}); !errors.Is(err, livedomain.ErrVersionConflict) {
		t.Fatalf("stale err=%v", err)
	}
	if _, err := service.Kill(ctx, liveapp.ChangeRequest{
		Scope: string(livedomain.ScopeAll), Confirmation: "kill live",
		Reason: "invalid phrase integration", ExpectedVersion: 3, Actor: "operator-4",
	}); !errors.Is(err, livedomain.ErrInvalid) {
		t.Fatalf("invalid err=%v", err)
	}

	var succeeded, rejected, versionRejected, phraseRejected int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
		  count(*) FILTER (WHERE outcome='SUCCEEDED'),
		  count(*) FILTER (WHERE outcome='REJECTED'),
		  count(*) FILTER (WHERE reason_code='LIVE_CONTROL_VERSION_CONFLICT'),
		  count(*) FILTER (WHERE reason_code='LIVE_CONTROL_CONFIRMATION_INVALID')
		FROM ordering_live_control_audits
	`).Scan(&succeeded, &rejected, &versionRejected, &phraseRejected); err != nil {
		t.Fatal(err)
	}
	if succeeded != 2 || rejected != 2 || versionRejected != 1 || phraseRejected != 1 {
		t.Fatalf("audit counts succeeded=%d rejected=%d version=%d phrase=%d",
			succeeded, rejected, versionRejected, phraseRejected)
	}
}

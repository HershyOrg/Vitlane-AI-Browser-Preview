package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"strings"

	livecontrolapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livecontrolpostgres "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// runLiveControlCommand deliberately exposes reactivation only to a server
// shell. The browser API has a kill command but no inverse endpoint.
func runLiveControlCommand(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 || args[0] != "reactivate" {
		return fmt.Errorf("usage: vitlane live-control reactivate --scope <scope> --expected-version <n> --actor <operator> --reason <reason> --confirmation <exact phrase>")
	}
	flags := flag.NewFlagSet("live-control reactivate", flag.ContinueOnError)
	scope := flags.String("scope", "", "Live control scope")
	expectedVersion := flags.Int64("expected-version", 0, "observed control version")
	actor := flags.String("actor", "", "operator identity")
	reason := flags.String("reason", "", "audited operational reason")
	confirmation := flags.String("confirmation", "", "exact reactivation phrase")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	database, err := sharedpostgres.Open(ctx, env("DATABASE_URL",
		"postgres://vitlane:vitlane@127.0.0.1:5432/vitlane?sslmode=disable"))
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(ctx, env("MIGRATIONS_DIR", "migrations")); err != nil {
		return err
	}
	service := livecontrolapp.NewService(livecontrolpostgres.NewRepository(database),
		livecontrolapp.StaticPolicy{
			OrderIssue:     env("LIVE_AGENCY_ORDER_ISSUE_ENABLED", "false") == "true",
			PayPalMoney:    env("PAYPAL_LIVE_CAPTURE_ENABLED", "false") == "true",
			MerchantEffect: env("MANUAL_MERCHANT_EFFECT_ENABLED", "false") == "true",
		},
		sharedapp.SystemClock{}, sharedapp.UUIDGenerator{})
	state, err := service.Reactivate(ctx, livecontrolapp.ChangeRequest{
		Scope: *scope, Confirmation: *confirmation, Reason: *reason,
		ExpectedVersion: *expectedVersion, Actor: *actor,
	})
	if err != nil {
		return err
	}
	logger.Info("PayPal Live runtime scope reactivated",
		"event", "ordering.live_control.reactivated", "scope", strings.TrimSpace(*scope),
		"version", state.Version, "actor", strings.TrimSpace(*actor))
	return nil
}

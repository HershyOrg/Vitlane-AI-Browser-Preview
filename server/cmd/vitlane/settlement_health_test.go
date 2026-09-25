package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type settlementHealthTestChain struct {
	heads settlementapp.ChainHeads
	err   error
}

func (c settlementHealthTestChain) Heads(context.Context) (settlementapp.ChainHeads, error) {
	return c.heads, c.err
}

func TestSettlementHealthRequiresWorkersRPCAndCursor(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	baseDatabase, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer baseDatabase.Close()
	schema := "phase5_health_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated health schema: %v", err)
		}
	}()

	isolatedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := isolatedURL.Query()
	query.Set("search_path", schema)
	isolatedURL.RawQuery = query.Encode()
	database, err := sharedpostgres.Open(ctx, isolatedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, "../../migrations"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	defer database.Close()

	const (
		chainID = uint64(991342)
		address = "0x1111111111111111111111111111111111111111"
	)
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO chain_cursors(chain_id, contract_address, finalized_block, updated_at)
		VALUES ($1, $2, 95, $3)
	`, chainID, address, now); err != nil {
		t.Fatal(err)
	}
	health := newSettlementRuntimeHealth()
	// Record actual runtime identities independently of the monitored registry.
	// Deriving them from that registry hid stale, never-scheduled worker names.
	for _, worker := range []string{"settlement_reconciler", "settlement_command_worker",
		"order_process_reducer", "order_owner_effects", "agency_order_lifecycle_worker"} {
		health.record(worker, now, nil)
	}
	chain := settlementHealthTestChain{
		heads: settlementapp.ChainHeads{Safe: 100, Finalized: 98},
	}
	report := health.report(ctx, now, database, chain, chainID, address)
	if report.Status != "ready" || report.FinalizedCursor != 95 ||
		!report.FinalizedCursorSeen || report.OutboxConflictCount != 0 ||
		report.RPCSafeBlock != 100 || report.RPCFinalizedBlock != 98 {
		t.Fatalf("ready report=%#v", report)
	}

	health.record("order_owner_effects", now, errors.New("Owner tick failed"))
	ownerFailure := health.report(ctx, now, database, chain, chainID, address)
	if ownerFailure.Status != "degraded" || !slices.Contains(ownerFailure.ReasonCodes, "WORKER_UNHEALTHY:order_owner_effects") {
		t.Fatalf("Owner failure hidden: %#v", ownerFailure)
	}
	health.record("order_owner_effects", now, nil)

	degraded := health.report(
		ctx, now.Add(31*time.Second), database,
		settlementHealthTestChain{err: errors.New("rpc unavailable")},
		chainID, address,
	)
	if degraded.Status != "degraded" ||
		!slices.Contains(degraded.ReasonCodes, "GIWA_RPC_UNAVAILABLE") ||
		!slices.Contains(
			degraded.ReasonCodes,
			"WORKER_UNHEALTHY:settlement_reconciler",
		) {
		t.Fatalf("degraded report=%#v", degraded)
	}
}

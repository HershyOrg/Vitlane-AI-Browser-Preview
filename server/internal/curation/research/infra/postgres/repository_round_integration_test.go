package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestScanRoundAcceptsNullableAndTerminalFailureColumns(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	requested, err := scanRound(database.DB.QueryRowContext(ctx, `
		SELECT 'round-requested', 'session-1', 'user-1', 1,
		       'research-context.v1', 1, 'context-hash', '{}'::jsonb,
		       'REQUESTED', NULL::text, NULL::text, NULL::boolean,
		       NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
		       TIMESTAMPTZ '2026-08-14 00:00:00+00', NULL::timestamptz
	`))
	if err != nil {
		t.Fatalf("scan requested round with nullable failure: %v", err)
	}
	if requested.Status != researchdomain.RoundStatusRequested ||
		requested.FailureReasonCode != "" || requested.FailureRetryable != nil {
		t.Fatalf("requested round=%+v", requested)
	}

	failed, err := scanRound(database.DB.QueryRowContext(ctx, `
		SELECT 'round-failed', 'session-1', 'user-1', 2,
		       'research-context.v1', 1, 'context-hash', '{}'::jsonb,
		       'FAILED', NULL::text, 'CANDIDATE_RANKING_EMPTY', false,
		       NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
		       TIMESTAMPTZ '2026-08-14 00:00:00+00',
		       TIMESTAMPTZ '2026-08-14 00:01:00+00'
	`))
	if err != nil {
		t.Fatalf("scan failed round: %v", err)
	}
	if failed.Status != researchdomain.RoundStatusFailed ||
		failed.FailureReasonCode != "CANDIDATE_RANKING_EMPTY" ||
		failed.FailureRetryable == nil || *failed.FailureRetryable {
		t.Fatalf("failed round=%+v", failed)
	}
}

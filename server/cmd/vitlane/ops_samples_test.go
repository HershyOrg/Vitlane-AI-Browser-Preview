package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestDecimateSamplesKeepsNewestPoint(t *testing.T) {
	points := make([]opsSamplePoint, 25)
	for index := range points {
		points[index].SampledAt = strconv.Itoa(index)
	}
	kept := decimateSamples(points, 10)
	if len(kept) > 10 || len(kept) == 0 {
		t.Fatalf("decimation size wrong: %d", len(kept))
	}
	if kept[len(kept)-1].SampledAt != "24" {
		t.Fatalf("newest point lost: %#v", kept[len(kept)-1])
	}
	for index := 1; index < len(kept); index++ {
		previous, _ := strconv.Atoi(kept[index-1].SampledAt)
		current, _ := strconv.Atoi(kept[index].SampledAt)
		if current <= previous {
			t.Fatalf("order broken at %d: %#v", index, kept)
		}
	}
	if same := decimateSamples(points, 100); len(same) != len(points) {
		t.Fatalf("under the limit nothing may be dropped: %d", len(same))
	}
}

func TestSamplePointOmitsAbsentSections(t *testing.T) {
	sampledAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	minimal := samplePointFrom(sampledAt, opsHealthReport{Status: "ready"})
	if minimal.ActiveSessions != nil || minimal.DiskUsedPct != nil ||
		minimal.CursorLagBlocks != nil {
		t.Fatalf("absent sections must stay null: %#v", minimal)
	}

	lag := opsHealthReport{
		Status: "ready",
		Settlement: &settlementHealthReport{
			FinalizedCursorSeen: true,
			RPCFinalizedBlock:   1000,
			FinalizedCursor:     940,
		},
		Host: &opsHostFacts{DiskUsedPct: 51, MemoryUsedPct: 72},
	}
	full := samplePointFrom(sampledAt, lag)
	if full.CursorLagBlocks == nil || *full.CursorLagBlocks != 60 {
		t.Fatalf("cursor lag wrong: %#v", full.CursorLagBlocks)
	}
	if full.DiskUsedPct == nil || *full.DiskUsedPct != 51 {
		t.Fatalf("host disk wrong: %#v", full.DiskUsedPct)
	}
}

// 7-3c: the sampler writes the same document the readback serves, retention
// trims on every insert, and the operator endpoint returns chart-ready
// points with absent sections as null.
func TestOpsHealthSamplesRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	// This test applies the full migration set. CI serializes PostgreSQL test
	// packages, but keep a bounded two-minute allowance for cold runners.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	baseDatabase, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer baseDatabase.Close()
	lockConnection, err := baseDatabase.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx,
		`SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	defer lockConnection.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	schema := "ops_samples_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated samples schema: %v", err)
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

	now := time.Now().UTC()
	sessions := int64(3)
	failureRate := 0.25
	rich := opsHealthReport{
		Status: "ready",
		Core:   opsCoreHealth{Status: "ready"},
		Observed: &opsAggregates{
			Intelligence: intelligenceSummary{FailureRate24h: failureRate},
			Accounts:     accountSummary{ActiveSessions: sessions},
		},
		Host: &opsHostFacts{DiskUsedPct: 43, MemoryUsedPct: 67},
	}
	if err := insertOpsHealthSample(
		ctx, database, now.Add(-10*time.Minute), rich,
	); err != nil {
		t.Fatal(err)
	}
	if err := insertOpsHealthSample(
		ctx, database, now.Add(-5*time.Minute),
		opsHealthReport{Status: "degraded", Core: opsCoreHealth{Status: "ready"}},
	); err != nil {
		t.Fatal(err)
	}
	// A sample beyond retention must be swept by the next insert.
	if err := insertOpsHealthSample(
		ctx, database, now.Add(-opsSampleRetention-time.Hour), rich,
	); err != nil {
		t.Fatal(err)
	}
	if err := insertOpsHealthSample(ctx, database, now, rich); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := database.DB.QueryRowContext(
		ctx, `SELECT count(*) FROM ops_health_samples`,
	).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 3 {
		t.Fatalf("retention sweep failed: %d rows", retained)
	}

	handler := newOpsSamplesHandler(database)
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/api/v1/admin/ops/samples", nil))
	if recorder.Code != 200 {
		t.Fatalf("samples endpoint failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response opsSamplesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Hours != 24 || len(response.Points) != 3 {
		t.Fatalf("unexpected response: hours=%d points=%d",
			response.Hours, len(response.Points))
	}
	first, second := response.Points[0], response.Points[1]
	if first.DiskUsedPct == nil || *first.DiskUsedPct != 43 ||
		first.ActiveSessions == nil || *first.ActiveSessions != sessions ||
		first.IntelligenceFailureRate == nil ||
		*first.IntelligenceFailureRate != failureRate {
		t.Fatalf("rich point lost values: %#v", first)
	}
	if second.Status != "degraded" || second.DiskUsedPct != nil ||
		second.ActiveSessions != nil {
		t.Fatalf("minimal point must keep nulls: %#v", second)
	}

	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(
		"GET", "/api/v1/admin/ops/samples?hours=9999", nil,
	))
	if recorder.Code != 400 {
		t.Fatalf("out-of-range window must be 400, got %d", recorder.Code)
	}
}

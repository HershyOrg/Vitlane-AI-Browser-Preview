package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// GAP-007: settlement runtime degradation must never fail core readiness,
// and a core database outage must never be reported as settlement state.
func TestReadinessSeparatesCoreFromSettlementDegraded(t *testing.T) {
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
	schema := "ops_health_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated ops health schema: %v", err)
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
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO chain_cursors(chain_id, contract_address, finalized_block, updated_at)
		VALUES ($1, $2, 95, $3)
	`, chainID, address, now); err != nil {
		t.Fatal(err)
	}

	// Settlement fully degraded: stale workers plus an unreachable GIWA RPC.
	staleHealth := newSettlementRuntimeHealth()
	failingChain := settlementHealthTestChain{err: errors.New("rpc unavailable")}

	recorder := httptest.NewRecorder()
	newCoreReadinessHandler(database)(
		recorder, httptest.NewRequest("GET", "/readyz", nil),
	)
	if recorder.Code != 200 {
		t.Fatalf(
			"core readiness must stay 200 under settlement degradation, got %d",
			recorder.Code,
		)
	}

	degraded := buildOpsHealthReport(
		ctx, now, database, nil, staleHealth, failingChain, chainID, address,
		"v1",
	)
	if degraded.Status != "degraded" || degraded.Core.Status != "ready" {
		t.Fatalf("degraded report=%#v", degraded)
	}
	if degraded.Settlement == nil || degraded.Settlement.Status != "degraded" {
		t.Fatalf("degraded settlement=%#v", degraded.Settlement)
	}
	opsRecorder := httptest.NewRecorder()
	newOpsHealthHandler(&opsReporter{
		database:         database,
		settlementHealth: staleHealth,
		chain:            failingChain,
		chainID:          chainID,
		contractAddress:  address,
		activeKeyVersion: "v1",
	})(opsRecorder, httptest.NewRequest("GET", "/api/v1/admin/ops/health", nil))
	if opsRecorder.Code != 200 {
		t.Fatalf("degraded ops health must stay 200, got %d", opsRecorder.Code)
	}
	if got := opsRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("ops health Cache-Control=%q want no-store", got)
	}
	for _, expected := range []string{
		"WORKER_UNHEALTHY:settlement_reconciler", "GIWA_RPC_UNAVAILABLE",
	} {
		if !slices.Contains(degraded.DegradedReasonCodes, expected) {
			t.Fatalf(
				"degraded reason %q missing from %v",
				expected, degraded.DegradedReasonCodes,
			)
		}
	}

	// Settlement healthy again: the aggregate returns to ready.
	healthyHealth := newSettlementRuntimeHealth()
	for _, worker := range settlementWorkerNames {
		healthyHealth.record(worker, now, nil)
	}
	ready := buildOpsHealthReport(
		ctx, now, database, nil, healthyHealth,
		settlementHealthTestChain{
			heads: settlementapp.ChainHeads{Safe: 100, Finalized: 98},
		},
		chainID, address, "v1",
	)
	if ready.Status != "ready" || len(ready.DegradedReasonCodes) != 0 ||
		ready.Settlement == nil || ready.Settlement.Status != "ready" {
		t.Fatalf("ready report=%#v", ready)
	}

	// Without the settlement runtime the report only carries the core section.
	coreOnly := buildOpsHealthReport(
		ctx, now, database, nil, nil, nil, chainID, address, "v1",
	)
	if coreOnly.Status != "ready" || coreOnly.Settlement != nil {
		t.Fatalf("core-only report=%#v", coreOnly)
	}

	// Core database outage: readiness fails and the report says unavailable.
	closedDatabase, err := sharedpostgres.Open(ctx, isolatedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	newCoreReadinessHandler(closedDatabase)(
		recorder, httptest.NewRequest("GET", "/readyz", nil),
	)
	if recorder.Code != 503 {
		t.Fatalf("core readiness must fail on database outage, got %d", recorder.Code)
	}
	unavailable := buildOpsHealthReport(
		ctx, now, closedDatabase, nil, nil, nil, chainID, address, "v1",
	)
	if unavailable.Status != "unavailable" ||
		!slices.Contains(unavailable.Core.ReasonCodes, "DATABASE_UNAVAILABLE") {
		t.Fatalf("unavailable report=%#v", unavailable)
	}
}

func TestOpsAggregatesReadback(t *testing.T) {
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
	schema := "ops_aggregates_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated aggregates schema: %v", err)
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
	userOne := "11111111-1111-4111-8111-111111111111"
	userTwo := "22222222-2222-4222-8222-222222222222"
	for _, user := range []string{userOne, userTwo} {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO users(id, status, created_at, updated_at)
			VALUES ($1, 'ACTIVE', $2, $2)
		`, user, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []struct {
		id      string
		user    string
		expires time.Time
		revoked *time.Time
	}{
		{"31111111-1111-4111-8111-111111111111", userOne, now.Add(6 * 24 * time.Hour), nil},
		{"32222222-2222-4222-8222-222222222222", userOne, now.Add(6 * 24 * time.Hour), &now},
		{"33333333-3333-4333-8333-333333333333", userTwo, now.Add(-time.Minute), nil},
	}
	for index, session := range sessions {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO auth_sessions(
				id, user_id, token_hash, expires_at, revoked_at, created_at,
				authenticated_at
			) VALUES ($1, $2, decode(lpad($3, 64, '0'), 'hex'), $4, $5, $6, $6)
		`, session.id, session.user, strconv.Itoa(index+1),
			session.expires, session.revoked, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO managed_runner_reservations(
			id, usage_date, user_id, amount_micros, status, request_key,
			expires_at, created_at, completed_at
		) VALUES (
			'41111111-1111-4111-8111-111111111111', $1::date, $2, 100,
			'UNKNOWN', 'req-unknown-1', $3, $4, $5
		)
	`, now, userOne, now.Add(-90*time.Minute), now.Add(-2*time.Hour),
		now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// FK targets for attempts and audit events need the whole product graph,
	// which this observation-only readback never joins. Seed with the FK
	// triggers off on one dedicated connection; CHECK constraints still hold.
	seedConnection, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer seedConnection.Close()
	if _, err := seedConnection.ExecContext(
		ctx, "SET session_replication_role = replica",
	); err != nil {
		t.Fatal(err)
	}
	attempts := []struct {
		id, status string
		completed  *time.Time
	}{
		{"51111111-1111-4111-8111-111111111111", "SUCCEEDED", &now},
		{"52222222-2222-4222-8222-222222222222", "FAILED", &now},
		{"53333333-3333-4333-8333-333333333333", "EFFECT_UNKNOWN", nil},
	}
	for index, attempt := range attempts {
		if _, err := seedConnection.ExecContext(ctx, `
			INSERT INTO intelligence_attempts(
				id, job_id, user_id, ordinal, provider, request_key, status,
				deadline_at, started_at, completed_at
			) VALUES (
				$1, $2, $3, $4, 'MANAGED', $5, $6, $7, $8, $9
			)
		`, attempt.id,
			"61111111-1111-4111-8111-11111111111"+strconv.Itoa(index),
			userOne, index+1,
			"71111111-1111-4111-8111-11111111111"+strconv.Itoa(index),
			attempt.status, now.Add(time.Hour), now.Add(-time.Hour),
			attempt.completed); err != nil {
			t.Fatal(err)
		}
	}
	type auditEvent struct {
		id, agencyOrder, outcome, denial, previous, hash string
	}
	agencyOrderOne := "81111111-1111-4111-8111-111111111111"
	agencyOrderTwo := "82222222-2222-4222-8222-222222222222"
	events := []auditEvent{
		{"91111111-1111-4111-8111-111111111111", agencyOrderOne, "GRANTED", "",
			"", "sha256:aaaa"},
		{"92222222-2222-4222-8222-222222222222", agencyOrderOne, "DENIED",
			"PAYMENT_NOT_FINALIZED", "sha256:aaaa", "sha256:bbbb"},
		// A first event that claims a predecessor is a broken link.
		{"93333333-3333-4333-8333-333333333333", agencyOrderTwo, "GRANTED", "",
			"sha256:forged", "sha256:cccc"},
	}
	for index, event := range events {
		if _, err := seedConnection.ExecContext(ctx, `
			INSERT INTO agency_order_pii_access_audits(
				id, agency_order_id, execution_unit_id, shipping_snapshot_id,
				actor_user_id, action, reason_code,
				reason_detail, outcome, denial_code, correlation_id,
				idempotency_key, previous_event_hash, event_hash, created_at
			) VALUES (
				$1, $2, '83333333-3333-4333-8333-333333333333',
				'84444444-4444-4444-8444-444444444444', $3,
				'SHIPPING_ADDRESS_REVEAL', 'CUSTOMER_SUPPORT',
				'ops readback aggregate test', $4, NULLIF($5, ''),
				$6, $7, NULLIF($8, ''), $9, $10
			)
		`, event.id, event.agencyOrder, userOne, event.outcome, event.denial,
			"corr-"+strconv.Itoa(index), "idem-"+strconv.Itoa(index),
			event.previous, event.hash,
			now.Add(time.Duration(index-10)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seedConnection.ExecContext(
		ctx, "SET session_replication_role = DEFAULT",
	); err != nil {
		t.Fatal(err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO user_deletions(user_id, requested_at)
		VALUES ($1, $2)
	`, userTwo, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shipping_profiles(
			id, user_id, label, country, masked_summary, encrypted_payload,
			payload_nonce, key_version, payload_hmac, profile_version,
			is_default, created_at, updated_at
		) VALUES (
			'a1111111-1111-4111-8111-111111111111', $1, 'home', 'KR',
			'KR ***42', '\xdeadbeef', '\x0102030405060708090a0b0c', 'v0',
			'hmac-sha256:test', 1, true, $2, $2
		)
	`, userOne, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	aggregates, err := collectOpsAggregates(ctx, database, now, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if aggregates.PIIAccess.Granted24h != 2 || aggregates.PIIAccess.Denied24h != 1 {
		t.Fatalf("pii summary=%#v", aggregates.PIIAccess)
	}
	if aggregates.PIILifecycle.PendingDeletions != 1 ||
		aggregates.PIILifecycle.StaleKeyRows != 1 {
		t.Fatalf("pii lifecycle summary=%#v", aggregates.PIILifecycle)
	}
	if aggregates.PIIAccess.BrokenChainLinks != 1 {
		t.Fatalf("broken chain links=%d", aggregates.PIIAccess.BrokenChainLinks)
	}
	if aggregates.Intelligence.Attempts24h != 3 ||
		aggregates.Intelligence.Failed24h != 1 ||
		aggregates.Intelligence.EffectUnknownOpen != 1 {
		t.Fatalf("intelligence summary=%#v", aggregates.Intelligence)
	}
	if aggregates.Intelligence.FailureRate24h < 0.32 ||
		aggregates.Intelligence.FailureRate24h > 0.35 {
		t.Fatalf("failure rate=%f", aggregates.Intelligence.FailureRate24h)
	}
	if aggregates.CostReservations.UnknownCount != 1 {
		t.Fatalf("reservation summary=%#v", aggregates.CostReservations)
	}
	if aggregates.CostReservations.OldestUnknownAgeSeconds < 7100 ||
		aggregates.CostReservations.OldestUnknownAgeSeconds > 7300 {
		t.Fatalf(
			"unknown age seconds=%d",
			aggregates.CostReservations.OldestUnknownAgeSeconds,
		)
	}
	if aggregates.Accounts.ActiveSessions != 1 ||
		aggregates.Accounts.UsersCreated24h != 2 ||
		aggregates.Accounts.ActiveUsers24h != 2 {
		t.Fatalf("account summary=%#v", aggregates.Accounts)
	}
}

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	runnerpostgres "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type ledgerFixture struct {
	database *sharedpostgres.Database
	budget   *runnerapp.BudgetService
	ledger   *runnerpostgres.Ledger
	userID   string
	limits   runnerdomain.Limits
}

func newLedgerFixture(
	t *testing.T,
	ctx context.Context,
	limits runnerdomain.Limits,
) ledgerFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(ctx, "../../../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lockConnection.Close() })
	if _, err := lockConnection.ExecContext(ctx,
		`SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lockConnection.ExecContext(context.Background(),
			`SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	})
	if _, err := database.DB.ExecContext(ctx, `
		TRUNCATE users, managed_runner_usage_daily,
		         managed_runner_reservations CASCADE
	`); err != nil {
		t.Fatal(err)
	}

	clock := sharedapp.SystemClock{}
	ids := sharedapp.UUIDGenerator{}
	accountService := accountapp.NewService(
		accountpostgres.NewRepository(database), clock, ids,
	)
	user, err := accountService.CreateDevelopmentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledger := runnerpostgres.NewLedger(database)
	return ledgerFixture{
		database: database,
		ledger:   ledger,
		budget: runnerapp.NewBudgetService(
			ledger, limits, database, clock, ids,
		),
		userID: string(user.ID),
		limits: limits,
	}
}

// testModel prices one output token at exactly 1 micro so a reservation of N
// micros is trivially readable in the assertions below.
func testModel(maxOutputTokens int64) runnerdomain.Model {
	return runnerdomain.Model{
		Key: "test", Label: "Test", ProviderModelID: "test",
		InputMicrosPerMTok: 0, OutputMicrosPerMTok: 1_000_000,
		MaxOutputTokens: maxOutputTokens,
	}
}

func TestConcurrentReservationsNeverExceedUserDailyLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	limits := runnerdomain.Limits{
		ServerHard: 1_000_000, ServerAdmission: 1_000_000, UserDaily: 10,
	}
	fixture := newLedgerFixture(t, ctx, limits)
	model := testModel(1) // 1 micro worst case, so exactly 10 may pass.

	// Twenty callers race for ten units of budget. Without row locks and a
	// reservation taken before the call, they would all read a spend of zero
	// and all be admitted.
	const attempts = 20
	var wait sync.WaitGroup
	granted := make([]bool, attempts)
	for index := range attempts {
		wait.Go(func() {
			_, err := fixture.budget.Reserve(
				ctx, fixture.userID, "", model, 0,
			)
			granted[index] = err == nil
		})
	}
	wait.Wait()

	total := 0
	for _, ok := range granted {
		if ok {
			total++
		}
	}
	if total != 10 {
		t.Fatalf("granted %d reservations, want exactly 10", total)
	}

	server, user, err := fixture.ledger.ReadCounters(
		ctx, runnerdomain.UsageDate(time.Now()), fixture.userID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.CommittedMicros() != 10 {
		t.Fatalf("user committed %d micros, want 10", user.CommittedMicros())
	}
	if server.CommittedMicros() != 10 {
		t.Fatalf("server committed %d micros, want 10", server.CommittedMicros())
	}
}

func TestServerAdmissionStopsAllUsers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	limits := runnerdomain.Limits{
		ServerHard: 10, ServerAdmission: 5, UserDaily: 1_000_000,
	}
	fixture := newLedgerFixture(t, ctx, limits)
	model := testModel(1)

	for range 5 {
		if _, err := fixture.budget.Reserve(
			ctx, fixture.userID, "", model, 0,
		); err != nil {
			t.Fatalf("reserve under admission: %v", err)
		}
	}
	// The user is nowhere near their own cap, but the server admission line is
	// reached, so the sixth call must be refused with the server reason.
	_, err := fixture.budget.Reserve(ctx, fixture.userID, "", model, 0)
	if !errors.Is(err, runnerdomain.ErrServerDailyLimit) {
		t.Fatalf("error = %v, want ErrServerDailyLimit", err)
	}
}

func TestSettleReplacesReservationWithActualCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newLedgerFixture(t, ctx, runnerdomain.DefaultLimits())
	model := testModel(1_000)

	reservation, err := fixture.budget.Reserve(
		ctx, fixture.userID, "", model, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.AmountMicros != 1_000 {
		t.Fatalf("reserved %d micros, want 1000", reservation.AmountMicros)
	}

	// The real response used 40 of the 1000 reserved output tokens.
	if err := fixture.budget.Settle(ctx, reservation, model,
		runnerdomain.TokenUsage{InputTokens: 12, OutputTokens: 40},
	); err != nil {
		t.Fatal(err)
	}

	_, user, err := fixture.ledger.ReadCounters(
		ctx, runnerdomain.UsageDate(time.Now()), fixture.userID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.ReservedMicros != 0 {
		t.Fatalf("reserved %d micros after settle, want 0", user.ReservedMicros)
	}
	if user.SettledMicros != 40 {
		t.Fatalf("settled %d micros, want 40", user.SettledMicros)
	}
	if user.InputTokens != 12 || user.OutputTokens != 40 ||
		user.TotalTokens() != 52 {
		t.Fatalf(
			"tokens input=%d output=%d total=%d, want 12/40/52",
			user.InputTokens, user.OutputTokens, user.TotalTokens(),
		)
	}
	history, err := fixture.budget.ServerUsageHistory(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Days) != 7 {
		t.Fatalf("history days=%d, want 7", len(history.Days))
	}
	today := history.Days[len(history.Days)-1].Counters
	if today.InputTokens != 12 || today.OutputTokens != 40 ||
		history.Totals.TotalTokens() != 52 {
		t.Fatalf(
			"server history today=%+v totals=%+v",
			today, history.Totals,
		)
	}
}

func TestExpiredReservationBecomesUnknownWithoutReturningHeadroom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newLedgerFixture(t, ctx, runnerdomain.DefaultLimits())
	model := testModel(1_000)

	reservation, err := fixture.budget.Reserve(
		ctx, fixture.userID, "", model, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process that died mid-call: the reservation outlives its TTL.
	// Both timestamps move back together because the row requires
	// expires_at > created_at.
	if _, err := fixture.database.DB.ExecContext(ctx, `
		UPDATE managed_runner_reservations
		SET created_at = now() - interval '1 hour',
		    expires_at = now() - interval '50 minutes'
		WHERE id = $1
	`, reservation.ID); err != nil {
		t.Fatal(err)
	}

	marked, err := fixture.budget.MarkExpiredUnknown(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 1 {
		t.Fatalf("marked %d reservations, want 1", marked)
	}
	_, user, err := fixture.ledger.ReadCounters(
		ctx, runnerdomain.UsageDate(time.Now()), fixture.userID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.ReservedMicros != reservation.AmountMicros {
		t.Fatalf("reserved %d micros after unknown, want %d", user.ReservedMicros, reservation.AmountMicros)
	}
	var status runnerdomain.ReservationStatus
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT status FROM managed_runner_reservations WHERE id=$1
	`, reservation.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != runnerdomain.ReservationUnknown {
		t.Fatalf("reservation status=%s want UNKNOWN", status)
	}
}

func TestDuplicateProviderRequestKeyDoesNotReserveOrCallAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newLedgerFixture(t, ctx, runnerdomain.DefaultLimits())
	model := testModel(1_000)
	first, err := fixture.budget.ReserveCall(
		ctx, fixture.userID, "", "stable-provider-request-key", model, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.budget.ReserveCall(
		ctx, fixture.userID, "", "stable-provider-request-key", model, 0,
	); !errors.Is(err, runnerdomain.ErrReservationExists) {
		t.Fatalf("duplicate reservation error=%v", err)
	}
	_, user, err := fixture.ledger.ReadCounters(
		ctx, runnerdomain.UsageDate(time.Now()), fixture.userID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.ReservedMicros != first.AmountMicros || user.RequestCount != 1 {
		t.Fatalf("duplicate changed counters: %#v first=%#v", user, first)
	}
	var count int
	if err := fixture.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT COUNT(*) FROM managed_runner_reservations
		WHERE request_key=$1
	`, "stable-provider-request-key").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("reservation count=%d want=1", count)
	}
}

func TestUsageReportsServerExhaustionForTheGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	limits := runnerdomain.Limits{
		ServerHard: 10, ServerAdmission: 4, UserDaily: 1_000_000,
	}
	fixture := newLedgerFixture(t, ctx, limits)
	model := testModel(4)

	usage, err := fixture.budget.Usage(ctx, fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ServerExhausted {
		t.Fatal("server should not be exhausted before any spend")
	}

	if _, err := fixture.budget.Reserve(
		ctx, fixture.userID, "", model, 0,
	); err != nil {
		t.Fatal(err)
	}
	usage, err = fixture.budget.Usage(ctx, fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.ServerExhausted {
		t.Fatal("server should be exhausted at the admission line")
	}
}

// GAP-024: an UNKNOWN reservation is closed exactly once by an audited
// operator decision, RELEASED returns headroom and SETTLED converts it.
func TestResolveUnknownReservationExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	limits := runnerdomain.Limits{
		ServerHard: 1_000_000, ServerAdmission: 1_000_000, UserDaily: 1_000,
	}
	fixture := newLedgerFixture(t, ctx, limits)
	model := testModel(100)
	operatorID := "0f000000-0000-4000-8000-00000000ee01"
	if _, err := fixture.database.DB.ExecContext(ctx, `
		INSERT INTO users(id, status, created_at, updated_at)
		VALUES ($1, 'ACTIVE', now(), now())
	`, operatorID); err != nil {
		t.Fatal(err)
	}
	amount := func() int64 { return model.WorstCost(0) }

	reserveUnknown := func(key string) runnerdomain.Reservation {
		t.Helper()
		reservation, err := fixture.budget.ReserveCall(
			ctx, fixture.userID, "", key, model, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.budget.MarkUnknown(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		return reservation
	}
	counters := func() (runnerdomain.UsageCounters, runnerdomain.UsageCounters) {
		t.Helper()
		server, user, err := fixture.ledger.ReadCounters(
			ctx, runnerdomain.UsageDate(time.Now()), fixture.userID,
		)
		if err != nil {
			t.Fatal(err)
		}
		return server, user
	}

	// RELEASED returns the held headroom.
	released := reserveUnknown("resolve-release")
	_, userBefore := counters()
	resolved, err := fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
		ReservationID:     released.ID,
		OperatorUserID:    operatorID,
		Outcome:           runnerdomain.ReservationReleased,
		ReasonDetail:      "provider usage export has no row for this key",
		EvidenceReference: "billing-export-2026-08-08#none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != runnerdomain.ReservationReleased ||
		resolved.CompletedAt == nil {
		t.Fatalf("released resolution=%#v", resolved)
	}
	_, userAfter := counters()
	if userAfter.ReservedMicros != userBefore.ReservedMicros-amount() ||
		userAfter.SettledMicros != userBefore.SettledMicros {
		t.Fatalf("release counters before=%#v after=%#v", userBefore, userAfter)
	}

	// A second decision on the same reservation is refused.
	_, err = fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
		ReservationID:     released.ID,
		OperatorUserID:    operatorID,
		Outcome:           runnerdomain.ReservationSettled,
		ReasonDetail:      "double resolution attempt must fail",
		EvidenceReference: "billing-export-2026-08-08#dup",
	})
	if !errors.Is(err, runnerdomain.ErrReservationNotUnknown) {
		t.Fatalf("second resolution error=%v", err)
	}

	// SETTLED converts headroom into settled spend at the evidenced amount.
	settledTarget := reserveUnknown("resolve-settle")
	evidenced := int64(7)
	_, userBefore = counters()
	if _, err := fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
		ReservationID:       settledTarget.ID,
		OperatorUserID:      operatorID,
		Outcome:             runnerdomain.ReservationSettled,
		ReasonDetail:        "provider billing row matches this request key",
		EvidenceReference:   "billing-export-2026-08-08#row-42",
		SettledAmountMicros: &evidenced,
	}); err != nil {
		t.Fatal(err)
	}
	_, userAfter = counters()
	if userAfter.ReservedMicros != userBefore.ReservedMicros-amount() ||
		userAfter.SettledMicros != userBefore.SettledMicros+evidenced {
		t.Fatalf("settle counters before=%#v after=%#v", userBefore, userAfter)
	}

	// Unknown ID and non-UNKNOWN states are rejected.
	if _, err := fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
		ReservationID:     "0f000000-0000-4000-8000-00000000dead",
		OperatorUserID:    operatorID,
		Outcome:           runnerdomain.ReservationReleased,
		ReasonDetail:      "missing reservation must 404",
		EvidenceReference: "n/a-lookup",
	}); !errors.Is(err, runnerdomain.ErrReservationNotFound) {
		t.Fatalf("missing reservation error=%v", err)
	}
	held, err := fixture.budget.ReserveCall(
		ctx, fixture.userID, "", "resolve-held", model, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
		ReservationID:     held.ID,
		OperatorUserID:    operatorID,
		Outcome:           runnerdomain.ReservationReleased,
		ReasonDetail:      "held reservation must not resolve",
		EvidenceReference: "n/a-held",
	}); !errors.Is(err, runnerdomain.ErrReservationNotUnknown) {
		t.Fatalf("held reservation error=%v", err)
	}

	// Concurrency: two operators race, exactly one decision lands.
	raceTarget := reserveUnknown("resolve-race")
	outcomes := []runnerdomain.ReservationStatus{
		runnerdomain.ReservationReleased, runnerdomain.ReservationSettled,
	}
	results := make(chan error, len(outcomes))
	var wait sync.WaitGroup
	for _, outcome := range outcomes {
		wait.Add(1)
		go func(target runnerdomain.ReservationStatus) {
			defer wait.Done()
			_, err := fixture.budget.ResolveUnknown(ctx, runnerapp.UnknownResolutionInput{
				ReservationID:     raceTarget.ID,
				OperatorUserID:    operatorID,
				Outcome:           target,
				ReasonDetail:      "concurrent resolution race entry",
				EvidenceReference: "billing-export-2026-08-08#race",
			})
			results <- err
		}(outcome)
	}
	wait.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, runnerdomain.ErrReservationNotUnknown):
			conflicted++
		default:
			t.Fatalf("unexpected race error=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("race succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	var resolutionCount int
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM managed_runner_reservation_resolutions
		WHERE reservation_id=$1
	`, raceTarget.ID).Scan(&resolutionCount); err != nil {
		t.Fatal(err)
	}
	if resolutionCount != 1 {
		t.Fatalf("race resolution rows=%d", resolutionCount)
	}
}

func TestSharedBackgroundCallUsesOnlyServerBudget(t *testing.T) {
	ctx := context.Background()
	f := newLedgerFixture(t, ctx, runnerdomain.Limits{ServerHard: 100, ServerAdmission: 100, UserDaily: 1})
	model := testModel(10)
	reservation, e := f.budget.ReserveCall(ctx, "", "", "background:test", model, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.budget.Settle(ctx, reservation, model, runnerdomain.TokenUsage{OutputTokens: 3}); e != nil {
		t.Fatal(e)
	}
	var users int
	var spent int64
	if e = f.database.DB.QueryRowContext(ctx, `SELECT count(*) FROM managed_runner_usage_daily WHERE scope='USER'`).Scan(&users); e != nil {
		t.Fatal(e)
	}
	if e = f.database.DB.QueryRowContext(ctx, `SELECT settled_micros FROM managed_runner_usage_daily WHERE scope='SERVER'`).Scan(&spent); e != nil {
		t.Fatal(e)
	}
	if users != 0 || spent != 3 {
		t.Fatalf("user rows=%d server spent=%d", users, spent)
	}
	var nullUser bool
	if e = f.database.DB.QueryRowContext(ctx, `SELECT user_id IS NULL FROM managed_runner_reservations WHERE id=$1`, reservation.ID).Scan(&nullUser); e != nil || !nullUser {
		t.Fatalf("shared identity: %v %v", nullUser, e)
	}
}

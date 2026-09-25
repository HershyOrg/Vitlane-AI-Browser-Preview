package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
)

type watchdogRepositoryFake struct {
	hot             []WatchdogSnapshot
	cold            []WatchdogSnapshot
	hotSince        time.Time
	hotLimit        int
	coldLimit       int
	advancedAfter   string
	advancedAt      time.Time
	divergenceCalls []string
	divergenceErr   error
}

func (r *watchdogRepositoryFake) ListHotWatchdogSnapshots(
	_ context.Context, updatedSince time.Time, limit int,
) ([]WatchdogSnapshot, error) {
	r.hotSince, r.hotLimit = updatedSince, limit
	return r.hot, nil
}

func (r *watchdogRepositoryFake) ListColdWatchdogSnapshots(
	_ context.Context, limit int,
) ([]WatchdogSnapshot, error) {
	r.coldLimit = limit
	return r.cold, nil
}

func (r *watchdogRepositoryFake) AdvanceColdWatchdogCursor(
	_ context.Context, afterAgencyOrderID string, now time.Time,
) error {
	r.advancedAfter, r.advancedAt = afterAgencyOrderID, now
	return nil
}

func (r *watchdogRepositoryFake) AppendMigratedEvent(
	context.Context, WatchdogSnapshot, time.Time,
) (bool, error) {
	return false, nil
}

func (r *watchdogRepositoryFake) ListFoldBootstrapCandidates(
	context.Context, int,
) ([]string, error) {
	return nil, nil
}

func (r *watchdogRepositoryFake) AppendMerchantOrdersSnapshotEvent(
	context.Context, string, time.Time,
) (bool, error) {
	return false, nil
}

func (r *watchdogRepositoryFake) AppendDivergenceEvent(
	_ context.Context, agencyOrderID, _, _, _ string, _ time.Time,
) (bool, error) {
	r.divergenceCalls = append(r.divergenceCalls, agencyOrderID)
	return true, r.divergenceErr
}

func TestWatchdogDoesNotAdvanceColdCursorAfterInspectionError(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := &watchdogRepositoryFake{
		cold: []WatchdogSnapshot{{
			AgencyOrderID: "order-cold",
			State:         processdomain.StateWaitingCustomerPayment,
			ModelVersion:  processdomain.ProcessModelVersion,
			ProcessState:  processdomain.ProcessState{PaymentSucceeded: true},
		}},
		divergenceErr: errors.New("append failed"),
	}
	watchdog := NewWatchdog(repository, fakeClock{now: now}, slog.New(slog.DiscardHandler))

	if err := watchdog.Tick(context.Background()); !errors.Is(err, repository.divergenceErr) {
		t.Fatalf("error=%v", err)
	}
	if repository.advancedAfter != "" || !repository.advancedAt.IsZero() {
		t.Fatalf("cold cursor advanced after error: after=%q at=%v",
			repository.advancedAfter, repository.advancedAt)
	}
}

func TestWatchdogChecksBoundedHotAndColdWithoutDuplicateWork(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	divergent := WatchdogSnapshot{
		AgencyOrderID: "order-shared",
		State:         processdomain.StateWaitingCustomerPayment,
		ModelVersion:  processdomain.ProcessModelVersion,
		ProcessState:  processdomain.ProcessState{PaymentSucceeded: true},
	}
	repository := &watchdogRepositoryFake{
		hot: []WatchdogSnapshot{divergent},
		cold: []WatchdogSnapshot{
			divergent,
			{AgencyOrderID: "order-cold-last", State: processdomain.StateWaitingCustomerPayment,
				ModelVersion: processdomain.ProcessModelVersion},
		},
	}
	watchdog := NewWatchdog(repository, fakeClock{now: now}, slog.New(slog.DiscardHandler))

	if err := watchdog.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-24 * time.Hour); !repository.hotSince.Equal(want) {
		t.Fatalf("hot since=%v want=%v", repository.hotSince, want)
	}
	if repository.hotLimit != 200 || repository.coldLimit != 50 {
		t.Fatalf("limits hot=%d cold=%d", repository.hotLimit, repository.coldLimit)
	}
	if len(repository.divergenceCalls) != 1 || repository.divergenceCalls[0] != "order-shared" {
		t.Fatalf("duplicate hot/cold divergence work: %v", repository.divergenceCalls)
	}
	if repository.advancedAfter != "order-cold-last" || !repository.advancedAt.Equal(now) {
		t.Fatalf("cold cursor after=%q at=%v", repository.advancedAfter, repository.advancedAt)
	}
}

// 미종결 커맨드가 남은 주문은 수렴 중이다 — owner facts가 먼저 종결에 닿아도
// (예: GIWA 보상 finality가 커맨드 재시도 사이에 도착) 발산으로 기록하지 않는다.
func TestWatchdogSkipsOrdersWithPendingEffects(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	inFlight := WatchdogSnapshot{
		AgencyOrderID:  "order-in-flight",
		State:          processdomain.StateWaitingCustomerPayment,
		ModelVersion:   processdomain.ProcessModelVersion,
		PendingEffects: 1,
		ProcessState:   processdomain.ProcessState{PaymentSucceeded: true},
	}
	repository := &watchdogRepositoryFake{hot: []WatchdogSnapshot{inFlight}}
	watchdog := NewWatchdog(repository, fakeClock{now: now}, slog.New(slog.DiscardHandler))
	if err := watchdog.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.divergenceCalls) != 0 {
		t.Fatalf("in-flight order flagged as divergent: %v", repository.divergenceCalls)
	}
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ordering 레인(ADR-0070 §4.6): 레인끼리 서로 기다리지 않고, step 오류는
// 격리되며, wake 신호가 주기 전에 즉시 깨운다.
func TestLanesRunIndependentlyAndIsolateErrors(t *testing.T) {
	health := newSettlementRuntimeHealth()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var slowStarted, fastTicks atomic.Int32
	slowRelease := make(chan struct{})
	slow := lane{name: "slow", interval: time.Millisecond, steps: []laneStep{{
		name: "order_owner_effects",
		tick: func(ctx context.Context) error {
			slowStarted.Add(1)
			select {
			case <-slowRelease:
			case <-ctx.Done():
			}
			return errors.New("provider timeout")
		},
	}}}
	fast := lane{name: "fast", interval: time.Millisecond, steps: []laneStep{{
		name: "order_process_reducer",
		tick: func(context.Context) error {
			fastTicks.Add(1)
			return nil
		},
	}}}
	logger := slog.New(slog.DiscardHandler)
	go health.runLane(ctx, slow, logger)
	go health.runLane(ctx, fast, logger)

	deadline := time.After(5 * time.Second)
	for fastTicks.Load() < 20 || slowStarted.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("fast lane starved: fast=%d slowStarted=%d", fastTicks.Load(), slowStarted.Load())
		case <-time.After(time.Millisecond):
		}
	}
	// 느린 레인이 아직 첫 tick 안에 있는 동안 빠른 레인은 20번 이상 돌았다.
	close(slowRelease)
	deadline = time.After(5 * time.Second)
	for {
		health.mu.RLock()
		failed := health.workers["order_owner_effects"].LastErrorCode != ""
		healthy := health.workers["order_process_reducer"].LastErrorCode == "" &&
			!health.workers["order_process_reducer"].LastSuccessAt.IsZero()
		health.mu.RUnlock()
		if failed && healthy {
			break
		}
		select {
		case <-deadline:
			t.Fatal("heartbeats did not isolate the failed lane")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestWakeSignalTriggersImmediateLaneTick(t *testing.T) {
	health := newSettlementRuntimeHealth()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ticks atomic.Int32
	wake := newWakeSignal()
	go health.runLane(ctx, lane{name: "manager", interval: time.Hour, wake: wake,
		steps: []laneStep{{name: "order_process_reducer", tick: func(context.Context) error {
			ticks.Add(1)
			return nil
		}}}}, slog.New(slog.DiscardHandler))
	deadline := time.After(2 * time.Second)
	for ticks.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("lane did not run its first tick")
		case <-time.After(time.Millisecond):
		}
	}
	wake.Notify()
	wake.Notify() // 밀린 신호는 최대 한 번의 추가 tick으로 합쳐진다.
	deadline = time.After(2 * time.Second)
	for ticks.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("wake signal did not trigger an immediate tick")
		case <-time.After(time.Millisecond):
		}
	}
	// 신호가 소진된 뒤 한 시간 주기 안에서는 더 이상 tick이 없다.
	time.Sleep(20 * time.Millisecond)
	settled := ticks.Load()
	if settled > 3 {
		t.Fatalf("coalesced wake produced too many ticks: %d", settled)
	}
	time.Sleep(20 * time.Millisecond)
	if ticks.Load() != settled {
		t.Fatalf("lane ticked without wake or interval: %d", ticks.Load())
	}
}

func TestLaneStepsRunInOrderWithinOneLane(t *testing.T) {
	health := newSettlementRuntimeHealth()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var calls []string
	done := make(chan struct{})
	go func() {
		health.runLane(ctx, lane{name: "settlement", interval: time.Millisecond, steps: []laneStep{
			{name: "settlement_reconciler", tick: func(context.Context) error {
				mu.Lock()
				calls = append(calls, "observe")
				mu.Unlock()
				return nil
			}},
			{name: "settlement_command_worker", tick: func(context.Context) error {
				mu.Lock()
				calls = append(calls, "command")
				if len(calls) >= 6 {
					cancel()
				}
				mu.Unlock()
				return nil
			}},
		}}, slog.New(slog.DiscardHandler))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("lane did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	for index, call := range calls[:6] {
		want := "observe"
		if index%2 == 1 {
			want = "command"
		}
		if call != want {
			t.Fatalf("calls=%v", calls)
		}
	}
}

func TestProductionPollingCadenceBoundsIdleLoad(t *testing.T) {
	if orderingProcessEvery < 2 {
		t.Fatalf("process cadence hot-polls: %d", orderingProcessEvery)
	}
	if orderingGIWAIntakeEvery < 3 || orderingPaymentEvery < 5 {
		t.Fatalf("payment cadence hot-polls: giwa=%d payment=%d",
			orderingGIWAIntakeEvery, orderingPaymentEvery)
	}
	if orderingCleanupEvery < 10 {
		t.Fatalf("durable maintenance cadence hot-polls: cleanup=%d", orderingCleanupEvery)
	}
	if orderingOwnerEffectsConcurrency < 1 {
		t.Fatal("lane concurrency must be at least one worker")
	}
	if workerHealthWindow("order_owner_effects") <= workerHealthWindow("order_process_reducer") {
		t.Fatal("Owner Effect lane must tolerate bounded provider calls in its health window")
	}
}

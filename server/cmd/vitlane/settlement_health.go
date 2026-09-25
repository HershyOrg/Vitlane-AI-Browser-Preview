package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	orderProcessWorkerName      = "order_process_reducer"
	orderOwnerEffectsWorkerName = "order_owner_effects"
)

var settlementWorkerNames = []string{
	"settlement_reconciler",
	"settlement_command_worker",
	orderProcessWorkerName,
	orderOwnerEffectsWorkerName,
	"agency_order_lifecycle_worker",
}

// workerHealthWindow는 heartbeat 성공 간격의 허용치다. Owner Effect 레인은 외부
// provider 호출(PayPal 30s, receipt lock 2분 클래스)을 품으므로 더 넓다.
func workerHealthWindow(name string) time.Duration {
	if name == orderOwnerEffectsWorkerName {
		return 2 * time.Minute
	}
	return 30 * time.Second
}

type workerHeartbeat struct {
	LastAttemptAt time.Time `json:"lastAttemptAt,omitempty"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitempty"`
	LastErrorCode string    `json:"lastErrorCode,omitempty"`
}

type settlementRuntimeHealth struct {
	mu      sync.RWMutex
	workers map[string]workerHeartbeat
}

// settlementHealthReport describes the settlement runtime only; the database
// pool and core availability live in the ops health core section.
type settlementHealthReport struct {
	Status              string                     `json:"status"`
	Workers             map[string]workerHeartbeat `json:"workers"`
	RPCSafeBlock        uint64                     `json:"rpcSafeBlock"`
	RPCFinalizedBlock   uint64                     `json:"rpcFinalizedBlock"`
	FinalizedCursor     uint64                     `json:"finalizedCursor"`
	FinalizedCursorSeen bool                       `json:"finalizedCursorSeen"`
	OutboxConflictCount int64                      `json:"outboxConflictCount"`
	ReasonCodes         []string                   `json:"reasonCodes"`
}

func newSettlementRuntimeHealth() *settlementRuntimeHealth {
	workers := make(map[string]workerHeartbeat, len(settlementWorkerNames))
	for _, name := range settlementWorkerNames {
		workers[name] = workerHeartbeat{}
	}
	return &settlementRuntimeHealth{workers: workers}
}

func (h *settlementRuntimeHealth) run(
	ctx context.Context,
	name string,
	interval time.Duration,
	logger *slog.Logger,
	tick func(context.Context) error,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		err := tick(ctx)
		h.record(name, time.Now().UTC(), err)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.ErrorContext(ctx, "phase5 monitored worker tick failed",
				"worker", name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// wakeSignal은 레인 간 즉시 깨우기다(ADR-0070 §4.6). 폴링 주기는 fallback으로
// 유지되며(AGENTS §6 — LISTEN/NOTIFY 없음) 신호는 프로세스 내부 채널이라
// 인프라가 아니다. 버퍼 1 — 밀린 신호는 한 번으로 합쳐진다.
type wakeSignal struct {
	ch chan struct{}
}

func newWakeSignal() *wakeSignal {
	return &wakeSignal{ch: make(chan struct{}, 1)}
}

// Notify는 non-blocking이다 — 이미 신호가 대기 중이면 합쳐진다.
func (s *wakeSignal) Notify() {
	if s == nil {
		return
	}
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

func (s *wakeSignal) channel() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.ch
}

// laneStep은 레인 안에서 순서대로 도는 한 단계다(의존 순서가 있는 step만 같은
// 레인에 묶는다 — 예: 정산 관찰 → 정산 커맨드). 이름은 heartbeat 키다.
type laneStep struct {
	name string
	tick func(context.Context) error
}

// lane은 독립 goroutine으로 도는 주기 작업이다(ADR-0070 §4.6 — Step 직렬
// 스케줄러의 대체). 레인끼리는 서로 기다리지 않으므로 Owner의 외부 호출이
// 리듀서나 결제 대사를 막지 않는다. wake가 있으면 주기 전에도 즉시 돈다.
type lane struct {
	name     string
	interval time.Duration
	steps    []laneStep
	wake     *wakeSignal
}

// runLane은 레인 하나를 ctx가 끝날 때까지 돌린다. step 오류는 격리된다 —
// 실패한 step만 heartbeat에 남고 같은 레인의 다음 step은 계속 돈다(각 step은
// 멱등 대사라 다음 주기가 재수렴한다).
func (h *settlementRuntimeHealth) runLane(ctx context.Context, lane lane, logger *slog.Logger) {
	timer := time.NewTimer(lane.interval)
	defer timer.Stop()
	for {
		for _, step := range lane.steps {
			if ctx.Err() != nil {
				return
			}
			err := step.tick(ctx)
			h.record(step.name, time.Now().UTC(), err)
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.ErrorContext(ctx, "ordering lane step failed",
					"lane", lane.name, "worker", step.name, "error", err)
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(lane.interval)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-lane.wake.channel():
		}
	}
}

func (h *settlementRuntimeHealth) record(name string, now time.Time, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	heartbeat := h.workers[name]
	heartbeat.LastAttemptAt = now
	if err == nil {
		heartbeat.LastSuccessAt = now
		heartbeat.LastErrorCode = ""
	} else {
		heartbeat.LastErrorCode = "TICK_FAILED"
	}
	h.workers[name] = heartbeat
}

func (h *settlementRuntimeHealth) report(
	ctx context.Context,
	now time.Time,
	database *sharedpostgres.Database,
	chain interface {
		Heads(context.Context) (settlementapp.ChainHeads, error)
	},
	chainID uint64,
	contractAddress string,
) settlementHealthReport {
	report := settlementHealthReport{
		Status: "ready", Workers: map[string]workerHeartbeat{},
		ReasonCodes: []string{},
	}
	h.mu.RLock()
	for _, name := range settlementWorkerNames {
		heartbeat := h.workers[name]
		report.Workers[name] = heartbeat
		if heartbeat.LastSuccessAt.IsZero() ||
			now.Sub(heartbeat.LastSuccessAt) > workerHealthWindow(name) ||
			heartbeat.LastErrorCode != "" {
			report.ReasonCodes = append(report.ReasonCodes, "WORKER_UNHEALTHY:"+name)
		}
	}
	h.mu.RUnlock()

	heads, err := chain.Heads(ctx)
	if err != nil {
		report.ReasonCodes = append(report.ReasonCodes, "GIWA_RPC_UNAVAILABLE")
	} else {
		report.RPCSafeBlock = heads.Safe
		report.RPCFinalizedBlock = heads.Finalized
	}

	err = database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT finalized_block
		FROM chain_cursors
		WHERE chain_id=$1 AND lower(contract_address)=lower($2)
	`, chainID, contractAddress).Scan(&report.FinalizedCursor)
	if err == nil {
		report.FinalizedCursorSeen = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		report.ReasonCodes = append(report.ReasonCodes, "CHAIN_CURSOR_QUERY_FAILED")
	} else {
		report.ReasonCodes = append(report.ReasonCodes, "CHAIN_CURSOR_NOT_INITIALIZED")
	}

	if err := database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM settlement_command_outbox
		WHERE state='CONFLICT'
	`).Scan(&report.OutboxConflictCount); err != nil {
		report.ReasonCodes = append(report.ReasonCodes, "OUTBOX_QUERY_FAILED")
	} else if report.OutboxConflictCount > 0 {
		report.ReasonCodes = append(report.ReasonCodes, "OUTBOX_CONFLICT")
	}
	// Settlement runtime faults degrade TEST Settlement only; core
	// availability is owned by the ops health core section (ADR-0040 §3).
	if len(report.ReasonCodes) > 0 {
		report.Status = "degraded"
	}
	return report
}

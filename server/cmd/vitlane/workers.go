package main

import (
	"context"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	intelligenceworker "github.com/vitlane/vitlane/server/internal/curation/intelligence/iface/worker"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

// 주문 계열 워커는 독립 레인으로 돈다(ADR-0070 §4.6 — ADR-0056 §8의 단일
// 사이클 Step 스케줄러 폐기). 레인끼리는 서로 기다리지 않으므로 Owner의
// 외부 provider 호출이 리듀서·결제 대사·정산을 멈추지 않는다. 주기는 벽시계
// 초 단위이며 durable queue의 허용 지연에 맞춘다. Processor와 Owner의 커밋 이후 통지가 두 레인을
// 깨워(Effect 발행 → 실행, 결과 이벤트 → 결정) 정상 수렴 지연을 폴링 주기
// 아래로 내린다. 폴링은 fallback으로 유지된다(AGENTS §6 — LISTEN/NOTIFY 없음).
const (
	orderingSchedulerInterval = time.Second
	orderingProcessEvery      = 2
	orderingGIWAIntakeEvery   = 3
	orderingPaymentEvery      = 5
	orderingCleanupEvery      = 10
	orderingWatchdogEvery     = 300
	// 병렬 상한(ADR-0070 D6 추천값 — PR-5 측정 뒤 조정).
	orderingOwnerEffectsConcurrency = 4
)

func laneInterval(every int) time.Duration {
	return time.Duration(every) * orderingSchedulerInterval
}

// startWorkers launches the same background goroutines the previous
// single-file run() did, in the same order.
func startWorkers(ctx context.Context, app *application) {
	if app.analytics != nil {
		go app.analytics.Run(ctx)
	}
	ctx = runtimepolicy.WithWorkClass(ctx, runtimepolicy.Worker)
	logger := app.logger
	clock := app.clock
	accountRepository := app.accountRepository
	intelligenceService := app.intelligenceService
	settlementHealth := app.settlementHealth

	lanes := make([]lane, 0, 8)
	if app.settlementReconciler != nil {
		// 정산 관찰 → 정산 커맨드는 의존 순서라 한 레인이다.
		lanes = append(lanes, lane{name: "settlement", interval: laneInterval(orderingPaymentEvery),
			steps: []laneStep{
				{name: "settlement_reconciler", tick: app.settlementReconciler.Tick},
				{name: "settlement_command_worker", tick: app.settlementCommandWorker.Tick},
			}})
	}
	if app.giwaIntake != nil {
		// GIWA 수납 합류 sweep — payment 소유 쓰기(ADR-0055 §4).
		lanes = append(lanes, lane{name: "giwa_intake", interval: laneInterval(orderingGIWAIntakeEvery),
			steps: []laneStep{{name: "payment_giwa_intake_worker", tick: app.giwaIntake.Tick}}})
	}
	if app.paymentHandler != nil {
		lanes = append(lanes, lane{name: "paypal", interval: laneInterval(orderingPaymentEvery),
			steps: []laneStep{{name: "payment_paypal_worker", tick: app.paymentService.Tick}}})
	}
	if app.orderProcessor != nil {
		eventWake, effectWake := newWakeSignal(), newWakeSignal()
		app.orderProcessor.SetWakes(effectWake.Notify, eventWake.Notify)
		app.orderQueue.SetWake(eventWake.Notify)
		app.orderEffects.SetConcurrency(orderingOwnerEffectsConcurrency)
		lanes = append(lanes,
			lane{name: orderProcessWorkerName, interval: laneInterval(orderingProcessEvery), wake: eventWake, steps: []laneStep{{name: orderProcessWorkerName, tick: app.orderProcessor.Tick}}},
			lane{name: orderOwnerEffectsWorkerName, interval: laneInterval(orderingProcessEvery), wake: effectWake, steps: []laneStep{{name: orderOwnerEffectsWorkerName, tick: app.orderEffects.Tick}}})
	}
	if app.agencyOrderWorker != nil {
		lanes = append(lanes, lane{name: "agency_order_cleanup", interval: laneInterval(orderingCleanupEvery),
			steps: []laneStep{{name: "agency_order_lifecycle_worker", tick: app.agencyOrderWorker.Tick}}})
	}
	if app.orderProcessWatchdog != nil {
		lanes = append(lanes, lane{name: "order_process_watchdog", interval: laneInterval(orderingWatchdogEvery),
			steps: []laneStep{{name: "order_process_watchdog", tick: app.orderProcessWatchdog.Tick}}})
	}
	for _, l := range lanes {
		go settlementHealth.runLane(ctx, l, logger)
	}
	if app.backgroundService != nil && app.backgroundService.Enabled {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					work, cancel := context.WithTimeout(ctx, 3*time.Minute)
					if err := app.backgroundService.Tick(work); err != nil {
						logger.Error("background research cycle failed", "event", "research.background.cycle_failed", "error", err)
					}
					cancel()
				}
			}
		}()
	}
	if app.conversationService != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := app.conversationService.Tick(ctx); err != nil {
						logger.Error("curation conversation response failed", "event", "curation.conversation.response_failed", "error", err)
					}
				}
			}
		}()
	}
	if app.threadService != nil {
		go app.threadService.Run(ctx)
	}
	if intelligenceService != nil {
		go func() {
			_ = intelligenceworker.New(
				intelligenceService, app.providers, logger,
			).Run(ctx)
		}()
	}
	go func() {
		_ = accountapp.NewSecurityMaintenanceWorker(
			accountRepository, clock, time.Minute, 100, logger,
		).Run(ctx)
	}()
	// GAP-020: drains deletion requests after the grace window and purges
	// snapshots past their retention deadline. Runs regardless of phase5 —
	// account deletion exists for every deployment shape.
	go func() {
		_ = accountapp.NewPIILifecycleWorker(
			accountRepository, clock, 5*time.Minute,
			time.Duration(app.config.piiDeletionGraceHours)*time.Hour,
			time.Duration(app.config.piiSnapshotRetentionDays)*24*time.Hour,
			50, logger,
		).Run(ctx)
	}()
	// GAP-020: online re-encryption drains rows sealed under retired key
	// versions. It idles at one cheap query per tick once everything is on
	// the active version.
	if app.piiKeyring != nil {
		go func() {
			_ = accountapp.NewPIIKeyRotationWorker(
				accountRepository, app.piiKeyring,
				app.piiKeyring.ActiveVersion, clock, time.Minute, 20, logger,
			).Run(ctx)
		}()
	}
	// Five-minute ops health snapshots feed the operator dashboard's
	// time-series charts (ADR-0040 §6 supplement).
	go runOpsHealthSampler(ctx, app.opsReporter, app.database, logger)
}

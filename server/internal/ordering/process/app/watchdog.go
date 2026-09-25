package app

import (
	"context"
	"log/slog"
	"time"

	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// WatchdogSnapshot compares persisted process state with read-only Owner facts.
type WatchdogSnapshot struct {
	AgencyOrderID   string
	State           processdomain.State
	TerminalReason  processdomain.TerminalReason
	LastReasonCode  string
	LastAppliedSeq  int64
	UnappliedEvents int
	// PendingEffects counts unresolved business expectations, including consumed deliveries.
	PendingEffects int
	ModelVersion   int
	ProcessState   processdomain.ProcessState
	MerchantOrders []procmsg.MerchantOrderSnapshot
}

// ObservedState builds a diagnostic projection without granting execution authority.
func (s WatchdogSnapshot) ObservedState() processdomain.ProcessState {
	w := s.ProcessState
	w.ApplyMerchantOrdersSnapshot(procmsg.MerchantOrdersSnapshotPayload{
		MerchantOrders: s.MerchantOrders,
	})
	return w
}

type WatchdogRepository interface {
	ListHotWatchdogSnapshots(ctx context.Context, updatedSince time.Time, limit int) ([]WatchdogSnapshot, error)
	ListColdWatchdogSnapshots(ctx context.Context, limit int) ([]WatchdogSnapshot, error)
	AdvanceColdWatchdogCursor(ctx context.Context, afterAgencyOrderID string, now time.Time) error
	AppendDivergenceEvent(ctx context.Context, agencyOrderID, expectedState, expectedReason, detail string, now time.Time) (bool, error)
}

const (
	watchdogHotWindow = 24 * time.Hour
	watchdogHotLimit  = 200
	watchdogColdLimit = 50
)

// Watchdog은 5분 주기 bounded 발산 감시자다 — owner facts는 읽기만 하고,
// 이벤트 발행 누락으로 멈춘 주문을 divergence 이벤트로 ATTENTION_REQUIRED에
// 올린다(자동 치유 없음). "폴링 제거"의 명문화된 유일 예외다(ADR-0056 §5).
type Watchdog struct {
	repository WatchdogRepository
	clock      sharedapp.Clock
	logger     *slog.Logger
}

func NewWatchdog(
	repository WatchdogRepository,
	clock sharedapp.Clock,
	logger *slog.Logger,
) *Watchdog {
	return &Watchdog{repository: repository, clock: clock, logger: logger}
}

func (w *Watchdog) Tick(ctx context.Context) error {
	now := w.clock.Now()

	hot, err := w.repository.ListHotWatchdogSnapshots(
		ctx, now.Add(-watchdogHotWindow), watchdogHotLimit,
	)
	if err != nil {
		return err
	}
	cold, err := w.repository.ListColdWatchdogSnapshots(ctx, watchdogColdLimit)
	if err != nil {
		return err
	}
	// Cold는 전체 corpus를 순환하므로 hot과 겹칠 수 있다. 한 tick 안에서는
	// 같은 주문을 한 번만 검사해 owner facts 재계산과 divergence append를
	// 중복하지 않는다.
	snapshots := make([]WatchdogSnapshot, 0, len(hot)+len(cold))
	seen := make(map[string]struct{}, len(hot)+len(cold))
	appendUnique := func(snapshot WatchdogSnapshot) {
		if _, exists := seen[snapshot.AgencyOrderID]; exists {
			return
		}
		seen[snapshot.AgencyOrderID] = struct{}{}
		snapshots = append(snapshots, snapshot)
	}
	for _, snapshot := range hot {
		appendUnique(snapshot)
	}
	for _, snapshot := range cold {
		appendUnique(snapshot)
	}
	for _, snapshot := range snapshots {
		// Pending observations and effects have not reached a fixed point.
		// An old model requires migration; attention requires explicit review.
		if snapshot.UnappliedEvents > 0 || snapshot.PendingEffects > 0 ||
			snapshot.ModelVersion < processdomain.ProcessModelVersion ||
			snapshot.State == processdomain.StateAttentionRequired {
			continue
		}
		expected := processdomain.ProjectState(processdomain.Process{
			AgencyOrderID:  snapshot.AgencyOrderID,
			State:          snapshot.State,
			TerminalReason: snapshot.TerminalReason,
			LastReasonCode: snapshot.LastReasonCode,
			Version:        1,
			ProcessState:   snapshot.ObservedState(),
		}, now)
		// 저장 stage가 관찰된 facts의 고정점이 아니면 어떤 이벤트가 누락됐다.
		if expected.State == snapshot.State && expected.TerminalReason == snapshot.TerminalReason && expected.LastReasonCode == snapshot.LastReasonCode {
			continue
		}
		inserted, err := w.repository.AppendDivergenceEvent(ctx,
			snapshot.AgencyOrderID, string(expected.State),
			string(expected.TerminalReason),
			"stored stage is not a fixed point of observed owner facts", now)
		if err != nil {
			return err
		}
		if inserted {
			w.logger.Error("order process divergence detected",
				"event", "order_process.watchdog.divergence",
				"agencyOrderId", snapshot.AgencyOrderID,
				"storedState", string(snapshot.State),
				"expectedState", string(expected.State))
		}
	}
	// Cold page 전체 검사가 끝난 뒤에만 cursor를 전진시킨다. 중간 오류나
	// process crash는 같은 page를 재검사하며 divergence event dedup이 안전을
	// 보장한다.
	afterAgencyOrderID := ""
	if len(cold) > 0 {
		afterAgencyOrderID = cold[len(cold)-1].AgencyOrderID
	}
	return w.repository.AdvanceColdWatchdogCursor(ctx, afterAgencyOrderID, now)
}

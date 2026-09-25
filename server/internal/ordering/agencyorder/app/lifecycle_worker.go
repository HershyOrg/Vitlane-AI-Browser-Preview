package app

import (
	"context"
	"log/slog"
)

// LifecycleWorker는 OrderSheet cleanup 유지보수만 남았다(ADR-0056 — stage
// 전진은 OrderProcessor, Receipt 발급은 AgencyOrder의 issue_receipt Effect 소비자가
// 소유한다).
type LifecycleWorker struct {
	cleanup *OrderSheetCleanupService
	logger  *slog.Logger
}

func (w *LifecycleWorker) EnableOrderSheetCleanup(cleanup *OrderSheetCleanupService) {
	w.cleanup = cleanup
}

func NewLifecycleWorker(logger *slog.Logger) *LifecycleWorker {
	return &LifecycleWorker{logger: logger}
}

func (w *LifecycleWorker) Tick(ctx context.Context) error {
	if w.cleanup == nil {
		return nil
	}
	if err := w.cleanup.Tick(ctx); err != nil {
		w.logger.Error("AgencyOrder OrderSheet cleanup tick failed",
			"event", "agency_order.order_sheet_cleanup.failed", "error", err)
		return err
	}
	return nil
}

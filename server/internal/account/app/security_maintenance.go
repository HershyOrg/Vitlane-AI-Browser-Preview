package app

import (
	"context"
	"log/slog"
	"time"
)

type SecurityCleanupResult struct {
	WalletAttempts int
	LoginAttempts  int
	MobileHandoffs int
	RateBuckets    int
}

type SecurityMaintenanceRepository interface {
	CleanupTerminalSecurityArtifacts(
		context.Context,
		time.Time,
		int,
	) (SecurityCleanupResult, error)
}

type SecurityMaintenanceWorker struct {
	repository SecurityMaintenanceRepository
	clock      interface{ Now() time.Time }
	interval   time.Duration
	batchSize  int
	logger     *slog.Logger
}

func NewSecurityMaintenanceWorker(
	repository SecurityMaintenanceRepository,
	clock interface{ Now() time.Time },
	interval time.Duration,
	batchSize int,
	logger *slog.Logger,
) *SecurityMaintenanceWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &SecurityMaintenanceWorker{
		repository: repository, clock: clock, interval: interval,
		batchSize: batchSize, logger: logger,
	}
}

func (w *SecurityMaintenanceWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		result, err := w.repository.CleanupTerminalSecurityArtifacts(
			ctx, w.clock.Now(), w.batchSize,
		)
		if err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(
				ctx, "clean terminal Account authentication artifacts",
				"event", "account.security_cleanup.failed",
				"result", "failed", "error", err,
			)
		} else if err == nil &&
			(result.WalletAttempts+result.LoginAttempts+result.MobileHandoffs+result.RateBuckets) > 0 {
			w.logger.InfoContext(
				ctx, "cleaned terminal Account authentication artifacts",
				"event", "account.security_cleanup.completed",
				"result", "success",
				"wallet_attempts", result.WalletAttempts,
				"login_attempts", result.LoginAttempts,
				"mobile_handoffs", result.MobileHandoffs,
				"rate_buckets", result.RateBuckets,
			)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

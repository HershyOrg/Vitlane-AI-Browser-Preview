package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// PIILifecycleRepository is the durable side of the deletion pipeline
// (GAP-020, ADR-0040 §8). Every method is idempotent so a crashed tick can
// simply run again.
type PIILifecycleRepository interface {
	// DueDeletions returns user IDs whose deletion request has passed the
	// grace window and has not been purged yet.
	DueDeletions(ctx context.Context, before time.Time, limit int) ([]string, error)
	// PurgeUserPII erases the user's identifying satellite data in one
	// transaction: profile ciphertext, external identities and sessions.
	// Snapshots tied to issued AgencyOrders receive a retention deadline instead of an
	// immediate purge. It reports whether snapshots were retained, and false
	// handled when the user was not in DELETION_REQUESTED anymore.
	PurgeUserPII(
		ctx context.Context, userID string, now time.Time,
		snapshotRetention time.Duration,
	) (handled bool, snapshotsRetained bool, err error)
	// PurgeExpiredSnapshots erases snapshot ciphertext whose retention
	// deadline has passed.
	PurgeExpiredSnapshots(ctx context.Context, now time.Time, limit int) (int, error)
}

// PIILifecycleWorker drains deletion requests and the snapshot retention
// queue. There is no operator override that skips the ledger: the pipeline
// is the only writer of purged_at.
type PIILifecycleWorker struct {
	repository        PIILifecycleRepository
	clock             sharedapp.Clock
	interval          time.Duration
	grace             time.Duration
	snapshotRetention time.Duration
	batchSize         int
	logger            *slog.Logger
}

func NewPIILifecycleWorker(
	repository PIILifecycleRepository,
	clock sharedapp.Clock,
	interval time.Duration,
	grace time.Duration,
	snapshotRetention time.Duration,
	batchSize int,
	logger *slog.Logger,
) *PIILifecycleWorker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if snapshotRetention <= 0 {
		snapshotRetention = 5 * 365 * 24 * time.Hour
	}
	return &PIILifecycleWorker{
		repository: repository, clock: clock, interval: interval,
		grace: grace, snapshotRetention: snapshotRetention,
		batchSize: batchSize, logger: logger,
	}
}

func (w *PIILifecycleWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "pii lifecycle tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tick processes one bounded batch. Each user purge commits on its own so a
// failure on one account never blocks the rest of the queue.
func (w *PIILifecycleWorker) Tick(ctx context.Context) error {
	now := w.clock.Now()
	due, err := w.repository.DueDeletions(ctx, now.Add(-w.grace), w.batchSize)
	if err != nil {
		return err
	}
	for _, userID := range due {
		handled, retained, err := w.repository.PurgeUserPII(
			ctx, userID, now, w.snapshotRetention,
		)
		if err != nil {
			return err
		}
		if handled {
			w.logger.InfoContext(ctx, "user pii purged",
				"event", "account.pii_purged",
				"user_id", userID, "snapshots_retained", retained)
		}
	}
	purged, err := w.repository.PurgeExpiredSnapshots(ctx, now, w.batchSize)
	if err != nil {
		return err
	}
	if purged > 0 {
		w.logger.InfoContext(ctx, "expired shipping snapshots purged",
			"event", "account.snapshots_purged", "count", purged)
	}
	return nil
}

// EncryptedRowRef points at one stored ciphertext for online re-encryption.
type EncryptedRowRef struct {
	Kind      string // PROFILE | SNAPSHOT
	ID        string
	UserID    string
	Encrypted EncryptedPII
}

// PIIKeyRotationRepository feeds and persists the online re-encryption pass
// (ADR-0040 §8). Updates are guarded by the old key version so a row settled
// concurrently is skipped, never double-written.
type PIIKeyRotationRepository interface {
	StaleEncryptedRows(
		ctx context.Context, activeVersion string, limit int,
	) ([]EncryptedRowRef, error)
	UpdateEncryptedRow(
		ctx context.Context, ref EncryptedRowRef,
		encrypted EncryptedPII, now time.Time,
	) (bool, error)
}

// PIIKeyRotationWorker rewrites rows sealed under retired key versions with
// the active one. It only makes progress while old versions remain in the
// keyring; removing an old key from configuration before this drains would
// fail decryption loudly rather than silently losing data.
type PIIKeyRotationWorker struct {
	repository PIIKeyRotationRepository
	cipher     PIICipher
	active     func() string
	clock      sharedapp.Clock
	interval   time.Duration
	batchSize  int
	logger     *slog.Logger
}

func NewPIIKeyRotationWorker(
	repository PIIKeyRotationRepository,
	cipher PIICipher,
	activeVersion func() string,
	clock sharedapp.Clock,
	interval time.Duration,
	batchSize int,
	logger *slog.Logger,
) *PIIKeyRotationWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	if batchSize <= 0 {
		batchSize = 20
	}
	return &PIIKeyRotationWorker{
		repository: repository, cipher: cipher, active: activeVersion,
		clock: clock, interval: interval, batchSize: batchSize, logger: logger,
	}
}

func (w *PIIKeyRotationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if _, err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "pii key rotation tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *PIIKeyRotationWorker) Tick(ctx context.Context) (int, error) {
	now := w.clock.Now()
	rows, err := w.repository.StaleEncryptedRows(ctx, w.active(), w.batchSize)
	if err != nil {
		return 0, err
	}
	rotated := 0
	for _, row := range rows {
		plaintext, err := w.cipher.Decrypt(ctx, row.Encrypted, row.UserID)
		if err != nil {
			return rotated, err
		}
		resealed, err := w.cipher.Encrypt(ctx, plaintext, row.UserID)
		if err != nil {
			return rotated, err
		}
		updated, err := w.repository.UpdateEncryptedRow(ctx, row, resealed, now)
		if err != nil {
			return rotated, err
		}
		if updated {
			rotated++
		}
	}
	if rotated > 0 {
		w.logger.InfoContext(ctx, "pii rows re-encrypted",
			"event", "account.pii_reencrypted",
			"count", rotated, "active_version", w.active())
	}
	return rotated, nil
}

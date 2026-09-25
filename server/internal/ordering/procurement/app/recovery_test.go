package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// 입구 검증은 repo 도달 전에 잘라낸다 — DB cast 500 대신 422 계약.
func TestRecoveryInputValidationShortCircuits(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, nil, nil, fakeClock{now: time.Unix(1700000000, 0)}, false)

	if _, err := service.ListRecovery(context.Background(), "not-a-uuid"); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("non-uuid order id must be invalid: %v", err)
	}
	valid := "0e5f8475-f38b-4c35-9dc1-b0148d9ea94b"
	if _, _, err := service.CreateRecovery(context.Background(), "op-1", "idem-recovery-01",
		RecoveryCreateInput{MerchantOrderID: valid, Cause: "CHARGE_WITHOUT_ORDER"},
	); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("auto-only cause must be rejected: %v", err)
	}
	if _, _, err := service.CreateRecovery(context.Background(), "op-1", "short",
		RecoveryCreateInput{MerchantOrderID: valid, Cause: "OTHER"},
	); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("short idempotency key must be rejected: %v", err)
	}
	if _, _, err := service.CreateRecovery(context.Background(), "op-1", "idem-recovery-02",
		RecoveryCreateInput{MerchantOrderID: valid, Cause: "OTHER", ReceivedAmountMinor: -5},
	); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("negative received must be rejected: %v", err)
	}
	if _, _, err := service.RecordRecovery(context.Background(), valid, "op-1",
		"idem-recovery-03", 100, "", 0,
	); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("expectedVersion 0 must be rejected: %v", err)
	}
	if err := service.DeleteRecovery(context.Background(), "nope", "op-1", 1); !errors.Is(err, domain.ErrRecoveryInvalid) {
		t.Fatalf("non-uuid entry id must be invalid: %v", err)
	}
}

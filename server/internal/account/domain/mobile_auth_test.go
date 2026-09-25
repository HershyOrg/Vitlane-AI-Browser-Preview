package domain

import (
	"errors"
	"testing"
	"time"
)

func TestMobileAuthHandoffIsBoundSingleUseAndShortLived(t *testing.T) {
	now := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	handoff, err := NewMobileAuthHandoff(
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		make([]byte, 32), make([]byte, 32),
		now.Add(7*24*time.Hour), now, now, 2*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("unexpected handoff expiry: %s", handoff.ExpiresAt)
	}
	if err := handoff.CanConsume(now.Add(time.Minute), make([]byte, 32)); err != nil {
		t.Fatalf("valid handoff rejected: %v", err)
	}
	wrong := make([]byte, 32)
	wrong[0] = 1
	if err := handoff.CanConsume(now.Add(time.Minute), wrong); !errors.Is(err, ErrMobileAuthInvalid) {
		t.Fatalf("wrong verifier challenge accepted: %v", err)
	}
	if err := handoff.CanConsume(now.Add(2*time.Minute), make([]byte, 32)); !errors.Is(err, ErrMobileAuthExpired) {
		t.Fatalf("expired handoff accepted: %v", err)
	}
	consumedAt := now.Add(time.Minute)
	handoff.ConsumedAt = &consumedAt
	if err := handoff.CanConsume(now.Add(time.Minute), make([]byte, 32)); !errors.Is(err, ErrMobileAuthConsumed) {
		t.Fatalf("consumed handoff accepted: %v", err)
	}
}

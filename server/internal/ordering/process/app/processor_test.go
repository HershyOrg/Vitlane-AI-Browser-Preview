package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type processorTestClock struct{}

func (processorTestClock) Now() time.Time { return time.Now().UTC() }

type isolatedProcessStore struct {
	ProcessStore
	blocked, other chan struct{}
	failure        error
}

func (s *isolatedProcessStore) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func (s *isolatedProcessStore) ListDueTimers(context.Context, time.Time, int) ([]DueTimer, error) {
	return nil, s.failure
}
func (s *isolatedProcessStore) ListOrdersWithUnappliedEvents(context.Context, int) ([]string, error) {
	return []string{"blocked", "other"}, nil
}
func (s *isolatedProcessStore) LockOrder(ctx context.Context, id string) error {
	if id == "blocked" {
		select {
		case <-s.blocked:
			return s.failure
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	close(s.other)
	return nil
}
func (s *isolatedProcessStore) LoadDecision(context.Context, string) (DecisionInput, error) {
	return DecisionInput{}, nil
}
func TestProcessorIsolatesOrderLockAndTimerFailures(t *testing.T) {
	failure := errors.New("broken order timer")
	s := &isolatedProcessStore{blocked: make(chan struct{}), other: make(chan struct{}), failure: failure}
	p := NewOrderProcessor(s, processorTestClock{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Tick(ctx) }()
	select {
	case <-s.other:
	case <-ctx.Done():
		t.Fatal("one order lock or timer error prevented another order from reducing")
	}
	close(s.blocked)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("lost failure: %v", err)
	}
}

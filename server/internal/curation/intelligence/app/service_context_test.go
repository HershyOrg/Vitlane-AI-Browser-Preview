package app

import (
	"context"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

func TestOutcomeFinalizerOutlivesAttemptCancellation(t *testing.T) {
	lifecycle, stopLifecycle := context.WithCancel(context.Background())
	defer stopLifecycle()
	service := &Service{
		finalizer: runtimepolicy.NewFinalizer(lifecycle, time.Second),
	}
	attemptContext, cancelAttempt := context.WithCancel(context.Background())
	cancelAttempt()
	if attemptContext.Err() != context.Canceled {
		t.Fatal("attempt context was not cancelled")
	}

	finalizeContext, cancelFinalize := service.finalizer.Context()
	defer cancelFinalize()
	if err := finalizeContext.Err(); err != nil {
		t.Fatalf("finalization inherited attempt cancellation: %v", err)
	}

	stopLifecycle()
	select {
	case <-finalizeContext.Done():
	case <-time.After(time.Second):
		t.Fatal("process shutdown did not cancel finalization")
	}
}

package runtimepolicy

import (
	"context"
	"testing"
	"time"
)

func TestWithTimeoutNeverExtendsParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelParent()
	child, cancelChild := WithTimeout(parent, time.Minute)
	defer cancelChild()
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Equal(parentDeadline) {
		t.Fatalf("child deadline %s extended parent %s", childDeadline, parentDeadline)
	}
}

func TestWithDeadlineNeverExtendsParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelParent()
	child, cancelChild := WithDeadline(parent, time.Now().Add(time.Minute))
	defer cancelChild()
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Equal(parentDeadline) {
		t.Fatalf("child deadline %s extended parent %s", childDeadline, parentDeadline)
	}
}

func TestWorkClassDefaultsToInteractive(t *testing.T) {
	if got := WorkClassFrom(context.Background()); got != Interactive {
		t.Fatalf("default work class = %q", got)
	}
	ctx := WithWorkClass(context.Background(), Worker)
	if got := WorkClassFrom(ctx); got != Worker {
		t.Fatalf("worker class = %q", got)
	}
}

func TestFinalizerIgnoresCallerButStopsWithLifecycle(t *testing.T) {
	lifecycle, stop := context.WithCancel(context.Background())
	finalizer := NewFinalizer(lifecycle, time.Second)
	caller, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	_ = caller
	ctx, cancel := finalizer.Context()
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("finalizer inherited caller cancellation: %v", err)
	}
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("finalizer did not inherit lifecycle cancellation")
	}
}

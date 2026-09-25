// Package runtimepolicy owns the lifecycle rules shared by HTTP requests,
// background workers, database calls and external adapters.
package runtimepolicy

import (
	"context"
	"time"
)

// WorkClass selects bounded database and operation timeouts. Callers set the
// class once at the ingress boundary instead of attaching unrelated timeout
// numbers to individual queries.
type WorkClass string

const (
	Interactive WorkClass = "interactive"
	Worker      WorkClass = "worker"
	Admin       WorkClass = "migration_admin"
)

type workClassKey struct{}

func WithWorkClass(ctx context.Context, class WorkClass) context.Context {
	if ctx == nil {
		panic("runtimepolicy: nil context")
	}
	switch class {
	case Interactive, Worker, Admin:
	default:
		class = Interactive
	}
	return context.WithValue(ctx, workClassKey{}, class)
}

func WorkClassFrom(ctx context.Context) WorkClass {
	if ctx != nil {
		if class, ok := ctx.Value(workClassKey{}).(WorkClass); ok {
			return class
		}
	}
	return Interactive
}

// WithTimeout returns a child bounded by duration without extending an
// existing earlier deadline.
func WithTimeout(
	ctx context.Context,
	duration time.Duration,
) (context.Context, context.CancelFunc) {
	if ctx == nil {
		panic("runtimepolicy: nil context")
	}
	if duration <= 0 {
		return context.WithCancel(ctx)
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= duration {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, duration)
}

// WithDeadline applies a durable attempt deadline without extending a process
// or worker deadline that is already earlier.
func WithDeadline(
	ctx context.Context,
	deadline time.Time,
) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return context.WithCancel(ctx)
	}
	if current, ok := ctx.Deadline(); ok && !deadline.Before(current) {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline)
}

// Finalizer creates the short context used only after an external call has
// started. It is owned by the service lifecycle, not the caller, so a browser
// disconnect cannot prevent recording success, failure or an unknown effect.
// Process shutdown still cancels it.
type Finalizer struct {
	lifecycle context.Context
	timeout   time.Duration
}

func NewFinalizer(lifecycle context.Context, timeout time.Duration) Finalizer {
	if lifecycle == nil {
		panic("runtimepolicy: nil lifecycle context")
	}
	if timeout <= 0 {
		panic("runtimepolicy: finalization timeout must be positive")
	}
	return Finalizer{lifecycle: lifecycle, timeout: timeout}
}

func (f Finalizer) Context() (context.Context, context.CancelFunc) {
	return WithTimeout(f.lifecycle, f.timeout)
}

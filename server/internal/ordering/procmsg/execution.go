package procmsg

import (
	"context"
	"errors"
)

// ExecutionScope is installed by an Owner's inbox after validating the durable
// immutable effect and its current claim. It is never accepted from HTTP input.
type ExecutionScope struct {
	AgencyOrderID   string
	MerchantOrderID string
	FlowID          string
	EffectID        string
	Action          string
	ClaimVersion    int64
	InputHash       string
}
type executionScopeKey struct{}

var ErrExecutionRequired = errors.New("ORDER_PROCESS_EXECUTION_REQUIRED")

func WithExecutionScope(ctx context.Context, scope ExecutionScope) context.Context {
	return context.WithValue(ctx, executionScopeKey{}, scope)
}
func ExecutionFrom(ctx context.Context) (ExecutionScope, bool) {
	s, ok := ctx.Value(executionScopeKey{}).(ExecutionScope)
	return s, ok && s.FlowID != "" && s.EffectID != ""
}
func RequireExecution(ctx context.Context, merchantOrderID string, actions ...string) error {
	s, ok := ExecutionFrom(ctx)
	if !ok || s.MerchantOrderID != merchantOrderID {
		return ErrExecutionRequired
	}
	for _, action := range actions {
		if s.Action == action {
			return nil
		}
	}
	return ErrExecutionRequired
}

package procmsg

import (
	"context"
	"errors"
	"time"
)

var ErrStaleDelivery = errors.New("ORDER_EFFECT_DELIVERY_STALE")
var ErrEffectInvalid = errors.New("ORDER_EFFECT_INVALID")

type Delivery struct {
	Effect       ProcessEffect
	ClaimVersion int64
	Attempt      int
}

// Disposition has no business meaning. Owners report verified facts separately.
type Disposition int

const (
	KeepClaim Disposition = iota
	RetryDelivery
	Consumed
)

// EffectInbox is the persistence port for one Owner's effect consumer. A
// callback owns only that Owner's local transaction; it cannot select another
// Owner or grant the next business transition. Reports join its event outbox.
type EffectInbox interface {
	Consume(context.Context, Delivery, func(context.Context, ProcessEffect) error) error
	WithDelivery(context.Context, Delivery, func(context.Context, ProcessEffect) (Disposition, error)) error
	Observe(context.Context, Delivery, func(context.Context, ProcessEffect) error) error
	Report(context.Context, ProcessEffect, string, string, any, string, time.Time) error
}

func EffectContext(ctx context.Context, e ProcessEffect) context.Context {
	return WithExecutionScope(ctx, ExecutionScope{AgencyOrderID: e.AgencyOrderID, MerchantOrderID: e.MerchantOrderID, FlowID: e.FlowID, EffectID: e.ID, Action: e.Type, InputHash: e.InputHash})
}

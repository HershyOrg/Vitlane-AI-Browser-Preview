package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// ProcessStore contains persistence only. There is no Owner execution port,
// callback, provider preparation, claim, or business admission function here.
type ProcessStore interface {
	WithinTransaction(context.Context, func(context.Context) error) error
	LockOrder(context.Context, string) error
	AppendRequest(context.Context, procmsg.ActionRequest, time.Time) (bool, error)
	ReadRequestReceipt(context.Context, string, string) (procmsg.RequestReceipt, error)
	SaveRequestReceipt(context.Context, string, procmsg.RequestReceipt, time.Time) error
	LoadDecision(context.Context, string) (DecisionInput, error)
	SaveDecision(context.Context, DecisionInput, domain.Decision, []EffectWrite, time.Time) (DecisionOutcome, error)
	InsertEffect(context.Context, string, domain.EffectDraft, time.Time) (bool, error)
	AppendTimerEvent(context.Context, procmsg.ProcessEvent, time.Time) (bool, error)
	ListDueTimers(context.Context, time.Time, int) ([]DueTimer, error)
	ListOrdersWithUnappliedEvents(context.Context, int) ([]string, error)
}

type OrderProcessor struct {
	store                   ProcessStore
	clock                   sharedapp.Clock
	wakeMu                  sync.RWMutex
	effectsWake, eventsWake func()
}

func NewOrderProcessor(store ProcessStore, clock sharedapp.Clock) *OrderProcessor {
	return &OrderProcessor{store: store, clock: clock}
}
func (p *OrderProcessor) SetWakes(effects, events func()) {
	p.wakeMu.Lock()
	defer p.wakeMu.Unlock()
	p.effectsWake, p.eventsWake = effects, events
}
func (p *OrderProcessor) notify(effects, events bool) {
	p.wakeMu.RLock()
	ew, iw := p.effectsWake, p.eventsWake
	p.wakeMu.RUnlock()
	if effects && ew != nil {
		ew()
	}
	if events && iw != nil {
		iw()
	}
}

// Submit persists the request before an optional foreground reduction. A timeout
// after persistence returns RECEIVED, never a false rejection or a new identity.
func (p *OrderProcessor) Submit(ctx context.Context, r procmsg.ActionRequest) (procmsg.RequestReceipt, error) {
	if sharedapp.InTransaction(ctx) || r.Validate() != nil {
		return procmsg.RequestReceipt{}, procmsg.ErrRequestInvalid
	}
	inserted := false
	err := p.store.WithinTransaction(ctx, func(tx context.Context) error {
		if err := p.store.LockOrder(tx, r.AgencyOrderID); err != nil {
			return err
		}
		var err error
		inserted, err = p.store.AppendRequest(tx, r, p.clock.Now())
		return err
	})
	if err != nil {
		return procmsg.RequestReceipt{}, err
	}
	p.notify(false, inserted)
	_, reduceErr := p.ReduceWithLock(ctx, r.AgencyOrderID)
	result, readErr := p.store.ReadRequestReceipt(ctx, r.AgencyOrderID, r.ID)
	if readErr == nil {
		return result, nil
	}
	if reduceErr != nil || ctx.Err() != nil {
		return procmsg.RequestReceipt{SchemaVersion: "vitlane.order-process-receipt.v1", AgencyOrderID: r.AgencyOrderID, RequestID: r.ID, FlowID: procmsg.RequestFlowID(r.AgencyOrderID, r.ID), MerchantOrderID: r.MerchantOrderID, Kind: r.Kind, Outcome: "RECEIVED", Guidance: procmsg.Guidance{ReasonCode: "PROCESS_PENDING", CustomerAction: "WAIT", OperatorAction: "WAIT"}}, nil
	}
	return result, readErr
}

// ReduceWithLock commits at most one durable event; callers cannot inject an Owner result
// through an in-memory shortcut. Lock lifetime never includes Owner execution.
func (p *OrderProcessor) ReduceWithLock(ctx context.Context, orderID string) (outcome DecisionOutcome, err error) {
	if sharedapp.InTransaction(ctx) {
		return outcome, procmsg.ErrRequestInvalid
	}
	created := 0
	err = p.store.WithinTransaction(ctx, func(tx context.Context) error {
		if err := p.store.LockOrder(tx, orderID); err != nil {
			return err
		}
		input, err := p.store.LoadDecision(tx, orderID)
		if err != nil {
			return err
		}
		if input.Event == nil {
			return nil
		}
		now := p.clock.Now()
		decision, err := domain.Reduce(input.Process, *input.Event, now)
		if err != nil {
			return err
		}
		writes := make([]EffectWrite, 0, len(decision.Effects))
		for _, effect := range decision.Effects {
			inserted, err := p.store.InsertEffect(tx, orderID, effect, now)
			if err != nil {
				return err
			}
			if inserted {
				created++
			}
			writes = append(writes, EffectWrite{AgencyOrderID: orderID, EffectDraft: effect})
		}
		for _, receipt := range decision.Receipts {
			if err := p.store.SaveRequestReceipt(tx, orderID, receipt, now); err != nil {
				return err
			}
		}
		outcome, err = p.store.SaveDecision(tx, input, decision, writes, now)
		outcome.EffectsIssued = created
		return err
	})
	if err != nil {
		return DecisionOutcome{}, err
	}
	p.notify(created > 0, false)
	return
}

func (p *OrderProcessor) Receipt(ctx context.Context, order, id string) (procmsg.RequestReceipt, error) {
	return p.store.ReadRequestReceipt(ctx, order, id)
}

// FireDueTimers turns persisted deadlines into normal inputs. It neither executes
// effects nor clears unresolved permissions because a deadline or lease elapsed.
func (p *OrderProcessor) FireDueTimers(ctx context.Context, now time.Time, limit int) (int, error) {
	timers, err := p.store.ListDueTimers(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	var failures []error
	for _, timer := range timers {
		inserted := false
		err := p.store.WithinTransaction(ctx, func(tx context.Context) error {
			if err := p.store.LockOrder(tx, timer.AgencyOrderID); err != nil {
				return err
			}
			var err error
			inserted, err = p.store.AppendTimerEvent(tx, procmsg.ProcessEvent{AgencyOrderID: timer.AgencyOrderID, Source: procmsg.SourceTimer, Type: procmsg.EventTimerFired, Payload: procmsg.TimerFiredPayload{Kind: procmsg.TimerKindDelayRuleNotice}, DedupKey: procmsg.TimerDedupKey(timer.AgencyOrderID, procmsg.TimerKindDelayRuleNotice, timer.At), OccurredAt: now}, now)
			return err
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if inserted {
			n++
			p.notify(false, true)
		}
	}
	return n, errors.Join(failures...)
}

// Tick is the event consumer. A failed order is retried without holding up other
// orders; each successful decision has its own short transaction.
func (p *OrderProcessor) Tick(ctx context.Context) error {
	_, timerErr := p.FireDueTimers(ctx, p.clock.Now(), 50)
	orders, err := p.store.ListOrdersWithUnappliedEvents(ctx, 200)
	if err != nil {
		return errors.Join(timerErr, err)
	}
	// Independent order locks may wait independently. Each reduction is bounded
	// so a busy order cannot occupy a consumer indefinitely.
	results := make(chan error, len(orders))
	slots := make(chan struct{}, 4)
	for _, order := range orders {
		slots <- struct{}{}
		go func(id string) {
			defer func() { <-slots }()
			turn, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			// Drain without waiting for another poll, releasing the Order lock
			// and committing each event before reading the next one.
			for {
				outcome, err := p.ReduceWithLock(turn, id)
				if err != nil || !outcome.Applied {
					results <- err
					return
				}
			}
		}(order)
	}
	failures := []error{timerErr}
	for range orders {
		if err := <-results; err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *OrderProcessor) Requests(ctx context.Context, order string) ([]procmsg.RequestReceipt, error) {
	store, ok := p.store.(interface {
		ListRequestReceipts(context.Context, string) ([]procmsg.RequestReceipt, error)
	})
	if !ok {
		return nil, procmsg.ErrRequestInvalid
	}
	return store.ListRequestReceipts(ctx, order)
}

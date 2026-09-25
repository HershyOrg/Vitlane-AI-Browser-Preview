package queue

import (
	"context"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"sync"
	"time"
)

type Consumer interface {
	Accept(context.Context, procmsg.Delivery) error
}

// Worker schedules deliveries independently. It never holds an Order lock while
// waiting for an Owner and never serializes sibling MOs behind external I/O.
type Worker struct {
	queue       *Queue
	consumers   map[string]Consumer
	concurrency int
}

func NewWorker(q *Queue) *Worker {
	return &Worker{queue: q, consumers: map[string]Consumer{}, concurrency: 4}
}
func (w *Worker) Register(target string, c Consumer) {
	if target == "" || c == nil {
		panic("effect consumer required")
	}
	if _, exists := w.consumers[target]; exists {
		panic("duplicate effect owner")
	}
	w.consumers[target] = c
}
func (w *Worker) SetConcurrency(n int) { w.concurrency = max(1, n) }
func (w *Worker) Tick(ctx context.Context) error {
	targets := make([]string, 0, len(w.consumers))
	for target := range w.consumers {
		targets = append(targets, target)
	}
	deliveries, err := w.queue.Claim(ctx, targets, time.Now().UTC(), 50)
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	slots := make(chan struct{}, w.concurrency)
	for _, delivery := range deliveries {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			group.Wait()
			return ctx.Err()
		}
		group.Add(1)
		go func(d procmsg.Delivery) {
			defer group.Done()
			defer func() { <-slots }()
			accept := w.consumers[d.Effect.Target].Accept
			if d.Effect.Type == procmsg.EffectRetryDelivery {
				accept = w.queue.RetryAuthorized
			}
			if err := accept(ctx, d); err != nil && !errors.Is(err, procmsg.ErrStaleDelivery) {
				retryErr := w.queue.Retry(ctx, d, time.Now().UTC())
				mu.Lock()
				failures = append(failures, err, retryErr)
				mu.Unlock()
			}
		}(delivery)
	}
	group.Wait()
	return errors.Join(failures...)
}

package procmsg

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

type collectedEvent struct {
	event ProcessEvent
	now   time.Time
}
type collector struct {
	events []collectedEvent
	keys   map[string]bool
}
type collectorKey struct{}

// CollectEvents defers event-sequence locks until every Owner resource write has
// finished. The returned flush MUST run in the same transaction before commit.
// Nested calls join the root collector and cannot flush it early.
func CollectEvents(ctx context.Context, q Queryer) (context.Context, func() error) {
	if _, ok := ctx.Value(collectorKey{}).(*collector); ok {
		return ctx, func() error { return nil }
	}
	c := &collector{keys: map[string]bool{}}
	collected := context.WithValue(ctx, collectorKey{}, c)
	return collected, func() error {
		orders := map[string]bool{}
		for _, e := range c.events {
			orders[e.event.AgencyOrderID] = true
		}
		ids := make([]string, 0, len(orders))
		for id := range orders {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := LockOrderEventStream(ctx, q, id); err != nil {
				return err
			}
		}
		for _, e := range c.events {
			if _, err := AppendEvent(ctx, q, e.event, e.now); err != nil {
				return err
			}
		}
		return nil
	}
}

func collectEvent(ctx context.Context, event ProcessEvent, payload []byte, now time.Time) (bool, bool) {
	c, ok := ctx.Value(collectorKey{}).(*collector)
	if !ok {
		return false, false
	}
	if c.keys[event.DedupKey] {
		return true, false
	}
	// Freeze the payload now: later Owner mutations must not rewrite earlier facts.
	event.Payload = json.RawMessage(append([]byte(nil), payload...))
	c.keys[event.DedupKey] = true
	c.events = append(c.events, collectedEvent{event, now})
	return true, true
}

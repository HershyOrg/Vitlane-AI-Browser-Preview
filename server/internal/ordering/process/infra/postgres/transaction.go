package postgres

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

func (r *Store) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		collected, flush := procmsg.CollectEvents(tx, r.database.Queryer(tx))
		if err := fn(collected); err != nil {
			return err
		}
		return flush()
	})
}

func (r *Store) AppendTimerEvent(ctx context.Context, e procmsg.ProcessEvent, now time.Time) (bool, error) {
	return procmsg.AppendTimerEvent(ctx, r.database.Queryer(ctx), e, now)
}

package queue

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

// RetryAuthorized changes only transport scheduling after a reducer-issued
// Owner retry request. It never clears a lease, changes input, or reopens ACKed
// responsibility. Owner-owned reconciliation keeps its existing business key.
func (q *Queue) RetryAuthorized(ctx context.Context, d procmsg.Delivery) error {
	return q.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
		r, err := procmsg.ParsePayload[procmsg.ActionRequest](e.Payload)
		if err != nil || r.ReferenceID == e.ID || r.ID != e.RequestID || r.Kind != procmsg.RequestRetryEffect {
			return procmsg.ErrEffectInvalid
		}
		now := time.Now().UTC()
		_, err = q.database.Queryer(tx).ExecContext(tx, `UPDATE order_process_effects SET next_attempt_at=$5,updated_at=$5 WHERE id=$1 AND agency_order_id=$2 AND COALESCE(merchant_order_id::text,'')=$3 AND target=$4 AND delivery_state='PENDING' AND (lease_until IS NULL OR lease_until<$5)`, r.ReferenceID, e.AgencyOrderID, e.MerchantOrderID, e.Target, now)
		if err != nil {
			return err
		}
		return q.Report(tx, e, "SUCCEEDED", "RETRY_SCHEDULED", nil, "retry-scheduled", now)
	})
}

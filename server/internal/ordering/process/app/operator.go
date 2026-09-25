package app

import (
	"context"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

var ErrInterventionNotFound = errors.New("ORDER_PROCESS_INTERVENTION_NOT_FOUND")

// An unresolved obligation can be retried using its original identity. Operator
// intervention cannot assert a merchant/provider result or release permission.
func InterventionActions(string) []string { return []string{"RETRY"} }

type InterventionItem struct {
	EffectID        string           `json:"effectId"`
	AgencyOrderID   string           `json:"agencyOrderId"`
	MerchantOrderID string           `json:"merchantOrderId,omitempty"`
	Target          string           `json:"target"`
	Type            string           `json:"type"`
	LastErrorCode   string           `json:"lastErrorCode,omitempty"`
	AttemptCount    int              `json:"attemptCount"`
	Guidance        procmsg.Guidance `json:"guidance"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}
type interventionReader interface {
	ListInterventions(context.Context, int) ([]InterventionItem, error)
	CountInterventions(context.Context) (int, error)
	ReadEffectScope(context.Context, string) (procmsg.ProcessEffect, error)
}

func (p *OrderProcessor) ListInterventions(ctx context.Context, n int) ([]InterventionItem, error) {
	r, ok := p.store.(interventionReader)
	if !ok {
		return nil, ErrInterventionNotFound
	}
	return r.ListInterventions(ctx, min(100, max(1, n)))
}
func (p *OrderProcessor) CountInterventions(ctx context.Context) (int, error) {
	r, ok := p.store.(interventionReader)
	if !ok {
		return 0, ErrInterventionNotFound
	}
	return r.CountInterventions(ctx)
}
func (p *OrderProcessor) RetryEffect(ctx context.Context, id, actor, key string) (procmsg.RequestReceipt, error) {
	r, ok := p.store.(interventionReader)
	if !ok {
		return procmsg.RequestReceipt{}, ErrInterventionNotFound
	}
	e, err := r.ReadEffectScope(ctx, id)
	if err != nil {
		return procmsg.RequestReceipt{}, err
	}
	return p.Submit(ctx, procmsg.ActionRequest{ID: key, AgencyOrderID: e.AgencyOrderID, MerchantOrderID: e.MerchantOrderID, ActorID: actor, ActorRole: "OPERATOR", Kind: procmsg.RequestRetryEffect, ReferenceID: e.ID})
}

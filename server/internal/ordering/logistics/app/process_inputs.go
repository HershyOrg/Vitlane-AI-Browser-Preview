package app

import (
	"context"
	"encoding/json"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

type ActionInput struct {
	Carrier             string            `json:"carrier"`
	TrackingRef         string            `json:"trackingRef"`
	ExpectedUnitIDs     []string          `json:"expectedUnitIds"`
	Status              string            `json:"status"`
	Note                string            `json:"note"`
	OccurredAt          time.Time         `json:"occurredAt"`
	Exceptions          map[string]string `json:"exceptions"`
	Decision            string            `json:"decision"`
	State               string            `json:"state"`
	MerchantDisposition string            `json:"merchantDisposition"`
}
type processScopeReader interface {
	ActionScope(context.Context, procmsg.ActionRequest) (procmsg.ActionRequest, error)
}

func (s *Service) EnableProcessInputs(i procmsg.ActionInputs) { s.inputs = i }
func (s *Service) StageAction(ctx context.Context, r procmsg.ActionRequest, input any) (procmsg.ActionRequest, error) {
	repo, ok := s.repository.(processScopeReader)
	if !ok || s.inputs == nil {
		return r, procmsg.ErrRequestInvalid
	}
	r, err := repo.ActionScope(ctx, r)
	if err != nil {
		return r, err
	}
	return s.inputs.Stage(ctx, r, input)
}
func (c *EffectConsumer) applyAction(ctx context.Context, e procmsg.ProcessEffect) error {
	p, err := procmsg.ParsePayload[procmsg.OwnerActionContext](e.Payload)
	if err != nil || p.ID != e.RequestID || p.AgencyOrderID != e.AgencyOrderID || p.MerchantOrderID != e.MerchantOrderID || c.service.inputs == nil {
		return procmsg.ErrEffectInvalid
	}
	raw, err := c.service.inputs.Load(ctx, p.ActionRequest)
	if err != nil {
		return err
	}
	var in ActionInput
	if json.Unmarshal(raw, &in) != nil {
		return procmsg.ErrEffectInvalid
	}
	s := c.service
	switch p.Kind {
	case procmsg.RequestCreateShipment:
		if p.MerchantState != "PLACED" {
			return procmsg.ErrEffectInvalid
		}
		_, err = s.CreateShipment(ctx, e.MerchantOrderID, in.Carrier, in.TrackingRef, p.ActorID, in.ExpectedUnitIDs)
	case procmsg.RequestShipmentEvent:
		_, err = s.RecordEvent(ctx, p.ReferenceID, in.Status, in.Note, p.ActorID, in.OccurredAt)
	case procmsg.RequestConfirmDelivery:
		_, err = s.ConfirmDelivered(ctx, p.ReferenceID, p.ActorID, in.Exceptions)
	case procmsg.RequestResolveDelivery:
		_, _, err = s.ResolveDeliveryException(ctx, p.ReferenceID, in.Decision, in.Note, p.ActorID)
	case procmsg.RequestCreateReturn:
		_, err = s.CreateReturn(ctx, p.ReferenceID, in.Note, p.ActorID)
	case procmsg.RequestUpdateReturn:
		_, err = s.UpdateReturn(ctx, p.ReferenceID, in.State, in.MerchantDisposition, in.Note, p.ActorID)
	default:
		return procmsg.ErrEffectInvalid
	}
	return err
}

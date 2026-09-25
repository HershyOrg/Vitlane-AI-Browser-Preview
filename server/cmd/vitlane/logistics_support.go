package main

import (
	"context"
	"encoding/json"
	"strings"

	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	logisticsdomain "github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type logisticsSupportConversation struct {
	support  *supportapp.Service
	ordering *agencyorderapp.LifecycleService
}

func (p *logisticsSupportConversation) PublishDeliveryResolution(
	ctx context.Context,
	resolution logisticsdomain.DeliveryResolution,
	operatorUserID string,
) error {
	owner, err := p.ordering.ResolveOrderOwner(ctx, resolution.AgencyOrderID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"resolutionId":   resolution.ID,
		"expectedUnitId": resolution.ExpectedUnitID,
		"cause":          resolution.Cause, "decision": resolution.Decision,
		"publicRationale": resolution.Note, "decidedAt": resolution.CreatedAt,
		"actionPath": "/agencyOrder/" + resolution.AgencyOrderID,
	})
	_, _, err = p.support.PublishBusinessCard(ctx, supportapp.BusinessCardInput{
		TargetUserID: owner, Author: supportdomain.AuthorOperator,
		ActorUserID: strings.TrimSpace(operatorUserID), Body: resolution.Note,
		AgencyOrderID:  resolution.AgencyOrderID,
		IdempotencyKey: "support:delivery-resolution:" + resolution.ID,
		Type:           supportdomain.CardDeliveryResolution,
		ReferenceType:  supportdomain.ReferenceDelivery,
		ReferenceID:    resolution.ID, PublicPayload: payload,
	})
	return err
}

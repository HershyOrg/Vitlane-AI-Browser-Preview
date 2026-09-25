package adapters

import (
	"context"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
)

type OperatorShipping struct {
	shipping *accountapp.ShippingService
}

func NewOperatorShipping(shipping *accountapp.ShippingService) *OperatorShipping {
	return &OperatorShipping{shipping: shipping}
}

func (a *OperatorShipping) RevealShippingSnapshot(
	ctx context.Context,
	snapshotID string,
) (agencyapp.OperatorShippingAddress, error) {
	address, err := a.shipping.RevealSnapshot(ctx, snapshotID)
	if err != nil {
		return agencyapp.OperatorShippingAddress{}, err
	}
	return agencyapp.OperatorShippingAddress{
		RecipientName: address.RecipientName,
		AddressLine1:  address.AddressLine1, AddressLine2: address.AddressLine2,
		City: address.City, Region: address.Region, PostalCode: address.PostalCode,
		Country: address.Country, Phone: address.Phone,
	}, nil
}

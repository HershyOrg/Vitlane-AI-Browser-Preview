package adapters

import (
	"context"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
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
) (procurementapp.OperatorShippingAddress, error) {
	address, err := a.shipping.RevealSnapshot(ctx, snapshotID)
	if err != nil {
		return procurementapp.OperatorShippingAddress{}, err
	}
	return procurementapp.OperatorShippingAddress{
		RecipientName: address.RecipientName,
		AddressLine1:  address.AddressLine1, AddressLine2: address.AddressLine2,
		City: address.City, Region: address.Region, PostalCode: address.PostalCode,
		Country: address.Country, Phone: address.Phone,
	}, nil
}

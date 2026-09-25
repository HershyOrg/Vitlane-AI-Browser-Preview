package adapters

import (
	"context"
	"errors"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type Shipping struct{ service *accountapp.ShippingService }

func NewShipping(service *accountapp.ShippingService) *Shipping { return &Shipping{service: service} }

func (s *Shipping) DefaultAddress(ctx context.Context, userID string) (agencyapp.ShippingAddress, bool, error) {
	address, found, err := s.service.DefaultAddress(ctx, userID)
	if err != nil {
		return agencyapp.ShippingAddress{}, false, translateShippingError(err)
	}
	return mapAddress(address), found, nil
}

func (s *Shipping) CreateOrderSheetSnapshot(ctx context.Context, userID string, input agencyapp.ShippingAddress) (agencydomain.ShippingSnapshot, error) {
	snapshot, err := s.service.CreateOrderSheetSnapshot(ctx, userID, accountdomain.ShippingAddress{
		RecipientName: input.RecipientName, AddressLine1: input.AddressLine1,
		AddressLine2: input.AddressLine2, City: input.City, Region: input.Region,
		PostalCode: input.PostalCode, Country: input.Country, Phone: input.Phone,
	})
	if err != nil {
		return agencydomain.ShippingSnapshot{}, translateShippingError(err)
	}
	return agencydomain.ShippingSnapshot{
		SnapshotRef: snapshot.ID, SnapshotRevision: snapshot.ProfileVersion,
		SnapshotHash: snapshot.SnapshotHMAC, MaskedSummary: snapshot.MaskedSummary,
		Country: snapshot.Country,
	}, nil
}

func translateShippingError(err error) error {
	if errors.Is(err, accountdomain.ErrShippingProfileMissing) {
		return agencydomain.ErrShippingRequired
	}
	if errors.Is(err, accountdomain.ErrShippingAddressInvalid) {
		return agencydomain.ErrShippingInvalid
	}
	return err
}

func (s *Shipping) RevealSnapshot(ctx context.Context, userID, snapshotID string) (agencyapp.ShippingAddress, error) {
	address, err := s.service.RevealSnapshotForUser(ctx, userID, snapshotID)
	if err != nil {
		return agencyapp.ShippingAddress{}, err
	}
	return mapAddress(address), nil
}

func mapAddress(value accountdomain.ShippingAddress) agencyapp.ShippingAddress {
	return agencyapp.ShippingAddress{
		RecipientName: value.RecipientName, AddressLine1: value.AddressLine1,
		AddressLine2: value.AddressLine2, City: value.City, Region: value.Region,
		PostalCode: value.PostalCode, Country: value.Country, Phone: value.Phone,
	}
}

package domain

import (
	"strings"
	"time"
)

// AmazonObservation is the smallest dated record of what the provider showed
// for one saved ASIN: a title, an optional observed price and the time it was
// seen. Korean products already keep an equivalent observation so their cards
// render from the database; this gives Amazon the same rule with a narrower
// field set. Images, descriptions, sellers and AI-derived text are not stored,
// and a stored price is never a current quote: it is what was seen at
// ObservedAt and the screen always shows that time (ADR-0077).
type AmazonObservation struct {
	SchemaVersion string               `json:"schemaVersion"`
	VariantRef    SourceVariantRef     `json:"variantRef"`
	ProductURL    string               `json:"productUrl"`
	Title         string               `json:"title"`
	Price         VariantObservedPrice `json:"price"`
	ObservedAt    time.Time            `json:"observedAt"`
}

const AmazonObservationSchemaVersion = "vitlane.amazon-observation.v1"

func (o AmazonObservation) Validate() error {
	if o.SchemaVersion != AmazonObservationSchemaVersion || o.VariantRef.Validate() != nil ||
		o.VariantRef.Source != SourceAmazon || strings.TrimSpace(o.Title) == "" ||
		len(o.Title) > 2000 || o.ObservedAt.IsZero() {
		return ErrSourceReference
	}
	if err := o.VariantRef.ValidateExternalURL(o.ProductURL); err != nil {
		return err
	}
	if o.Price.Kind == "OBSERVED" {
		if o.Price.AmountMinor == nil || *o.Price.AmountMinor < 0 ||
			*o.Price.AmountMinor > 9007199254740991 || o.Price.Currency == "" {
			return ErrSourceReference
		}
		return nil
	}
	if o.Price.Kind != "UNKNOWN" || o.Price.AmountMinor != nil {
		return ErrSourceReference
	}
	return nil
}

// NewAmazonObservation keeps only the fields Vitlane stores for a saved ASIN.
func NewAmazonObservation(variant VariantObservation, title string, now time.Time) (AmazonObservation, error) {
	url, err := variant.VariantRef.ExternalURL()
	if err != nil {
		return AmazonObservation{}, err
	}
	observedAt := variant.ObservedAt
	if observedAt.IsZero() {
		observedAt = now
	}
	observation := AmazonObservation{
		SchemaVersion: AmazonObservationSchemaVersion, VariantRef: variant.VariantRef,
		ProductURL: url, Title: strings.TrimSpace(title), Price: variant.Price,
		ObservedAt: observedAt.UTC(),
	}
	if observation.Price.Kind != "OBSERVED" {
		observation.Price = VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_UNCONFIRMED"}
	}
	if err := observation.Validate(); err != nil {
		return AmazonObservation{}, err
	}
	return observation, nil
}

// PurchaseCheckSnapshotFromAmazonObservation lets the server fill the account
// list snapshot for an Amazon check the same way it does for Korean products.
func PurchaseCheckSnapshotFromAmazonObservation(o AmazonObservation) (PurchaseCheckSnapshot, error) {
	if err := o.Validate(); err != nil {
		return PurchaseCheckSnapshot{}, err
	}
	snapshot := PurchaseCheckSnapshot{ProductTitle: o.Title, Merchant: "Amazon", PriceUnknown: true}
	if o.Price.Kind == "OBSERVED" && o.Price.AmountMinor != nil {
		snapshot.PriceUnknown = false
		snapshot.PriceMinor = *o.Price.AmountMinor
		snapshot.Currency = o.Price.Currency
	}
	return NewPurchaseCheckSnapshot(snapshot)
}

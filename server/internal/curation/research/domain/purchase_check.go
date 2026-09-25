package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrPurchaseCheckSnapshotInvalid = errors.New("PURCHASE_CHECK_SNAPSHOT_INVALID")

// PurchaseCheckSnapshot is what the user saw when they marked a product as
// purchased. It is mutable self-report context for the account list, never a
// catalog, price, stock or order authority. A purchase check with a snapshot is
// still not an order confirmation.
type PurchaseCheckSnapshot struct {
	ProductTitle string `json:"productTitle"`
	VariantTitle string `json:"variantTitle,omitempty"`
	Merchant     string `json:"merchant,omitempty"`
	PriceMinor   int64  `json:"priceMinor"`
	PriceUnknown bool   `json:"priceUnknown"`
	Currency     string `json:"currency,omitempty"`
}

// NewPurchaseCheckSnapshot normalizes and validates a snapshot. An unknown
// price carries no meaningful amount; a known price needs an ISO currency.
func NewPurchaseCheckSnapshot(value PurchaseCheckSnapshot) (PurchaseCheckSnapshot, error) {
	value.ProductTitle = strings.TrimSpace(value.ProductTitle)
	value.VariantTitle = strings.TrimSpace(value.VariantTitle)
	value.Merchant = strings.TrimSpace(value.Merchant)
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	if value.PriceUnknown {
		value.PriceMinor = 0
	}
	if value.ProductTitle == "" || len(value.ProductTitle) > 2000 ||
		len(value.VariantTitle) > 240 || len(value.Merchant) > 240 || value.PriceMinor < 0 {
		return PurchaseCheckSnapshot{}, ErrPurchaseCheckSnapshotInvalid
	}
	if value.Currency != "" && !purchaseCheckCurrency(value.Currency) {
		return PurchaseCheckSnapshot{}, ErrPurchaseCheckSnapshotInvalid
	}
	if !value.PriceUnknown && value.Currency == "" {
		return PurchaseCheckSnapshot{}, ErrPurchaseCheckSnapshotInvalid
	}
	return value, nil
}

func purchaseCheckCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

// PurchaseCheckSnapshotFromObservation derives the Korean product snapshot from
// the observation the server already stores. No client value and no provider
// call is involved; an over-long seller name is dropped rather than truncated.
func PurchaseCheckSnapshotFromObservation(o ExternalProductObservation) (PurchaseCheckSnapshot, error) {
	snapshot := PurchaseCheckSnapshot{ProductTitle: o.Title, PriceUnknown: true}
	if o.Seller.Kind == "KNOWN" && len(strings.TrimSpace(o.Seller.Name)) <= 240 && utf8.ValidString(o.Seller.Name) {
		snapshot.Merchant = o.Seller.Name
	}
	if o.Price.Kind == "OBSERVED" && o.Price.AmountMinor != nil {
		snapshot.PriceUnknown = false
		snapshot.PriceMinor = *o.Price.AmountMinor
		snapshot.Currency = o.Price.Currency
	}
	return NewPurchaseCheckSnapshot(snapshot)
}

// PurchaseCheck is one account-scope row of the user's self-reported purchases.
// Exactly one of ProductRef (Korean product) or VariantRef (Amazon ASIN) is set.
// A nil Snapshot means the record predates snapshot capture; the subject and
// its original product page are still known from the reference.
type PurchaseCheck struct {
	CurationID  string                 `json:"curationId"`
	TargetID    string                 `json:"targetId,omitempty"`
	TargetTitle string                 `json:"targetTitle,omitempty"`
	CandidateID string                 `json:"candidateId"`
	ProductRef  *SourceProductRef      `json:"productRef,omitempty"`
	VariantRef  *SourceVariantRef      `json:"variantRef,omitempty"`
	ProductURL  string                 `json:"productUrl,omitempty"`
	Checked     bool                   `json:"checked"`
	Version     int64                  `json:"version"`
	RecordedAt  time.Time              `json:"recordedAt"`
	Evidence    string                 `json:"evidence"`
	Snapshot    *PurchaseCheckSnapshot `json:"snapshot"`
	SnapshotAt  *time.Time             `json:"snapshotAt,omitempty"`
}

// ExternalURL is the original product page of the checked subject, derived
// from the stored reference rather than from any stored or client-sent URL.
func (c PurchaseCheck) ExternalURL() (string, error) {
	if c.ProductRef != nil {
		return c.ProductRef.ExternalProductURL()
	}
	if c.VariantRef != nil {
		return c.VariantRef.ExternalURL()
	}
	return "", ErrSourceReference
}

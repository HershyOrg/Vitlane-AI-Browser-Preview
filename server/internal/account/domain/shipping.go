package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	ErrShippingAddressInvalid = errors.New("SHIPPING_ADDRESS_INVALID")
	ErrShippingProfileMissing = errors.New("SHIPPING_PROFILE_MISSING")
	ErrShippingSnapshotPurged = errors.New("SHIPPING_SNAPSHOT_PURGED")
	postalCodePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{1,15}$`)
)

type ShippingAddress struct {
	RecipientName string `json:"recipientName"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2,omitempty"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Phone         string `json:"phone,omitempty"`
}

func (a ShippingAddress) Normalize() (ShippingAddress, error) {
	a.RecipientName = strings.TrimSpace(a.RecipientName)
	a.AddressLine1 = strings.TrimSpace(a.AddressLine1)
	a.AddressLine2 = strings.TrimSpace(a.AddressLine2)
	a.City = strings.TrimSpace(a.City)
	a.Region = strings.TrimSpace(a.Region)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	a.Country = strings.ToUpper(strings.TrimSpace(a.Country))
	a.Phone = strings.TrimSpace(a.Phone)
	if a.RecipientName == "" || a.AddressLine1 == "" || a.City == "" ||
		a.Region == "" || len(a.Country) != 2 || !postalCodePattern.MatchString(a.PostalCode) ||
		len(a.RecipientName) > 120 || len(a.AddressLine1) > 200 ||
		len(a.AddressLine2) > 200 || len(a.City) > 100 || len(a.Region) > 100 ||
		len(a.Phone) > 40 {
		return ShippingAddress{}, ErrShippingAddressInvalid
	}
	return a, nil
}

func (a ShippingAddress) MaskedSummary() string {
	postal := strings.ReplaceAll(strings.ReplaceAll(a.PostalCode, " ", ""), "-", "")
	if len(postal) > 2 {
		postal = strings.Repeat("•", len(postal)-2) + postal[len(postal)-2:]
	} else {
		postal = "••"
	}
	return a.Country + " · " + postal
}

type ShippingProfile struct {
	ID               string     `json:"id"`
	UserID           string     `json:"userId"`
	Label            string     `json:"label"`
	Country          string     `json:"country"`
	MaskedSummary    string     `json:"maskedSummary"`
	EncryptedPayload []byte     `json:"-"`
	PayloadNonce     []byte     `json:"-"`
	KeyVersion       string     `json:"keyVersion"`
	PayloadHMAC      string     `json:"-"`
	Version          int64      `json:"version"`
	IsDefault        bool       `json:"isDefault"`
	RetiredAt        *time.Time `json:"retiredAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type ShippingSnapshot struct {
	ID               string     `json:"id"`
	UserID           string     `json:"userId"`
	SourceProfileID  string     `json:"sourceProfileId"`
	ProfileVersion   int64      `json:"profileVersion"`
	Country          string     `json:"country"`
	MaskedSummary    string     `json:"maskedSummary"`
	EncryptedPayload []byte     `json:"-"`
	PayloadNonce     []byte     `json:"-"`
	KeyVersion       string     `json:"keyVersion"`
	SnapshotHMAC     string     `json:"snapshotHmac"`
	PurgeAfter       *time.Time `json:"purgeAfter,omitempty"`
	PurgedAt         *time.Time `json:"purgedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

func (s ShippingSnapshot) Available() bool {
	return s.PurgedAt == nil && len(s.EncryptedPayload) > 0 && len(s.PayloadNonce) > 0
}

package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrLikedVariantInvalid = errors.New("LIKED_VARIANT_INVALID")

var likedVariantUUIDPatternV2 = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`,
)

// LikedVariantV2 is mutable user preference state, not a catalog or checkout
// authority. Price and titles only describe what the user liked at UpdatedAt.
type LikedVariantV2 struct {
	UserID       string    `json:"-"`
	CurationID   string    `json:"curationId"`
	CandidateID  string    `json:"candidateId"`
	VariantID    string    `json:"variantId"`
	ProductTitle string    `json:"productTitle"`
	VariantTitle string    `json:"variantTitle"`
	ProductURL   string    `json:"productUrl,omitempty"`
	Merchant     string    `json:"merchant"`
	PriceMinor   int64     `json:"priceMinor"`
	PriceUnknown bool      `json:"priceUnknown,omitempty"`
	Currency     string    `json:"currency"`
	TargetTitle  string    `json:"targetTitle"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func NewLikedVariantV2(value LikedVariantV2) (LikedVariantV2, error) {
	value.UserID = strings.TrimSpace(value.UserID)
	value.CurationID = strings.TrimSpace(value.CurationID)
	value.CandidateID = strings.TrimSpace(value.CandidateID)
	value.VariantID = strings.TrimSpace(value.VariantID)
	value.ProductTitle = strings.TrimSpace(value.ProductTitle)
	value.VariantTitle = strings.TrimSpace(value.VariantTitle)
	value.ProductURL = strings.TrimSpace(value.ProductURL)
	value.Merchant = strings.TrimSpace(value.Merchant)
	if value.PriceUnknown {
		value.PriceMinor = 0
	}
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	value.TargetTitle = strings.TrimSpace(value.TargetTitle)
	if !likedVariantUUIDPatternV2.MatchString(value.UserID) ||
		!likedVariantUUIDPatternV2.MatchString(value.CurationID) || value.CandidateID == "" ||
		len(value.CandidateID) > 512 || value.VariantID == "" || len(value.VariantID) > 512 ||
		value.ProductTitle == "" || len(value.ProductTitle) > 240 ||
		value.VariantTitle == "" || len(value.VariantTitle) > 240 ||
		value.Merchant == "" || len(value.Merchant) > 240 ||
		value.TargetTitle == "" || len(value.TargetTitle) > 240 ||
		len(value.Currency) != 3 || value.PriceMinor < 0 || value.UpdatedAt.IsZero() {
		return LikedVariantV2{}, ErrLikedVariantInvalid
	}
	if value.ProductURL != "" {
		parsed, err := url.Parse(value.ProductURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			len(value.ProductURL) > 2048 {
			return LikedVariantV2{}, ErrLikedVariantInvalid
		}
	}
	for _, character := range value.Currency {
		if character < 'A' || character > 'Z' {
			return LikedVariantV2{}, ErrLikedVariantInvalid
		}
	}
	return value, nil
}

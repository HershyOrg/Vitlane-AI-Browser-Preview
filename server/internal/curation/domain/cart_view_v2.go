package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrCartItemV2Invalid = errors.New("CART_ITEM_V2_INVALID")

// CartItemV2 is an intentionally fallible CartView item. It remembers what
// the user selected and the price they saw, but it is not an Offer, a Quote or
// proof that the item is still purchasable. Exactness begins only when the
// user explicitly prepares an AgencyOrder.
type CartItemV2 struct {
	ID                   string    `json:"id"`
	CandidateID          string    `json:"candidateId"`
	ProductTitleSnapshot string    `json:"productTitleSnapshot"`
	ProductURL           string    `json:"productUrl,omitempty"`
	VariantID            string    `json:"variantId"`
	VariantTitleSnapshot string    `json:"variantTitleSnapshot"`
	SelectedOptions      []string  `json:"selectedOptions"`
	PreviewPriceMinor    int64     `json:"previewPriceMinor"`
	PreviewCurrency      string    `json:"previewCurrency"`
	Quantity             int       `json:"quantity"`
	ObservedAt           time.Time `json:"observedAt"`
	AddedAt              time.Time `json:"addedAt"`
}

func NewCartItemV2(value CartItemV2) (CartItemV2, error) {
	value.ID = strings.TrimSpace(value.ID)
	value.CandidateID = strings.TrimSpace(value.CandidateID)
	value.ProductTitleSnapshot = strings.TrimSpace(value.ProductTitleSnapshot)
	value.ProductURL = strings.TrimSpace(value.ProductURL)
	value.VariantID = strings.TrimSpace(value.VariantID)
	value.VariantTitleSnapshot = strings.TrimSpace(value.VariantTitleSnapshot)
	value.PreviewCurrency = strings.ToUpper(strings.TrimSpace(value.PreviewCurrency))
	for index := range value.SelectedOptions {
		value.SelectedOptions[index] = strings.TrimSpace(value.SelectedOptions[index])
	}
	if err := value.Validate(); err != nil {
		return CartItemV2{}, err
	}
	return value, nil
}

func (value CartItemV2) Validate() error {
	if value.ID == "" || value.CandidateID == "" ||
		value.ProductTitleSnapshot == "" || value.VariantID == "" ||
		value.VariantTitleSnapshot == "" || len(value.PreviewCurrency) != 3 ||
		value.PreviewPriceMinor < 0 || value.Quantity < 1 || value.Quantity > 99 ||
		value.ObservedAt.IsZero() || value.AddedAt.IsZero() ||
		value.AddedAt.Before(value.ObservedAt) {
		return ErrCartItemV2Invalid
	}
	for _, option := range value.SelectedOptions {
		if strings.TrimSpace(option) == "" {
			return ErrCartItemV2Invalid
		}
	}
	return nil
}

// CartItemV2InvalidField는 Validate와 같은 정규화 기준으로 첫 실패 필드를
// 돌려준다. 간헐적 클라이언트 결함(미수화 후보 데이터가 그대로 제출되는 경우)의
// 원인 필드를 오류 reason만으로 특정하기 위한 진단 전용 함수다.
func CartItemV2InvalidField(value CartItemV2) string {
	switch {
	case strings.TrimSpace(value.ID) == "":
		return "ID"
	case strings.TrimSpace(value.CandidateID) == "":
		return "CANDIDATE"
	case strings.TrimSpace(value.ProductTitleSnapshot) == "":
		return "PRODUCT_TITLE"
	case strings.TrimSpace(value.VariantID) == "":
		return "VARIANT"
	case strings.TrimSpace(value.VariantTitleSnapshot) == "":
		return "VARIANT_TITLE"
	case len(strings.ToUpper(strings.TrimSpace(value.PreviewCurrency))) != 3:
		return "CURRENCY"
	case value.PreviewPriceMinor < 0:
		return "PRICE"
	case value.Quantity < 1 || value.Quantity > 99:
		return "QUANTITY"
	case value.ObservedAt.IsZero():
		return "OBSERVED_AT"
	case value.AddedAt.IsZero() || value.AddedAt.Before(value.ObservedAt):
		return "ADDED_AT"
	default:
		return "OPTIONS"
	}
}

// CartViewV2 has no checkout lifecycle. It is merely the current list of
// fallible item selections that the user may later submit to Order preparation.
type CartViewV2 struct {
	CurationID string       `json:"curationId"`
	Items      []CartItemV2 `json:"items"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

func NewCartViewV2(curationID string, items []CartItemV2, updatedAt time.Time) (CartViewV2, error) {
	view := CartViewV2{
		CurationID: strings.TrimSpace(curationID),
		Items:      append([]CartItemV2(nil), items...),
		UpdatedAt:  updatedAt,
	}
	if view.CurationID == "" || view.UpdatedAt.IsZero() {
		return CartViewV2{}, ErrCartItemV2Invalid
	}
	seen := make(map[string]struct{}, len(view.Items))
	for _, item := range view.Items {
		if item.Validate() != nil {
			return CartViewV2{}, ErrCartItemV2Invalid
		}
		identity := item.CandidateID + "\x00" + item.VariantID
		if _, exists := seen[identity]; exists {
			return CartViewV2{}, ErrCartItemV2Invalid
		}
		seen[identity] = struct{}{}
	}
	return view, nil
}

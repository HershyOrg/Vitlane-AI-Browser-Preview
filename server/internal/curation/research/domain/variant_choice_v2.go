package domain

// VariantRefV2 identifies a variant observed from a verified storefront read.
// It is a selection reference only; it is not an offer, price guarantee, or
// order authorization.
type VariantRefV2 struct {
	Provider   string
	MerchantID string
	ProductID  string
	VariantID  string
}

// VariantOptionSelectionV2 records the option labels the user selected for a
// CartView item. The values are revalidated by the order-preparation lookup.
type VariantOptionSelectionV2 struct {
	Name  string
	Value string
}

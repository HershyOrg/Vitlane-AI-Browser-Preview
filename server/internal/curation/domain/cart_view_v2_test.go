package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCartItemV2IsASelectionSnapshotNotAnExactOffer(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	item, err := NewCartItemV2(CartItemV2{
		ID: "draft-1", CandidateID: "candidate-1",
		ProductTitleSnapshot: "Commuter Pack", ProductURL: "https://shop.example/products/pack",
		VariantID: "gid://shopify/ProductVariant/1", VariantTitleSnapshot: "Black",
		SelectedOptions: []string{"Black"}, PreviewPriceMinor: 7600,
		PreviewCurrency: "usd", Quantity: 1, ObservedAt: now, AddedAt: now,
	})
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	if item.PreviewCurrency != "USD" || item.VariantID == "" {
		t.Fatalf("item=%+v", item)
	}
	// No token, offer hash, offer expiry or resolved-offer identity is part of
	// the Cart contract. Fresh provider facts are a preparation concern.
}

func TestCartItemV2RejectsMissingVariantOrInvalidQuantity(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, err := NewCartItemV2(CartItemV2{
		ID: "draft-1", CandidateID: "candidate-1", ProductTitleSnapshot: "Pack",
		VariantTitleSnapshot: "Black", PreviewCurrency: "USD", Quantity: 0,
		ObservedAt: now, AddedAt: now,
	})
	if !errors.Is(err, ErrCartItemV2Invalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestCartViewV2AllowsMultipleVariantsForCandidateAndRejectsDuplicateVariant(t *testing.T) {
	now := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	base := CartItemV2{
		ID: "item-1", CandidateID: "candidate-1", ProductTitleSnapshot: "Pack",
		VariantID: "gid://shopify/ProductVariant/1", VariantTitleSnapshot: "Black",
		PreviewCurrency: "USD", PreviewPriceMinor: 7600, Quantity: 1,
		ObservedAt: now, AddedAt: now,
	}
	second := base
	second.ID = "item-2"
	second.VariantID = "gid://shopify/ProductVariant/2"
	second.VariantTitleSnapshot = "Blue"
	if _, err := NewCartViewV2("curation-1", []CartItemV2{base, second}, now); err != nil {
		t.Fatalf("multiple variants should be valid: %v", err)
	}
	duplicate := base
	duplicate.ID = "item-3"
	if _, err := NewCartViewV2("curation-1", []CartItemV2{base, duplicate}, now); !errors.Is(err, ErrCartItemV2Invalid) {
		t.Fatalf("duplicate err=%v", err)
	}
}

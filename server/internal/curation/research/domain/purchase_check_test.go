package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewPurchaseCheckSnapshotNormalizesAndRejects(t *testing.T) {
	known, err := NewPurchaseCheckSnapshot(PurchaseCheckSnapshot{ProductTitle: "  Q20i Headphones ", VariantTitle: " Black ", Merchant: " Amazon ", PriceMinor: 3997, Currency: "usd"})
	if err != nil || known.ProductTitle != "Q20i Headphones" || known.VariantTitle != "Black" || known.Merchant != "Amazon" || known.Currency != "USD" || known.PriceMinor != 3997 || known.PriceUnknown {
		t.Fatalf("normalized snapshot: %#v %v", known, err)
	}
	unknown, err := NewPurchaseCheckSnapshot(PurchaseCheckSnapshot{ProductTitle: "Pen", PriceMinor: 99, PriceUnknown: true})
	if err != nil || unknown.PriceMinor != 0 || unknown.Currency != "" {
		t.Fatalf("unknown price keeps a meaningless amount: %#v %v", unknown, err)
	}
	for name, value := range map[string]PurchaseCheckSnapshot{
		"empty title":         {PriceUnknown: true},
		"blank title":         {ProductTitle: "   ", PriceUnknown: true},
		"long title":          {ProductTitle: strings.Repeat("a", 2001), PriceUnknown: true},
		"long variant":        {ProductTitle: "Pen", VariantTitle: strings.Repeat("b", 241), PriceUnknown: true},
		"long merchant":       {ProductTitle: "Pen", Merchant: strings.Repeat("c", 241), PriceUnknown: true},
		"negative price":      {ProductTitle: "Pen", PriceMinor: -1, Currency: "USD"},
		"known price no code": {ProductTitle: "Pen", PriceMinor: 1},
		"bad currency":        {ProductTitle: "Pen", PriceMinor: 1, Currency: "US"},
		"digits currency":     {ProductTitle: "Pen", PriceMinor: 1, Currency: "U5D"},
		"unknown bad code":    {ProductTitle: "Pen", PriceUnknown: true, Currency: "dollars"},
	} {
		if _, err := NewPurchaseCheckSnapshot(value); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestPurchaseCheckSnapshotFromObservation(t *testing.T) {
	amount := int64(64000)
	observed := ExternalProductObservation{Title: "라미 사파리 만년필", Price: VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "KRW"}, Seller: ObservedSeller{Kind: "KNOWN", ID: "s1", Name: "쿠팡"}}
	snapshot, err := PurchaseCheckSnapshotFromObservation(observed)
	if err != nil || snapshot.ProductTitle != "라미 사파리 만년필" || snapshot.PriceMinor != 64000 || snapshot.Currency != "KRW" || snapshot.PriceUnknown || snapshot.Merchant != "쿠팡" {
		t.Fatalf("observed snapshot: %#v %v", snapshot, err)
	}
	unknown := ExternalProductObservation{Title: "Pen", Price: VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "NO_PRICE"}, Seller: ObservedSeller{Kind: "KNOWN", ID: "s2", Name: strings.Repeat("가", 100)}}
	snapshot, err = PurchaseCheckSnapshotFromObservation(unknown)
	if err != nil || !snapshot.PriceUnknown || snapshot.PriceMinor != 0 || snapshot.Currency != "" || snapshot.Merchant != "" {
		t.Fatalf("unknown price or long seller not handled: %#v %v", snapshot, err)
	}
	if _, err = PurchaseCheckSnapshotFromObservation(ExternalProductObservation{Title: " "}); err == nil {
		t.Fatal("blank observation accepted")
	}
}

func TestPurchaseCheckExternalURL(t *testing.T) {
	now := time.Now()
	korean := PurchaseCheck{ProductRef: &SourceProductRef{Source: SourceCoupang, Marketplace: "KR", ProductID: "8825648110"}, RecordedAt: now}
	if url, err := korean.ExternalURL(); err != nil || url != "https://www.coupang.com/vp/products/8825648110" {
		t.Fatalf("korean url: %s %v", url, err)
	}
	amazon := PurchaseCheck{VariantRef: &SourceVariantRef{Source: SourceAmazon, Marketplace: "US", ASIN: "B012345678"}}
	if url, err := amazon.ExternalURL(); err != nil || url != "https://www.amazon.com/dp/B012345678" {
		t.Fatalf("amazon url: %s %v", url, err)
	}
	if _, err := (PurchaseCheck{}).ExternalURL(); err == nil {
		t.Fatal("subject-less record produced a URL")
	}
}

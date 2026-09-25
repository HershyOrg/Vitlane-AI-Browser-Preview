package domain

import (
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	"testing"
	"time"
)

func TestDealPriceUsesDisplayedPriceWithoutDelivery(t *testing.T) {
	now := time.Now()
	price, limit, shipping := int64(9900), int64(10000), int64(500)
	p := DealProduct{Country: "KR", Currency: "KRW", PriceMinor: &price, ExpiresAt: now.Add(time.Hour)}
	terms := c.SubscriptionTerms{Country: "KR", Currency: "KRW", MaximumMinor: &limit, ExpiresAt: now.Add(time.Hour)}
	if !DealFilter(p, terms, now) {
		t.Fatal("missing shipping rejected displayed price")
	}
	p.ShippingMinor = &shipping
	if DealFilter(p, terms, now) {
		t.Fatal("known delivery was not counted")
	}
	p.ShippingMinor = nil
	p.PriceMinor = nil
	if !DealFilter(p, terms, now) {
		t.Fatal("unknown price should remain eligible for coarse semantic matching")
	}
	p.Country = "US"
	if DealFilter(p, terms, now) {
		t.Fatal("country mismatch")
	}
	p.Country = "KR"
	p.Title = "Refurbished headphones"
	terms.Criteria.Exclusions = []string{"refurbished"}
	if DealFilter(p, terms, now) {
		t.Fatal("excluded product accepted")
	}
}
func TestTermsCacheIgnoresDeadlineButPreservesConditions(t *testing.T) {
	a := c.SubscriptionTerms{Country: "KR", Keywords: []string{"headphones"}, ExpiresAt: time.Now()}
	b := a
	b.ExpiresAt = b.ExpiresAt.Add(time.Hour)
	if TermsHash(a) != TermsHash(b) {
		t.Fatal("same criteria did not share matching")
	}
	b.Country = "US"
	if TermsHash(a) == TermsHash(b) {
		t.Fatal("different country shared a judgment")
	}
}

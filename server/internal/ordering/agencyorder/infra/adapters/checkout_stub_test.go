package adapters

import (
	"context"
	"testing"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func TestCheckoutStubProducesReadyDomesticPreflight(t *testing.T) {
	now := time.Now().UTC()
	stub, err := NewCheckoutStub("test", "http://127.0.0.1/.well-known/ucp-agent.json", "profile-hash")
	if err != nil {
		t.Fatal(err)
	}
	stub.now = func() time.Time { return now }
	line := agencydomain.ExactLine{
		LineID: "line-1", ShopDomain: "shop.example", Quantity: 1,
		LineSubtotal: agencydomain.Money{AmountMinor: 1250, Currency: "USD"},
	}
	explored, err := stub.Explore(context.Background(), agencyapp.MerchantRequest{
		OrderSheetSessionID: "sheet-1", ShopDomain: "shop.example", Lines: []agencydomain.ExactLine{line},
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := stub.Preflight(context.Background(), agencyapp.MerchantRequest{
		OrderSheetSessionID: "sheet-1", ShopDomain: "shop.example",
		Lines: []agencydomain.ExactLine{line}, Existing: &explored,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := agencydomain.OrderSheetSession{
		ID: "sheet-1", SourceCart: agencydomain.SourceCartSnapshot{CartID: "cart-1", CartVersion: 1, SnapshotHash: "cart-hash"},
		Lines:             []agencydomain.ExactLine{line},
		ShippingAddress:   agencydomain.ShippingSnapshot{SnapshotRef: "shipping-1", SnapshotHash: "shipping-hash", Country: "US"},
		MerchantCheckouts: []agencydomain.MerchantCheckout{explored}, Version: 1,
		State: agencydomain.SessionEditing, CreatedAt: now, ExpiresAt: now.Add(20 * time.Minute),
	}
	if err := session.SelectRail(agencydomain.PaymentRailTVITUSD); err != nil {
		t.Fatal(err)
	}
	if err := session.ApplyPreflight([]agencydomain.MerchantCheckout{preflight}, now); err != nil {
		t.Fatalf("apply stub preflight: %v; session=%#v checkout=%#v", err, session, preflight)
	}
	if session.State != agencydomain.SessionReady || session.MerchantCheckouts[0].TaxTotal.AmountMinor != 161 {
		t.Fatalf("unexpected ready snapshot: %#v", session)
	}
}

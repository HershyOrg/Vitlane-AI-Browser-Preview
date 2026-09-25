package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type recordedShopifyCallV2 struct {
	api, operation, outcome string
	retryAfter              int
	closed                  bool
}

type shopifyLedgerFakeV2 struct {
	calls  []*recordedShopifyCallV2
	denied error
}

func (ledger *shopifyLedgerFakeV2) ReserveProviderCall(_ context.Context, api, operation string, _ time.Time) (string, error) {
	if ledger.denied != nil {
		return "", ledger.denied
	}
	ledger.calls = append(ledger.calls, &recordedShopifyCallV2{api: api, operation: operation})
	return string(rune('a' + len(ledger.calls) - 1)), nil
}

func (ledger *shopifyLedgerFakeV2) CompleteProviderCall(_ context.Context, id, outcome string, _, retryAfter int, _ time.Time) error {
	call := ledger.calls[int(id[0]-'a')]
	call.outcome, call.retryAfter, call.closed = outcome, retryAfter, true
	return nil
}

type shopifyGatewayFakeV2 struct {
	searchErr     error
	lookupCalls   int
	providerCalls int
}

func (gateway *shopifyGatewayFakeV2) SearchProducts(context.Context, CatalogProductSearchRequest) (CatalogProductSearchResult, error) {
	return CatalogProductSearchResult{Outcome: CatalogOutcomeSuccess}, gateway.searchErr
}
func (*shopifyGatewayFakeV2) LookupMedia(context.Context, CatalogMediaLookupRequest) (CatalogMediaLookupResult, error) {
	return CatalogMediaLookupResult{}, nil
}
func (gateway *shopifyGatewayFakeV2) LookupOffers(context.Context, CatalogOfferLookupRequest) (CatalogOfferLookupResult, error) {
	gateway.lookupCalls++
	return CatalogOfferLookupResult{ProviderCallCount: gateway.providerCalls, Outcome: CatalogOutcomeSuccess}, nil
}

// Every Shopify catalog call lands in the operator's ledger: a research search,
// a page's price lookup, and each provider call behind one logical lookup.
func TestAccountedCatalogGatewayV2RecordsEveryShopifyCall(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)}
	ledger := &shopifyLedgerFakeV2{}
	inner := &shopifyGatewayFakeV2{providerCalls: 3}
	gateway := NewAccountedCatalogGatewayV2(inner, ledger, clock)

	if _, err := gateway.SearchProducts(context.Background(), CatalogProductSearchRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.LookupOffers(context.Background(), CatalogOfferLookupRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.LookupMedia(context.Background(), CatalogMediaLookupRequest{}); err != nil {
		t.Fatal(err)
	}
	want := []recordedShopifyCallV2{
		{api: "SHOPIFY_UCP", operation: "SEARCH", outcome: "SUCCESS", closed: true},
		{api: "SHOPIFY_UCP", operation: "DETAIL", outcome: "SUCCESS", closed: true},
		{api: "SHOPIFY_UCP", operation: "DETAIL", outcome: "SUCCESS", closed: true},
		{api: "SHOPIFY_UCP", operation: "DETAIL", outcome: "SUCCESS", closed: true},
		{api: "SHOPIFY_UCP", operation: "DETAIL", outcome: "SUCCESS", closed: true},
	}
	if len(ledger.calls) != len(want) {
		t.Fatalf("one search, three provider calls behind one lookup, one media lookup: got %d rows", len(ledger.calls))
	}
	for index, call := range ledger.calls {
		if *call != want[index] {
			t.Fatalf("row %d = %+v, want %+v", index, *call, want[index])
		}
	}
}

// A failed call is closed with the reason the adapter classified, so the
// operator's failure breakdown names what went wrong; a cancelled request still
// closes its row instead of leaving it running.
func TestAccountedCatalogGatewayV2ClosesFailedAndCancelledCalls(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)}
	ledger := &shopifyLedgerFakeV2{}
	limited := fault.New(fault.RateLimited, string(CatalogFailureRateLimited), true)
	limited.RetryAfter = 1500 * time.Millisecond
	gateway := NewAccountedCatalogGatewayV2(&shopifyGatewayFakeV2{searchErr: limited}, ledger, clock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gateway.SearchProducts(ctx, CatalogProductSearchRequest{}); !errors.Is(err, limited) {
		t.Fatalf("the adapter's failure is returned unchanged: %v", err)
	}
	if call := ledger.calls[0]; !call.closed || call.outcome != string(CatalogFailureRateLimited) || call.retryAfter != 2 {
		t.Fatalf("failed call = %+v", *call)
	}

	unclassified := NewAccountedCatalogGatewayV2(&shopifyGatewayFakeV2{searchErr: errors.New("boom")}, ledger, clock)
	_, _ = unclassified.SearchProducts(context.Background(), CatalogProductSearchRequest{})
	if call := ledger.calls[1]; call.outcome != "SHOPIFY_CALL_FAILED" {
		t.Fatalf("an unclassified failure still gets a safe code: %+v", *call)
	}
}

// The operator's switch is honoured before Shopify is called at all.
func TestAccountedCatalogGatewayV2HonoursTheOperatorSwitch(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)}
	disabled := fault.New(fault.ProviderUnavailable, "CATALOG_API_DISABLED", false)
	inner := &shopifyGatewayFakeV2{providerCalls: 1}
	gateway := NewAccountedCatalogGatewayV2(inner, &shopifyLedgerFakeV2{denied: disabled}, clock)
	if _, err := gateway.LookupOffers(context.Background(), CatalogOfferLookupRequest{}); !errors.Is(err, disabled) {
		t.Fatalf("want the ledger's denial, got %v", err)
	}
	if inner.lookupCalls != 0 {
		t.Fatal("Shopify was called while switched off")
	}
	if NewAccountedCatalogGatewayV2(inner, nil, clock) != liveCatalogGatewayV2(inner) {
		t.Fatal("without a ledger the gateway is used as it is")
	}
	if definition, ok := CatalogAPIDefinitionFor(ShopifyCatalogAPIID); !ok || definition.APIProvider != "Shopify" || OperatorCatalogAPIs()[0].ID != ShopifyCatalogAPIID {
		t.Fatalf("Shopify is a listed API product: %+v", definition)
	}
}

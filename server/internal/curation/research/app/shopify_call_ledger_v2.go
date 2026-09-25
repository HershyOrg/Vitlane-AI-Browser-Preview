package app

import (
	"context"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// CatalogCallLedgerV2 is the part of the operator's API ledger a gateway
// needs: open a call (which also honours the operator's On/Off switch) and
// close it with what happened.
type CatalogCallLedgerV2 interface {
	ReserveProviderCall(context.Context, string, string, time.Time) (string, error)
	CompleteProviderCall(context.Context, string, string, int, int, time.Time) error
}

// accountedCatalogGatewayV2 records every Shopify catalog call in the same
// ledger as the other research APIs. It wraps whichever Shopify gateway is
// wired — the live UCP adapter or the local review stub — so the numbers the
// operator sees are counted the same way everywhere.
type accountedCatalogGatewayV2 struct {
	inner  liveCatalogGatewayV2
	ledger CatalogCallLedgerV2
	clock  sharedapp.Clock
}

// NewAccountedCatalogGatewayV2 returns inner unchanged when there is no ledger
// (catalog research without the operator tables, unit tests).
func NewAccountedCatalogGatewayV2(inner liveCatalogGatewayV2, ledger CatalogCallLedgerV2, clock sharedapp.Clock) liveCatalogGatewayV2 {
	if inner == nil || ledger == nil || clock == nil {
		return inner
	}
	return &accountedCatalogGatewayV2{inner: inner, ledger: ledger, clock: clock}
}

func (gateway *accountedCatalogGatewayV2) SearchProducts(ctx context.Context, request CatalogProductSearchRequest) (CatalogProductSearchResult, error) {
	call, err := gateway.ledger.ReserveProviderCall(ctx, ShopifyCatalogAPIID, "SEARCH", gateway.clock.Now())
	if err != nil {
		return CatalogProductSearchResult{}, err
	}
	result, err := gateway.inner.SearchProducts(ctx, request)
	gateway.complete(ctx, call, err, 1, "SEARCH")
	return result, err
}

func (gateway *accountedCatalogGatewayV2) LookupMedia(ctx context.Context, request CatalogMediaLookupRequest) (CatalogMediaLookupResult, error) {
	call, err := gateway.ledger.ReserveProviderCall(ctx, ShopifyCatalogAPIID, "DETAIL", gateway.clock.Now())
	if err != nil {
		return CatalogMediaLookupResult{}, err
	}
	result, err := gateway.inner.LookupMedia(ctx, request)
	gateway.complete(ctx, call, err, 1, "DETAIL")
	return result, err
}

func (gateway *accountedCatalogGatewayV2) LookupOffers(ctx context.Context, request CatalogOfferLookupRequest) (CatalogOfferLookupResult, error) {
	call, err := gateway.ledger.ReserveProviderCall(ctx, ShopifyCatalogAPIID, "DETAIL", gateway.clock.Now())
	if err != nil {
		return CatalogOfferLookupResult{}, err
	}
	result, err := gateway.inner.LookupOffers(ctx, request)
	// One logical lookup may have been several provider calls (batches of 50
	// identifiers, a split retry). The adapter reports how many it made.
	gateway.complete(ctx, call, err, result.ProviderCallCount, "DETAIL")
	return result, err
}

// complete closes the opened call and, when the adapter reports that it made
// more provider calls than one, records the others too. Closing must survive a
// cancelled request: a call that is never closed would look like it is still
// running and would hold a concurrency slot until its lease expires.
func (gateway *accountedCatalogGatewayV2) complete(ctx context.Context, call string, failure error, providerCalls int, operation string) {
	outcome, retryAfter := "SUCCESS", 0
	if failure != nil {
		outcome = "SHOPIFY_CALL_FAILED"
		if classified, ok := fault.As(failure); ok {
			if classified.Reason != "" {
				outcome = classified.Reason
			}
			if classified.RetryAfter > 0 {
				retryAfter = int((classified.RetryAfter + time.Second - 1) / time.Second)
			}
		}
	}
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	now := gateway.clock.Now()
	_ = gateway.ledger.CompleteProviderCall(finalCtx, call, outcome, 0, retryAfter, now)
	for extra := 1; extra < providerCalls; extra++ {
		id, err := gateway.ledger.ReserveProviderCall(finalCtx, ShopifyCatalogAPIID, operation, now)
		if err != nil {
			return
		}
		_ = gateway.ledger.CompleteProviderCall(finalCtx, id, outcome, 0, 0, now)
	}
}

func (gateway *accountedCatalogGatewayV2) DescribeDiscovery() DiscoveryDescriptor {
	return discoveryDescriptor(gateway.inner, EnglishDiscoveryDescriptor("SHOPIFY"))
}

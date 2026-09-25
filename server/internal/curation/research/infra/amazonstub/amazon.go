// Package amazonstub replays a local review fixture. It has no HTTP client,
// credentials, quota reservations, or fallback to a live provider.
package amazonstub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	app "github.com/vitlane/vitlane/server/internal/curation/research/app"
	domain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type Product struct {
	ASIN           string                    `json:"asin"`
	Title          string                    `json:"title"`
	PriceMinor     *int64                    `json:"priceMinor"`
	SnapshotAt     time.Time                 `json:"snapshotAt"`
	Provenance     string                    `json:"provenance"`
	Variants       []app.AmazonVariantOption `json:"variants"`
	RelationStatus string                    `json:"relationStatus"`
	Truncated      bool                      `json:"truncated"`
}
type Fixture struct {
	SchemaVersion string    `json:"schemaVersion"`
	CapturedAt    time.Time `json:"capturedAt"`
	RecoveryNotes []string  `json:"recoveryNotes"`
	Products      []Product `json:"products"`
}
type Gateway struct {
	products map[string]Product
	order    []string
}

func New(path string) (*Gateway, error) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 2<<20 {
		return nil, fmt.Errorf("AMAZON_STUB_FIXTURE_UNAVAILABLE")
	}
	var f Fixture
	if json.Unmarshal(raw, &f) != nil || f.SchemaVersion != "vitlane.amazon-local-stub.v1" || len(f.Products) == 0 {
		return nil, fmt.Errorf("AMAZON_STUB_FIXTURE_INVALID")
	}
	g := &Gateway{products: map[string]Product{}}
	for _, p := range f.Products {
		ref := domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: p.ASIN}
		if ref.Validate() != nil || p.Title == "" || p.Provenance == "" || p.SnapshotAt.IsZero() || (p.PriceMinor != nil && (*p.PriceMinor < 0 || *p.PriceMinor > 9007199254740991)) {
			return nil, fmt.Errorf("AMAZON_STUB_FIXTURE_INVALID")
		}
		if _, exists := g.products[p.ASIN]; exists {
			return nil, fmt.Errorf("AMAZON_STUB_FIXTURE_INVALID")
		}
		g.products[p.ASIN] = p
		g.order = append(g.order, p.ASIN)
	}
	for _, p := range g.products {
		for _, v := range p.Variants {
			if _, ok := g.products[v.ASIN]; !ok {
				return nil, fmt.Errorf("AMAZON_STUB_VARIANT_MISSING")
			}
		}
	}
	return g, nil
}

func (g *Gateway) LookupAmazon(ctx context.Context, ref domain.SourceVariantRef) (app.AmazonProductDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.AmazonProductDetail{}, err
	}
	if ref.Validate() != nil || ref.Source != domain.SourceAmazon {
		return app.AmazonProductDetail{}, fault.New(fault.InvalidInput, "AMAZON_REFERENCE_INVALID", false)
	}
	p, ok := g.products[ref.ASIN]
	if !ok {
		return app.AmazonProductDetail{}, fault.New(fault.ProviderUnavailable, "AMAZON_STUB_SNAPSHOT_MISSING", false)
	}
	price := domain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_CACHED"}
	amount := int64(0)
	if p.PriceMinor != nil {
		amount = *p.PriceMinor
		price = domain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "USD"}
	}
	url, _ := ref.ExternalURL()
	productRef := domain.SourceProductRef{Source: domain.SourceAmazon, Marketplace: "US", AnchorASIN: ref.ASIN}
	observation := domain.VariantObservation{ObservationID: "local-stub:" + ref.ASIN, VariantRef: ref, Price: price, Availability: "UNKNOWN", DeliveryEligibility: "UNCONFIRMED", Seller: domain.ObservedSeller{Kind: "UNKNOWN"}, PurchaseRoute: "EXTERNAL", ProductURL: url, ObservedAt: p.SnapshotAt, RefreshAfter: p.SnapshotAt.Add(15 * time.Minute)}
	money := app.CatalogMoney{AmountMinor: amount, Currency: "USD"}
	product := app.CatalogProductObservation{SourceProductRef: &productRef, VariantObservation: &observation, ProviderProductID: productRef.IdentityKey(), Title: p.Title, Locator: &app.CatalogProductLocator{Kind: app.CatalogLocatorProductURL, ProductURL: &app.CatalogProductURLLocator{CanonicalURL: url}}, PriceRange: app.CatalogPriceRange{Minimum: money, Maximum: money}, Media: []app.CatalogMedia{}, Categories: []app.CatalogCategory{}}
	return app.AmazonProductDetail{Product: product, Variants: append([]app.AmazonVariantOption(nil), p.Variants...), RelationStatus: p.RelationStatus, Truncated: p.Truncated}, nil
}

func (g *Gateway) SearchAmazon(ctx context.Context, in app.AmazonSearchRequest) (app.AmazonSearchResult, error) {
	if in.Marketplace != "US" {
		return app.AmazonSearchResult{}, fault.New(fault.InvalidInput, "AMAZON_MARKET_UNSUPPORTED", false)
	}
	result := app.AmazonSearchResult{Products: []app.CatalogProductObservation{}, Partial: true}
	for _, asin := range g.order {
		detail, err := g.LookupAmazon(ctx, domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: asin})
		if err != nil {
			return result, err
		}
		result.Products = append(result.Products, detail.Product)
	}
	return result, nil
}
func (g *Gateway) RefreshAmazonUsage(ctx context.Context) (app.CatalogAPIUsage, error) {
	return app.CatalogAPIUsage{Source: domain.SourceAmazon, Enabled: true, Mode: "STUB"}, ctx.Err()
}

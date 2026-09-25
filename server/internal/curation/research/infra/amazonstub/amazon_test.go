package amazonstub

import (
	"context"
	"encoding/json"
	app "github.com/vitlane/vitlane/server/internal/curation/research/app"
	domain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalSnapshotLookupVariantsAndQuotaNeverRequireLiveProvider(t *testing.T) {
	at := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	price := int64(3997)
	variants := []app.AmazonVariantOption{{ASIN: "B012345678", Labels: []app.LiveVariantOptionV2{{Name: "color", Value: "Black"}}}, {ASIN: "B987654321", Labels: []app.LiveVariantOptionV2{{Name: "color", Value: "Blue"}}}}
	fixture := Fixture{SchemaVersion: "vitlane.amazon-local-stub.v1", CapturedAt: at, Products: []Product{
		{ASIN: "B012345678", Title: "Cached headset", PriceMinor: &price, SnapshotAt: at, Provenance: "USER_LIKED_SNAPSHOT", Variants: variants, RelationStatus: "RELATED_REFS"},
		{ASIN: "B987654321", Title: "Variant fixture", SnapshotAt: at, Provenance: "CACHED_VARIANT_RELATION_TITLE_PLACEHOLDER", Variants: variants, RelationStatus: "RELATED_REFS"},
	}}
	raw, _ := json.Marshal(fixture)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	g, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	detail, err := g.LookupAmazon(ctx, domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"})
	if err != nil || detail.Product.VariantObservation.Validate() != nil || len(detail.Variants) != 2 || detail.Product.Locator.Validate() != nil {
		t.Fatalf("cached detail: %#v %v", detail, err)
	}
	if !detail.Product.VariantObservation.ObservedAt.Equal(at) {
		t.Fatal("snapshot time changed into a live observation")
	}
	unknown, err := g.LookupAmazon(ctx, domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: "B987654321"})
	if err != nil || unknown.Product.VariantObservation.Price.Kind != "UNKNOWN" || unknown.Product.VariantObservation.Price.AmountMinor != nil {
		t.Fatalf("uncached price was invented: %#v %v", unknown, err)
	}
	if _, err = g.LookupAmazon(ctx, domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: "B000000000"}); err == nil {
		t.Fatal("cache miss must fail without a fallback")
	}
	result, err := g.SearchAmazon(ctx, app.AmazonSearchRequest{Query: "headsets", Marketplace: "US"})
	if err != nil || len(result.Products) != 2 || !result.Partial {
		t.Fatal(result, err)
	}
	usage, err := g.RefreshAmazonUsage(ctx)
	if err != nil || usage.Mode != "STUB" || usage.Quota != nil {
		t.Fatal(usage, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = g.LookupAmazon(cancelCtx, domain.SourceVariantRef{Source: domain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"}); err == nil {
		t.Fatal("cancel ignored")
	}
	fixture.Products = fixture.Products[:1]
	raw, _ = json.Marshal(fixture)
	_ = os.WriteFile(path, raw, 0600)
	if _, err = New(path); err == nil {
		t.Fatal("missing referenced variant accepted")
	}
}

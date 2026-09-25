// Package catalogstub is a deterministic Phase 8 Shopify gateway for tests
// and local review.
//
// It must never be reachable in production. New refuses to build one outside
// development and test, so a misconfigured deploy fails at startup rather than
// serving fixture products as real ones.
package catalogstub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

type Searcher struct{}

func New(appEnvironment string) (*Searcher, error) {
	if appEnvironment == "production" {
		return nil, fmt.Errorf(
			"the stub catalog searcher is not allowed in production",
		)
	}
	return &Searcher{}, nil
}

// offerSpec drives the fixture. Prices deliberately straddle common budgets so
// a run also proves the server-side price filter removes what the user cannot
// buy, rather than only proving the happy path.
type offerSpec struct {
	slug   string
	name   string
	seller string
	domain string
	amount string
}

var offerSpecs = []offerSpec{
	{"aurora", "Aurora %s", "Aurora Goods", "aurora.example.com", "12.50"},
	{"basalt", "Basalt %s", "Basalt Supply", "basalt.example.com", "19.00"},
	{"cinder", "Cinder %s", "Cinder Works", "cinder.example.com", "24.99"},
	{"dune", "Dune %s", "Dune Trading", "dune.example.com", "38.00"},
	{"ember", "Ember %s", "Ember Studio", "ember.example.com", "57.25"},
	{"fjord", "Fjord %s", "Fjord Outfitters", "fjord.example.com", "96.00"},
	// Deliberately over any modest budget: the filter must drop it.
	{"glacier", "Glacier %s", "Glacier Co", "glacier.example.com", "480.00"},
	// Deliberately unavailable: the observation path must drop it too.
	{"hollow", "Hollow %s", "Hollow Ltd", "hollow.example.com", "21.00"},
}

// SearchProducts is the Phase 8 provider-neutral fixture boundary. It keeps
// the development catalog behind the same applied-filter and product-level
// evidence contract as Shopify, so worker tests cannot pass by exercising the
// retired observation/submission path.
func (s *Searcher) SearchProducts(
	_ context.Context,
	request researchapp.CatalogProductSearchRequest,
) (researchapp.CatalogProductSearchResult, error) {
	if err := request.Validate(); err != nil {
		return researchapp.CatalogProductSearchResult{}, err
	}
	proof, err := researchapp.NewCatalogAppliedFilterProofV2(
		request,
		researchapp.CatalogProviderShopifyGlobalV2,
		"2026-04-08",
		"catalog-stub:search:"+stubToken(request.Query),
		researchapp.CatalogShopifyGlobalCapabilityV2,
		"2026-04-08",
		nil,
	)
	if err != nil {
		return researchapp.CatalogProductSearchResult{}, err
	}
	products := make([]researchapp.CatalogProductObservation, 0, request.Limit)
	for index, spec := range offerSpecs {
		if len(products) >= request.Limit {
			break
		}
		product, err := stubProductObservation(
			spec, request.Query, request.Context.Currency, index,
		)
		if err != nil {
			return researchapp.CatalogProductSearchResult{}, err
		}
		if !stubProductMatchesSearchFilters(request.Filters, product) {
			continue
		}
		product.ProviderOrder = len(products)
		products = append(products, product)
	}
	return researchapp.CatalogProductSearchResult{
		Provider:        researchapp.CatalogProviderShopifyGlobalV2,
		ProtocolVersion: "2026-04-08",
		Outcome:         researchapp.CatalogOutcomeSuccess,
		Products:        products,
		Pagination:      researchapp.CatalogPagination{HasNextPage: false},
		AppliedFilters:  proof,
	}, nil
}

func stubProductMatchesSearchFilters(
	filters researchapp.CatalogProductSearchFilters,
	product researchapp.CatalogProductObservation,
) bool {
	if filters.Available != nil {
		if product.PreviewVariant == nil ||
			product.PreviewVariant.Availability.Available == nil ||
			*product.PreviewVariant.Availability.Available != *filters.Available {
			return false
		}
	}
	if filters.Price == nil {
		return true
	}
	if filters.Price.MinimumMinor != nil &&
		product.PriceRange.Maximum.AmountMinor < *filters.Price.MinimumMinor {
		return false
	}
	if filters.Price.MaximumMinor != nil &&
		product.PriceRange.Minimum.AmountMinor > *filters.Price.MaximumMinor {
		return false
	}
	return true
}

func (s *Searcher) LookupMedia(
	ctx context.Context,
	request researchapp.CatalogMediaLookupRequest,
) (researchapp.CatalogMediaLookupResult, error) {
	if err := request.Validate(); err != nil || request.ProviderCallAdmission == nil {
		return researchapp.CatalogMediaLookupResult{}, fmt.Errorf("catalog stub media request is invalid")
	}
	release, err := request.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
	if err != nil {
		return researchapp.CatalogMediaLookupResult{}, err
	}
	release()
	unresolved := make([]string, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		unresolved = append(unresolved, input.CorrelationKey)
	}
	return researchapp.CatalogMediaLookupResult{
		Provider:        researchapp.CatalogProviderShopifyGlobalV2,
		ProtocolVersion: "2026-04-08", ProviderCallCount: 1,
		Outcome: researchapp.CatalogOutcomeSuccess, UnresolvedKeys: unresolved,
	}, nil
}

func (s *Searcher) LookupOffers(
	ctx context.Context,
	request researchapp.CatalogOfferLookupRequest,
) (researchapp.CatalogOfferLookupResult, error) {
	if err := request.Validate(); err != nil || request.ProviderCallAdmission == nil {
		return researchapp.CatalogOfferLookupResult{}, fmt.Errorf("catalog stub offer request is invalid")
	}
	release, err := request.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
	if err != nil {
		return researchapp.CatalogOfferLookupResult{}, err
	}
	release()
	matches := make([]researchapp.CatalogOfferMatch, 0, len(request.Inputs))
	unresolved := make([]string, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		spec, found := stubOfferForIdentifier(input.Identifier)
		if !found {
			unresolved = append(unresolved, input.DraftID)
			continue
		}
		product, productErr := stubProductObservation(
			spec, "saved product", request.Context.Currency, 0,
		)
		if productErr != nil {
			return researchapp.CatalogOfferLookupResult{}, productErr
		}
		matches = append(matches, researchapp.CatalogOfferMatch{
			DraftID: input.DraftID, RequestedIdentifier: input.Identifier,
			Match: "exact", Product: product, Variant: *product.PreviewVariant,
		})
	}
	return researchapp.CatalogOfferLookupResult{
		Provider:        researchapp.CatalogProviderShopifyGlobalV2,
		ProtocolVersion: "2026-04-08", ProviderCallCount: 1,
		Outcome: researchapp.CatalogOutcomeSuccess, Matches: matches,
		UnresolvedIDs: unresolved,
	}, nil
}

func stubOfferForIdentifier(identifier string) (offerSpec, bool) {
	identifier = strings.TrimSpace(identifier)
	for _, spec := range offerSpecs {
		if identifier == "stub-variant-"+spec.slug ||
			strings.Contains(identifier, "/products/"+spec.slug) {
			return spec, true
		}
	}
	return offerSpec{}, false
}

func stubProductObservation(
	spec offerSpec,
	query, currency string,
	providerOrder int,
) (researchapp.CatalogProductObservation, error) {
	amountMinor, err := stubMinorAmount(spec.amount, currency)
	if err != nil {
		return researchapp.CatalogProductObservation{}, err
	}
	available := spec.slug != "hollow"
	name := fmt.Sprintf(spec.name, strings.TrimSpace(query))
	productURL := fmt.Sprintf("https://%s/products/%s", spec.domain, spec.slug)
	imageURL := fmt.Sprintf("https://%s/images/%s.jpg", spec.domain, spec.slug)
	return researchapp.CatalogProductObservation{
		ProviderProductID: "stub-product-" + spec.slug,
		Handle:            spec.slug,
		Title:             name,
		Description: researchapp.CatalogDescription{
			Plain: fmt.Sprintf("%s from %s for deterministic local review.", name, spec.seller),
		},
		Locator: &researchapp.CatalogProductLocator{
			Kind: researchapp.CatalogLocatorProductURL,
			ProductURL: &researchapp.CatalogProductURLLocator{
				CanonicalURL: productURL,
			},
		},
		PreviewVariant: &researchapp.CatalogPreviewVariant{
			ID:    "stub-variant-" + spec.slug,
			Title: "Default",
			URL:   productURL,
			Price: researchapp.CatalogMoney{
				AmountMinor: amountMinor, Currency: currency,
			},
			Availability: researchapp.CatalogAvailability{Available: &available},
			Media:        []researchapp.CatalogMedia{{Type: "IMAGE", URL: imageURL}},
			Seller: &researchapp.CatalogSeller{
				ID: "stub-seller-" + spec.slug, Name: spec.seller,
				Domain: spec.domain, URL: "https://" + spec.domain,
			},
		},
		PriceRange: researchapp.CatalogPriceRange{
			Minimum: researchapp.CatalogMoney{AmountMinor: amountMinor, Currency: currency},
			Maximum: researchapp.CatalogMoney{AmountMinor: amountMinor, Currency: currency},
		},
		Media:         []researchapp.CatalogMedia{{Type: "IMAGE", URL: imageURL}},
		ProviderOrder: providerOrder,
	}, nil
}

func stubMinorAmount(amount string, currency string) (int64, error) {
	exponent := 2
	if currency == "JPY" || currency == "KRW" {
		exponent = 0
	}
	parts := strings.SplitN(amount, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	for len(fraction) < exponent {
		fraction += "0"
	}
	if len(fraction) > exponent {
		fraction = fraction[:exponent]
	}
	minor := whole
	for range exponent {
		minor *= 10
	}
	if fraction != "" {
		value, parseErr := strconv.ParseInt(fraction, 10, 64)
		if parseErr != nil {
			return 0, parseErr
		}
		minor += value
	}
	return minor, nil
}

func stubToken(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:8])
}

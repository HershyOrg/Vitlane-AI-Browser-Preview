package shopifyucp

import (
	"context"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

func (gateway *GatewayV2) SearchProducts(
	ctx context.Context,
	request researchapp.CatalogProductSearchRequest,
) (researchapp.CatalogProductSearchResult, error) {
	if err := gateway.providerRateLimitCooldownV2(); err != nil {
		return researchapp.CatalogProductSearchResult{}, err
	}
	if err := request.Validate(); err != nil {
		return researchapp.CatalogProductSearchResult{}, invalidCatalogRequestV2()
	}
	call, err := gateway.callCatalogV2(
		ctx, "search_catalog", buildSearchRequestV2(request), ucpSearchCapabilityV2,
	)
	if err != nil {
		return researchapp.CatalogProductSearchResult{}, err
	}
	products := []wireProductV2{}
	if call.content.Products != nil {
		products = *call.content.Products
	}
	result := researchapp.CatalogProductSearchResult{
		Provider: string(Provider), ProtocolVersion: call.content.UCP.Version,
		Outcome: call.outcome, Products: []researchapp.CatalogProductObservation{},
		Messages: bindCatalogMessageSubjectsV2(call.messages, products),
	}
	if call.outcome == researchapp.CatalogOutcomeBusinessError {
		return result, nil
	}
	proofCapability := ucpSearchCapabilityV2
	if searchUsesShopifyGlobalFiltersV2(request) {
		proofCapability = researchapp.CatalogShopifyGlobalCapabilityV2
		if !capabilityHasVersionV2(
			call.content.UCP.Capabilities, proofCapability, result.ProtocolVersion,
		) {
			return researchapp.CatalogProductSearchResult{}, schemaMismatchV2()
		}
	}
	appliedFilters, err := researchapp.NewCatalogAppliedFilterProofV2(
		request, result.Provider, result.ProtocolVersion, call.requestRef,
		proofCapability, result.ProtocolVersion, call.messages,
	)
	if err != nil {
		return researchapp.CatalogProductSearchResult{}, schemaMismatchV2()
	}
	result.AppliedFilters = appliedFilters
	result.RawCount = len(products)
	result.Products = make([]researchapp.CatalogProductObservation, 0, len(products))
	for index, product := range products {
		observation, err := normalizeProductObservationV2(product, index)
		if err != nil {
			result.Partial = true
			result.RejectedCount++
			continue
		}
		result.Products = append(result.Products, observation)
	}
	pagination, err := normalizePaginationV2(call.content.Pagination)
	if err != nil {
		return researchapp.CatalogProductSearchResult{}, err
	}
	result.Pagination = pagination
	return result, nil
}

func searchUsesShopifyGlobalFiltersV2(
	request researchapp.CatalogProductSearchRequest,
) bool {
	filters := request.Filters
	return filters.Available != nil || filters.ShipsTo != nil ||
		len(filters.Conditions) > 0 || len(filters.Attributes) > 0
}

func buildSearchRequestV2(
	request researchapp.CatalogProductSearchRequest,
) wireSearchRequestV2 {
	filters := wireSearchFiltersV2{
		Available:  request.Filters.Available,
		Categories: cloneTrimmedStringsV2(request.Filters.Categories),
		Condition:  cloneTrimmedStringsV2(request.Filters.Conditions),
	}
	if request.Filters.ShipsTo != nil {
		filters.ShipsTo = &wireDestinationV2{
			Country:    strings.TrimSpace(request.Filters.ShipsTo.Country),
			Region:     strings.TrimSpace(request.Filters.ShipsTo.Region),
			PostalCode: strings.TrimSpace(request.Filters.ShipsTo.PostalCode),
		}
	}
	if request.Filters.Price != nil {
		filters.Price = &wirePriceFilterV2{
			Min: request.Filters.Price.MinimumMinor,
			Max: request.Filters.Price.MaximumMinor,
		}
	}
	filters.Attributes = make(
		[]wireAttributeFilterV2, 0, len(request.Filters.Attributes),
	)
	for _, attribute := range request.Filters.Attributes {
		filters.Attributes = append(filters.Attributes, wireAttributeFilterV2{
			Name:   strings.TrimSpace(attribute.Name),
			Values: cloneTrimmedStringsV2(attribute.Values),
		})
	}
	var wireFilters *wireSearchFiltersV2
	if filters.Available != nil || filters.ShipsTo != nil || filters.Price != nil ||
		len(filters.Categories) > 0 || len(filters.Condition) > 0 ||
		len(filters.Attributes) > 0 {
		wireFilters = &filters
	}
	return wireSearchRequestV2{
		Query:   strings.TrimSpace(request.Query),
		Context: wireContextFromAppV2(request.Context),
		Filters: wireFilters,
		Pagination: &wirePaginationRequestV2{
			Cursor: strings.TrimSpace(request.Cursor), Limit: request.Limit,
		},
	}
}

func normalizeProductObservationV2(
	product wireProductV2,
	providerOrder int,
) (researchapp.CatalogProductObservation, error) {
	if strings.TrimSpace(product.ID) == "" || strings.TrimSpace(product.Title) == "" ||
		product.Variants == nil {
		return researchapp.CatalogProductObservation{}, schemaMismatchV2()
	}
	priceRange := researchapp.CatalogPriceRange{}
	if product.PriceRange != nil {
		var err error
		priceRange, err = normalizePriceRangeV2(product.PriceRange)
		if err != nil {
			return researchapp.CatalogProductObservation{}, err
		}
	}
	media := normalizeOptionalMediaV4(product.Media)
	categories := make([]researchapp.CatalogCategory, 0, len(product.Categories))
	for _, category := range product.Categories {
		if strings.TrimSpace(category.Value) == "" {
			return researchapp.CatalogProductObservation{}, schemaMismatchV2()
		}
		categories = append(categories, researchapp.CatalogCategory{
			Value: category.Value, Taxonomy: category.Taxonomy,
		})
	}
	observation := researchapp.CatalogProductObservation{
		ProviderProductID: product.ID,
		Handle:            product.Handle,
		Title:             product.Title,
		Description:       normalizeDescriptionV2(product.Description),
		PriceRange:        priceRange,
		Media:             media,
		Categories:        categories,
		ProviderOrder:     providerOrder,
	}
	if len(product.Metadata) > 0 && string(product.Metadata) != "null" && len(product.Metadata) <= 4000 {
		base := observation.Description.Plain
		if base == "" {
			base = observation.Description.Markdown
		}
		if base == "" {
			base = observation.Description.HTML
		}
		observation.Description.Plain = base + "\nProvider-inferred metadata (not independently verified specifications): " + string(product.Metadata)
	}
	variants := *product.Variants
	for _, variant := range variants {
		preview, err := normalizePreviewVariantV2(variant)
		if err != nil || preview.Availability.Available != nil && !*preview.Availability.Available {
			continue
		}
		observation.PreviewVariant = &preview
		break
	}
	productURL := strings.TrimSpace(product.URL)
	if productURL != "" {
		if !validHTTPSResourceURLV2(productURL) {
			return researchapp.CatalogProductObservation{}, schemaMismatchV2()
		}
		observation.Locator = &researchapp.CatalogProductLocator{
			Kind:       researchapp.CatalogLocatorProductURL,
			ProductURL: &researchapp.CatalogProductURLLocator{CanonicalURL: productURL},
		}
		return observation, nil
	}
	if observation.PreviewVariant != nil && observation.PreviewVariant.Seller != nil {
		seller := observation.PreviewVariant.Seller
		if strings.TrimSpace(seller.Domain) != "" && !validSellerDomainV2(seller.Domain) {
			return researchapp.CatalogProductObservation{}, schemaMismatchV2()
		}
		if strings.TrimSpace(observation.PreviewVariant.ID) != "" &&
			validSellerDomainV2(seller.Domain) {
			observation.Locator = &researchapp.CatalogProductLocator{
				Kind: researchapp.CatalogLocatorMerchantVariant,
				MerchantVariant: &researchapp.CatalogMerchantVariantLocator{
					VariantID:    observation.PreviewVariant.ID,
					SellerDomain: strings.TrimSpace(seller.Domain),
					SellerID:     seller.ID,
				},
			}
		}
	}
	return observation, nil
}

func normalizePreviewVariantV2(
	variant wireVariantV2,
) (researchapp.CatalogPreviewVariant, error) {
	if strings.TrimSpace(variant.ID) == "" || strings.TrimSpace(variant.Title) == "" ||
		variant.Price == nil {
		return researchapp.CatalogPreviewVariant{}, schemaMismatchV2()
	}
	price, err := normalizeMoneyV2(variant.Price)
	if err != nil {
		return researchapp.CatalogPreviewVariant{}, err
	}
	media := normalizeOptionalMediaV4(variant.Media)
	preview := researchapp.CatalogPreviewVariant{
		ID: variant.ID, Title: variant.Title,
		Description: normalizeDescriptionV2(variant.Description),
		URL:         variant.URL, Price: price, Media: media,
	}
	if variant.URL != "" && !validHTTPSResourceURLV2(variant.URL) {
		return researchapp.CatalogPreviewVariant{}, schemaMismatchV2()
	}
	if variant.Availability != nil {
		preview.Availability = researchapp.CatalogAvailability{
			Available:  variant.Availability.Available,
			Status:     variant.Availability.Status,
			RunningLow: variant.Availability.RunningLow,
		}
	}
	if variant.Seller != nil {
		if variant.Seller.URL != "" && !validHTTPSResourceURLV2(variant.Seller.URL) {
			return researchapp.CatalogPreviewVariant{}, schemaMismatchV2()
		}
		if variant.Seller.Domain != "" && !validSellerDomainV2(variant.Seller.Domain) {
			return researchapp.CatalogPreviewVariant{}, schemaMismatchV2()
		}
		preview.Seller = &researchapp.CatalogSeller{
			ID: variant.Seller.ID, Name: variant.Seller.Name,
			Domain: variant.Seller.Domain, URL: variant.Seller.URL,
		}
	}
	if variant.Eligible != nil {
		preview.NativeCheckoutEligible = variant.Eligible.NativeCheckout
	}
	return preview, nil
}

func normalizeDescriptionV2(description *wireDescriptionV2) researchapp.CatalogDescription {
	if description == nil {
		return researchapp.CatalogDescription{}
	}
	return researchapp.CatalogDescription{
		Plain: description.Plain, HTML: description.HTML, Markdown: description.Markdown,
	}
}

func normalizePriceRangeV2(
	priceRange *wirePriceRangeV2,
) (researchapp.CatalogPriceRange, error) {
	if priceRange.Min == nil || priceRange.Max == nil {
		return researchapp.CatalogPriceRange{}, schemaMismatchV2()
	}
	minimum, err := normalizeMoneyV2(priceRange.Min)
	if err != nil {
		return researchapp.CatalogPriceRange{}, err
	}
	maximum, err := normalizeMoneyV2(priceRange.Max)
	if err != nil {
		return researchapp.CatalogPriceRange{}, err
	}
	if minimum.Currency != maximum.Currency || minimum.AmountMinor > maximum.AmountMinor {
		return researchapp.CatalogPriceRange{}, schemaMismatchV2()
	}
	return researchapp.CatalogPriceRange{Minimum: minimum, Maximum: maximum}, nil
}

func normalizeMoneyV2(money *wireMoneyV2) (researchapp.CatalogMoney, error) {
	if money == nil || money.Amount == nil || *money.Amount < 0 || !validCurrencyV2(money.Currency) {
		return researchapp.CatalogMoney{}, schemaMismatchV2()
	}
	return researchapp.CatalogMoney{
		AmountMinor: *money.Amount, Currency: money.Currency,
	}, nil
}

func normalizeMediaV2(media []wireMediaV2) ([]researchapp.CatalogMedia, error) {
	normalized := make([]researchapp.CatalogMedia, 0, len(media))
	for _, item := range media {
		if strings.TrimSpace(item.Type) == "" || !validHTTPSResourceURLV2(item.URL) ||
			(item.Width != nil && *item.Width <= 0) ||
			(item.Height != nil && *item.Height <= 0) {
			return nil, schemaMismatchV2()
		}
		normalized = append(normalized, researchapp.CatalogMedia{
			Type: item.Type, URL: item.URL, AltText: item.AltText,
			Width: item.Width, Height: item.Height,
		})
	}
	return normalized, nil
}

func normalizePaginationV2(
	pagination *wirePaginationV2,
) (researchapp.CatalogPagination, error) {
	if pagination == nil {
		return researchapp.CatalogPagination{}, nil
	}
	if pagination.HasNextPage == nil ||
		(*pagination.HasNextPage && strings.TrimSpace(pagination.Cursor) == "") ||
		(pagination.TotalCount != nil && *pagination.TotalCount < 0) {
		return researchapp.CatalogPagination{}, schemaMismatchV2()
	}
	return researchapp.CatalogPagination{
		Cursor: pagination.Cursor, HasNextPage: *pagination.HasNextPage,
		TotalCount: pagination.TotalCount,
	}, nil
}

func wireContextFromAppV2(context researchapp.CatalogBuyerContext) *wireContextV2 {
	wireContext := wireContextV2{
		AddressCountry: strings.TrimSpace(context.Country),
		AddressRegion:  strings.TrimSpace(context.Region),
		PostalCode:     strings.TrimSpace(context.PostalCode),
		Language:       strings.TrimSpace(context.Language),
		Currency:       strings.TrimSpace(context.Currency),
		Intent:         strings.TrimSpace(context.Intent),
	}
	if wireContext == (wireContextV2{}) {
		return nil
	}
	return &wireContext
}

func cloneTrimmedStringsV2(values []string) []string {
	cloned := make([]string, 0, len(values))
	for _, value := range values {
		cloned = append(cloned, strings.TrimSpace(value))
	}
	return cloned
}

func validCurrencyV2(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, value := range currency {
		if value < 'A' || value > 'Z' {
			return false
		}
	}
	return true
}

func validHTTPSResourceURLV2(raw string) bool {
	return validProfileURLV2(strings.TrimSpace(raw))
}

func validSellerDomainV2(raw string) bool {
	domain := strings.TrimSpace(raw)
	if domain == "" || strings.ContainsAny(domain, "/?#@") {
		return false
	}
	return validProfileURLV2("https://" + domain)
}

func (gateway *GatewayV2) DescribeDiscovery() researchapp.DiscoveryDescriptor {
	return researchapp.EnglishDiscoveryDescriptor("SHOPIFY")
}

func normalizeOptionalMediaV4(values []wireMediaV2) []researchapp.CatalogMedia {
	result := []researchapp.CatalogMedia{}
	for _, value := range values {
		if valid, err := normalizeMediaV2([]wireMediaV2{value}); err == nil {
			result = append(result, valid...)
		}
	}
	return result
}

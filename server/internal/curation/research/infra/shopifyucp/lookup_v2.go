package shopifyucp

import (
	"context"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type lookupEntryV2 struct {
	identifier string
}

type lookupCorrelationV2 struct {
	productID string
	variantID string
	match     string
	media     []researchapp.CatalogMedia
	product   researchapp.CatalogProductObservation
	variant   researchapp.CatalogPreviewVariant
}

type lookupBatchResultV2 struct {
	outcome       researchapp.CatalogOutcome
	correlations  map[string]lookupCorrelationV2
	messages      []researchapp.CatalogProviderMessage
	providerCalls int
}

func (gateway *GatewayV2) LookupOffers(
	ctx context.Context,
	request researchapp.CatalogOfferLookupRequest,
) (researchapp.CatalogOfferLookupResult, error) {
	if err := gateway.providerRateLimitCooldownV2(); err != nil {
		return researchapp.CatalogOfferLookupResult{}, err
	}
	if err := request.Validate(); err != nil || request.ProviderCallAdmission == nil {
		return researchapp.CatalogOfferLookupResult{}, invalidCatalogRequestV2()
	}
	mediaInputs := make([]researchapp.CatalogMediaLookupInput, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		mediaInputs = append(mediaInputs, researchapp.CatalogMediaLookupInput{
			CorrelationKey: strings.TrimSpace(input.DraftID),
			Identifier:     strings.TrimSpace(input.Identifier),
		})
	}
	entries := deduplicateLookupEntriesV2(mediaInputs)
	aggregate := lookupBatchResultV2{
		outcome:      researchapp.CatalogOutcomeSuccess,
		correlations: make(map[string]lookupCorrelationV2, len(entries)),
		messages:     []researchapp.CatalogProviderMessage{},
	}
	for start := 0; start < len(entries); start += gateway.lookupBatchSize {
		end := min(start+gateway.lookupBatchSize, len(entries))
		batch, err := gateway.lookupBatchWithSplitV2(
			ctx, entries[start:end], request.Context, request.ProviderCallAdmission,
		)
		mergeLookupBatchResultV2(&aggregate, batch)
		if err != nil {
			return materializeOfferLookupResultV2(request, aggregate, gateway.expectedVersion), err
		}
	}
	return materializeOfferLookupResultV2(request, aggregate, gateway.expectedVersion), nil
}

func (gateway *GatewayV2) LookupMedia(
	ctx context.Context,
	request researchapp.CatalogMediaLookupRequest,
) (researchapp.CatalogMediaLookupResult, error) {
	if err := gateway.providerRateLimitCooldownV2(); err != nil {
		return researchapp.CatalogMediaLookupResult{}, err
	}
	if err := request.Validate(); err != nil || request.ProviderCallAdmission == nil {
		return researchapp.CatalogMediaLookupResult{}, invalidCatalogRequestV2()
	}
	entries := deduplicateLookupEntriesV2(request.Inputs)
	aggregate := lookupBatchResultV2{
		outcome:      researchapp.CatalogOutcomeSuccess,
		correlations: make(map[string]lookupCorrelationV2, len(entries)),
		messages:     []researchapp.CatalogProviderMessage{},
	}
	for start := 0; start < len(entries); start += gateway.lookupBatchSize {
		end := min(start+gateway.lookupBatchSize, len(entries))
		batch, err := gateway.lookupBatchWithSplitV2(
			ctx, entries[start:end], request.Context, request.ProviderCallAdmission,
		)
		mergeLookupBatchResultV2(&aggregate, batch)
		if err != nil {
			return materializeLookupResultV2(request, aggregate, gateway.expectedVersion), err
		}
	}
	return materializeLookupResultV2(request, aggregate, gateway.expectedVersion), nil
}

func (gateway *GatewayV2) lookupBatchWithSplitV2(
	ctx context.Context,
	entries []lookupEntryV2,
	buyerContext researchapp.CatalogBuyerContext,
	admission researchapp.CatalogProviderCallAdmissionV2,
) (lookupBatchResultV2, error) {
	result, err := gateway.lookupBatchV2(ctx, entries, buyerContext, admission)
	if err == nil || !isExplicitBatchSizeFailureV2(err) || len(entries) <= 1 {
		return result, err
	}
	initialCalls := result.providerCalls
	middle := len(entries) / 2
	left, err := gateway.lookupBatchWithSplitV2(
		ctx, entries[:middle], buyerContext, admission,
	)
	left.providerCalls += initialCalls
	if err != nil {
		return left, err
	}
	right, err := gateway.lookupBatchWithSplitV2(
		ctx, entries[middle:], buyerContext, admission,
	)
	mergeLookupBatchResultV2(&left, right)
	return left, err
}

func (gateway *GatewayV2) lookupBatchV2(
	ctx context.Context,
	entries []lookupEntryV2,
	buyerContext researchapp.CatalogBuyerContext,
	admission researchapp.CatalogProviderCallAdmissionV2,
) (lookupBatchResultV2, error) {
	identifiers := make([]string, 0, len(entries))
	requested := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		identifiers = append(identifiers, entry.identifier)
		requested[entry.identifier] = struct{}{}
	}
	result := lookupBatchResultV2{}
	release := func() {}
	if admission != nil {
		var err error
		release, err = admission.AcquireCatalogProviderCall(ctx)
		if err != nil {
			return result, err
		}
		if release == nil {
			return result, fault.New(
				fault.InternalFailure, "CATALOG_CALL_ADMISSION_INVALID", false,
			)
		}
	}
	result.providerCalls = 1
	call, err := gateway.callCatalogV2(
		ctx,
		"lookup_catalog",
		wireLookupRequestV2{IDs: identifiers, Context: wireContextFromAppV2(buyerContext)},
		ucpLookupCapabilityV2,
	)
	release()
	if err != nil {
		return result, err
	}
	products := []wireProductV2{}
	if call.content.Products != nil {
		products = *call.content.Products
	}
	result = lookupBatchResultV2{
		outcome:       call.outcome,
		correlations:  make(map[string]lookupCorrelationV2, len(entries)),
		messages:      bindCatalogMessageSubjectsV2(call.messages, products),
		providerCalls: 1,
	}
	if call.outcome == researchapp.CatalogOutcomeBusinessError {
		return result, nil
	}
	for productIndex, product := range products {
		observation, err := normalizeProductObservationV2(product, productIndex)
		if err != nil {
			return result, err
		}
		for _, variant := range *product.Variants {
			preview, err := normalizePreviewVariantV2(variant)
			if err != nil {
				return result, err
			}
			if len(variant.Inputs) == 0 {
				return result, correlationMismatchV2()
			}
			media := preview.Media
			if len(media) == 0 {
				media = observation.Media
			}
			for _, input := range variant.Inputs {
				identifier := strings.TrimSpace(input.ID)
				if identifier == "" {
					return result, correlationMismatchV2()
				}
				if _, exists := requested[identifier]; !exists {
					return result, correlationMismatchV2()
				}
				correlation := lookupCorrelationV2{
					productID: product.ID, variantID: variant.ID,
					match: input.Match, media: media,
					product: observation, variant: preview,
				}
				if previous, exists := result.correlations[identifier]; exists &&
					(previous.productID != correlation.productID ||
						previous.variantID != correlation.variantID) {
					return result, correlationMismatchV2()
				}
				result.correlations[identifier] = correlation
			}
		}
	}
	return result, nil
}

func materializeOfferLookupResultV2(
	request researchapp.CatalogOfferLookupRequest,
	aggregate lookupBatchResultV2,
	protocolVersion string,
) researchapp.CatalogOfferLookupResult {
	result := researchapp.CatalogOfferLookupResult{
		Provider: string(Provider), ProtocolVersion: protocolVersion,
		ProviderCallCount: aggregate.providerCalls,
		Outcome:           aggregate.outcome, Matches: []researchapp.CatalogOfferMatch{},
		UnresolvedIDs: []string{}, Messages: aggregate.messages,
	}
	if result.Outcome == "" {
		result.Outcome = researchapp.CatalogOutcomeSuccess
	}
	for _, input := range request.Inputs {
		identifier := strings.TrimSpace(input.Identifier)
		correlation, exists := aggregate.correlations[identifier]
		if !exists {
			result.UnresolvedIDs = append(result.UnresolvedIDs, input.DraftID)
			continue
		}
		product := correlation.product
		variant := correlation.variant
		product.PreviewVariant = &variant
		result.Matches = append(result.Matches, researchapp.CatalogOfferMatch{
			DraftID: input.DraftID, RequestedIdentifier: identifier,
			Match: correlation.match, Product: product, Variant: variant,
		})
	}
	return result
}

func deduplicateLookupEntriesV2(
	inputs []researchapp.CatalogMediaLookupInput,
) []lookupEntryV2 {
	seen := make(map[string]struct{}, len(inputs))
	entries := make([]lookupEntryV2, 0, len(inputs))
	for _, input := range inputs {
		identifier := strings.TrimSpace(input.Identifier)
		if _, exists := seen[identifier]; exists {
			continue
		}
		seen[identifier] = struct{}{}
		entries = append(entries, lookupEntryV2{identifier: identifier})
	}
	return entries
}

func mergeLookupBatchResultV2(target *lookupBatchResultV2, source lookupBatchResultV2) {
	if target.correlations == nil {
		target.correlations = map[string]lookupCorrelationV2{}
	}
	for identifier, correlation := range source.correlations {
		target.correlations[identifier] = correlation
	}
	target.messages = append(target.messages, source.messages...)
	target.providerCalls += source.providerCalls
	if source.outcome == researchapp.CatalogOutcomeBusinessError {
		target.outcome = researchapp.CatalogOutcomeBusinessError
	}
}

func materializeLookupResultV2(
	request researchapp.CatalogMediaLookupRequest,
	aggregate lookupBatchResultV2,
	protocolVersion string,
) researchapp.CatalogMediaLookupResult {
	result := researchapp.CatalogMediaLookupResult{
		Provider: string(Provider), ProtocolVersion: protocolVersion,
		ProviderCallCount: aggregate.providerCalls,
		Outcome:           aggregate.outcome,
		Matches:           []researchapp.CatalogMediaMatch{},
		UnresolvedKeys:    []string{},
		Messages:          aggregate.messages,
	}
	if result.Outcome == "" {
		result.Outcome = researchapp.CatalogOutcomeSuccess
	}
	for _, input := range request.Inputs {
		identifier := strings.TrimSpace(input.Identifier)
		correlation, exists := aggregate.correlations[identifier]
		if !exists {
			result.UnresolvedKeys = append(result.UnresolvedKeys, input.CorrelationKey)
			continue
		}
		result.Matches = append(result.Matches, researchapp.CatalogMediaMatch{
			CorrelationKey:      input.CorrelationKey,
			RequestedIdentifier: identifier,
			ProductID:           correlation.productID,
			VariantID:           correlation.variantID,
			Match:               correlation.match,
			Media:               correlation.media,
		})
	}
	return result
}

func correlationMismatchV2() error {
	return newCatalogFaultV2(
		fault.ProviderRejected, researchapp.CatalogFailureCorrelation, false, 0,
	)
}

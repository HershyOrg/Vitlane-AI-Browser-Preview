package app

import (
	"context"
	"errors"
	"strings"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	CatalogFailureBusinessOutcomeV2   CatalogFailureReason = "PROVIDER_BUSINESS_OUTCOME"
	catalogSearchCoordinatorInvalidV2                      = "CATALOG_SEARCH_COORDINATOR_INVALID"
)

type CatalogSearchExecutionStatusV2 string

const (
	CatalogSearchExecutionResultsV2   CatalogSearchExecutionStatusV2 = "RESULTS"
	CatalogSearchExecutionNoResultsV2 CatalogSearchExecutionStatusV2 = "NO_RESULTS"
	CatalogSearchExecutionFailedV2    CatalogSearchExecutionStatusV2 = "FAILED"
)

type CatalogSearchAttemptOutcomeV2 string

const (
	CatalogSearchAttemptSucceededV2 CatalogSearchAttemptOutcomeV2 = "SUCCEEDED"
	CatalogSearchAttemptFailedV2    CatalogSearchAttemptOutcomeV2 = "FAILED"
)

// CatalogSearchAttemptV2 is the one request of the translated plan and what
// came back. There is no second attempt with a reshaped query: which phrase to
// ask is the SearchPlan's decision, and the next Round asks the next phrase.
type CatalogSearchAttemptV2 struct {
	Outcome       CatalogSearchAttemptOutcomeV2
	RawCount      int
	UsableCount   int
	FailureReason string
}

// CatalogAdmissionInputV2 is the only observation shape exposed by this
// coordinator to later filter/rank slices. Locator is a required value, so an
// unresolvable provider row cannot accidentally become a Candidate.
type CatalogAdmissionInputV2 struct {
	Observation CatalogProductObservation
	Locator     CatalogProductLocator
	HardFilters CatalogAdmissionHardFilterProofV2
}

type CatalogSearchExecutionV2 struct {
	PlanID          string
	Status          CatalogSearchExecutionStatusV2
	Attempts        []CatalogSearchAttemptV2
	AdmissionInputs []CatalogAdmissionInputV2
	Messages        []CatalogProviderMessage
	FailureReason   string
}

type CatalogSearchCoordinatorV2 struct {
	gateway CatalogGatewayV2
}

func NewCatalogSearchCoordinatorV2(
	gateway CatalogGatewayV2,
) (*CatalogSearchCoordinatorV2, error) {
	if gateway == nil {
		return nil, fault.New(
			fault.InvalidInput, catalogSearchCoordinatorInvalidV2, false,
		)
	}
	return &CatalogSearchCoordinatorV2{gateway: gateway}, nil
}

func (coordinator *CatalogSearchCoordinatorV2) Execute(
	ctx context.Context,
	plan researchdomain.CatalogSearchPlanV2,
) (CatalogSearchExecutionV2, error) {
	execution := CatalogSearchExecutionV2{
		PlanID: plan.PlanID, Status: CatalogSearchExecutionFailedV2,
		Attempts:        []CatalogSearchAttemptV2{},
		AdmissionInputs: []CatalogAdmissionInputV2{},
		Messages:        []CatalogProviderMessage{},
	}
	if coordinator == nil || coordinator.gateway == nil {
		err := fault.New(fault.InternalFailure, catalogSearchCoordinatorInvalidV2, false)
		execution.FailureReason = catalogSearchCoordinatorInvalidV2
		return execution, err
	}
	if err := plan.Validate(); err != nil {
		classified := fault.Wrap(
			err, fault.InvalidInput, catalogSearchPlanCompileInvalidV2, false,
		)
		execution.FailureReason = catalogSearchPlanCompileInvalidV2
		return execution, classified
	}

	for _, planned := range plan.Requests {
		attempt := CatalogSearchAttemptV2{}
		gatewayRequest := catalogGatewayRequestV2(planned)
		response, err := coordinator.gateway.SearchProducts(ctx, gatewayRequest)
		if err != nil {
			err = typedCoordinatorFailureV2(err)
			attempt.Outcome = CatalogSearchAttemptFailedV2
			attempt.FailureReason = safeFailureReasonV2(err)
			execution.Attempts = append(execution.Attempts, attempt)
			execution.FailureReason = attempt.FailureReason
			return execution, err
		}
		if response.Outcome == CatalogOutcomeBusinessError {
			err = fault.New(
				fault.ProviderRejected, string(CatalogFailureBusinessOutcomeV2), false,
			)
			attempt.Outcome = CatalogSearchAttemptFailedV2
			attempt.FailureReason = string(CatalogFailureBusinessOutcomeV2)
			attempt.RawCount = len(response.Products)
			execution.Attempts = append(execution.Attempts, attempt)
			execution.Messages = append(execution.Messages, response.Messages...)
			execution.FailureReason = attempt.FailureReason
			return execution, err
		}
		if response.Outcome != CatalogOutcomeSuccess ||
			strings.TrimSpace(response.Provider) == "" ||
			strings.TrimSpace(response.ProtocolVersion) != plan.ProviderProtocolVersion ||
			len(response.Products) > planned.Limit {
			err = fault.New(
				fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
			)
			attempt.Outcome = CatalogSearchAttemptFailedV2
			attempt.FailureReason = string(CatalogFailureSchemaMismatch)
			attempt.RawCount = len(response.Products)
			execution.Attempts = append(execution.Attempts, attempt)
			execution.FailureReason = attempt.FailureReason
			return execution, err
		}
		if err := response.AppliedFilters.ValidateForRequest(
			gatewayRequest, response.Provider, response.ProtocolVersion,
		); err != nil {
			err = fault.Wrap(
				err, fault.ProviderRejected,
				string(CatalogFailureFilterNotEnforced), false,
			)
			attempt.Outcome = CatalogSearchAttemptFailedV2
			attempt.FailureReason = string(CatalogFailureFilterNotEnforced)
			attempt.RawCount = len(response.Products)
			execution.Attempts = append(execution.Attempts, attempt)
			execution.Messages = append(execution.Messages, response.Messages...)
			execution.FailureReason = attempt.FailureReason
			return execution, err
		}

		admissionInputs, err := catalogAdmissionInputsV2(
			planned, response.Products, response.AppliedFilters,
		)
		if err != nil {
			attempt.Outcome = CatalogSearchAttemptFailedV2
			attempt.FailureReason = safeFailureReasonV2(err)
			attempt.RawCount = len(response.Products)
			execution.Attempts = append(execution.Attempts, attempt)
			execution.FailureReason = attempt.FailureReason
			return execution, err
		}
		attempt.Outcome = CatalogSearchAttemptSucceededV2
		attempt.RawCount = len(response.Products)
		attempt.UsableCount = len(admissionInputs)
		execution.Attempts = append(execution.Attempts, attempt)
		execution.Messages = append(execution.Messages, response.Messages...)
		if len(admissionInputs) > 0 {
			execution.Status = CatalogSearchExecutionResultsV2
			execution.AdmissionInputs = admissionInputs
			return execution, nil
		}
	}

	execution.Status = CatalogSearchExecutionNoResultsV2
	return execution, nil
}

func catalogGatewayRequestV2(
	request researchdomain.CatalogSearchRequestV2,
) CatalogProductSearchRequest {
	available := request.Filters.Available
	attributes := make([]CatalogAttributeFilter, 0, len(request.Filters.Attributes))
	for _, attribute := range request.Filters.Attributes {
		attributes = append(attributes, CatalogAttributeFilter{
			Name: attribute.Name, Values: append([]string(nil), attribute.Values...),
		})
	}
	filters := CatalogProductSearchFilters{
		Available:  &available,
		ShipsTo:    &CatalogDestination{Country: request.Filters.ShipsTo.Country},
		Categories: append([]string(nil), request.Filters.Categories...),
		Conditions: append([]string(nil), request.Filters.Conditions...),
		Attributes: attributes,
	}
	if request.Filters.Price != nil {
		filters.Price = &CatalogPriceFilter{
			MinimumMinor: cloneSearchMinorV2(request.Filters.Price.MinimumMinor),
			MaximumMinor: cloneSearchMinorV2(request.Filters.Price.MaximumMinor),
		}
	}
	return CatalogProductSearchRequest{Cursor: request.Cursor,
		Query: request.Query,
		Context: CatalogBuyerContext{
			Country:  request.Context.AddressCountry,
			Currency: request.Context.Currency,
			Language: request.Context.Language,
			Intent:   request.Context.Intent,
		},
		Filters: filters,
		Limit:   request.Limit,
	}
}

func catalogAdmissionInputsV2(
	request researchdomain.CatalogSearchRequestV2,
	products []CatalogProductObservation,
	appliedFilters CatalogAppliedFilterProofV2,
) ([]CatalogAdmissionInputV2, error) {
	inputs := make([]CatalogAdmissionInputV2, 0, min(len(products), request.Limit))
	seenProducts := make(map[string]struct{}, len(products))
	for _, product := range products {
		if strings.TrimSpace(product.ProviderProductID) == "" ||
			strings.TrimSpace(product.Title) == "" ||
			product.PriceRange.Minimum.AmountMinor < 0 ||
			product.PriceRange.Maximum.AmountMinor < product.PriceRange.Minimum.AmountMinor ||
			product.PriceRange.Minimum.Currency != product.PriceRange.Maximum.Currency {
			continue
		}
		if product.Locator == nil {
			continue
		}
		if err := product.Locator.Validate(); err != nil {
			continue
		}
		if product.Locator.Kind == CatalogLocatorMerchantVariant {
			merchant := product.Locator.MerchantVariant
			if product.PreviewVariant == nil || merchant == nil ||
				product.PreviewVariant.ID != merchant.VariantID ||
				product.PreviewVariant.Seller == nil ||
				!strings.EqualFold(
					product.PreviewVariant.Seller.Domain, merchant.SellerDomain,
				) {
				continue
			}
		}
		if !catalogProductCategoriesCompatibleV2(request.Filters.Categories, product.Categories) {
			continue
		}
		// Required price bounds, currency conversion and lexical constraints are
		// evaluated once by the common discovery admission policy.
		if product.PreviewVariant != nil &&
			product.PreviewVariant.Availability.Available != nil &&
			!*product.PreviewVariant.Availability.Available {
			continue
		}
		physical, ok, err := newCatalogPhysicalEligibilityProofV2(
			appliedFilters, product,
		)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		identity := strings.TrimSpace(product.ProviderProductID)
		if _, exists := seenProducts[identity]; exists {
			continue
		}
		seenProducts[identity] = struct{}{}
		inputs = append(inputs, CatalogAdmissionInputV2{
			Observation: product, Locator: *product.Locator,
			HardFilters: CatalogAdmissionHardFilterProofV2{
				AppliedFilters: appliedFilters, PhysicalEligibility: physical,
			},
		})
		if len(inputs) == request.Limit {
			break
		}
	}
	return inputs, nil
}

func catalogProductCategoriesCompatibleV2(
	requested []string,
	observed []CatalogCategory,
) bool {
	if len(requested) == 0 || len(observed) == 0 {
		return true
	}
	wanted := make(map[string]struct{}, len(requested))
	for _, value := range normalizeCatalogFilterValuesV2(requested) {
		wanted[value] = struct{}{}
	}
	for _, category := range observed {
		if _, exists := wanted[normalizeCatalogFilterTokenV2(category.Value)]; exists {
			return true
		}
	}
	return false
}

func catalogPriceRangeIntersectsV2(
	filter *researchdomain.CatalogSearchPriceV2,
	priceRange CatalogPriceRange,
) bool {
	if filter == nil {
		return true
	}
	if filter.MinimumMinor != nil &&
		priceRange.Maximum.AmountMinor < *filter.MinimumMinor {
		return false
	}
	if filter.MaximumMinor != nil &&
		priceRange.Minimum.AmountMinor > *filter.MaximumMinor {
		return false
	}
	return true
}

func typedCoordinatorFailureV2(err error) error {
	if _, ok := fault.As(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return fault.Wrap(
			err, fault.CallerCancelled, "CATALOG_SEARCH_CANCELLED", false,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fault.Wrap(
			err, fault.DeadlineExceeded, string(CatalogFailureUnavailable), true,
		)
	}
	return fault.Wrap(
		err, fault.InternalFailure, catalogSearchCoordinatorInvalidV2, false,
	)
}

func safeFailureReasonV2(err error) string {
	classified, ok := fault.As(err)
	if !ok || strings.TrimSpace(classified.Reason) == "" {
		return catalogSearchCoordinatorInvalidV2
	}
	return classified.Reason
}

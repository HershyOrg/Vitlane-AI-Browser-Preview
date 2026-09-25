package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (s *LiveCatalogReviewServiceV2) ExternalProductState(ctx context.Context, user, curation, candidate string) (map[string]any, error) {
	c, err := s.resolveCandidateV2(ctx, user, curation, candidate)
	if err != nil {
		return nil, err
	}
	if !c.ProductRef().Source.KoreanExternal() {
		return nil, fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_REFERENCE_INVALID", false)
	}
	feedback, err := s.PurchaseFeedback(ctx, user, curation)
	if err != nil {
		return nil, err
	}
	stored, err := s.workspace.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil {
		return nil, err
	}
	reaction := researchdomain.ProductReaction{TargetID: c.PlanTargetID, CandidateID: c.CandidateID, ProductRef: c.ProductRef(), Sentiment: "NONE"}
	for _, value := range stored.ProductInteractions {
		if value.CandidateID == candidate {
			reaction = value
		}
	}
	_, supported := s.workspace.(ProductReactionRepository)
	return map[string]any{"schemaVersion": "vitlane.external-product-state.v1", "productRef": c.ProductRef(), "purchaseFeedback": feedback, "reactionAllowed": supported && productFallbackAllowed(c, stored), "reaction": reaction}, nil
}

func CatalogProductFromExternalObservation(o researchdomain.ExternalProductObservation) (CatalogProductObservation, error) {
	if err := o.Validate(); err != nil {
		return CatalogProductObservation{}, err
	}
	p := CatalogProductObservation{ProviderProductID: o.ProductRef.IdentityKey(), SourceProductRef: &o.ProductRef, ExternalObservation: &o, Title: o.Title, Description: CatalogDescription{Plain: o.Description}, Locator: &CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: o.ProductURL}}, Media: []CatalogMedia{}, Categories: []CatalogCategory{}}
	if o.ImageURL != "" {
		p.Media = append(p.Media, CatalogMedia{Type: "IMAGE", URL: o.ImageURL, AltText: o.Title})
	}
	if o.Price.Kind == "OBSERVED" {
		p.PriceRange = CatalogPriceRange{Minimum: CatalogMoney{AmountMinor: *o.Price.AmountMinor, Currency: o.Price.Currency}, Maximum: CatalogMoney{AmountMinor: *o.Price.AmountMinor, Currency: o.Price.Currency}}
	}
	return p, nil
}

func (service *LiveCatalogReviewServiceV2) hydrateExternalProduct(ctx context.Context, input CatalogResearchHydrationInputV2) (CatalogWorkspaceViewV2, error) {
	candidate, err := service.resolveCandidateV2(ctx, input.UserID, input.CurationID, input.CandidateID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	if candidate.PlanTargetID != input.TargetID || candidate.ProductRef().Source != input.Source || candidate.ExternalObservation == nil {
		return CatalogWorkspaceViewV2{}, fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_REFERENCE_INVALID", false)
	}
	product, err := CatalogProductFromExternalObservation(*candidate.ExternalObservation)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	product.ProviderProductID = candidate.CandidateID
	stored, err := service.workspace.LoadCatalogWorkspaceStateV2(ctx, input.UserID, input.CurationID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	metadata := CatalogPoolMetadataV2{TargetID: input.TargetID}
	for _, pool := range stored.Pools {
		if pool.TargetID == input.TargetID {
			metadata = pool
		}
	}
	pool := CatalogWorkspacePoolV2{Metadata: metadata, Products: []CatalogProductObservation{}, HiddenProducts: []CatalogProductObservation{}, Assessments: map[string]LiveCandidateAssessmentV2{candidate.CandidateID: candidate.Assessment}, Hydrations: map[string]CatalogCandidateHydrationV2{candidate.CandidateID: {Status: CatalogCandidateHydrationReadyV2}}, Messages: []CatalogProviderMessage{}}
	if candidate.Visible {
		pool.Products = append(pool.Products, product)
	} else {
		pool.HiddenProducts = append(pool.HiddenProducts, product)
	}
	return CatalogWorkspaceViewV2{Pools: []CatalogWorkspacePoolV2{pool}, Messages: []CatalogProviderMessage{}, Configurations: []CatalogWorkspaceConfigurationViewV2{}, Interactions: []CatalogVariantInteractionV2{}}, nil
}

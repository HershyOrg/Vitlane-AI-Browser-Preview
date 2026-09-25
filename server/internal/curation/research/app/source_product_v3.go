package app

import (
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"strings"
)

func (r CatalogCandidateReferenceV2) ProductRef() researchdomain.SourceProductRef {
	if source := researchdomain.Source(r.SourceKind); source.KoreanExternal() {
		return researchdomain.SourceProductRef{Source: source, Marketplace: "KR", ProductID: strings.TrimPrefix(r.ProviderProductID, strings.ToLower(r.SourceKind)+":KR:")}
	}
	if r.SourceKind == "AMAZON" {
		return researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: strings.TrimPrefix(r.ProviderProductID, "amazon:US:")}
	}
	return researchdomain.SourceProductRef{Source: researchdomain.SourceShopify, ProductID: r.ProviderProductID}
}
func (p CatalogProductObservation) ProductRef() researchdomain.SourceProductRef {
	if p.SourceProductRef != nil {
		return *p.SourceProductRef
	}
	return researchdomain.SourceProductRef{Source: researchdomain.SourceShopify, ProductID: p.ProviderProductID}
}
func (p CatalogProductObservation) Source() researchdomain.Source { return p.ProductRef().Source }

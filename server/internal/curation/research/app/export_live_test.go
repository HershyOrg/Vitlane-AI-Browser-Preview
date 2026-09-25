package app

import "context"

// Test-only doors for the external live test (shopify_live_test.go), which has to
// live outside this package to import the real Shopify adapter.

// SearchWorkspacePlanForProfileForTestV2 is the production search entry.
func SearchWorkspacePlanForProfileForTestV2(ctx context.Context, service *LiveCatalogReviewServiceV2, input CatalogWorkspaceSearchInputV2, profile CatalogTargetSearchProfileV2) (LiveCatalogReviewResultV2, error) {
	return service.searchWorkspacePlanForProfileV2(ctx, input, profile)
}

// ListingMentionsForTestV2 reports whether a listing's text contains a phrase the
// way the relevance guard reads it (contiguous tokens in one field).
func ListingMentionsForTestV2(product CatalogProductObservation, phrase string) bool {
	return candidateRankingFieldsContainV2(candidateRankingLexicalFieldsV2(product), phrase)
}

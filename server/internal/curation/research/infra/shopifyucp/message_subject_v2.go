package shopifyucp

import (
	"strconv"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

const (
	catalogMessageSubjectSearchV2  = "SEARCH"
	catalogMessageSubjectProductV2 = "PRODUCT"
)

// bindCatalogMessageSubjectsV2 keeps the provider path intact while adding a
// response-scoped product identity that survives lookup batching and response
// reordering. Unknown paths remain unbound and are handled fail-safe by the UI.
func bindCatalogMessageSubjectsV2(
	messages []researchapp.CatalogProviderMessage,
	products []wireProductV2,
) []researchapp.CatalogProviderMessage {
	bound := append([]researchapp.CatalogProviderMessage(nil), messages...)
	for index := range bound {
		path := strings.TrimSpace(bound[index].Path)
		if path == "" || path == "$" || path == "$.products" {
			bound[index].SubjectKind = catalogMessageSubjectSearchV2
			continue
		}
		productIndex, ok := catalogMessageProductIndexV2(path)
		if !ok || productIndex >= len(products) {
			continue
		}
		productID := strings.TrimSpace(products[productIndex].ID)
		if productID == "" {
			continue
		}
		bound[index].SubjectKind = catalogMessageSubjectProductV2
		bound[index].SubjectRef = productID
	}
	return bound
}

func catalogMessageProductIndexV2(path string) (int, bool) {
	const prefix = "$.products["
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	remainder := path[len(prefix):]
	closing := strings.IndexByte(remainder, ']')
	if closing < 1 {
		return 0, false
	}
	indexText := remainder[:closing]
	for _, character := range indexText {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	suffix := remainder[closing+1:]
	if suffix != "" && !strings.HasPrefix(suffix, ".") && !strings.HasPrefix(suffix, "[") {
		return 0, false
	}
	index, err := strconv.Atoi(indexText)
	return index, err == nil && index >= 0
}

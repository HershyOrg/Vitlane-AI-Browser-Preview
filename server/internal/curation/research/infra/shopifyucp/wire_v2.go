package shopifyucp

import "encoding/json"

const (
	ucpCatalogVersionV2   = "2026-04-08"
	ucpSearchCapabilityV2 = "dev.ucp.shopping.catalog.search"
	ucpLookupCapabilityV2 = "dev.ucp.shopping.catalog.lookup"
)

type wireRPCRequestV2 struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int64            `json:"id"`
	Method  string           `json:"method"`
	Params  wireCallParamsV2 `json:"params"`
}

type wireCallParamsV2 struct {
	Name      string              `json:"name"`
	Arguments wireCallArgumentsV2 `json:"arguments"`
}

type wireCallArgumentsV2 struct {
	Meta    map[string]wireAgentProfileV2 `json:"meta"`
	Catalog any                           `json:"catalog"`
}

type wireAgentProfileV2 struct {
	Profile string `json:"profile"`
}

type wireSearchRequestV2 struct {
	Query      string                   `json:"query,omitempty"`
	Context    *wireContextV2           `json:"context,omitempty"`
	Filters    *wireSearchFiltersV2     `json:"filters,omitempty"`
	Pagination *wirePaginationRequestV2 `json:"pagination,omitempty"`
}

type wireLookupRequestV2 struct {
	IDs     []string       `json:"ids"`
	Context *wireContextV2 `json:"context,omitempty"`
}

type wireContextV2 struct {
	AddressCountry string `json:"address_country,omitempty"`
	AddressRegion  string `json:"address_region,omitempty"`
	PostalCode     string `json:"postal_code,omitempty"`
	Language       string `json:"language,omitempty"`
	Currency       string `json:"currency,omitempty"`
	Intent         string `json:"intent,omitempty"`
}

type wireSearchFiltersV2 struct {
	Available  *bool                   `json:"available,omitempty"`
	ShipsTo    *wireDestinationV2      `json:"ships_to,omitempty"`
	Categories []string                `json:"categories,omitempty"`
	Condition  []string                `json:"condition,omitempty"`
	Attributes []wireAttributeFilterV2 `json:"attributes,omitempty"`
	Price      *wirePriceFilterV2      `json:"price,omitempty"`
}

type wireDestinationV2 struct {
	Country    string `json:"country,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

type wireAttributeFilterV2 struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type wirePriceFilterV2 struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

type wirePaginationRequestV2 struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type wireRPCResponseV2 struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Result  *wireRPCResultV2 `json:"result,omitempty"`
	Error   *wireRPCErrorV2  `json:"error,omitempty"`
}

type wireRPCResultV2 struct {
	StructuredContent *wireStructuredContentV2 `json:"structuredContent"`
}

type wireRPCErrorV2 struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type wireStructuredContentV2 struct {
	UCP        wireUCPMetadataV2 `json:"ucp"`
	Products   *[]wireProductV2  `json:"products,omitempty"`
	Messages   []wireMessageV2   `json:"messages,omitempty"`
	Pagination *wirePaginationV2 `json:"pagination,omitempty"`
}

type wireUCPMetadataV2 struct {
	Version      string                        `json:"version"`
	Status       *string                       `json:"status,omitempty"`
	Capabilities map[string][]wireCapabilityV2 `json:"capabilities"`
}

type wireCapabilityV2 struct {
	Version string `json:"version"`
}

type wireMessageV2 struct {
	Type         string `json:"type"`
	Code         string `json:"code,omitempty"`
	Path         string `json:"path,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Content      string `json:"content"`
	Severity     string `json:"severity,omitempty"`
	Presentation string `json:"presentation,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
	URL          string `json:"url,omitempty"`
}

type wireProductV2 struct {
	Metadata    json.RawMessage    `json:"metadata,omitempty"`
	ID          string             `json:"id"`
	Handle      string             `json:"handle,omitempty"`
	Title       string             `json:"title"`
	Description *wireDescriptionV2 `json:"description,omitempty"`
	URL         string             `json:"url,omitempty"`
	Categories  []wireCategoryV2   `json:"categories,omitempty"`
	PriceRange  *wirePriceRangeV2  `json:"price_range,omitempty"`
	Media       []wireMediaV2      `json:"media,omitempty"`
	Variants    *[]wireVariantV2   `json:"variants,omitempty"`
}

type wireDescriptionV2 struct {
	Plain    string `json:"plain,omitempty"`
	HTML     string `json:"html,omitempty"`
	Markdown string `json:"markdown,omitempty"`
}

type wireCategoryV2 struct {
	Value    string `json:"value"`
	Taxonomy string `json:"taxonomy,omitempty"`
}

type wirePriceRangeV2 struct {
	Min *wireMoneyV2 `json:"min"`
	Max *wireMoneyV2 `json:"max"`
}

type wireMoneyV2 struct {
	Amount   *int64 `json:"amount"`
	Currency string `json:"currency"`
}

type wireMediaV2 struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	AltText string `json:"alt_text,omitempty"`
	Width   *int   `json:"width,omitempty"`
	Height  *int   `json:"height,omitempty"`
}

type wireVariantV2 struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Description  *wireDescriptionV2  `json:"description,omitempty"`
	URL          string              `json:"url,omitempty"`
	Price        *wireMoneyV2        `json:"price,omitempty"`
	Availability *wireAvailabilityV2 `json:"availability,omitempty"`
	Media        []wireMediaV2       `json:"media,omitempty"`
	Seller       *wireSellerV2       `json:"seller,omitempty"`
	Eligible     *wireEligibleV2     `json:"eligible,omitempty"`
	Inputs       []wireInputV2       `json:"inputs,omitempty"`
}

type wireAvailabilityV2 struct {
	Available  *bool  `json:"available,omitempty"`
	Status     string `json:"status,omitempty"`
	RunningLow *bool  `json:"running_low,omitempty"`
}

type wireSellerV2 struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
	URL    string `json:"url,omitempty"`
}

type wireEligibleV2 struct {
	NativeCheckout bool `json:"native_checkout,omitempty"`
}

type wireInputV2 struct {
	ID    string `json:"id"`
	Match string `json:"match,omitempty"`
}

type wirePaginationV2 struct {
	Cursor      string `json:"cursor,omitempty"`
	HasNextPage *bool  `json:"has_next_page,omitempty"`
	TotalCount  *int   `json:"total_count,omitempty"`
}

func (v *wireProductV2) UnmarshalJSON(raw []byte) error {
	type fields wireProductV2
	var decoded fields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		*v = wireProductV2{}
		return nil
	}
	*v = wireProductV2(decoded)
	return nil
}

func (v *wireVariantV2) UnmarshalJSON(raw []byte) error {
	type fields wireVariantV2
	var decoded fields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		*v = wireVariantV2{}
		return nil
	}
	*v = wireVariantV2(decoded)
	return nil
}

func (v *wireMediaV2) UnmarshalJSON(raw []byte) error {
	type fields wireMediaV2
	var decoded fields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		*v = wireMediaV2{}
		return nil
	}
	*v = wireMediaV2(decoded)
	return nil
}

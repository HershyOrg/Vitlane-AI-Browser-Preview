package app

import (
	"math/big"
	"net/url"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type LiveVariantOptionV2 struct {
	Name  string
	Value string
}

type LiveVariantReviewRowV2 struct {
	PriceUnknown    bool
	VariantID       string
	Title           string
	PriceMinor      int64
	Currency        string
	Available       bool
	SelectedOptions []LiveVariantOptionV2
	MediaURL        string
	ProductURL      string
}

type LiveVariantReviewPageV2 struct {
	Source         researchdomain.Source
	RelationToken  string
	RelationStatus string
	Truncated      bool
	CandidateID    string
	ProductTitle   string
	MerchantDomain string
	Rows           []LiveVariantReviewRowV2
	PageSize       int
	HasNext        bool
	NextCursor     string
	ObservedAt     string
	Metrics        LiveCatalogReviewMetricsV2
}

// EnableVariantChoiceV2 activates the production-shaped Storefront
// product(handle) page service for the authenticated Phase 8 workspace.
func (service *LiveCatalogReviewServiceV2) EnableVariantChoiceV2(
	choice *VariantChoiceServiceV2,
) error {
	if service == nil || choice == nil {
		return fault.New(fault.InvalidInput, "LIVE_VARIANT_CHOICE_CONFIG_INVALID", false)
	}
	service.variantChoice = choice
	return nil
}

func liveVariantReviewPageFromChoiceV2(
	choice VariantChoicePageV2,
	metrics LiveCatalogReviewMetricsV2,
) (LiveVariantReviewPageV2, error) {
	rows := make([]LiveVariantReviewRowV2, 0, len(choice.Rows))
	for _, row := range choice.Rows {
		priceMinor, ok := liveStorefrontMinorUnitsV2(row.ObservedPrice)
		if !ok {
			return LiveVariantReviewPageV2{}, fault.New(
				fault.ProviderRejected, string(CatalogFailureSchemaMismatch), false,
			)
		}
		options := make([]LiveVariantOptionV2, 0, len(row.SelectedOptions))
		for _, option := range row.SelectedOptions {
			options = append(options, LiveVariantOptionV2{Name: option.Name, Value: option.Value})
		}
		mediaURL := ""
		if row.Media != nil {
			mediaURL = row.Media.URL
		}
		rows = append(rows, LiveVariantReviewRowV2{
			VariantID: row.VariantRef.VariantID, Title: row.Title,
			PriceMinor: priceMinor, Currency: string(row.ObservedPrice.Currency),
			Available: row.AvailableForSale, SelectedOptions: options,
			MediaURL: mediaURL, ProductURL: liveVariantProductURLV2(
				choice.ProductURL, row.VariantRef.VariantID,
			),
		})
	}
	return LiveVariantReviewPageV2{
		CandidateID: choice.CandidateID, ProductTitle: choice.ProductTitle,
		MerchantDomain: choice.MerchantDomain, Rows: rows,
		PageSize: VariantChoicePageSizeV2, HasNext: choice.NextCursor != "",
		NextCursor: choice.NextCursor,
		ObservedAt: choice.ObservedAt.Format(time.RFC3339Nano), Metrics: metrics,
	}, nil
}

func liveStorefrontMinorUnitsV2(money shareddomain.Money) (int64, bool) {
	value, ok := new(big.Rat).SetString(money.Amount)
	if !ok || value.Sign() < 0 {
		return 0, false
	}
	exponent := workspaceCurrencyExponentV2(string(money.Currency))
	scale := int64(1)
	for range exponent {
		scale *= 10
	}
	value.Mul(value, big.NewRat(scale, 1))
	return value.Num().Int64(), value.IsInt() && value.Num().IsInt64()
}

func liveVariantProductURLV2(productURL, variantID string) string {
	parsed, err := url.Parse(strings.TrimSpace(productURL))
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	variantID = strings.TrimPrefix(strings.TrimSpace(variantID), "gid://shopify/ProductVariant/")
	if variantID == "" {
		return parsed.String()
	}
	query := parsed.Query()
	query.Set("variant", variantID)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

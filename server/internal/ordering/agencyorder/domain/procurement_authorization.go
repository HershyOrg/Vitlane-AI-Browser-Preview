package domain

import (
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const (
	ProcurementAuthorizationManualOperator = "MANUAL_OPERATOR_PURCHASE"
	ProcurementConditionProviderSnapshot   = "PROVIDER_SNAPSHOT"
	ProcurementConditionNotProvided        = "NOT_PROVIDED_BY_PROVIDER"
	ManualSiteStepResolution               = "MANUAL_SITE_STEP"

	ProcurementAuthorizationCopyV1 = "procurement-authorization.v1"
	procurementMessageLimit        = 500
)

// ProviderNotice is immutable checkout observation evidence. Presentation and
// audience are server-owned so clients never infer order behavior from raw
// provider strings. Notices do not participate in quote readiness.
type ProviderNotice struct {
	Source       string `json:"source"`
	Type         string `json:"type,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Code         string `json:"code,omitempty"`
	SafePath     string `json:"safePath,omitempty"`
	Text         string `json:"text,omitempty"`
	Presentation string `json:"presentation"`
	Audience     string `json:"audience"`
	Registered   bool   `json:"registered"`
}

type ProviderPolicyLink struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url"`
}

// ManualSiteStep records a checkout condition that the operator handles on
// the merchant site. It is evidence, not an automated checkout dependency.
type ManualSiteStep struct {
	Resolution string `json:"resolution"`
	Kind       string `json:"kind"`
	Code       string `json:"code"`
	SafePath   string `json:"safePath,omitempty"`
}

// ProcurementApproval is the exact customer-authored portion of an
// authorization. AcceptedAt is server-owned and therefore intentionally absent.
type ProcurementApproval struct {
	OrderMessage    string `json:"orderMessage,omitempty"`
	DeliveryMessage string `json:"deliveryMessage,omitempty"`
	AgencyConsent   bool   `json:"agencyConsent"`
	PrivacyConsent  bool   `json:"privacyConsent"`
	Locale          string `json:"locale"`
	CopyVersion     string `json:"copyVersion"`
}

func (a ProcurementApproval) Validate() error {
	if !a.AgencyConsent || !a.PrivacyConsent ||
		(a.Locale != "en-US" && a.Locale != "ko-KR") ||
		a.CopyVersion != ProcurementAuthorizationCopyV1 ||
		utf8.RuneCountInString(a.OrderMessage) > procurementMessageLimit ||
		utf8.RuneCountInString(a.DeliveryMessage) > procurementMessageLimit {
		return ErrInvalid
	}
	return nil
}

func (a ProcurementApproval) SameAs(other ProcurementApproval) bool {
	left, leftErr := shareddomain.CanonicalJSONHash(a.normalized())
	right, rightErr := shareddomain.CanonicalJSONHash(other.normalized())
	return leftErr == nil && rightErr == nil && left == right
}

func (a ProcurementApproval) normalized() ProcurementApproval {
	return a
}

type AuthorizedProcurementLine struct {
	LineID          string   `json:"lineId"`
	ProductURL      string   `json:"productUrl"`
	ProductTitle    string   `json:"productTitle"`
	VariantID       string   `json:"variantId"`
	VariantTitle    string   `json:"variantTitle"`
	SelectedOptions []string `json:"selectedOptions"`
	Quantity        int      `json:"quantity"`
	UnitPrice       Money    `json:"unitPrice"`
	LineSubtotal    Money    `json:"lineSubtotal"`
}

type ProcurementConditions struct {
	ShippingStatus     string               `json:"shippingStatus"`
	CancellationStatus string               `json:"cancellationStatus"`
	ReturnStatus       string               `json:"returnStatus"`
	DeliveryGroups     []DeliveryGroup      `json:"deliveryGroups"`
	TaxTotal           Money                `json:"taxTotal"`
	DutiesDisposition  string               `json:"dutiesDisposition"`
	PolicyLinks        []ProviderPolicyLink `json:"policyLinks,omitempty"`
}

type AuthorizedProcurementShop struct {
	MerchantID         string                      `json:"merchantId"`
	ShopDomain         string                      `json:"shopDomain"`
	Lines              []AuthorizedProcurementLine `json:"lines"`
	ApprovedAmount     Money                       `json:"approvedAmount"`
	Conditions         ProcurementConditions       `json:"conditions"`
	ProviderNotices    []ProviderNotice            `json:"providerNotices,omitempty"`
	ManualSiteSteps    []ManualSiteStep            `json:"manualSiteSteps,omitempty"`
	QuoteFingerprint   string                      `json:"quoteFingerprint"`
	EvidenceHash       string                      `json:"evidenceHash"`
	ContinueURLSafeRef string                      `json:"continueUrlSafeRef,omitempty"`
	ContinueURLHash    string                      `json:"continueUrlHash,omitempty"`
}

// ProcurementAuthorization is an immutable customer-approved scope for a
// human operator to purchase from the merchant site. Shopify checkout
// capabilities are evidence/reference only and never the authorization itself.
type ProcurementAuthorization struct {
	Kind                    string                      `json:"kind"`
	Shops                   []AuthorizedProcurementShop `json:"shops"`
	ApprovedPassThrough     Money                       `json:"approvedPassThrough"`
	ApprovedAgencyFee       Money                       `json:"approvedAgencyFee"`
	ApprovedCustomerPayable Money                       `json:"approvedCustomerPayable"`
	CustomerApproval        ProcurementApproval         `json:"customerApproval"`
	OrderSheetSessionID     string                      `json:"orderSheetSessionId"`
	SourceCartSnapshotHash  string                      `json:"sourceCartSnapshotHash"`
	DisplayedSnapshotHash   string                      `json:"displayedSnapshotHash"`
	ExecutionProfileHash    string                      `json:"executionProfileHash"`
	AcceptedAt              time.Time                   `json:"acceptedAt"`
	AuthorizationHash       string                      `json:"authorizationHash"`
}

func BuildProcurementAuthorization(
	session OrderSheetSession,
	approval ProcurementApproval,
	executionProfileHash string,
	acceptedAt time.Time,
) (ProcurementAuthorization, error) {
	if err := approval.Validate(); err != nil || strings.TrimSpace(executionProfileHash) == "" ||
		strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.SourceCart.SnapshotHash) == "" ||
		strings.TrimSpace(session.DisplayedSnapshotHash) == "" || acceptedAt.IsZero() {
		return ProcurementAuthorization{}, ErrInvalid
	}
	approval = approval.normalized()

	linesByID := make(map[string]ExactLine, len(session.Lines))
	for _, line := range session.Lines {
		if err := validateAuthorizationLine(line); err != nil {
			return ProcurementAuthorization{}, err
		}
		if _, duplicate := linesByID[line.LineID]; duplicate {
			return ProcurementAuthorization{}, ErrInvalid
		}
		linesByID[line.LineID] = line
	}

	authorization := ProcurementAuthorization{
		Kind:                    ProcurementAuthorizationManualOperator,
		ApprovedPassThrough:     session.PassThroughTotal,
		ApprovedAgencyFee:       session.AgencyFee,
		ApprovedCustomerPayable: session.CustomerPayableTotal,
		CustomerApproval:        approval,
		OrderSheetSessionID:     session.ID,
		SourceCartSnapshotHash:  session.SourceCart.SnapshotHash,
		DisplayedSnapshotHash:   session.DisplayedSnapshotHash,
		ExecutionProfileHash:    executionProfileHash,
		AcceptedAt:              acceptedAt.UTC(),
	}
	coveredLines := make(map[string]struct{}, len(linesByID))
	for _, checkout := range session.MerchantCheckouts {
		shop, err := buildAuthorizedShop(checkout, linesByID)
		if err != nil {
			return ProcurementAuthorization{}, err
		}
		for _, line := range shop.Lines {
			if _, duplicate := coveredLines[line.LineID]; duplicate {
				return ProcurementAuthorization{}, ErrInvalid
			}
			coveredLines[line.LineID] = struct{}{}
		}
		authorization.Shops = append(authorization.Shops, shop)
	}
	if len(coveredLines) != len(linesByID) {
		return ProcurementAuthorization{}, ErrInvalid
	}
	sort.Slice(authorization.Shops, func(i, j int) bool {
		return authorization.Shops[i].ShopDomain < authorization.Shops[j].ShopDomain
	})
	hash, err := shareddomain.CanonicalJSONHash(authorization)
	if err != nil {
		return ProcurementAuthorization{}, err
	}
	authorization.AuthorizationHash = hash
	return authorization, nil
}

func (a ProcurementAuthorization) VerifyHash() error {
	want := a.AuthorizationHash
	if strings.TrimSpace(want) == "" {
		return ErrInvalid
	}
	a.AuthorizationHash = ""
	actual, err := shareddomain.CanonicalJSONHash(a)
	if err != nil || actual != want {
		return ErrInvalid
	}
	return nil
}

func (a ProcurementAuthorization) ValidateForOrder(order AgencyOrder) error {
	if err := a.VerifyHash(); err != nil || a.CustomerApproval.Validate() != nil ||
		a.Kind != ProcurementAuthorizationManualOperator || len(a.Shops) == 0 ||
		a.ExecutionProfileHash == "" || a.ExecutionProfileHash != order.ExecutionProfileHash ||
		a.OrderSheetSessionID != order.IssuanceEvidence.OrderSheetSessionID ||
		a.SourceCartSnapshotHash != order.SourceCart.SnapshotHash ||
		a.DisplayedSnapshotHash != order.IssuanceEvidence.DisplayedSnapshotHash ||
		a.ApprovedPassThrough != order.PassThroughTotal ||
		a.ApprovedAgencyFee != order.AgencyFee.Total ||
		a.ApprovedCustomerPayable != order.CustomerPayableTotal ||
		!a.AcceptedAt.Equal(order.IssuedAt) {
		return ErrInvalid
	}
	return nil
}

func validateAuthorizationLine(line ExactLine) error {
	if strings.TrimSpace(line.LineID) == "" || strings.TrimSpace(line.ShopDomain) == "" ||
		strings.TrimSpace(line.ProductURL) == "" || strings.TrimSpace(line.ProductTitle) == "" ||
		strings.TrimSpace(line.VariantID) == "" || strings.TrimSpace(line.VariantTitle) == "" ||
		line.Quantity < 1 || line.UnitPrice.Validate(false) != nil || line.LineSubtotal.Validate(false) != nil ||
		line.UnitPrice.AmountMinor > math.MaxInt64/int64(line.Quantity) ||
		line.LineSubtotal.AmountMinor != line.UnitPrice.AmountMinor*int64(line.Quantity) {
		return ErrInvalid
	}
	return nil
}

func buildAuthorizedShop(
	checkout MerchantCheckout,
	linesByID map[string]ExactLine,
) (AuthorizedProcurementShop, error) {
	shopDomain := strings.ToLower(strings.TrimSpace(checkout.ShopDomain))
	if shopDomain == "" || strings.TrimSpace(checkout.MerchantID) == "" ||
		checkout.AuthoritativeTotal.Validate(false) != nil || checkout.TaxTotal.Validate(true) != nil ||
		strings.TrimSpace(checkout.QuoteFingerprint) == "" || strings.TrimSpace(checkout.EvidenceHash) == "" {
		return AuthorizedProcurementShop{}, ErrInvalid
	}
	shop := AuthorizedProcurementShop{
		MerchantID: checkout.MerchantID, ShopDomain: shopDomain,
		ApprovedAmount: checkout.AuthoritativeTotal,
		Conditions: ProcurementConditions{
			ShippingStatus:     ProcurementConditionNotProvided,
			CancellationStatus: ProcurementConditionNotProvided,
			ReturnStatus:       ProcurementConditionNotProvided,
			DeliveryGroups:     cloneAuthorizationDeliveryGroups(checkout.DeliveryGroups),
			TaxTotal:           checkout.TaxTotal, DutiesDisposition: checkout.DutiesDisposition,
			PolicyLinks: append([]ProviderPolicyLink(nil), checkout.PolicyLinks...),
		},
		ProviderNotices:  append([]ProviderNotice(nil), checkout.ProviderNotices...),
		ManualSiteSteps:  append([]ManualSiteStep(nil), checkout.ManualSiteSteps...),
		QuoteFingerprint: checkout.QuoteFingerprint, EvidenceHash: checkout.EvidenceHash,
		// Optional diagnostic references only; purchasing never depends on them.
		ContinueURLSafeRef: checkout.ContinueURLSafeRef, ContinueURLHash: checkout.ContinueURLHash,
	}
	for _, link := range checkout.PolicyLinks {
		if !validProviderPolicyLink(link, shopDomain) {
			return AuthorizedProcurementShop{}, ErrInvalid
		}
		switch strings.ToUpper(strings.TrimSpace(link.Kind)) {
		case "SHIPPING", "FULFILLMENT":
			shop.Conditions.ShippingStatus = ProcurementConditionProviderSnapshot
		case "CANCELLATION", "CANCEL":
			shop.Conditions.CancellationStatus = ProcurementConditionProviderSnapshot
		case "RETURN", "REFUND":
			shop.Conditions.ReturnStatus = ProcurementConditionProviderSnapshot
		}
	}
	if len(checkout.DeliveryGroups) > 0 {
		shop.Conditions.ShippingStatus = ProcurementConditionProviderSnapshot
	}
	for _, notice := range checkout.ProviderNotices {
		if !validProviderNotice(notice) {
			return AuthorizedProcurementShop{}, ErrInvalid
		}
	}
	for _, lineRef := range checkout.LineRefs {
		line, found := linesByID[lineRef]
		if !found || strings.ToLower(strings.TrimSpace(line.ShopDomain)) != shopDomain {
			return AuthorizedProcurementShop{}, ErrInvalid
		}
		shop.Lines = append(shop.Lines, AuthorizedProcurementLine{
			LineID: line.LineID, ProductURL: line.ProductURL, ProductTitle: line.ProductTitle,
			VariantID: line.VariantID, VariantTitle: line.VariantTitle,
			SelectedOptions: append([]string(nil), line.SelectedOptions...), Quantity: line.Quantity,
			UnitPrice: line.UnitPrice, LineSubtotal: line.LineSubtotal,
		})
	}
	if len(shop.Lines) == 0 {
		return AuthorizedProcurementShop{}, ErrInvalid
	}
	sort.Slice(shop.Lines, func(i, j int) bool { return shop.Lines[i].LineID < shop.Lines[j].LineID })
	return shop, nil
}

func validProviderPolicyLink(link ProviderPolicyLink, shopDomain string) bool {
	parsed, err := url.Parse(strings.TrimSpace(link.URL))
	return err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), shopDomain) &&
		strings.TrimSpace(link.Kind) != "" && len(link.URL) <= 2048 && utf8.RuneCountInString(link.Label) <= 200
}

func validProviderNotice(notice ProviderNotice) bool {
	if (notice.Source != "MESSAGE" && notice.Source != "REQUIREMENT") ||
		(notice.Presentation != "NOTICE" && notice.Presentation != "DISCLOSURE" &&
			notice.Presentation != "INTERNAL") ||
		(notice.Audience != "CUSTOMER_AND_OPERATOR" && notice.Audience != "OPERATOR") ||
		utf8.RuneCountInString(notice.Text) > 1000 ||
		(notice.Type == "" && notice.Severity == "" && notice.Code == "" && notice.Text == "") {
		return false
	}
	for _, value := range []string{notice.Type, notice.Severity, notice.Code, notice.SafePath} {
		if len(value) > 128 {
			return false
		}
		for _, char := range value {
			if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
				!(char >= '0' && char <= '9') && char != '_' && char != '-' && char != '.' {
				return false
			}
		}
	}
	return true
}

func cloneAuthorizationDeliveryGroups(source []DeliveryGroup) []DeliveryGroup {
	result := make([]DeliveryGroup, len(source))
	for index := range source {
		result[index] = source[index]
		result[index].LineRefs = append([]string(nil), source[index].LineRefs...)
		result[index].Options = append([]DeliveryOption(nil), source[index].Options...)
	}
	return result
}

package domain

import (
	"strings"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// PaymentRailPayPalLive is a dormant issuance choice. Runtime configuration
// keeps it unavailable until the explicit Live issue gate is enabled; defining
// the value here lets the same immutable order model survive that activation.
const PaymentRailPayPalLive PaymentRail = "PAYPAL_LIVE"

const (
	PaymentProviderPayPal = "PAYPAL"
	PaymentProviderGIWA   = "GIWA"

	ProviderEnvironmentSandbox = "SANDBOX"
	ProviderEnvironmentLive    = "LIVE"
	ProviderEnvironmentTestnet = "TESTNET"

	AssetUSD     = "USD"
	AssetTVITUSD = "TVITUSD"

	EconomicEffectNoRealValue = "NO_REAL_VALUE"
	EconomicEffectRealMoney   = "REAL_MONEY"

	MerchantExecutionSimulatedNoEffect = "SIMULATED_NO_EFFECT"
	MerchantExecutionLiveEffect        = "LIVE_MERCHANT_EFFECT"
)

// OrderExecutionProfile is the immutable intended execution mode of one
// AgencyOrder. Deployment configuration may choose which profile new orders
// can issue, but it never rewrites an existing order's profile.
type OrderExecutionProfile struct {
	PaymentRail           string `json:"paymentRail"`
	ProviderEnvironment   string `json:"providerEnvironment"`
	Asset                 string `json:"asset"`
	EconomicEffect        string `json:"economicEffect"`
	MerchantExecutionMode string `json:"merchantExecutionMode"`
}

// Validate only admits the three reviewed profile tuples. In particular there
// is no GIWA value-asset profile and no Sandbox real-money combination.
func (p OrderExecutionProfile) Validate() error {
	p = p.normalized()
	switch p {
	case payPalSandboxExecutionProfile(),
		tvitUSDExecutionProfile(),
		payPalLiveExecutionProfile():
		return nil
	default:
		return ErrInvalid
	}
}

func (p OrderExecutionProfile) normalized() OrderExecutionProfile {
	return OrderExecutionProfile{
		PaymentRail:           strings.ToUpper(strings.TrimSpace(p.PaymentRail)),
		ProviderEnvironment:   strings.ToUpper(strings.TrimSpace(p.ProviderEnvironment)),
		Asset:                 strings.ToUpper(strings.TrimSpace(p.Asset)),
		EconomicEffect:        strings.ToUpper(strings.TrimSpace(p.EconomicEffect)),
		MerchantExecutionMode: strings.ToUpper(strings.TrimSpace(p.MerchantExecutionMode)),
	}
}

func (p OrderExecutionProfile) Hash() (string, error) {
	p = p.normalized()
	if err := p.Validate(); err != nil {
		return "", err
	}
	return shareddomain.CanonicalJSONHash(p)
}

func payPalSandboxExecutionProfile() OrderExecutionProfile {
	return OrderExecutionProfile{
		PaymentRail: PaymentProviderPayPal, ProviderEnvironment: ProviderEnvironmentSandbox,
		Asset: AssetUSD, EconomicEffect: EconomicEffectNoRealValue,
		MerchantExecutionMode: MerchantExecutionSimulatedNoEffect,
	}
}

func tvitUSDExecutionProfile() OrderExecutionProfile {
	return OrderExecutionProfile{
		PaymentRail: PaymentProviderGIWA, ProviderEnvironment: ProviderEnvironmentTestnet,
		Asset: AssetTVITUSD, EconomicEffect: EconomicEffectNoRealValue,
		MerchantExecutionMode: MerchantExecutionSimulatedNoEffect,
	}
}

func payPalLiveExecutionProfile() OrderExecutionProfile {
	return OrderExecutionProfile{
		PaymentRail: PaymentProviderPayPal, ProviderEnvironment: ProviderEnvironmentLive,
		Asset: AssetUSD, EconomicEffect: EconomicEffectRealMoney,
		MerchantExecutionMode: MerchantExecutionLiveEffect,
	}
}

func ExecutionProfileForRail(rail PaymentRail) (OrderExecutionProfile, error) {
	switch rail {
	case PaymentRailTVITUSD:
		return tvitUSDExecutionProfile(), nil
	case PaymentRailPayPalSandbox:
		return payPalSandboxExecutionProfile(), nil
	case PaymentRailPayPalLive:
		return payPalLiveExecutionProfile(), nil
	default:
		return OrderExecutionProfile{}, ErrInvalid
	}
}

// LegacyPaymentSelection preserves the existing public PaymentSelection field
// while the normalized profile is persisted as independent scalar columns.
func (p OrderExecutionProfile) LegacyPaymentSelection() PaymentSelection {
	p = p.normalized()
	merchantExecution := "SIMULATED"
	if p.MerchantExecutionMode == MerchantExecutionLiveEffect {
		merchantExecution = "LIVE"
	}
	return PaymentSelection{
		Rail: p.PaymentRail, ProviderEnvironment: p.ProviderEnvironment,
		Asset: p.Asset, EconomicEffect: p.EconomicEffect,
		MerchantExecution: merchantExecution,
	}
}

func ExecutionProfileFromSelection(selection PaymentSelection) (OrderExecutionProfile, error) {
	mode := strings.ToUpper(strings.TrimSpace(selection.MerchantExecution))
	switch mode {
	case "SIMULATED", MerchantExecutionSimulatedNoEffect:
		mode = MerchantExecutionSimulatedNoEffect
	case "LIVE", MerchantExecutionLiveEffect:
		mode = MerchantExecutionLiveEffect
	}
	profile := OrderExecutionProfile{
		PaymentRail: selection.Rail, ProviderEnvironment: selection.ProviderEnvironment,
		Asset: selection.Asset, EconomicEffect: selection.EconomicEffect,
		MerchantExecutionMode: mode,
	}.normalized()
	if err := profile.Validate(); err != nil {
		return OrderExecutionProfile{}, err
	}
	return profile, nil
}

func (p OrderExecutionProfile) MatchesSelection(selection PaymentSelection) bool {
	other, err := ExecutionProfileFromSelection(selection)
	return err == nil && p.normalized() == other
}

func (o AgencyOrder) ValidateExecutionProfile() error {
	if err := o.ExecutionProfile.Validate(); err != nil ||
		!o.ExecutionProfile.MatchesSelection(o.PaymentSelection) {
		return ErrInvalid
	}
	hash, err := o.ExecutionProfile.Hash()
	if err != nil || hash != o.ExecutionProfileHash ||
		o.ProcurementAuthorization.ExecutionProfileHash != hash {
		return ErrInvalid
	}
	return nil
}

func (i PaymentInstruction) ValidateExecutionProfile(order AgencyOrder) error {
	if err := order.ValidateExecutionProfile(); err != nil ||
		i.AgencyOrderID != order.ID ||
		i.AgencyOrderSnapshotHash != order.SnapshotHash ||
		i.ExecutionProfileHash != order.ExecutionProfileHash ||
		!order.ExecutionProfile.MatchesSelection(i.PaymentSelection) {
		return ErrInvalid
	}
	return nil
}

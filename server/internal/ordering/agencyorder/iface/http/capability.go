package http

import (
	"context"
	"net/http"
	"strings"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type LiveCapabilityReader interface {
	State(context.Context) (liveapp.Snapshot, error)
}

type paymentRail struct {
	State                  string `json:"state"`
	OrderIssueState        string `json:"orderIssueState"`
	PaymentInitiationState string `json:"paymentInitiationState"`
	PaymentMethod          string `json:"paymentMethod"`
	ProviderEnvironment    string `json:"providerEnvironment"`
	Asset                  string `json:"asset"`
	EconomicEffect         string `json:"economicEffect"`
}

// Capability v2 advertises all three independent choices. Live reads the
// database-backed deny overlay on every request and fails closed when it
// cannot be read; no credential or merchant binding is exposed.
func Capability(enabled bool, checkoutProvider string, sandboxEnabled, liveConfigured bool,
	live LiveCapabilityReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, reason, provider := "UNAVAILABLE", "AGENCY_ORDER_DISABLED", "NONE"
		tvitState := "UNAVAILABLE"
		if enabled {
			state, reason = "READY", ""
			provider = strings.ToUpper(strings.TrimSpace(checkoutProvider))
			tvitState = "READY"
		}
		sandboxState := "UNAVAILABLE"
		if enabled && sandboxEnabled {
			sandboxState = "READY"
		}
		liveState, liveIssue, liveMoney := "UNAVAILABLE", "UNAVAILABLE", "UNAVAILABLE"
		var revision int64
		if enabled && liveConfigured && live != nil {
			liveState, liveIssue, liveMoney = "PAUSED", "PAUSED", "PAUSED"
			if snapshot, err := live.State(r.Context()); err == nil {
				revision = snapshot.Version
				if snapshot.OrderIssue.Effective {
					liveIssue = "READY"
				}
				if snapshot.PayPalMoney.Effective {
					liveMoney = "READY"
				}
				if snapshot.OrderIssue.Effective && snapshot.PayPalMoney.Effective &&
					snapshot.MerchantEffect.Effective {
					liveState = "READY"
				}
			}
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"schemaVersion": "vitlane.agency-order-capability.v2",
			"capability": map[string]any{
				"state": state, "reasonCode": reason, "checkoutProvider": provider,
				"capabilityRevision": revision,
				"paymentRails": map[string]any{
					"tvitusd": paymentRail{State: tvitState, OrderIssueState: tvitState,
						PaymentInitiationState: tvitState, PaymentMethod: "TVITUSD",
						ProviderEnvironment: "TESTNET", Asset: "TVITUSD", EconomicEffect: "NO_REAL_VALUE"},
					"paypalSandbox": paymentRail{State: sandboxState, OrderIssueState: sandboxState,
						PaymentInitiationState: sandboxState, PaymentMethod: "PAYPAL_SANDBOX",
						ProviderEnvironment: "SANDBOX", Asset: "USD", EconomicEffect: "NO_REAL_VALUE"},
					"paypalLive": paymentRail{State: liveState, OrderIssueState: liveIssue,
						PaymentInitiationState: liveMoney, PaymentMethod: "PAYPAL_LIVE",
						ProviderEnvironment: "LIVE", Asset: "USD", EconomicEffect: "REAL_MONEY"},
				},
			},
		})
	}
}

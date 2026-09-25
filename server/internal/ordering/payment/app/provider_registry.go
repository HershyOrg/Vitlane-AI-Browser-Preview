package app

import (
	"fmt"
	"strings"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

// ProviderRegistration keeps PayPal clients and webhook identities separated
// by provider environment. Issue/CaptureEnabled are new-effect gates; GET,
// webhook reconciliation and refunds remain routable for historical orders.
type ProviderRegistration struct {
	Environment    string
	Client         ProviderClient
	WebhookID      string
	IssueEnabled   bool
	CaptureEnabled bool
}

type ProviderRegistry struct {
	entries map[string]ProviderRegistration
}

func NewProviderRegistry(registrations ...ProviderRegistration) (ProviderRegistry, error) {
	registry := ProviderRegistry{entries: make(map[string]ProviderRegistration, len(registrations))}
	for _, registration := range registrations {
		environment := strings.ToUpper(strings.TrimSpace(registration.Environment))
		if (environment != "SANDBOX" && environment != "LIVE") ||
			registration.Client == nil || strings.TrimSpace(registration.WebhookID) == "" {
			return ProviderRegistry{}, fmt.Errorf("%w: invalid PayPal provider registration", domain.ErrInvalid)
		}
		if _, exists := registry.entries[environment]; exists {
			return ProviderRegistry{}, fmt.Errorf("%w: duplicate PayPal environment %s", domain.ErrInvalid, environment)
		}
		registration.Environment = environment
		registration.WebhookID = strings.TrimSpace(registration.WebhookID)
		registry.entries[environment] = registration
	}
	return registry, nil
}

func (r ProviderRegistry) Get(environment string) (ProviderRegistration, bool) {
	registration, ok := r.entries[strings.ToUpper(strings.TrimSpace(environment))]
	return registration, ok
}

func (r ProviderRegistry) Environments() []string {
	result := make([]string, 0, len(r.entries))
	for _, environment := range []string{"SANDBOX", "LIVE"} {
		if _, ok := r.entries[environment]; ok {
			result = append(result, environment)
		}
	}
	return result
}

package domain

import (
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"time"
)

// SubscriptionTerms are copied into a proposal and never edited after acceptance.
type SubscriptionTerms struct {
	SchemaVersion string              `json:"schemaVersion"`
	Criteria      TargetCriteriaSetV1 `json:"criteria"`
	Country       string              `json:"country"`
	Currency      string              `json:"currency"`
	MaximumMinor  *int64              `json:"maximumMinor,omitempty"`
	Keywords      []string            `json:"keywords"`
	ExpiresAt     time.Time           `json:"expiresAt"`
}

func (t SubscriptionTerms) Validate(now time.Time) error {
	if t.SchemaVersion != "vitlane.research-subscription-terms.v1" || t.Criteria.Validate() != nil ||
		(t.Country != "KR" && t.Country != "US") || (t.Currency != "KRW" && t.Currency != "USD") ||
		len(t.Keywords) == 0 || len(t.Keywords) > 12 || !t.ExpiresAt.After(now) || t.ExpiresAt.After(now.Add(31*24*time.Hour)) {
		return fault.New(fault.InvalidInput, "RESEARCH_SUBSCRIPTION_INVALID", false)
	}
	if t.MaximumMinor != nil && (*t.MaximumMinor < 0 || *t.MaximumMinor > 9007199254740991) {
		return fault.New(fault.InvalidInput, "RESEARCH_SUBSCRIPTION_INVALID", false)
	}
	for _, k := range t.Keywords {
		if strings.TrimSpace(k) == "" || len(k) > 300 {
			return fault.New(fault.InvalidInput, "RESEARCH_SUBSCRIPTION_INVALID", false)
		}
	}
	return nil
}

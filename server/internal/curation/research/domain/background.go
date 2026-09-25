package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	"net/url"
	"strings"
	"time"
)

type DealProduct struct {
	SchemaVersion string            `json:"schemaVersion"`
	Provider      string            `json:"provider"`
	ExternalID    string            `json:"externalId"`
	Identity      string            `json:"identity"`
	ProductRef    *SourceProductRef `json:"productRef,omitempty"`
	Title         string            `json:"title"`
	Description   string            `json:"description,omitempty"`
	URL           string            `json:"url"`
	ImageURL      string            `json:"imageUrl,omitempty"`
	Country       string            `json:"country"`
	Currency      string            `json:"currency"`
	PriceMinor    *int64            `json:"priceMinor,omitempty"`
	ShippingMinor *int64            `json:"shippingMinor,omitempty"`
	ObservedAt    time.Time         `json:"observedAt"`
	ExpiresAt     time.Time         `json:"expiresAt"`
}

func (p DealProduct) Valid(now time.Time) bool {
	u, e := url.Parse(p.URL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" {
		return false
	}
	if p.ProductRef != nil && (p.ProductRef.Validate() != nil || p.ProductRef.IdentityKey() != p.Identity) {
		return false
	}
	if p.ProductRef != nil {
		if p.Country != p.ProductRef.Marketplace {
			return false
		}
		if p.ProductRef.Source.KoreanExternal() {
			parsed, e := SourceProductFromURL(p.URL)
			if e != nil || parsed != *p.ProductRef {
				return false
			}
		} else if p.ProductRef.Source == SourceAmazon && (u.Host != "www.amazon.com" || u.Path != "/dp/"+p.ProductRef.AnchorASIN) {
			return false
		}
	}
	return p.SchemaVersion == "vitlane.deal-product.v1" && p.Provider != "" && len(p.Provider) < 100 &&
		p.ExternalID != "" && len(p.ExternalID) < 300 && p.Identity != "" && len(p.Identity) < 500 &&
		strings.TrimSpace(p.Title) != "" && len(p.Title) <= 2000 && len(p.Description) <= 4000 &&
		(p.Country == "KR" || p.Country == "US") && (p.Currency == "KRW" || p.Currency == "USD") &&
		!p.ObservedAt.IsZero() && !p.ObservedAt.After(now.Add(time.Minute)) && p.ExpiresAt.After(now) &&
		(p.PriceMinor == nil || (*p.PriceMinor >= 0 && *p.PriceMinor <= 9007199254740991)) &&
		(p.ShippingMinor == nil || (*p.ShippingMinor >= 0 && *p.ShippingMinor <= 9007199254740991))
}

// Missing delivery cost never blocks a deal. Unknown product price can still be
// semantically useful; only a known, comparable price can exceed the rough cap.
func (p DealProduct) RoughPrice() *int64 {
	if p.PriceMinor == nil {
		return nil
	}
	n := *p.PriceMinor
	if p.ShippingMinor != nil {
		n += *p.ShippingMinor
	}
	return &n
}
func DealFilter(p DealProduct, t c.SubscriptionTerms, now time.Time) bool {
	if !p.ExpiresAt.After(now) || p.Country != t.Country {
		return false
	}
	if n := p.RoughPrice(); n != nil && t.MaximumMinor != nil && p.Currency == t.Currency && *n > *t.MaximumMinor {
		return false
	}
	text := strings.ToLower(p.Title + " " + p.Description)
	for _, x := range t.Criteria.Exclusions {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(x))) {
			return false
		}
	}
	return true
}
func TermsHash(t c.SubscriptionTerms) string {
	// Expiry is lifecycle, not relevance; identical requests share semantic work.
	b, _ := json.Marshal(struct {
		Criteria          c.TargetCriteriaSetV1
		Country, Currency string
		MaximumMinor      *int64
		Keywords          []string
	}{t.Criteria, t.Country, t.Currency, t.MaximumMinor, t.Keywords})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type ResearchSubscription struct {
	ID         string              `json:"id"`
	CurationID string              `json:"curationId"`
	TargetID   string              `json:"targetId"`
	Status     string              `json:"status"`
	Terms      c.SubscriptionTerms `json:"terms"`
	CreatedAt  time.Time           `json:"createdAt"`
}
type ResearchFinding struct {
	ID             string      `json:"id"`
	SubscriptionID string      `json:"subscriptionId"`
	TargetID       string      `json:"targetId"`
	Status         string      `json:"status"`
	Product        DealProduct `json:"product"`
	Reason         string      `json:"reason"`
	CreatedAt      time.Time   `json:"createdAt"`
	CandidateID    string      `json:"candidateId,omitempty"`
}
type ResearchCategory struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Definition string `json:"definition"`
}

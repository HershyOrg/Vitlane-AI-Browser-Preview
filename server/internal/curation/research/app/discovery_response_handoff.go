package app

import (
	"encoding/json"
	"sync"
	"time"
)

// An execution response can be delivered once to the first visible-pool read.
// This is neither durable product storage nor a price/variant authority. A
// refresh, a configured candidate, expiry or another process uses fresh lookup.
type discoveryResponseHandoff struct {
	mu      sync.Mutex
	entries map[discoveryResponseKey]discoveryResponseEntry
}
type discoveryResponseKey struct{ owner, curation, target string }
type discoveryResponseEntry struct {
	expires  time.Time
	products map[string]CatalogProductObservation
}

func (h *discoveryResponseHandoff) put(input CatalogWorkspaceSearchInputV2, result LiveCatalogReviewResultV2, bindings []CatalogCandidateIDBindingV2, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries == nil {
		h.entries = map[discoveryResponseKey]discoveryResponseEntry{}
	}
	for k, e := range h.entries {
		if !e.expires.After(now) {
			delete(h.entries, k)
		}
	}
	// Bound both lifetime and cardinality; eviction is a safe fresh-lookup miss.
	if len(h.entries) >= 32 {
		var oldest discoveryResponseKey
		var expiry time.Time
		for k, e := range h.entries {
			if expiry.IsZero() || e.expires.Before(expiry) {
				oldest, expiry = k, e.expires
			}
		}
		delete(h.entries, oldest)
	}
	ids := map[string]string{}
	for _, b := range bindings {
		ids[b.ProposedCandidateID] = b.DurableCandidateID
	}
	entry := discoveryResponseEntry{expires: now.Add(time.Minute), products: map[string]CatalogProductObservation{}}
	for _, p := range result.Search.Products {
		if p.Source() != "SHOPIFY" || p.Locator == nil {
			continue
		}
		raw, err := json.Marshal(p)
		if err != nil {
			continue
		}
		var copy CatalogProductObservation
		if json.Unmarshal(raw, &copy) != nil {
			continue
		}
		id := p.ProviderProductID
		if ids[id] != "" {
			id = ids[id]
		}
		entry.products[id] = copy
		if len(entry.products) == 50 {
			break
		}
	}
	if len(entry.products) > 0 {
		h.entries[discoveryResponseKey{input.UserID, input.CurationID, input.TargetID}] = entry
	}
}
func (h *discoveryResponseHandoff) take(c CatalogCandidateReferenceV2, now time.Time) (CatalogProductObservation, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := discoveryResponseKey{c.UserID, c.CurationID, c.PlanTargetID}
	entry, ok := h.entries[key]
	if !ok {
		return CatalogProductObservation{}, false
	}
	if !entry.expires.After(now) {
		delete(h.entries, key)
		return CatalogProductObservation{}, false
	}
	p, ok := entry.products[c.CandidateID]
	if !ok || p.ProductRef().IdentityKey() != c.ProductRef().IdentityKey() {
		return CatalogProductObservation{}, false
	}
	delete(entry.products, c.CandidateID)
	if len(entry.products) == 0 {
		delete(h.entries, key)
	}
	return p, true
}

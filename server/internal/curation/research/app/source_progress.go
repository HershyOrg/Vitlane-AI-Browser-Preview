package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// QuerySearchProgress is how far one source has read one query. A source's
// position in "camping chair" says nothing about its position in "folding
// chair", so every (source, query) pair keeps its own.
type QuerySearchProgress struct {
	Cursor string `json:"cursor,omitempty"`
	Page   int    `json:"page"`
	// Exhausted: the source reported no further page for this query.
	Exhausted bool `json:"exhausted,omitempty"`
}

// SourceSearchProgress is one source's place in a Round's queries. Query,
// Cursor and Page are the query the source runs next and that query's own
// position; Queries remembers every other query of the same plan, so coming
// back to one continues where it stopped instead of starting over. Opaque
// cursors are scoped to the exact query, market and eligibility inputs.
type SourceSearchProgress struct {
	SchemaVersion string                         `json:"schemaVersion,omitempty"`
	Query         string                         `json:"query"`
	Cursor        string                         `json:"cursor,omitempty"`
	Page          int                            `json:"page"`
	SeedIndex     int                            `json:"seedIndex"`
	Queries       map[string]QuerySearchProgress `json:"queries,omitempty"`
}
type SourceProgressSet map[string]SourceSearchProgress
type sourceProgressRepository interface {
	ReadSourceProgress(context.Context, string, string, string, string) (SourceProgressSet, error)
	SaveSourceProgress(context.Context, string, string, string, string, SourceProgressSet) error
}

func researchSearchFingerprint(q CatalogIntelligenceCatalogQuery, c ResearchContext) string {
	raw, _ := json.Marshal(struct {
		Version string
		Query   CatalogIntelligenceCatalogQuery
		Country string
		Budget  *ResearchBudgetSnapshot
	}{DiscoveryPolicyVersion, q, string(c.ResearchScope.Country), c.Budget})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// SearchProgressFor answers which query a source runs now and from where. A
// source that has not run yet starts at the plan's primary query, page one.
func SearchProgressFor(saved SourceProgressSet, source, query string, seeds []string) SourceSearchProgress {
	p := saved[source]
	if p.Query == "" {
		p.Query = query
		p.SeedIndex = -1
		for i, seed := range seeds {
			if seed == query {
				p.SeedIndex = i
				break
			}
		}
	}
	// The per-query record wins over the mirror fields; a row saved before the
	// per-query record existed has only the mirror fields.
	if own, exists := p.Queries[p.Query]; exists {
		p.Cursor, p.Page = own.Cursor, own.Page
	}
	if p.Page < 1 {
		p.Page = 1
	}
	return p
}

// AdvanceSearchProgress records what the source just learned about the query
// it ran and chooses the one it runs next. The order is the one research has
// always used, depth first: while the current query has another page the source
// stays on it (an opaque cursor wins over a page number), and only when it runs
// out does the source move to the next query of the plan. What the plan changed
// is where the queries come from, not how they are paged. Each query keeps its
// own position, so a query that ran out is skipped instead of being restarted
// at page one; when every query has run out a fresh pass begins, because the
// catalog may have changed since. `queries` is the plan's phrases for this
// source's language, primary first.
func AdvanceSearchProgress(p SourceSearchProgress, queries []string, cursor string, hasNext bool, pageable bool) SourceSearchProgress {
	p.SchemaVersion = DiscoveryPolicyVersion
	positions := make(map[string]QuerySearchProgress, len(p.Queries)+1)
	for query, own := range p.Queries {
		positions[query] = own
	}
	p.Queries = positions
	if p.Page < 1 {
		p.Page = 1
	}
	if hasNext && cursor != "" && cursor != p.Cursor && p.Page < 100 {
		p.Cursor = cursor
		p.Page++
		p.Queries[p.Query] = QuerySearchProgress{Cursor: p.Cursor, Page: p.Page}
		return p
	}
	if pageable && hasNext && p.Page < 100 {
		p.Cursor = ""
		p.Page++
		p.Queries[p.Query] = QuerySearchProgress{Page: p.Page}
		return p
	}
	p.Queries[p.Query] = QuerySearchProgress{Cursor: p.Cursor, Page: p.Page, Exhausted: true}
	order := queries
	if len(order) == 0 {
		order = []string{p.Query}
	}
	index := func(step int) int {
		value := (p.SeedIndex + step) % len(order)
		if value < 0 {
			value += len(order)
		}
		return value
	}
	next, found := 0, false
	for step := 1; step <= len(order); step++ {
		if !p.Queries[order[index(step)]].Exhausted {
			next, found = index(step), true
			break
		}
	}
	if !found {
		p.Queries = map[string]QuerySearchProgress{}
		next = index(1)
	}
	p.SeedIndex = next
	p.Query = order[next]
	own := p.Queries[p.Query]
	p.Cursor, p.Page = own.Cursor, own.Page
	if p.Page < 1 {
		p.Page = 1
	}
	return p
}
func validQuerySeeds(country string, q CatalogIntelligenceCatalogQuery) bool {
	if len(q.QuerySeeds) < 1 || len(q.QuerySeeds) > 4 {
		return false
	}
	for _, seed := range q.QuerySeeds {
		if strings.TrimSpace(seed) == "" || len(seed) > 300 || (country == "US" && containsNonLatinCatalogLetter(seed)) {
			return false
		}
	}
	return true
}

type ResearchDiscoveryOutcome struct {
	SchemaVersion  string `json:"schemaVersion"`
	AddedCount     int    `json:"addedCount"`
	DuplicateCount int    `json:"duplicateCount"`
	RejectedCount  int    `json:"rejectedCount"`
	// EvaluatedCount and UnevaluatedCount split AddedCount: an admitted product
	// without a valid axis assessment is unevaluated, never dropped.
	EvaluatedCount   int    `json:"evaluatedCount"`
	UnevaluatedCount int    `json:"unevaluatedCount"`
	Status           string `json:"status"`
}

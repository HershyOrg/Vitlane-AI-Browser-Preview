package app

import "testing"

// One source's progress over a plan's queries pages the way research always has:
// a query is read page after page until it runs out, and only then the next one.
func TestSourceProgressPagesAQueryUntilItRunsOutThenMovesOn(t *testing.T) {
	queries := []string{"classic fountain pen", "fountain pen classic", "fountain pen"}
	p := SearchProgressFor(nil, "SHOPIFY", queries[0], queries)
	if p.Query != queries[0] || p.Page != 1 || p.Cursor != "" {
		t.Fatalf("a source starts at the primary query, page one: %+v", p)
	}
	// An opaque cursor: the source stays on the query and reads its next page.
	p = AdvanceSearchProgress(p, queries, "opaque:first/2", true, false)
	if p.Query != queries[0] || p.Cursor != "opaque:first/2" || p.Page != 2 {
		t.Fatalf("a query with another page is read on: %+v", p)
	}
	p = AdvanceSearchProgress(p, queries, "opaque:first/3", true, false)
	if p.Query != queries[0] || p.Cursor != "opaque:first/3" || p.Page != 3 {
		t.Fatalf("and on: %+v", p)
	}
	// It ran out: the next query of the plan, from its first page.
	p = AdvanceSearchProgress(p, queries, "", false, false)
	if p.Query != queries[1] || p.Cursor != "" || p.Page != 1 {
		t.Fatalf("only a query that ran out is left: %+v", p)
	}
	if own := p.Queries[queries[0]]; !own.Exhausted || own.Page != 3 {
		t.Fatalf("the query keeps its own record: %+v", own)
	}
	// The second and the third run out too: every query has, so a fresh pass begins at the primary.
	p = AdvanceSearchProgress(p, queries, "", false, false)
	if p.Query != queries[2] {
		t.Fatalf("queries are read in the plan's order: %+v", p)
	}
	p = AdvanceSearchProgress(p, queries, "", false, false)
	if p.Query != queries[0] || p.Page != 1 || p.Cursor != "" || len(p.Queries) != 0 {
		t.Fatalf("a fresh pass starts clean at the primary query: %+v", p)
	}

	// A numbered-page source (Amazon, a mall's site search) pages the same way, by number.
	amazon := AdvanceSearchProgress(SearchProgressFor(nil, "AMAZON", queries[0], queries), queries, "", true, true)
	if amazon.Page != 2 || amazon.Query != queries[0] {
		t.Fatalf("numbered pages are read on: %+v", amazon)
	}
	// A source without pages moves to the next query every Round.
	own := AdvanceSearchProgress(SearchProgressFor(nil, "OWN_PRODUCT", queries[0], queries), queries, "", true, false)
	if own.Page != 1 || own.Query != queries[1] {
		t.Fatalf("no pages, next query: %+v", own)
	}
	// A query that ran out is skipped, not restarted: the source had read the first query to its end
	// before a later plan change put it back at the front of the order.
	skipped := SourceSearchProgress{Query: queries[2], SeedIndex: 2, Page: 1, Queries: map[string]QuerySearchProgress{
		queries[0]: {Page: 4, Exhausted: true},
	}}
	skipped = AdvanceSearchProgress(skipped, queries, "", false, false)
	if skipped.Query != queries[1] {
		t.Fatalf("an exhausted query is skipped while another still has pages: %+v", skipped)
	}
	// Two sources never share a position.
	set := SourceProgressSet{"SHOPIFY": p, "AMAZON": amazon}
	if SearchProgressFor(set, "AMAZON", queries[0], queries).Page != 2 || SearchProgressFor(set, "KURLY_JSON", queries[0], queries).Page != 1 {
		t.Fatal("each source reads its own record")
	}
	// A row saved before positions were kept per query still continues from its cursor.
	legacy := SearchProgressFor(SourceProgressSet{"SHOPIFY": {Query: queries[0], Cursor: "opaque:old", Page: 3, SeedIndex: 0}}, "SHOPIFY", queries[0], queries)
	if legacy.Cursor != "opaque:old" || legacy.Page != 3 {
		t.Fatalf("an older row keeps its place: %+v", legacy)
	}
}
func TestAssessmentImagesRejectNonPublicURLs(t *testing.T) {
	for _, s := range []string{"http://example.com/a.png", "https://localhost/a", "https://127.0.0.1/a", "https://10.1.2.3/a", "https://foo.local/a", "https://user:pass@example.com/a"} {
		if safeAssessmentImageURL(s) {
			t.Fatal(s)
		}
	}
	if !safeAssessmentImageURL("https://cdn.shopify.com/image.png") {
		t.Fatal("public image rejected")
	}
}

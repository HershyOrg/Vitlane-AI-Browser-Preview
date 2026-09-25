package http

import (
	"net/http/httptest"
	"testing"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
)

func TestParseAgencyOrderListQueryRoundTripsOpaqueCursor(t *testing.T) {
	want := agencyapp.ListCursor{
		View: agencyapp.ListViewNeedsAttention, Sort: agencyapp.ListSortUpdatedDesc,
		UpdatedAt:     time.Date(2026, 8, 15, 2, 3, 4, 0, time.UTC),
		IssuedAt:      time.Date(2026, 8, 14, 2, 3, 4, 0, time.UTC),
		AgencyOrderID: "c5e88ca9-ade4-477f-a6d7-f82103629e30",
	}
	cursor, err := encodeListCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/agencyOrder?view=NEEDS_ATTENTION&sort=UPDATED_DESC&limit=25&cursor="+cursor, nil)
	query, err := parseListQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if query.Limit != 25 || query.Cursor == nil || *query.Cursor != want {
		t.Fatalf("query=%+v cursor=%+v", query, query.Cursor)
	}
}

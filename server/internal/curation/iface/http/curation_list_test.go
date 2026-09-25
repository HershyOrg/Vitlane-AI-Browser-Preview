package http

import (
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
)

func TestParseCurationListQueryRoundTripsOpaqueCursors(t *testing.T) {
	t.Parallel()

	want := curationapp.CurationListCursor{
		CreatedAt:  time.Date(2026, 9, 16, 9, 30, 0, 123456000, time.UTC),
		CurationID: "41b7771a-458a-41de-9cb7-bcdb88558152",
	}
	cursor, err := encodeCurationListCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"before", "after"} {
		query, err := parseCurationListQuery(httptest.NewRequest("GET", "/api/v1/curations?"+name+"="+cursor, nil))
		if err != nil {
			t.Fatal(err)
		}
		got := query.Before
		if name == "after" {
			got = query.After
		}
		if got == nil || *got != want {
			t.Fatalf("%s: query=%#v want=%#v", name, query, want)
		}
	}
	first, err := parseCurationListQuery(httptest.NewRequest("GET", "/api/v1/curations", nil))
	if err != nil || first.Before != nil || first.After != nil {
		t.Fatalf("first page query=%#v err=%v", first, err)
	}
}

func TestParseCurationListQueryRefusesTheRemovedSavedViews(t *testing.T) {
	t.Parallel()

	valid, err := encodeCurationListCursor(curationapp.CurationListCursor{
		CreatedAt:  time.Date(2026, 9, 16, 9, 30, 0, 0, time.UTC),
		CurationID: "41b7771a-458a-41de-9cb7-bcdb88558152",
	})
	if err != nil {
		t.Fatal(err)
	}
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for name, rawQuery := range map[string]string{
		"old view":         "view=IN_PROGRESS",
		"old sort":         "sort=UPDATED_DESC",
		"old limit":        "limit=20",
		"old cursor":       "cursor=" + valid,
		"repeated":         "before=" + valid + "&before=" + valid,
		"not base64":       "before=!!!!",
		"unknown field":    "after=" + encode(`{"createdAt":"2026-09-16T09:30:00Z","curationId":"41b7771a-458a-41de-9cb7-bcdb88558152","view":"ALL"}`),
		"id is not a UUID": "after=" + encode(`{"createdAt":"2026-09-16T09:30:00Z","curationId":"curation-1"}`),
		"missing time":     "before=" + encode(`{"curationId":"41b7771a-458a-41de-9cb7-bcdb88558152"}`),
	} {
		request := httptest.NewRequest("GET", "/api/v1/curations", nil)
		request.URL.RawQuery = rawQuery
		if _, err := url.ParseQuery(rawQuery); err != nil {
			t.Fatalf("%s: the test query itself must parse: %v", name, err)
		}
		if _, err := parseCurationListQuery(request); err == nil {
			t.Fatalf("%s: accepted %q", name, rawQuery)
		}
	}
}

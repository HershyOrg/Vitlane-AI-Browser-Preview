package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type curationListTestRepository struct {
	*memoryPlanningRepository
	page     CurationListRepositoryPage
	query    CurationListQuery
	pageSize int
	calls    int
}

func (r *curationListTestRepository) ListCurations(
	_ context.Context,
	_ string,
	query CurationListQuery,
	pageSize int,
) (CurationListRepositoryPage, error) {
	r.calls++
	r.query, r.pageSize = query, pageSize
	return r.page, nil
}

func curationListTestItems(count int) []CurationListItem {
	base := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	ids := []string{
		"52000000-0000-4000-8000-000000000003",
		"52000000-0000-4000-8000-000000000002",
		"52000000-0000-4000-8000-000000000001",
	}
	items := make([]CurationListItem, 0, count)
	for index := range count {
		items = append(items, CurationListItem{
			CurationID:    ids[index],
			IntentSummary: "만년필",
			CreatedAt:     base.Add(-time.Duration(index) * time.Hour),
		})
	}
	return items
}

func TestListCurationsFirstPageNamesTheNewestAndOlderRows(t *testing.T) {
	t.Parallel()

	repository := &curationListTestRepository{
		memoryPlanningRepository: &memoryPlanningRepository{},
		page:                     CurationListRepositoryPage{Items: curationListTestItems(3), More: true},
	}
	page, err := (&Service{repository: repository}).ListCurations(context.Background(), "user-1", CurationListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	// One sidebar page is twenty Curations (ADR-0079).
	if repository.pageSize != 20 {
		t.Fatalf("page size=%d", repository.pageSize)
	}
	if page.LatestCursor == nil || page.LatestCursor.CurationID != page.Items[0].CurationID ||
		!page.LatestCursor.CreatedAt.Equal(page.Items[0].CreatedAt) {
		t.Fatalf("latest=%#v", page.LatestCursor)
	}
	if page.NextCursor == nil || page.NextCursor.CurationID != page.Items[2].CurationID {
		t.Fatalf("next=%#v", page.NextCursor)
	}
}

func TestListCurationsCursorsFollowTheDirection(t *testing.T) {
	t.Parallel()

	cursor := CurationListCursor{
		CreatedAt:  time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC),
		CurationID: "52000000-0000-4000-8000-000000000009",
	}
	for _, test := range []struct {
		name       string
		query      CurationListQuery
		page       CurationListRepositoryPage
		wantLatest *CurationListCursor
		wantNext   bool
	}{
		// Nothing new keeps the caller's own cursor, so it can ask again later.
		{name: "after with nothing new", query: CurationListQuery{After: &cursor}, wantLatest: &cursor},
		{name: "after with new rows", query: CurationListQuery{After: &cursor},
			page:       CurationListRepositoryPage{Items: curationListTestItems(2)},
			wantLatest: &CurationListCursor{CreatedAt: curationListTestItems(1)[0].CreatedAt, CurationID: curationListTestItems(1)[0].CurationID}},
		// More new rows than a page holds: the next cursor tells the caller to start over.
		{name: "after with a full page", query: CurationListQuery{After: &cursor},
			page:       CurationListRepositoryPage{Items: curationListTestItems(3), More: true},
			wantLatest: &CurationListCursor{CreatedAt: curationListTestItems(1)[0].CreatedAt, CurationID: curationListTestItems(1)[0].CurationID},
			wantNext:   true},
		// An older page never moves what the caller knows as newest.
		{name: "before", query: CurationListQuery{Before: &cursor},
			page: CurationListRepositoryPage{Items: curationListTestItems(3), More: true}, wantNext: true},
		{name: "last older page", query: CurationListQuery{Before: &cursor},
			page: CurationListRepositoryPage{Items: curationListTestItems(1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &curationListTestRepository{memoryPlanningRepository: &memoryPlanningRepository{}, page: test.page}
			page, err := (&Service{repository: repository}).ListCurations(context.Background(), "user-1", test.query)
			if err != nil {
				t.Fatal(err)
			}
			if page.Items == nil {
				t.Fatal("items must encode as an empty array, not null")
			}
			if (test.wantLatest == nil) != (page.LatestCursor == nil) ||
				(test.wantLatest != nil && (page.LatestCursor.CurationID != test.wantLatest.CurationID ||
					!page.LatestCursor.CreatedAt.Equal(test.wantLatest.CreatedAt))) {
				t.Fatalf("latest=%#v want=%#v", page.LatestCursor, test.wantLatest)
			}
			if (page.NextCursor != nil) != test.wantNext {
				t.Fatalf("next=%#v want next=%v", page.NextCursor, test.wantNext)
			}
		})
	}
}

func TestListCurationsRejectsAmbiguousOrDamagedCursors(t *testing.T) {
	t.Parallel()

	valid := CurationListCursor{
		CreatedAt:  time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC),
		CurationID: "52000000-0000-4000-8000-000000000009",
	}
	notUUID, noTime := valid, valid
	notUUID.CurationID = "curation-9"
	noTime.CreatedAt = time.Time{}
	for name, query := range map[string]CurationListQuery{
		"both directions": {Before: &valid, After: &valid},
		"id is no UUID":   {Before: &notUUID},
		"no time":         {After: &noTime},
	} {
		repository := &curationListTestRepository{memoryPlanningRepository: &memoryPlanningRepository{}}
		_, err := (&Service{repository: repository}).ListCurations(context.Background(), "user-1", query)
		if !errors.Is(err, ErrCurationListQueryInvalid) || repository.calls != 0 {
			t.Fatalf("%s: err=%v calls=%d", name, err, repository.calls)
		}
	}
}

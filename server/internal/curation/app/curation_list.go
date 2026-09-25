package app

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// CurationListPageSize is how many Curations one sidebar page shows.
const CurationListPageSize = 20

var ErrCurationListQueryInvalid = errors.New("CURATION_LIST_QUERY_INVALID")

var curationListIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// CurationListCursor names one row of the creation-time order. The creation
// time orders the rows; the ID only breaks ties between Curations created at
// the same instant, because on its own a random UUID has no order.
type CurationListCursor struct {
	CreatedAt  time.Time `json:"createdAt"`
	CurationID string    `json:"curationId"`
}

// CurationListQuery asks for the newest page when both cursors are absent,
// for older rows with Before (the sidebar's "more") and for newer rows with
// After (the check a tab makes when it becomes visible again).
type CurationListQuery struct {
	Before *CurationListCursor
	After  *CurationListCursor
}

// CurationListItem is exactly what one sidebar row shows: the title and the
// Curation it links to. The creation time is the order the rows keep.
type CurationListItem struct {
	CurationID    string    `json:"curationId"`
	IntentSummary string    `json:"intentSummary"`
	CreatedAt     time.Time `json:"createdAt"`
}

type CurationListRepositoryPage struct {
	Items []CurationListItem
	// More reports a row beyond the last item in the requested direction.
	More bool
}

type CurationListRepository interface {
	ListCurations(context.Context, string, CurationListQuery, int) (CurationListRepositoryPage, error)
}

type CurationListPage struct {
	Items []CurationListItem
	// NextCursor continues toward older rows. On an After page it also means
	// more new rows exist than one page holds, so the caller starts over from
	// this page instead of prepending it.
	NextCursor *CurationListCursor
	// LatestCursor is the newest row the caller now knows, for its next After
	// check. Older pages leave it empty because they do not move it.
	LatestCursor *CurationListCursor
}

func (s *Service) ListCurations(
	ctx context.Context,
	userID string,
	query CurationListQuery,
) (CurationListPage, error) {
	repository, ok := s.repository.(CurationListRepository)
	if !ok {
		return CurationListPage{}, errors.New("CURATION_LIST_NOT_AVAILABLE")
	}
	if !validCurationListQuery(query) {
		return CurationListPage{}, ErrCurationListQueryInvalid
	}
	stored, err := repository.ListCurations(ctx, userID, query, CurationListPageSize)
	if err != nil {
		return CurationListPage{}, err
	}
	page := CurationListPage{Items: stored.Items}
	if page.Items == nil {
		page.Items = []CurationListItem{}
	}
	if stored.More && len(page.Items) > 0 {
		next := curationListCursorOf(page.Items[len(page.Items)-1])
		page.NextCursor = &next
	}
	if query.Before == nil {
		switch {
		case len(page.Items) > 0:
			latest := curationListCursorOf(page.Items[0])
			page.LatestCursor = &latest
		case query.After != nil:
			latest := *query.After
			page.LatestCursor = &latest
		}
	}
	return page, nil
}

func curationListCursorOf(item CurationListItem) CurationListCursor {
	return CurationListCursor{CreatedAt: item.CreatedAt, CurationID: item.CurationID}
}

func validCurationListQuery(query CurationListQuery) bool {
	if query.Before != nil && query.After != nil {
		return false
	}
	for _, cursor := range []*CurationListCursor{query.Before, query.After} {
		if cursor != nil && !ValidCurationListCursor(*cursor) {
			return false
		}
	}
	return true
}

// ValidCurationListCursor rejects a cursor the database would refuse, so a
// damaged cursor is a bad request rather than a server error.
func ValidCurationListCursor(cursor CurationListCursor) bool {
	return !cursor.CreatedAt.IsZero() && curationListIDPattern.MatchString(cursor.CurationID)
}

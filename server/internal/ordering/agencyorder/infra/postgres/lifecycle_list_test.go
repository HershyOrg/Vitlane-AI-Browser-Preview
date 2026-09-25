package postgres

import (
	"strings"
	"testing"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
)

func TestAgencyOrderListClausesKeepViewAndCursorOnSameProjection(t *testing.T) {
	where, order, args := agencyOrderListClauses("user-1", agencyapp.ListQuery{
		View: agencyapp.ListViewNeedsAttention, Sort: agencyapp.ListSortUpdatedDesc,
		Limit: 30,
		Cursor: &agencyapp.ListCursor{
			View: agencyapp.ListViewNeedsAttention, Sort: agencyapp.ListSortUpdatedDesc,
			UpdatedAt:     time.Date(2026, 8, 15, 2, 3, 4, 0, time.UTC),
			IssuedAt:      time.Date(2026, 8, 14, 2, 3, 4, 0, time.UTC),
			AgencyOrderID: "c5e88ca9-ade4-477f-a6d7-f82103629e30",
		},
	})
	if !strings.Contains(where, "PAYMENT_RECONCILIATION") ||
		!strings.Contains(where, "process.updated_at") ||
		order != "process.updated_at DESC, orders.issued_at DESC, orders.id DESC" ||
		len(args) != 4 {
		t.Fatalf("where=%s order=%s args=%v", where, order, args)
	}
}

func TestFinishedViewContainsEveryTerminalReason(t *testing.T) {
	predicate := agencyOrderListViewPredicate(agencyapp.ListViewFinished)
	if predicate != "process.state='TERMINAL'" || strings.Contains(predicate, "terminal_reason") {
		t.Fatalf("finished predicate=%q", predicate)
	}
}

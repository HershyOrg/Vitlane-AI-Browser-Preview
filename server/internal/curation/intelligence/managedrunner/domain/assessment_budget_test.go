package domain

import "testing"

func TestAssessmentBudgetUsesActualCandidateAndAxisCounts(t *testing.T) {
	model := DefaultModels()[1]
	for _, tc := range []struct {
		count, axes int
		want        int64
	}{{1, 8, 4000}, {3, 8, 4000}, {15, 3, 6000}, {25, 8, 20000}, {50, 3, 20000}, {50, 8, 40000}} {
		got := model.AssessmentModel(tc.count, tc.axes)
		if got.MaxOutputTokens != tc.want {
			t.Fatalf("%+v got %d", tc, got.MaxOutputTokens)
		}
	}
	if model.MaxOutputTokens != 8000 {
		t.Fatal("mutated registry")
	}
}

// The reservation keeps a margin above the largest per-candidate output the
// 2026-09-17 measurement observed (462 tokens at eight axes), and the minimum
// research headroom is what one query plus the smallest batch could cost.
func TestAssessmentReservationMarginAndMinimumHeadroom(t *testing.T) {
	model := DefaultModels()[1]
	if got := model.AssessmentModel(10, 8).MaxOutputTokens; got < 10*462 || got > 10*1120 {
		t.Fatalf("ten-candidate reservation %d must sit between the observed maximum and the old formula", got)
	}
	headroom := model.MinimumResearchHeadroom()
	query := model.WorstCost(2048)
	if headroom <= query || headroom > 2*query {
		t.Fatalf("headroom %d should be a query plus a floor-sized batch, query=%d", headroom, query)
	}
}

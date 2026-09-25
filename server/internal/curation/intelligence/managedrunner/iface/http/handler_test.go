package http

import (
	"testing"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

func TestHistoryDaysUsesClosedOperatorRanges(t *testing.T) {
	for input, want := range map[string]int{
		"":   30,
		"7":  7,
		"30": 30,
		"90": 90,
	} {
		got, ok := historyDays(input)
		if !ok || got != want {
			t.Fatalf("historyDays(%q)=(%d,%v), want (%d,true)", input, got, ok, want)
		}
	}
	for _, input := range []string{"0", "14", "91", "invalid"} {
		if _, ok := historyDays(input); ok {
			t.Fatalf("historyDays(%q) unexpectedly accepted", input)
		}
	}
}

func TestUsageSummaryReportsTokensCostAndPercent(t *testing.T) {
	summary := usageSummary(runnerdomain.UsageCounters{
		ReservedMicros: 250_000,
		SettledMicros:  1_000_000,
		RequestCount:   8,
		InputTokens:    12_000,
		OutputTokens:   3_000,
	}, 10_000_000)
	if summary.TotalTokens != 15_000 ||
		summary.SpentMicros != 1_250_000 ||
		summary.UsagePercent != 12.5 {
		t.Fatalf("summary=%+v", summary)
	}
}

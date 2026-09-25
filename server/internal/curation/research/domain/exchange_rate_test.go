package domain

import (
	"testing"
	"time"
)

func TestResearchExchangeConversionRespectsMinorUnitsAndStaleness(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	rate := DailyExchangeRate{Base: "USD", Quote: "KRW", Rate: "1340.18", AsOf: "2026-09-10", ObservedAt: now}
	for _, row := range []struct {
		amount   int64
		from, to string
		want     int64
	}{{100, "USD", "KRW", 1340}, {1234, "USD", "KRW", 16538}, {134018, "KRW", "USD", 10000}, {999, "KRW", "KRW", 999}} {
		got, err := ConvertResearchMinor(row.amount, row.from, row.to, rate, now)
		if err != nil || got != row.want {
			t.Fatalf("%+v got=%d err=%v", row, got, err)
		}
	}
	rate.AsOf = "2026-09-01"
	if _, err := ConvertResearchMinor(100, "USD", "KRW", rate, now); err == nil {
		t.Fatal("expired rate accepted")
	}
	rate.AsOf = "2026-09-10"
	rate.Rate = "0"
	if _, err := ConvertResearchMinor(100, "KRW", "USD", rate, now); err == nil {
		t.Fatal("zero divisor accepted")
	}
}

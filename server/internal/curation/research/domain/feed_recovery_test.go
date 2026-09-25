package domain

import (
	"testing"
	"time"
)

func TestFeedLinkRetrySchedule(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		attempt int
		result  FeedLinkResult
		status  string
		delay   time.Duration
	}{
		{1, FeedLinkResult{Attempted: true, Retryable: true}, "PENDING", 15 * time.Minute},
		{2, FeedLinkResult{Attempted: true, Retryable: true}, "PENDING", time.Hour},
		{3, FeedLinkResult{Attempted: true, Retryable: true}, "PENDING", 4 * time.Hour},
		{4, FeedLinkResult{Attempted: true, Retryable: true}, "FAILED", 0},
		{1, FeedLinkResult{Attempted: true}, "FAILED", 0},
		{0, FeedLinkResult{}, "PENDING", 15 * time.Minute},
		{1, FeedLinkResult{Attempted: true, Retryable: true, RetryAfter: 2 * time.Hour}, "PENDING", 2 * time.Hour},
	} {
		status, next := FeedLinkNextAttempt(tc.attempt, now, tc.result)
		if status != tc.status || next.Sub(now) != tc.delay {
			t.Fatalf("%+v: %s %s", tc, status, next.Sub(now))
		}
	}
}

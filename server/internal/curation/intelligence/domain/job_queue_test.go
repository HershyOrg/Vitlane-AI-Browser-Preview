package domain

import (
	"testing"
	"time"
)

func TestExecutedAttemptsExcludeDeferrals(t *testing.T) {
	job := Job{AttemptCount: 5, DeferCount: 3}
	if job.ExecutedAttempts() != 2 {
		t.Fatalf("executed = %d, want 2", job.ExecutedAttempts())
	}
	// A corrupt count can never make the ceiling negative.
	if (Job{AttemptCount: 1, DeferCount: 4}).ExecutedAttempts() != 0 {
		t.Fatal("executed attempts must not go negative")
	}
}

func TestRetryBackoffGrowsWithExecutedAttempts(t *testing.T) {
	for executed, want := range map[int]time.Duration{0: 5 * time.Second, 1: 5 * time.Second, 2: 20 * time.Second, 3: 60 * time.Second, 9: 60 * time.Second} {
		if got := RetryBackoff(executed); got != want {
			t.Fatalf("backoff(%d) = %s, want %s", executed, got, want)
		}
	}
	if MaximumDeferralWait > 2*time.Minute || MaximumDeferrals*int(MaximumDeferralWait) > int(20*time.Minute) {
		t.Fatalf("a job could queue for too long: %d × %s", MaximumDeferrals, MaximumDeferralWait)
	}
}

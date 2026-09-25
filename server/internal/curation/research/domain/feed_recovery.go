package domain

import (
	"net/url"
	"strings"
	"time"
)

// LastID advances only after the entire interval (LastID, HeadID] was read.
type FeedCheckpoint struct {
	Version  int64
	LastID   int64
	HeadID   int64
	BeforeID int64
}
type FeedBatch struct {
	Checkpoint FeedCheckpoint
	Products   []DealProduct
	Links      map[string]string
}
type FeedLinkJob struct {
	ID, Token, Provider, URL string
	Attempts                 int
	ExpiresAt                time.Time
}
type FeedLinkResult struct {
	ProductRef *SourceProductRef
	Attempted  bool
	Retryable  bool
	RetryAfter time.Duration
	Reason     string
}

const FeedLinkMaximumAttempts = 4

// Four actual attempts: initial, then 15 minutes, 1 hour and 4 hours.
func FeedLinkNextAttempt(attempts int, now time.Time, result FeedLinkResult) (string, time.Time) {
	if result.ProductRef != nil {
		return "RESOLVED", now
	}
	if result.Attempted && (!result.Retryable || attempts >= FeedLinkMaximumAttempts) {
		return "FAILED", now
	}
	delay := 15 * time.Minute
	if result.Attempted && attempts == 2 {
		delay = time.Hour
	}
	if result.Attempted && attempts >= 3 {
		delay = 4 * time.Hour
	}
	if result.RetryAfter > delay {
		delay = result.RetryAfter
	}
	return "PENDING", now.Add(delay)
}

func FeedLinkURLAllowed(provider, raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && provider == "TELEGRAM_JIRUM" && u.Scheme == "https" && u.Host == "link.coupang.com" && u.User == nil && strings.HasPrefix(u.Path, "/a/") && u.RawQuery == "" && u.Fragment == ""
}

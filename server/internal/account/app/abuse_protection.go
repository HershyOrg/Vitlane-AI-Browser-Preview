package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var ErrRateLimited = errors.New("RATE_LIMITED")

const (
	RatePolicyWalletRegistrationCreate   = "wallet_registration_create"
	RatePolicyWalletRegistrationComplete = "wallet_registration_complete"
	RatePolicyGoogleLoginBegin           = "google_login_begin"
	RatePolicyGoogleLoginComplete        = "google_login_complete"
)

type RateLimitRule struct {
	Policy    string
	Dimension string
	Subject   string
	Limit     int
	Window    time.Duration
}

func (r RateLimitRule) Valid() bool {
	return strings.TrimSpace(r.Policy) != "" &&
		strings.TrimSpace(r.Dimension) != "" &&
		strings.TrimSpace(r.Subject) != "" &&
		r.Limit > 0 && r.Window > 0
}

type RateLimitDecision struct {
	Allowed    bool
	Policy     string
	RetryAfter time.Duration
}

type AttemptRateLimiter interface {
	Acquire(context.Context, []RateLimitRule, time.Time) (RateLimitDecision, error)
}

type RateLimitError struct {
	Policy     string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

func RateLimitRetryAfter(err error) int {
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		return 0
	}
	seconds := int(math.Ceil(rateLimitErr.RetryAfter.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func enforceRateLimit(
	ctx context.Context,
	limiter AttemptRateLimiter,
	rules []RateLimitRule,
	now time.Time,
) error {
	if limiter == nil {
		return nil
	}
	active := make([]RateLimitRule, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.Subject) == "" {
			continue
		}
		if !rule.Valid() {
			return fmt.Errorf("invalid account attempt rate limit rule")
		}
		active = append(active, rule)
	}
	if len(active) == 0 {
		return nil
	}
	sort.Slice(active, func(i, j int) bool {
		left := active[i].Policy + "\x00" + active[i].Dimension + "\x00" + active[i].Subject
		right := active[j].Policy + "\x00" + active[j].Dimension + "\x00" + active[j].Subject
		return left < right
	})
	decision, err := limiter.Acquire(ctx, active, now)
	if err != nil {
		return fmt.Errorf("acquire account attempt rate limit: %w", err)
	}
	if decision.Allowed {
		return nil
	}
	return &RateLimitError{
		Policy: decision.Policy, RetryAfter: decision.RetryAfter,
	}
}

func walletCreateRateLimitRules(
	userID, address, origin, source string,
) []RateLimitRule {
	const window = 10 * time.Minute
	return []RateLimitRule{
		{Policy: RatePolicyWalletRegistrationCreate, Dimension: "user", Subject: userID, Limit: 10, Window: window},
		{Policy: RatePolicyWalletRegistrationCreate, Dimension: "address", Subject: address, Limit: 10, Window: window},
		{Policy: RatePolicyWalletRegistrationCreate, Dimension: "origin", Subject: origin, Limit: 100, Window: window},
		{Policy: RatePolicyWalletRegistrationCreate, Dimension: "source", Subject: source, Limit: 30, Window: window},
	}
}

func walletCompleteRateLimitRules(
	userID, address, origin, source string,
) []RateLimitRule {
	const window = 10 * time.Minute
	return []RateLimitRule{
		{Policy: RatePolicyWalletRegistrationComplete, Dimension: "user", Subject: userID, Limit: 20, Window: window},
		{Policy: RatePolicyWalletRegistrationComplete, Dimension: "address", Subject: address, Limit: 20, Window: window},
		{Policy: RatePolicyWalletRegistrationComplete, Dimension: "origin", Subject: origin, Limit: 200, Window: window},
		{Policy: RatePolicyWalletRegistrationComplete, Dimension: "source", Subject: source, Limit: 60, Window: window},
	}
}

func googleLoginBeginRateLimitRules(origin, source string) []RateLimitRule {
	const window = 10 * time.Minute
	return []RateLimitRule{
		{Policy: RatePolicyGoogleLoginBegin, Dimension: "origin", Subject: origin, Limit: 200, Window: window},
		{Policy: RatePolicyGoogleLoginBegin, Dimension: "source", Subject: source, Limit: 20, Window: window},
	}
}

func googleLoginCompleteRateLimitRules(
	browserBinding, origin, source string,
) []RateLimitRule {
	const window = 10 * time.Minute
	return []RateLimitRule{
		{Policy: RatePolicyGoogleLoginComplete, Dimension: "browser_binding", Subject: browserBinding, Limit: 10, Window: window},
		{Policy: RatePolicyGoogleLoginComplete, Dimension: "origin", Subject: origin, Limit: 200, Window: window},
		{Policy: RatePolicyGoogleLoginComplete, Dimension: "source", Subject: source, Limit: 30, Window: window},
	}
}

func EnforceRateLimit(
	ctx context.Context,
	limiter AttemptRateLimiter,
	rules []RateLimitRule,
	now time.Time,
) error {
	return enforceRateLimit(ctx, limiter, rules, now)
}

package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAccountRateLimitPolicyValues(t *testing.T) {
	type expectedRule struct {
		limit  int
		window time.Duration
	}
	rules := append(
		walletCreateRateLimitRules("user", "address", "origin", "source"),
		walletCompleteRateLimitRules("user", "address", "origin", "source")...,
	)
	rules = append(rules, googleLoginBeginRateLimitRules("origin", "source")...)
	rules = append(rules, googleLoginCompleteRateLimitRules("binding", "origin", "source")...)

	want := map[string]expectedRule{
		RatePolicyWalletRegistrationCreate + "/user":       {10, 10 * time.Minute},
		RatePolicyWalletRegistrationCreate + "/address":    {10, 10 * time.Minute},
		RatePolicyWalletRegistrationCreate + "/origin":     {100, 10 * time.Minute},
		RatePolicyWalletRegistrationCreate + "/source":     {30, 10 * time.Minute},
		RatePolicyWalletRegistrationComplete + "/user":     {20, 10 * time.Minute},
		RatePolicyWalletRegistrationComplete + "/address":  {20, 10 * time.Minute},
		RatePolicyWalletRegistrationComplete + "/origin":   {200, 10 * time.Minute},
		RatePolicyWalletRegistrationComplete + "/source":   {60, 10 * time.Minute},
		RatePolicyGoogleLoginBegin + "/origin":             {200, 10 * time.Minute},
		RatePolicyGoogleLoginBegin + "/source":             {20, 10 * time.Minute},
		RatePolicyGoogleLoginComplete + "/browser_binding": {10, 10 * time.Minute},
		RatePolicyGoogleLoginComplete + "/origin":          {200, 10 * time.Minute},
		RatePolicyGoogleLoginComplete + "/source":          {30, 10 * time.Minute},
	}
	if len(rules) != len(want) {
		t.Fatalf("policy rule count=%d want=%d", len(rules), len(want))
	}
	for _, rule := range rules {
		key := fmt.Sprintf("%s/%s", rule.Policy, rule.Dimension)
		expected, ok := want[key]
		if !ok || rule.Limit != expected.limit || rule.Window != expected.window {
			t.Fatalf("policy rule %s=%#v want=%#v", key, rule, expected)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing policy rules=%#v", want)
	}
}

func TestEnforceRateLimitSkipsAbsentOptionalSubjects(t *testing.T) {
	limiter := &walletRateLimiter{}
	err := enforceRateLimit(
		context.Background(), limiter,
		[]RateLimitRule{
			{Policy: "policy", Dimension: "optional", Limit: 1, Window: time.Minute},
			{Policy: "policy", Dimension: "source", Subject: "source", Limit: 1, Window: time.Minute},
		},
		time.Date(2026, 8, 7, 4, 30, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(limiter.calls) != 1 || len(limiter.calls[0]) != 1 ||
		limiter.calls[0][0].Dimension != "source" {
		t.Fatalf("rate limit calls=%#v", limiter.calls)
	}
}

func TestEnforceRateLimitRejectsMalformedActiveRule(t *testing.T) {
	limiter := &walletRateLimiter{}
	err := enforceRateLimit(
		context.Background(), limiter,
		[]RateLimitRule{{
			Policy: "policy", Dimension: "source", Subject: "source",
			Limit: 0, Window: time.Minute,
		}},
		time.Date(2026, 8, 7, 4, 30, 0, 0, time.UTC),
	)
	if err == nil || !strings.Contains(err.Error(), "invalid account attempt rate limit rule") {
		t.Fatalf("error=%v", err)
	}
	if len(limiter.calls) != 0 {
		t.Fatalf("invalid rule reached limiter: %#v", limiter.calls)
	}
}

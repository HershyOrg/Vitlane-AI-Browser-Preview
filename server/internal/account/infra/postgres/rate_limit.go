package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

var errRateLimitDenied = errors.New("account rate limit denied")

type RateLimiter struct {
	database   *sharedpostgres.Database
	secret     []byte
	keyVersion int
}

func NewRateLimiter(
	database *sharedpostgres.Database,
	secret string,
	keyVersion int,
) (*RateLimiter, error) {
	if database == nil || len(strings.TrimSpace(secret)) < 16 || keyVersion <= 0 {
		return nil, fmt.Errorf("invalid Account rate limiter configuration")
	}
	return &RateLimiter{
		database: database, secret: []byte(secret), keyVersion: keyVersion,
	}, nil
}

type keyedRateLimitRule struct {
	rule        accountapp.RateLimitRule
	subjectHash []byte
}

func (l *RateLimiter) Acquire(
	ctx context.Context,
	rules []accountapp.RateLimitRule,
	now time.Time,
) (accountapp.RateLimitDecision, error) {
	if len(rules) == 0 {
		return accountapp.RateLimitDecision{Allowed: true}, nil
	}
	keyed := make([]keyedRateLimitRule, 0, len(rules))
	for _, rule := range rules {
		if !rule.Valid() {
			return accountapp.RateLimitDecision{}, fmt.Errorf(
				"invalid rate limit rule %q/%q", rule.Policy, rule.Dimension,
			)
		}
		keyed = append(keyed, keyedRateLimitRule{
			rule: rule, subjectHash: l.subjectHash(rule.Dimension, rule.Subject),
		})
	}
	sort.Slice(keyed, func(i, j int) bool {
		left := keyed[i].rule.Policy + "\x00" + keyed[i].rule.Dimension + "\x00" + string(keyed[i].subjectHash)
		right := keyed[j].rule.Policy + "\x00" + keyed[j].rule.Dimension + "\x00" + string(keyed[j].subjectHash)
		return left < right
	})

	decision := accountapp.RateLimitDecision{Allowed: true}
	err := l.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := l.database.Queryer(txContext)
		for _, item := range keyed {
			windowStart := now.UTC().Truncate(item.rule.Window)
			expiresAt := windowStart.Add(item.rule.Window)
			var hitCount int
			var storedExpiry time.Time
			err := queryer.QueryRowContext(txContext, `
				INSERT INTO account_rate_limit_buckets(
					policy_id, dimension, subject_key_version, subject_hash,
					window_started_at, window_seconds, rule_limit,
					hit_count, expires_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,$9)
				ON CONFLICT (
					policy_id, dimension, subject_key_version, subject_hash
				) DO UPDATE SET
					window_started_at=CASE
						WHEN account_rate_limit_buckets.expires_at <= $9
						  OR account_rate_limit_buckets.window_seconds <> $6
						  OR account_rate_limit_buckets.rule_limit <> $7
						THEN $5 ELSE account_rate_limit_buckets.window_started_at
					END,
					window_seconds=$6,
					rule_limit=$7,
					hit_count=CASE
						WHEN account_rate_limit_buckets.expires_at <= $9
						  OR account_rate_limit_buckets.window_seconds <> $6
						  OR account_rate_limit_buckets.rule_limit <> $7
						THEN 1 ELSE account_rate_limit_buckets.hit_count + 1
					END,
					expires_at=CASE
						WHEN account_rate_limit_buckets.expires_at <= $9
						  OR account_rate_limit_buckets.window_seconds <> $6
						  OR account_rate_limit_buckets.rule_limit <> $7
						THEN $8 ELSE account_rate_limit_buckets.expires_at
					END,
					updated_at=$9
				RETURNING hit_count, expires_at
			`, item.rule.Policy, item.rule.Dimension, l.keyVersion,
				item.subjectHash, windowStart, int(item.rule.Window.Seconds()),
				item.rule.Limit, expiresAt, now.UTC()).Scan(&hitCount, &storedExpiry)
			if err != nil {
				return fmt.Errorf("increment Account rate limit bucket: %w", err)
			}
			if hitCount > item.rule.Limit {
				decision = accountapp.RateLimitDecision{
					Allowed: false, Policy: item.rule.Policy,
					RetryAfter: storedExpiry.Sub(now.UTC()),
				}
				return errRateLimitDenied
			}
		}
		return nil
	})
	if errors.Is(err, errRateLimitDenied) {
		if decision.RetryAfter <= 0 {
			decision.RetryAfter = time.Second
		}
		return decision, nil
	}
	if err != nil {
		return accountapp.RateLimitDecision{}, err
	}
	return decision, nil
}

func (l *RateLimiter) subjectHash(dimension, subject string) []byte {
	digest := hmac.New(sha256.New, l.secret)
	fmt.Fprintf(digest, "%d\x00%s\x00%s", l.keyVersion,
		strings.TrimSpace(dimension), strings.TrimSpace(subject))
	return digest.Sum(nil)
}

package lite

import (
	"errors"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestFixedWindowRateLimiterEmailPerMinute(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 1

	assertRateLimitAllowed(t, limiter, req)
	decision, err := limiter.Allow(req)
	assertRateLimitExceeded(t, decision, err, RateLimitRuleEmailMinute)
	if decision.RetryAfterSeconds != 27 {
		t.Fatalf("expected retry after 27 seconds, got %d", decision.RetryAfterSeconds)
	}
}

func TestFixedWindowRateLimiterEmailPerDay(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 10
	req.Policy.SameEmailPerDay = 1

	assertRateLimitAllowed(t, limiter, req)
	decision, err := limiter.Allow(req)
	assertRateLimitExceeded(t, decision, err, RateLimitRuleEmailDay)
	if decision.RetryAfterSeconds != 45267 {
		t.Fatalf("expected retry after 45267 seconds, got %d", decision.RetryAfterSeconds)
	}
}

func TestFixedWindowRateLimiterUserIPPerHour(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 10
	req.Policy.SameEmailPerDay = 10
	req.Policy.SameUserIPPerHour = 1

	assertRateLimitAllowed(t, limiter, req)
	decision, err := limiter.Allow(req)
	assertRateLimitExceeded(t, decision, err, RateLimitRuleUserIPHour)
}

func TestFixedWindowRateLimiterCallerIPPerHour(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 10
	req.Policy.SameEmailPerDay = 10
	req.Policy.SameUserIPPerHour = 10
	req.Policy.SameCallerIPPerHour = 1

	assertRateLimitAllowed(t, limiter, req)
	decision, err := limiter.Allow(req)
	assertRateLimitExceeded(t, decision, err, RateLimitRuleCallerIPHour)
}

func TestFixedWindowRateLimiterMissingUserIPSkipsUserIPLimit(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.UserIP = ""
	req.Policy.SameEmailPerMinute = 10
	req.Policy.SameEmailPerDay = 10
	req.Policy.SameUserIPPerHour = 1
	req.Policy.SameCallerIPPerHour = 10

	assertRateLimitAllowed(t, limiter, req)
	assertRateLimitAllowed(t, limiter, req)
}

func TestFixedWindowRateLimiterUTCWindowReset(t *testing.T) {
	now := time.Date(2026, 5, 29, 7, 59, 59, 1, time.FixedZone("UTC+8", 8*60*60))
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 1
	req.Policy.SameEmailPerDay = 1

	assertRateLimitAllowed(t, limiter, req)
	now = now.Add(2 * time.Second)
	assertRateLimitAllowed(t, limiter, req)
}

func TestFixedWindowRateLimiterRejectedRequestDoesNotIncrementOtherRules(t *testing.T) {
	now := fixedRateLimitTime()
	limiter := NewFixedWindowRateLimiter(func() time.Time { return now })

	req := testRateLimitRequest()
	req.Policy.SameEmailPerMinute = 1
	req.Policy.SameEmailPerDay = 10
	req.Policy.SameUserIPPerHour = 2
	req.Policy.SameCallerIPPerHour = 10

	assertRateLimitAllowed(t, limiter, req)
	decision, err := limiter.Allow(req)
	assertRateLimitExceeded(t, decision, err, RateLimitRuleEmailMinute)

	req.NormalizedToEmail = "other@example.com"
	assertRateLimitAllowed(t, limiter, req)
}

func assertRateLimitAllowed(t *testing.T, limiter *FixedWindowRateLimiter, req RateLimitRequest) {
	t.Helper()

	decision, err := limiter.Allow(req)
	if err != nil {
		t.Fatalf("expected rate limit to allow request: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allowed decision, got %+v", decision)
	}
}

func assertRateLimitExceeded(t *testing.T, decision RateLimitDecision, err error, rule RateLimitRule) {
	t.Helper()

	var exceeded RateLimitExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected rate limit exceeded error, got %v", err)
	}
	if exceeded.Code != domain.ErrorCodeRateLimited {
		t.Fatalf("expected rate_limited code, got %s", exceeded.Code)
	}
	if exceeded.Rule != rule || decision.Rule != rule {
		t.Fatalf("expected rule %s, got decision=%s error=%s", rule, decision.Rule, exceeded.Rule)
	}
	if decision.Allowed {
		t.Fatalf("expected denied decision, got %+v", decision)
	}
}

func testRateLimitRequest() RateLimitRequest {
	return RateLimitRequest{
		AppCode:           "project_a",
		SceneCode:         "register_code",
		NormalizedToEmail: "user@example.com",
		UserIP:            "1.2.3.4",
		CallerIP:          "127.0.0.1",
		Policy: domain.RateLimitPolicy{
			SameEmailPerMinute:  10,
			SameEmailPerDay:     10,
			SameUserIPPerHour:   10,
			SameCallerIPPerHour: 10,
		},
	}
}

func fixedRateLimitTime() time.Time {
	return time.Date(2026, 5, 28, 11, 25, 33, 100, time.UTC)
}

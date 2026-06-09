package lite

import (
	"fmt"
	"sync"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

// RateLimitRule identifies one fixed-window rule.
type RateLimitRule string

const (
	// RateLimitRuleEmailMinute limits one recipient address per minute.
	RateLimitRuleEmailMinute RateLimitRule = "email_minute"
	// RateLimitRuleEmailDay limits one recipient address per UTC day.
	RateLimitRuleEmailDay RateLimitRule = "email_day"
	// RateLimitRuleUserIPHour limits one end-user IP per hour.
	RateLimitRuleUserIPHour RateLimitRule = "user_ip_hour"
	// RateLimitRuleCallerIPHour limits one caller connection IP per hour.
	RateLimitRuleCallerIPHour RateLimitRule = "caller_ip_hour"
)

// RateLimitRequest contains the dimensions needed to consume fixed-window quota.
type RateLimitRequest struct {
	AppCode           string
	SceneCode         string
	NormalizedToEmail string
	UserIP            string
	CallerIP          string
	Policy            domain.RateLimitPolicy
}

// RateLimitDecision describes the result of one fixed-window quota check.
type RateLimitDecision struct {
	Allowed           bool
	Rule              RateLimitRule
	Limit             int
	CurrentCount      int
	RetryAfterSeconds int
	consumed          []rateLimitCounterKey
}

// RateLimitExceededError is returned when a request exceeds any limit rule.
type RateLimitExceededError struct {
	Code              domain.ErrorCode
	Rule              RateLimitRule
	Limit             int
	CurrentCount      int
	RetryAfterSeconds int
}

// Error returns the stable public API error code.
func (e RateLimitExceededError) Error() string {
	return string(e.Code)
}

// FixedWindowRateLimiter stores Lite mode counters in memory.
type FixedWindowRateLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	counters map[rateLimitCounterKey]rateLimitCounter
}

// NewFixedWindowRateLimiter creates an in-memory fixed-window rate limiter.
func NewFixedWindowRateLimiter(now func() time.Time) *FixedWindowRateLimiter {
	if now == nil {
		now = time.Now
	}

	return &FixedWindowRateLimiter{
		now:      now,
		counters: make(map[rateLimitCounterKey]rateLimitCounter),
	}
}

// Allow checks all fixed-window rules and consumes quota only when every rule passes.
func (l *FixedWindowRateLimiter) Allow(request RateLimitRequest) (RateLimitDecision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	l.deleteExpired(now)

	checks, err := buildRateLimitChecks(request, now)
	if err != nil {
		return RateLimitDecision{}, err
	}

	for _, check := range checks {
		key := check.key(request)
		counter := l.counters[key]
		if counter.Count >= check.limit {
			retryAfter := retryAfterSeconds(now, check.expiresAt)
			decision := RateLimitDecision{
				Allowed:           false,
				Rule:              check.rule,
				Limit:             check.limit,
				CurrentCount:      counter.Count,
				RetryAfterSeconds: retryAfter,
			}
			return decision, RateLimitExceededError{
				Code:              domain.ErrorCodeRateLimited,
				Rule:              check.rule,
				Limit:             check.limit,
				CurrentCount:      counter.Count,
				RetryAfterSeconds: retryAfter,
			}
		}
	}

	consumed := make([]rateLimitCounterKey, 0, len(checks))
	for _, check := range checks {
		key := check.key(request)
		counter := l.counters[key]
		counter.Count++
		counter.ExpiresAt = check.expiresAt
		l.counters[key] = counter
		consumed = append(consumed, key)
	}

	return RateLimitDecision{Allowed: true, consumed: consumed}, nil
}

// Rollback releases quota consumed by an allowed decision when request acceptance fails.
func (l *FixedWindowRateLimiter) Rollback(decision RateLimitDecision) {
	if !decision.Allowed || len(decision.consumed) == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, key := range decision.consumed {
		counter, exists := l.counters[key]
		if !exists {
			continue
		}
		if counter.Count <= 1 {
			delete(l.counters, key)
			continue
		}
		counter.Count--
		l.counters[key] = counter
	}
}

type rateLimitCounterKey struct {
	Rule        RateLimitRule
	AppCode     string
	SceneCode   string
	Subject     string
	WindowStart time.Time
}

type rateLimitCounter struct {
	Count     int
	ExpiresAt time.Time
}

type rateLimitCheck struct {
	rule        RateLimitRule
	subject     string
	limit       int
	windowStart time.Time
	expiresAt   time.Time
}

func (l *FixedWindowRateLimiter) deleteExpired(now time.Time) {
	for key, counter := range l.counters {
		if !now.Before(counter.ExpiresAt) {
			delete(l.counters, key)
		}
	}
}

func buildRateLimitChecks(request RateLimitRequest, now time.Time) ([]rateLimitCheck, error) {
	minuteStart := now.Truncate(time.Minute)
	hourStart := now.Truncate(time.Hour)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	checks := []rateLimitCheck{
		{
			rule:        RateLimitRuleEmailMinute,
			subject:     request.NormalizedToEmail,
			limit:       request.Policy.SameEmailPerMinute,
			windowStart: minuteStart,
			expiresAt:   minuteStart.Add(time.Minute),
		},
		{
			rule:        RateLimitRuleEmailDay,
			subject:     request.NormalizedToEmail,
			limit:       request.Policy.SameEmailPerDay,
			windowStart: dayStart,
			expiresAt:   dayStart.AddDate(0, 0, 1),
		},
		{
			rule:        RateLimitRuleCallerIPHour,
			subject:     request.CallerIP,
			limit:       request.Policy.SameCallerIPPerHour,
			windowStart: hourStart,
			expiresAt:   hourStart.Add(time.Hour),
		},
	}
	if request.UserIP != "" {
		checks = append(checks, rateLimitCheck{
			rule:        RateLimitRuleUserIPHour,
			subject:     request.UserIP,
			limit:       request.Policy.SameUserIPPerHour,
			windowStart: hourStart,
			expiresAt:   hourStart.Add(time.Hour),
		})
	}

	for _, check := range checks {
		if check.limit <= 0 {
			return nil, fmt.Errorf("rate limit %s must be greater than 0", check.rule)
		}
	}

	return checks, nil
}

func (c rateLimitCheck) key(request RateLimitRequest) rateLimitCounterKey {
	return rateLimitCounterKey{
		Rule:        c.rule,
		AppCode:     request.AppCode,
		SceneCode:   request.SceneCode,
		Subject:     c.subject,
		WindowStart: c.windowStart,
	}
}

func retryAfterSeconds(now time.Time, expiresAt time.Time) int {
	duration := expiresAt.Sub(now)
	if duration <= 0 {
		return 0
	}

	return int((duration + time.Second - time.Nanosecond) / time.Second)
}

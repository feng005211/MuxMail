package lite

import (
	"fmt"
	"sync"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

// IdempotencyStatus identifies the stored state for an idempotency key.
type IdempotencyStatus string

const (
	// IdempotencyStatusQueued means the first request reached the queue successfully.
	IdempotencyStatusQueued IdempotencyStatus = "queued"
)

// IdempotencyDecisionState identifies the result of an idempotency check.
type IdempotencyDecisionState string

const (
	// IdempotencyDecisionNew means the request can continue through the send pipeline.
	IdempotencyDecisionNew IdempotencyDecisionState = "new"
	// IdempotencyDecisionReplay means the request should return the original message ID.
	IdempotencyDecisionReplay IdempotencyDecisionState = "replay"
)

// IdempotencyCacheConfig contains in-memory cache settings.
type IdempotencyCacheConfig struct {
	Capacity int
	TTL      time.Duration
	Now      func() time.Time
}

// IdempotencyDecision describes whether a send request is new or a replay.
type IdempotencyDecision struct {
	State     IdempotencyDecisionState
	MessageID string
	Status    IdempotencyStatus
}

// IdempotencyConflictError is returned when one idempotency key is reused with different content.
type IdempotencyConflictError struct {
	Code domain.ErrorCode
}

// Error returns the stable public API error code.
func (e IdempotencyConflictError) Error() string {
	return string(e.Code)
}

// IdempotencyCache stores Lite mode idempotency records in memory.
type IdempotencyCache struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	limit   int
	entries map[idempotencyKey]idempotencyEntry
}

// NewIdempotencyCache creates an in-memory idempotency cache.
func NewIdempotencyCache(config IdempotencyCacheConfig) (*IdempotencyCache, error) {
	if config.Capacity <= 0 {
		return nil, fmt.Errorf("idempotency cache capacity must be greater than 0")
	}
	if config.TTL <= 0 {
		return nil, fmt.Errorf("idempotency cache ttl must be greater than 0")
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	return &IdempotencyCache{
		now:     config.Now,
		ttl:     config.TTL,
		limit:   config.Capacity,
		entries: make(map[idempotencyKey]idempotencyEntry),
	}, nil
}

// Check returns a replay decision, a conflict error, or permission to continue as a new request.
func (c *IdempotencyCache) Check(appCode string, sceneCode string, idempotencyHash string, requestFingerprint string) (IdempotencyDecision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now().UTC()
	c.deleteExpired(now)

	entry, exists := c.entries[newIdempotencyKey(appCode, sceneCode, idempotencyHash)]
	if !exists {
		return IdempotencyDecision{State: IdempotencyDecisionNew}, nil
	}
	if entry.RequestFingerprint != requestFingerprint {
		return IdempotencyDecision{}, IdempotencyConflictError{Code: domain.ErrorCodeIdempotencyConflict}
	}

	return IdempotencyDecision{
		State:     IdempotencyDecisionReplay,
		MessageID: entry.MessageID,
		Status:    entry.Status,
	}, nil
}

// MarkQueued stores the message ID only after the queue enqueue operation succeeds.
func (c *IdempotencyCache) MarkQueued(appCode string, sceneCode string, idempotencyHash string, requestFingerprint string, messageID string) error {
	if messageID == "" {
		return fmt.Errorf("message id is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now().UTC()
	c.deleteExpired(now)

	key := newIdempotencyKey(appCode, sceneCode, idempotencyHash)
	if existing, exists := c.entries[key]; exists {
		if existing.RequestFingerprint != requestFingerprint {
			return IdempotencyConflictError{Code: domain.ErrorCodeIdempotencyConflict}
		}
		if existing.MessageID != "" && existing.MessageID != messageID {
			return fmt.Errorf("idempotency key is already queued")
		}
	}

	c.entries[key] = idempotencyEntry{
		MessageID:          messageID,
		RequestFingerprint: requestFingerprint,
		Status:             IdempotencyStatusQueued,
		CreatedAt:          now,
	}
	c.evictOverflow()

	return nil
}

type idempotencyKey struct {
	AppCode         string
	SceneCode       string
	IdempotencyHash string
}

type idempotencyEntry struct {
	MessageID          string
	RequestFingerprint string
	Status             IdempotencyStatus
	CreatedAt          time.Time
}

func (c *IdempotencyCache) deleteExpired(now time.Time) {
	expiresBefore := now.Add(-c.ttl)
	for key, entry := range c.entries {
		if !entry.CreatedAt.After(expiresBefore) {
			delete(c.entries, key)
		}
	}
}

func (c *IdempotencyCache) evictOverflow() {
	for len(c.entries) > c.limit {
		var oldestKey idempotencyKey
		var oldestEntry idempotencyEntry
		first := true
		for key, entry := range c.entries {
			if first || entry.CreatedAt.Before(oldestEntry.CreatedAt) || (entry.CreatedAt.Equal(oldestEntry.CreatedAt) && idempotencyKeyLess(key, oldestKey)) {
				oldestKey = key
				oldestEntry = entry
				first = false
			}
		}
		delete(c.entries, oldestKey)
	}
}

func newIdempotencyKey(appCode string, sceneCode string, idempotencyHash string) idempotencyKey {
	return idempotencyKey{AppCode: appCode, SceneCode: sceneCode, IdempotencyHash: idempotencyHash}
}

func idempotencyKeyLess(left idempotencyKey, right idempotencyKey) bool {
	if left.AppCode != right.AppCode {
		return left.AppCode < right.AppCode
	}
	if left.SceneCode != right.SceneCode {
		return left.SceneCode < right.SceneCode
	}

	return left.IdempotencyHash < right.IdempotencyHash
}

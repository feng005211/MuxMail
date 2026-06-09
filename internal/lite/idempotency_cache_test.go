package lite

import (
	"errors"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestIdempotencyCacheReplaySuccess(t *testing.T) {
	now := fixedIdempotencyTime()
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, func() time.Time { return now })

	decision, err := cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("check new idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision, got %+v", decision)
	}

	if err := cache.MarkQueued("project_a", "register_code", "idem_hash", "fingerprint_a", "msg_01ABC"); err != nil {
		t.Fatalf("mark queued: %v", err)
	}

	decision, err = cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("check replay idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionReplay || decision.MessageID != "msg_01ABC" || decision.Status != IdempotencyStatusQueued {
		t.Fatalf("unexpected replay decision: %+v", decision)
	}
}

func TestIdempotencyCacheReservationCompletesToReplay(t *testing.T) {
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, fixedIdempotencyTime)

	reservation, decision, err := cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision, got %+v", decision)
	}
	if err := reservation.CompleteQueued("msg_01ABC"); err != nil {
		t.Fatalf("complete queued: %v", err)
	}

	_, decision, err = cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve replay idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionReplay || decision.MessageID != "msg_01ABC" {
		t.Fatalf("unexpected replay decision: %+v", decision)
	}
}

func TestIdempotencyCachePendingReservationRejectsDuplicateUntilReleased(t *testing.T) {
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, fixedIdempotencyTime)

	reservation, decision, err := cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve first idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision, got %+v", decision)
	}

	_, _, err = cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	assertIdempotencyConflict(t, err)
	_, err = cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	assertIdempotencyConflict(t, err)
	err = cache.MarkQueued("project_a", "register_code", "idem_hash", "fingerprint_a", "msg_pending")
	assertIdempotencyConflict(t, err)

	reservation.Release()
	reservation, decision, err = cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision after release, got %+v", decision)
	}
	reservation.Release()
}

func TestIdempotencyCacheReserveEvictsQueuedBeforePending(t *testing.T) {
	now := fixedIdempotencyTime()
	cache := openTestIdempotencyCache(t, 2, 24*time.Hour, func() time.Time { return now })

	if err := cache.MarkQueued("project_a", "register_code", "queued_hash", "fingerprint_queued", "msg_queued"); err != nil {
		t.Fatalf("mark queued: %v", err)
	}
	now = now.Add(time.Second)
	reservation, decision, err := cache.Reserve("project_a", "register_code", "pending_hash", "fingerprint_pending")
	if err != nil {
		t.Fatalf("reserve pending: %v", err)
	}
	defer reservation.Release()
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected pending reservation to be new, got %+v", decision)
	}

	now = now.Add(time.Second)
	next, decision, err := cache.Reserve("project_a", "register_code", "next_hash", "fingerprint_next")
	if err != nil {
		t.Fatalf("reserve next: %v", err)
	}
	defer next.Release()
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected next reservation to be new, got %+v", decision)
	}

	decision, err = cache.Check("project_a", "register_code", "queued_hash", "fingerprint_queued")
	if err != nil {
		t.Fatalf("check evicted queued entry: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected queued entry to be evicted before pending entries, got %+v", decision)
	}
	_, err = cache.Check("project_a", "register_code", "pending_hash", "fingerprint_pending")
	assertIdempotencyConflict(t, err)
	_, err = cache.Check("project_a", "register_code", "next_hash", "fingerprint_next")
	assertIdempotencyConflict(t, err)
}

func TestIdempotencyCacheReserveRejectsWhenAllEntriesPending(t *testing.T) {
	cache := openTestIdempotencyCache(t, 1, 24*time.Hour, fixedIdempotencyTime)

	reservation, decision, err := cache.Reserve("project_a", "register_code", "pending_hash", "fingerprint_pending")
	if err != nil {
		t.Fatalf("reserve pending: %v", err)
	}
	defer reservation.Release()
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected pending reservation to be new, got %+v", decision)
	}

	_, _, err = cache.Reserve("project_a", "register_code", "next_hash", "fingerprint_next")
	if err == nil {
		t.Fatal("expected reserve to reject when all entries are pending")
	}
	_, err = cache.Check("project_a", "register_code", "pending_hash", "fingerprint_pending")
	assertIdempotencyConflict(t, err)

	decision, err = cache.Check("project_a", "register_code", "next_hash", "fingerprint_next")
	if err != nil {
		t.Fatalf("check rejected reservation key: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected rejected reservation key to stay new, got %+v", decision)
	}
}

func TestIdempotencyCacheMarkQueuedDoesNotEvictPendingReservation(t *testing.T) {
	cache := openTestIdempotencyCache(t, 1, 24*time.Hour, fixedIdempotencyTime)

	reservation, decision, err := cache.Reserve("project_a", "register_code", "pending_hash", "fingerprint_pending")
	if err != nil {
		t.Fatalf("reserve pending: %v", err)
	}
	defer reservation.Release()
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected pending reservation to be new, got %+v", decision)
	}

	err = cache.MarkQueued("project_a", "register_code", "queued_hash", "fingerprint_queued", "msg_queued")
	if err == nil {
		t.Fatal("expected mark queued to reject when only pending entries can be evicted")
	}

	_, err = cache.Check("project_a", "register_code", "pending_hash", "fingerprint_pending")
	assertIdempotencyConflict(t, err)
	decision, err = cache.Check("project_a", "register_code", "queued_hash", "fingerprint_queued")
	if err != nil {
		t.Fatalf("check rejected queued key: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected rejected queued key to stay new, got %+v", decision)
	}
}

func TestIdempotencyCacheConflict(t *testing.T) {
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, fixedIdempotencyTime)

	if err := cache.MarkQueued("project_a", "register_code", "idem_hash", "fingerprint_a", "msg_01ABC"); err != nil {
		t.Fatalf("mark queued: %v", err)
	}

	_, err := cache.Check("project_a", "register_code", "idem_hash", "fingerprint_b")
	assertIdempotencyConflict(t, err)
}

func TestIdempotencyCacheTTLExpiryTreatsRequestAsNew(t *testing.T) {
	now := fixedIdempotencyTime()
	cache := openTestIdempotencyCache(t, 10, time.Hour, func() time.Time { return now })

	if err := cache.MarkQueued("project_a", "register_code", "idem_hash", "fingerprint_a", "msg_01ABC"); err != nil {
		t.Fatalf("mark queued: %v", err)
	}

	now = now.Add(time.Hour + time.Nanosecond)
	decision, err := cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("check after ttl: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision after ttl, got %+v", decision)
	}
}

func TestIdempotencyCacheTTLDoesNotExpirePendingReservation(t *testing.T) {
	now := fixedIdempotencyTime()
	cache := openTestIdempotencyCache(t, 10, time.Hour, func() time.Time { return now })

	reservation, decision, err := cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision, got %+v", decision)
	}

	now = now.Add(time.Hour + time.Nanosecond)
	_, _, err = cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	assertIdempotencyConflict(t, err)

	if err := reservation.CompleteQueued("msg_01ABC"); err != nil {
		t.Fatalf("complete queued after ttl: %v", err)
	}
	_, decision, err = cache.Reserve("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("reserve replay after completion: %v", err)
	}
	if decision.State != IdempotencyDecisionReplay || decision.MessageID != "msg_01ABC" {
		t.Fatalf("unexpected replay decision after completion: %+v", decision)
	}
}

func TestIdempotencyCacheCapacityEvictsEarliestCreatedAt(t *testing.T) {
	now := fixedIdempotencyTime()
	cache := openTestIdempotencyCache(t, 2, 24*time.Hour, func() time.Time { return now })

	if err := cache.MarkQueued("project_a", "register_code", "idem_1", "fingerprint_1", "msg_1"); err != nil {
		t.Fatalf("mark first queued: %v", err)
	}
	now = now.Add(time.Second)
	if err := cache.MarkQueued("project_a", "register_code", "idem_2", "fingerprint_2", "msg_2"); err != nil {
		t.Fatalf("mark second queued: %v", err)
	}
	now = now.Add(time.Second)
	if err := cache.MarkQueued("project_a", "register_code", "idem_3", "fingerprint_3", "msg_3"); err != nil {
		t.Fatalf("mark third queued: %v", err)
	}

	decision, err := cache.Check("project_a", "register_code", "idem_1", "fingerprint_1")
	if err != nil {
		t.Fatalf("check evicted first idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected earliest entry to be evicted, got %+v", decision)
	}

	decision, err = cache.Check("project_a", "register_code", "idem_2", "fingerprint_2")
	if err != nil {
		t.Fatalf("check second idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionReplay || decision.MessageID != "msg_2" {
		t.Fatalf("expected second entry to remain, got %+v", decision)
	}
}

func TestIdempotencyCacheNotMarkedIfQueueEnqueueFails(t *testing.T) {
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, fixedIdempotencyTime)

	decision, err := cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("check new idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision before queue enqueue, got %+v", decision)
	}

	decision, err = cache.Check("project_a", "register_code", "idem_hash", "fingerprint_a")
	if err != nil {
		t.Fatalf("check after simulated enqueue failure: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected idempotency to stay new until MarkQueued, got %+v", decision)
	}
}

func TestIdempotencyCacheScopeIncludesAppAndScene(t *testing.T) {
	cache := openTestIdempotencyCache(t, 10, 24*time.Hour, fixedIdempotencyTime)

	if err := cache.MarkQueued("project_a", "register_code", "idem_hash", "fingerprint_a", "msg_01ABC"); err != nil {
		t.Fatalf("mark queued: %v", err)
	}

	assertIdempotencyNew(t, cache, "project_b", "register_code", "idem_hash", "fingerprint_a")
	assertIdempotencyNew(t, cache, "project_a", "reset_password", "idem_hash", "fingerprint_a")
}

func assertIdempotencyNew(t *testing.T, cache *IdempotencyCache, appCode string, sceneCode string, idempotencyHash string, fingerprint string) {
	t.Helper()

	decision, err := cache.Check(appCode, sceneCode, idempotencyHash, fingerprint)
	if err != nil {
		t.Fatalf("check idempotency: %v", err)
	}
	if decision.State != IdempotencyDecisionNew {
		t.Fatalf("expected new decision, got %+v", decision)
	}
}

func assertIdempotencyConflict(t *testing.T, err error) {
	t.Helper()

	var conflict IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	if conflict.Code != domain.ErrorCodeIdempotencyConflict {
		t.Fatalf("expected idempotency_conflict code, got %s", conflict.Code)
	}
}

func openTestIdempotencyCache(t *testing.T, capacity int, ttl time.Duration, now func() time.Time) *IdempotencyCache {
	t.Helper()

	cache, err := NewIdempotencyCache(IdempotencyCacheConfig{
		Capacity: capacity,
		TTL:      ttl,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("open idempotency cache: %v", err)
	}

	return cache
}

func fixedIdempotencyTime() time.Time {
	return time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
}

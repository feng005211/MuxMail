package lite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestMemoryQueueEnqueueAndDequeue(t *testing.T) {
	now := fixedQueueTime()
	queue := openTestMemoryQueue(t, 2, func() time.Time { return now })

	if err := queue.Enqueue(testQueueTask("msg_1")); err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	if got := queue.PendingCount(); got != 1 {
		t.Fatalf("expected pending count 1, got %d", got)
	}

	task, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue task: %v", err)
	}
	if task.Message.MessageID != "msg_1" || task.AttemptNo != 1 {
		t.Fatalf("unexpected dequeued task: %+v", task)
	}
	if !task.AvailableAt.Equal(now.UTC()) {
		t.Fatalf("expected available_at %s, got %s", now.UTC(), task.AvailableAt)
	}
	if got := queue.PendingCount(); got != 0 {
		t.Fatalf("expected pending count 0 after dequeue, got %d", got)
	}
}

func TestMemoryQueueFull(t *testing.T) {
	queue := openTestMemoryQueue(t, 1, fixedQueueTime)

	if err := queue.Enqueue(testQueueTask("msg_1")); err != nil {
		t.Fatalf("enqueue first task: %v", err)
	}
	err := queue.Enqueue(testQueueTask("msg_2"))
	assertQueueFull(t, err)
}

func TestMemoryQueueDelayedRetryCountsTowardCapacity(t *testing.T) {
	queue := openTestMemoryQueue(t, 1, fixedQueueTime)

	if err := queue.EnqueueDelayed(testQueueTask("msg_1"), time.Minute); err != nil {
		t.Fatalf("enqueue delayed task: %v", err)
	}
	if got := queue.PendingCount(); got != 1 {
		t.Fatalf("expected delayed task to count toward capacity, got %d", got)
	}

	err := queue.Enqueue(testQueueTask("msg_2"))
	assertQueueFull(t, err)
}

func TestMemoryQueueDelayedTaskBecomesReady(t *testing.T) {
	now := fixedQueueTime()
	queue := openTestMemoryQueue(t, 2, func() time.Time { return now })

	if err := queue.EnqueueDelayed(testQueueTask("msg_1"), 30*time.Second); err != nil {
		t.Fatalf("enqueue delayed task: %v", err)
	}

	now = now.Add(30 * time.Second)
	task, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue delayed task: %v", err)
	}
	if task.Message.MessageID != "msg_1" {
		t.Fatalf("unexpected delayed task: %+v", task)
	}
	if !task.AvailableAt.Equal(fixedQueueTime().Add(30 * time.Second).UTC()) {
		t.Fatalf("unexpected available_at: %s", task.AvailableAt)
	}
}

func TestMemoryQueueInFlightDoesNotCountTowardCapacity(t *testing.T) {
	queue := openTestMemoryQueue(t, 1, fixedQueueTime)

	if err := queue.Enqueue(testQueueTask("msg_1")); err != nil {
		t.Fatalf("enqueue first task: %v", err)
	}
	task, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue first task: %v", err)
	}
	if task.Message.MessageID != "msg_1" {
		t.Fatalf("unexpected first task: %+v", task)
	}

	if err := queue.Enqueue(testQueueTask("msg_2")); err != nil {
		t.Fatalf("expected enqueue to succeed while first task is in-flight: %v", err)
	}
}

func TestMemoryQueueCloseDiscardsNotStartedTasks(t *testing.T) {
	queue := openTestMemoryQueue(t, 2, fixedQueueTime)

	if err := queue.Enqueue(testQueueTask("msg_1")); err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	if err := queue.EnqueueDelayed(testQueueTask("msg_2"), time.Minute); err != nil {
		t.Fatalf("enqueue delayed task: %v", err)
	}
	if err := queue.Close(); err != nil {
		t.Fatalf("close queue: %v", err)
	}
	if got := queue.PendingCount(); got != 0 {
		t.Fatalf("expected close to discard pending tasks, got %d", got)
	}

	_, err := queue.Dequeue(context.Background())
	if !errors.Is(err, ErrMemoryQueueClosed) {
		t.Fatalf("expected closed queue error, got %v", err)
	}
}

func assertQueueFull(t *testing.T, err error) {
	t.Helper()

	var full QueueFullError
	if !errors.As(err, &full) {
		t.Fatalf("expected queue full error, got %v", err)
	}
	if full.Code != domain.ErrorCodeQueueFull {
		t.Fatalf("expected queue_full code, got %s", full.Code)
	}
}

func testQueueTask(messageID string) QueueTask {
	return QueueTask{
		Message: domain.Message{
			MessageID: messageID,
			AppCode:   "project_a",
			SceneCode: "register_code",
		},
		AttemptNo: 1,
	}
}

func openTestMemoryQueue(t *testing.T, capacity int, now func() time.Time) *MemoryQueue {
	t.Helper()

	queue, err := NewMemoryQueue(MemoryQueueConfig{
		Capacity: capacity,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("open memory queue: %v", err)
	}

	return queue
}

func fixedQueueTime() time.Time {
	return time.Date(2026, 5, 28, 3, 4, 5, 0, time.FixedZone("UTC+8", 8*60*60))
}

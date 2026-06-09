package lite

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

// ErrMemoryQueueClosed is returned when the in-memory queue has been closed.
var ErrMemoryQueueClosed = errors.New("memory queue is closed")

// QueueTask is one message delivery task waiting for a worker.
type QueueTask struct {
	Message     domain.Message
	AttemptNo   int
	AvailableAt time.Time
}

// QueueFullError is returned when the in-memory queue has reached capacity.
type QueueFullError struct {
	Code domain.ErrorCode
}

// Error returns the stable public API error code.
func (e QueueFullError) Error() string {
	return string(e.Code)
}

// MemoryQueueConfig contains Lite mode queue settings.
type MemoryQueueConfig struct {
	Capacity int
	Now      func() time.Time
}

// MemoryQueue stores queued and delayed tasks in process memory.
type MemoryQueue struct {
	mu       sync.Mutex
	capacity int
	now      func() time.Time
	pending  int
	nextSeq  int64
	ready    []queuedTask
	delayed  delayedTaskHeap
	closed   bool
	notify   chan struct{}
}

// QueueReservation holds queue capacity while the caller finishes durable audit work.
type QueueReservation struct {
	queue *MemoryQueue
	used  bool
}

// NewMemoryQueue creates an in-memory queue with fixed capacity.
func NewMemoryQueue(config MemoryQueueConfig) (*MemoryQueue, error) {
	if config.Capacity <= 0 {
		return nil, fmt.Errorf("memory queue capacity must be greater than 0")
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	return &MemoryQueue{
		capacity: config.Capacity,
		now:      config.Now,
		notify:   make(chan struct{}),
	}, nil
}

// Enqueue adds an immediately available task.
func (q *MemoryQueue) Enqueue(task QueueTask) error {
	return q.EnqueueDelayed(task, 0)
}

// EnqueueDelayed adds a task that becomes available after delay.
func (q *MemoryQueue) EnqueueDelayed(task QueueTask, delay time.Duration) error {
	reservation, err := q.Reserve()
	if err != nil {
		return err
	}
	return reservation.CommitDelayed(task, delay)
}

// Reserve atomically claims one queue capacity slot for a later enqueue.
func (q *MemoryQueue) Reserve() (*QueueReservation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil, ErrMemoryQueueClosed
	}
	if q.pending >= q.capacity {
		return nil, QueueFullError{Code: domain.ErrorCodeQueueFull}
	}
	q.pending++

	return &QueueReservation{queue: q}, nil
}

// Commit enqueues an immediately available task into the reserved capacity slot.
func (r *QueueReservation) Commit(task QueueTask) error {
	return r.CommitDelayed(task, 0)
}

// CommitDelayed enqueues a delayed task into the reserved capacity slot.
func (r *QueueReservation) CommitDelayed(task QueueTask, delay time.Duration) error {
	if r == nil || r.queue == nil {
		return fmt.Errorf("queue reservation is required")
	}

	q := r.queue
	q.mu.Lock()
	defer q.mu.Unlock()

	if r.used {
		return fmt.Errorf("queue reservation already used")
	}
	r.used = true

	if q.closed {
		if q.pending > 0 {
			q.pending--
		}
		return ErrMemoryQueueClosed
	}
	if delay < 0 {
		delay = 0
	}

	now := q.now().UTC()
	q.nextSeq++
	item := queuedTask{
		task:      task,
		readyAt:   now.Add(delay),
		sequence:  q.nextSeq,
		available: now.Add(delay),
	}
	item.task.AvailableAt = item.available

	if delay == 0 {
		q.ready = append(q.ready, item)
	} else {
		heap.Push(&q.delayed, item)
	}
	q.signalLocked()

	return nil
}

// Release returns reserved capacity when the caller cannot commit the task.
func (r *QueueReservation) Release() {
	if r == nil || r.queue == nil {
		return
	}

	q := r.queue
	q.mu.Lock()
	defer q.mu.Unlock()

	if r.used {
		return
	}
	r.used = true
	if q.pending > 0 {
		q.pending--
	}
	q.signalLocked()
}

// Dequeue waits until a task is ready, the context is canceled, or the queue closes.
func (q *MemoryQueue) Dequeue(ctx context.Context) (QueueTask, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return QueueTask{}, ErrMemoryQueueClosed
		}

		now := q.now().UTC()
		q.moveReadyLocked(now)
		if len(q.ready) > 0 {
			item := q.ready[0]
			copy(q.ready, q.ready[1:])
			q.ready[len(q.ready)-1] = queuedTask{}
			q.ready = q.ready[:len(q.ready)-1]
			q.pending--
			q.mu.Unlock()
			return item.task, nil
		}

		notify := q.notify
		wait := q.nextDelayLocked(now)
		q.mu.Unlock()

		var timer *time.Timer
		var timerC <-chan time.Time
		if wait >= 0 {
			timer = time.NewTimer(wait)
			timerC = timer.C
		}

		select {
		case <-ctx.Done():
			stopTimer(timer)
			return QueueTask{}, ctx.Err()
		case <-notify:
			stopTimer(timer)
		case <-timerC:
		}
	}
}

// PendingCount returns queued and delayed tasks that are not yet in-flight.
func (q *MemoryQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.pending
}

// Close discards all not-yet-started tasks and wakes waiting workers.
func (q *MemoryQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil
	}

	q.closed = true
	q.pending = 0
	q.ready = nil
	q.delayed = nil
	q.signalLocked()

	return nil
}

type queuedTask struct {
	task      QueueTask
	readyAt   time.Time
	sequence  int64
	available time.Time
}

type delayedTaskHeap []queuedTask

func (h delayedTaskHeap) Len() int {
	return len(h)
}

func (h delayedTaskHeap) Less(i int, j int) bool {
	if !h[i].readyAt.Equal(h[j].readyAt) {
		return h[i].readyAt.Before(h[j].readyAt)
	}

	return h[i].sequence < h[j].sequence
}

func (h delayedTaskHeap) Swap(i int, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *delayedTaskHeap) Push(value any) {
	*h = append(*h, value.(queuedTask))
}

func (h *delayedTaskHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	old[len(old)-1] = queuedTask{}
	*h = old[:len(old)-1]
	return item
}

func (q *MemoryQueue) moveReadyLocked(now time.Time) {
	for q.delayed.Len() > 0 {
		item := q.delayed[0]
		if item.readyAt.After(now) {
			return
		}
		heap.Pop(&q.delayed)
		q.ready = append(q.ready, item)
	}
}

func (q *MemoryQueue) nextDelayLocked(now time.Time) time.Duration {
	if q.delayed.Len() == 0 {
		return -1
	}

	wait := q.delayed[0].readyAt.Sub(now)
	if wait < 0 {
		return 0
	}

	return wait
}

func (q *MemoryQueue) signalLocked() {
	close(q.notify)
	q.notify = make(chan struct{})
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

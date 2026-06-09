package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	"github.com/muxmail/muxmail/internal/provider"
)

func TestWorkerSuccessFirstAttempt(t *testing.T) {
	harness := newWorkerHarness(t, provider.Accepted("provider_123"))
	defer harness.close()

	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("process task: %v", err)
	}

	attempts := readJSONLLines(t, filepath.Join(harness.dir, "mail-attempts.jsonl"))
	if len(attempts) != 2 {
		t.Fatalf("expected sending and sent attempts, got %d", len(attempts))
	}
	assertRecordValue(t, attempts[0], "status", string(domain.AttemptStatusSending))
	assertRecordValue(t, attempts[1], "status", string(domain.AttemptStatusSent))
	assertRecordValue(t, attempts[1], "provider_message_id", "provider_123")

	messages := readJSONLLines(t, filepath.Join(harness.dir, "mail-messages.jsonl"))
	if len(messages) != 2 {
		t.Fatalf("expected sending and sent message records, got %d", len(messages))
	}
	assertRecordValue(t, messages[0], "status", string(domain.MessageStatusSending))
	assertRecordValue(t, messages[1], "status", string(domain.MessageStatusSent))
}

func TestWorkerTemporaryFailureThenSuccess(t *testing.T) {
	harness := newWorkerHarness(t,
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
		provider.Accepted("provider_456"),
	)
	defer harness.close()

	message := testWorkerMessage()
	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process first task: %v", err)
	}
	if got := harness.queue.PendingCount(); got != 1 {
		t.Fatalf("expected retry to be queued, got %d", got)
	}

	retryTask, err := harness.queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue retry task: %v", err)
	}
	if retryTask.AttemptNo != 2 {
		t.Fatalf("expected retry attempt 2, got %d", retryTask.AttemptNo)
	}
	if err := harness.worker.ProcessTask(context.Background(), retryTask); err != nil {
		t.Fatalf("process retry task: %v", err)
	}

	messages := readJSONLLines(t, filepath.Join(harness.dir, "mail-messages.jsonl"))
	if len(messages) != 4 {
		t.Fatalf("expected sending retrying sending sent, got %d records", len(messages))
	}
	assertRecordValue(t, messages[1], "status", string(domain.MessageStatusRetrying))
	assertRecordValue(t, messages[3], "status", string(domain.MessageStatusSent))
}

func TestWorkerRetryEnqueueFailureMarksMessageFailed(t *testing.T) {
	harness := newWorkerHarnessWithQueueCapacity(t, 1, provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"))
	defer harness.close()

	if err := harness.queue.Enqueue(lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("fill queue: %v", err)
	}

	err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1})
	if err == nil {
		t.Fatal("expected retry enqueue failure")
	}
	if !strings.Contains(err.Error(), string(domain.ErrorCodeQueueFull)) {
		t.Fatalf("expected queue full error, got %v", err)
	}

	messages := readJSONLLines(t, filepath.Join(harness.dir, "mail-messages.jsonl"))
	if len(messages) != 3 {
		t.Fatalf("expected sending retrying failed, got %d records", len(messages))
	}
	assertRecordValue(t, messages[1], "status", string(domain.MessageStatusRetrying))
	assertRecordValue(t, messages[2], "status", string(domain.MessageStatusFailed))
	assertRecordValue(t, messages[2], "error_code", string(domain.ErrorCodeQueueFull))
}

func TestWorkerChannelFailureThenSuccess(t *testing.T) {
	harness := newWorkerHarness(t,
		provider.ChannelFailure(domain.ErrorCodeProviderUnavailable, "channel failed"),
		provider.Accepted("provider_789"),
	)
	defer harness.close()

	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("process first task: %v", err)
	}
	retryTask, err := harness.queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue retry task: %v", err)
	}
	if err := harness.worker.ProcessTask(context.Background(), retryTask); err != nil {
		t.Fatalf("process retry task: %v", err)
	}

	requests := harness.fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(requests))
	}
	if requests[0].Channel.Code != "mock_primary" || requests[1].Channel.Code != "mock_backup" {
		t.Fatalf("expected failover from primary to backup, got %s then %s", requests[0].Channel.Code, requests[1].Channel.Code)
	}
}

func TestWorkerAttemptStatsUseSelectedChannelCode(t *testing.T) {
	now := time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
	dir := t.TempDir()
	log, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}
	defer log.Close()
	queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{Capacity: 10, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer queue.Close()
	stats, err := lite.NewFileStatsSink(lite.FileStatsSinkConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open stats sink: %v", err)
	}
	defer stats.Close()

	account := domain.ProviderAccount{Code: "mock_main", Provider: domain.ProviderMock, Enabled: true}
	fake := provider.NewFakeProvider(provider.Accepted("provider_stats"))
	resolver := providerResolverFunc(func(channelCode string) (ProviderChannelRuntime, bool) {
		if channelCode != "mock_primary" {
			return ProviderChannelRuntime{}, false
		}
		return ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_primary",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		}, true
	})
	worker, err := New(Config{
		Queue:                 queue,
		MessageLog:            log,
		Stats:                 stats,
		ProviderResolver:      resolver,
		MaxAttemptsPerMessage: 1,
		RetryBackoffSeconds:   []int{0},
		ProviderTimeout:       time.Second,
		WorkerConcurrency:     1,
		Now:                   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}

	message := testWorkerMessage()
	message.ProviderChannels = []string{"mock_primary"}
	if err := worker.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process task: %v", err)
	}

	summary, err := stats.Summary("project_a", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("summarize stats: %v", err)
	}
	duration, ok := summary.ProviderDurations["mock_primary"]
	if !ok || duration.Count != 1 {
		t.Fatalf("expected provider duration under selected channel code, got %+v", summary.ProviderDurations)
	}
	if got := summary.Metrics[lite.MetricAttemptsSent]; got != 1 {
		t.Fatalf("expected attempt sent metric, got %+v", summary.Metrics)
	}
}

func TestWorkerUnresolvedChannelWritesChannelFailureAttempt(t *testing.T) {
	harness := newWorkerHarness(t, provider.Accepted("provider_fallback"))
	defer harness.close()

	message := testWorkerMessage()
	message.ProviderChannels = []string{"missing_primary", "mock_backup"}
	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process missing channel task: %v", err)
	}
	if got := harness.fake.Requests(); len(got) != 0 {
		t.Fatalf("expected missing channel not to call provider, got %+v", got)
	}

	attempts, err := harness.log.ListAttempts("project_a", message.MessageID)
	if err != nil {
		t.Fatalf("list attempts after missing channel: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected missing channel failure attempt, got %+v", attempts)
	}
	missingAttempt := attempts[0]
	if missingAttempt.AttemptNo != 1 ||
		missingAttempt.Provider != "" ||
		missingAttempt.Transport != "" ||
		missingAttempt.ProviderChannelCode != "missing_primary" ||
		missingAttempt.Status != domain.AttemptStatusFailed ||
		missingAttempt.FailureClass != domain.FailureClassChannel ||
		missingAttempt.ErrorCode != domain.ErrorCodeProviderUnavailable {
		t.Fatalf("unexpected missing channel attempt: %+v", missingAttempt)
	}

	retryTask, err := harness.queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue fallback task: %v", err)
	}
	if retryTask.AttemptNo != 2 {
		t.Fatalf("expected fallback attempt 2, got %d", retryTask.AttemptNo)
	}
	if err := harness.worker.ProcessTask(context.Background(), retryTask); err != nil {
		t.Fatalf("process fallback task: %v", err)
	}

	requests := harness.fake.Requests()
	if len(requests) != 1 || requests[0].Channel.Code != "mock_backup" {
		t.Fatalf("expected one fallback provider call, got %+v", requests)
	}
	attempts, err = harness.log.ListAttempts("project_a", message.MessageID)
	if err != nil {
		t.Fatalf("list attempts after fallback: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("expected missing failure plus sending and sent fallback attempts, got %+v", attempts)
	}
	if attempts[1].ProviderChannelCode != "mock_backup" || attempts[2].ProviderMessageID != "provider_fallback" {
		t.Fatalf("unexpected fallback attempt metadata: %+v", attempts)
	}
}

func TestWorkerInvalidRuntimeMetadataFailsBeforeProviderCall(t *testing.T) {
	harness := newWorkerHarness(t, provider.Accepted("provider_should_not_send"))
	defer harness.close()

	fallbackResolver := testProviderResolver(harness.fake)
	harness.worker.resolver = providerResolverFunc(func(channelCode string) (ProviderChannelRuntime, bool) {
		if channelCode == "bad_primary" {
			return ProviderChannelRuntime{
				Account: domain.ProviderAccount{
					Code:     "bad_main",
					Provider: domain.Provider("bad"),
					Enabled:  true,
				},
				Channel: domain.ProviderChannel{
					Code:      "bad_primary",
					Account:   "bad_main",
					Transport: domain.Transport("bad"),
					Enabled:   true,
				},
				Provider: harness.fake,
			}, true
		}

		return fallbackResolver.Resolve(channelCode)
	})

	message := testWorkerMessage()
	message.ProviderChannels = []string{"bad_primary", "mock_backup"}
	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process invalid runtime metadata task: %v", err)
	}
	if got := harness.fake.Requests(); len(got) != 0 {
		t.Fatalf("expected invalid runtime metadata not to call provider, got %+v", got)
	}

	attempts, err := harness.log.ListAttempts("project_a", message.MessageID)
	if err != nil {
		t.Fatalf("list attempts after invalid runtime metadata: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected one invalid metadata failure attempt, got %+v", attempts)
	}
	if attempts[0].Provider != "" ||
		attempts[0].Transport != "" ||
		attempts[0].ProviderChannelCode != "bad_primary" ||
		attempts[0].FailureClass != domain.FailureClassChannel {
		t.Fatalf("unexpected invalid metadata attempt: %+v", attempts[0])
	}
}

func TestWorkerMismatchedRuntimeChannelFailsBeforeProviderCall(t *testing.T) {
	harness := newWorkerHarness(t, provider.Accepted("provider_should_not_send"))
	defer harness.close()

	fallbackResolver := testProviderResolver(harness.fake)
	harness.worker.resolver = providerResolverFunc(func(channelCode string) (ProviderChannelRuntime, bool) {
		if channelCode == "mock_primary" {
			return ProviderChannelRuntime{
				Account: domain.ProviderAccount{
					Code:     "mock_main",
					Provider: domain.ProviderMock,
					Enabled:  true,
				},
				Channel: domain.ProviderChannel{
					Code:      "mock_backup",
					Account:   "mock_main",
					Transport: domain.TransportAPI,
					Enabled:   true,
				},
				Provider: harness.fake,
			}, true
		}

		return fallbackResolver.Resolve(channelCode)
	})

	message := testWorkerMessage()
	message.ProviderChannels = []string{"mock_primary", "mock_backup"}
	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process mismatched runtime channel task: %v", err)
	}
	if got := harness.fake.Requests(); len(got) != 0 {
		t.Fatalf("expected mismatched runtime channel not to call provider, got %+v", got)
	}

	attempts, err := harness.log.ListAttempts("project_a", message.MessageID)
	if err != nil {
		t.Fatalf("list attempts after mismatched runtime channel: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected one mismatched runtime failure attempt, got %+v", attempts)
	}
	if attempts[0].Provider != "" ||
		attempts[0].ProviderAccountCode != "" ||
		attempts[0].Transport != "" ||
		attempts[0].ProviderChannelCode != "mock_primary" ||
		attempts[0].FailureClass != domain.FailureClassChannel {
		t.Fatalf("unexpected mismatched runtime attempt: %+v", attempts[0])
	}
	if got := harness.queue.PendingCount(); got != 1 {
		t.Fatalf("expected fallback retry to be queued, got %d", got)
	}
}

func TestWorkerPermanentFailureStops(t *testing.T) {
	harness := newWorkerHarness(t, provider.MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "invalid recipient"))
	defer harness.close()

	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("process task: %v", err)
	}
	if got := harness.queue.PendingCount(); got != 0 {
		t.Fatalf("expected permanent failure not to retry, got pending %d", got)
	}

	messages := readJSONLLines(t, filepath.Join(harness.dir, "mail-messages.jsonl"))
	last := messages[len(messages)-1]
	assertRecordValue(t, last, "status", string(domain.MessageStatusFailed))
	assertRecordValue(t, last, "error_code", string(domain.ErrorCodeInvalidRecipient))
}

func TestWorkerMaxAttemptsExhaustedWritesProviderUnavailable(t *testing.T) {
	harness := newWorkerHarness(t,
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
	)
	defer harness.close()

	task := lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := harness.worker.ProcessTask(context.Background(), task); err != nil {
			t.Fatalf("process attempt %d: %v", attempt, err)
		}
		if attempt < 3 {
			var err error
			task, err = harness.queue.Dequeue(context.Background())
			if err != nil {
				t.Fatalf("dequeue retry attempt %d: %v", attempt+1, err)
			}
		}
	}

	messages := readJSONLLines(t, filepath.Join(harness.dir, "mail-messages.jsonl"))
	last := messages[len(messages)-1]
	assertRecordValue(t, last, "status", string(domain.MessageStatusFailed))
	assertRecordValue(t, last, "error_code", string(domain.ErrorCodeProviderUnavailable))
}

func TestWorkerRetryAfterIsCappedAt300Seconds(t *testing.T) {
	now := time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
	harness := newWorkerHarnessWithPolicy(t, func() time.Time { return now }, []int{0, 30, 120},
		provider.WithRetryAfter(provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"), 999),
	)
	defer harness.close()

	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("process task: %v", err)
	}
	now = now.Add(299 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := harness.queue.Dequeue(ctx); err == nil {
		t.Fatalf("expected delayed retry not to be ready before cap")
	}

	now = now.Add(time.Second)
	task, err := harness.queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("expected delayed retry after cap: %v", err)
	}
	if task.AttemptNo != 2 {
		t.Fatalf("expected attempt 2 after retry-after cap, got %d", task.AttemptNo)
	}
}

func TestWorkerBackoffFollowsRetryBackoff(t *testing.T) {
	now := time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
	harness := newWorkerHarnessWithPolicy(t, func() time.Time { return now }, []int{0, 45, 0},
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
	)
	defer harness.close()

	if err := harness.worker.ProcessTask(context.Background(), lite.QueueTask{Message: testWorkerMessage(), AttemptNo: 1}); err != nil {
		t.Fatalf("process task: %v", err)
	}
	now = now.Add(44 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := harness.queue.Dequeue(ctx); err == nil {
		t.Fatalf("expected retry not to be ready before retry backoff")
	}

	now = now.Add(time.Second)
	task, err := harness.queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("expected retry after backoff: %v", err)
	}
	if task.AttemptNo != 2 {
		t.Fatalf("expected attempt 2 after backoff, got %d", task.AttemptNo)
	}
}

func TestSafeErrorMessageTruncatesAtUTF8Boundary(t *testing.T) {
	message := strings.Repeat("a", 255) + "\u754c"

	got := safeErrorMessage(message)
	if len(got) > 256 {
		t.Fatalf("expected truncated message to stay within 256 bytes, got %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected truncated message to remain valid UTF-8: %q", got)
	}
	if got != strings.Repeat("a", 255) {
		t.Fatalf("expected partial multibyte rune to be omitted, got %q", got)
	}
}

func TestSafeErrorMessageDropsInvalidUTF8(t *testing.T) {
	message := "provider " + string([]byte{0xff}) + " failed"

	got := safeErrorMessage(message)
	if !utf8.ValidString(got) {
		t.Fatalf("expected safe error message to be valid UTF-8: %q", got)
	}
	if got != "provider  failed" {
		t.Fatalf("expected invalid bytes to be dropped, got %q", got)
	}
}

type workerHarness struct {
	dir    string
	log    *lite.MessageLog
	queue  *lite.MemoryQueue
	fake   *provider.FakeProvider
	worker *Worker
}

type providerResolverFunc func(channelCode string) (ProviderChannelRuntime, bool)

func (f providerResolverFunc) Resolve(channelCode string) (ProviderChannelRuntime, bool) {
	return f(channelCode)
}

func newWorkerHarness(t *testing.T, results ...provider.SendResult) *workerHarness {
	t.Helper()

	return newWorkerHarnessWithNow(t, func() time.Time {
		return time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
	}, results...)
}

func newWorkerHarnessWithNow(t *testing.T, now func() time.Time, results ...provider.SendResult) *workerHarness {
	t.Helper()

	return newWorkerHarnessWithPolicy(t, now, []int{0, 0, 0}, results...)
}

func newWorkerHarnessWithPolicy(t *testing.T, now func() time.Time, retryBackoff []int, results ...provider.SendResult) *workerHarness {
	t.Helper()

	return newWorkerHarnessWithQueueCapacityAndPolicy(t, now, retryBackoff, 10, results...)
}

func newWorkerHarnessWithQueueCapacity(t *testing.T, capacity int, results ...provider.SendResult) *workerHarness {
	t.Helper()

	return newWorkerHarnessWithQueueCapacityAndPolicy(t, func() time.Time {
		return time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC)
	}, []int{0, 0, 0}, capacity, results...)
}

func newWorkerHarnessWithQueueCapacityAndPolicy(t *testing.T, now func() time.Time, retryBackoff []int, capacity int, results ...provider.SendResult) *workerHarness {
	t.Helper()

	dir := t.TempDir()
	log, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}
	queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{Capacity: capacity, Now: now})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	fake := provider.NewFakeProvider(results...)
	worker, err := New(Config{
		Queue:                 queue,
		MessageLog:            log,
		Stats:                 lite.NewNoopStatsSink(),
		ProviderResolver:      testProviderResolver(fake),
		MaxAttemptsPerMessage: 3,
		RetryBackoffSeconds:   retryBackoff,
		ProviderTimeout:       time.Second,
		WorkerConcurrency:     1,
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}

	return &workerHarness{dir: dir, log: log, queue: queue, fake: fake, worker: worker}
}

func (h *workerHarness) close() {
	_ = h.log.Close()
	_ = h.queue.Close()
}

func testProviderResolver(fake provider.Provider) ProviderResolver {
	account := domain.ProviderAccount{Code: "mock_main", Provider: domain.ProviderMock, Enabled: true}
	return NewStaticProviderResolver(
		ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_primary",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		},
		ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_backup",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		},
		ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_third",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		},
	)
}

func testWorkerMessage() domain.Message {
	return domain.Message{
		RequestID:        "req_01ABC",
		MessageID:        "msg_01ABC",
		AppCode:          "project_a",
		APIKeyName:       "default",
		SceneCode:        "register_code",
		ToDomain:         "example.com",
		ToHash:           "hash",
		Locale:           "en-US",
		Subject:          "Your code",
		TextBody:         "Your code is 123456.",
		ProviderChannels: []string{"mock_primary", "mock_backup", "mock_third"},
		Status:           domain.MessageStatusQueued,
	}
}

func readJSONLLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl %s: %v", path, err)
	}
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(rawLines))
	for _, line := range rawLines {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode jsonl line %q: %v", line, err)
		}
		records = append(records, record)
	}

	return records
}

func assertRecordValue(t *testing.T, record map[string]any, key string, value string) {
	t.Helper()

	if record[key] != value {
		t.Fatalf("expected %s=%q, got %v in %+v", key, value, record[key], record)
	}
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	"github.com/muxmail/muxmail/internal/provider"
	"github.com/muxmail/muxmail/internal/worker"
)

func TestSendAPIHappyPathWithMockProvider(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-integration")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected send response 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	task, err := runtime.Queue().Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue queued task: %v", err)
	}
	mock := provider.NewMockProvider()
	workerRuntime, err := worker.New(worker.Config{
		Queue:                 runtime.Queue(),
		MessageLog:            runtime.MessageLog(),
		Stats:                 runtime.Stats(),
		ProviderResolver:      mockProviderResolver(mock),
		MaxAttemptsPerMessage: runtime.Defaults().MaxAttemptsPerMessage,
		RetryBackoffSeconds:   []int{0, 0, 0},
		ProviderTimeout:       time.Duration(runtime.Defaults().ProviderTimeoutSeconds) * time.Second,
		WorkerConcurrency:     1,
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	if err := workerRuntime.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process queued task: %v", err)
	}

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 3 {
		t.Fatalf("expected queued, sending, sent records, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusQueued))
	assertRecordValue(t, messageRecords[1], "status", string(domain.MessageStatusSending))
	assertRecordValue(t, messageRecords[2], "status", string(domain.MessageStatusSent))

	attemptRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-attempts.jsonl"))
	if len(attemptRecords) != 2 {
		t.Fatalf("expected sending and sent attempt records, got %d", len(attemptRecords))
	}
	assertRecordValue(t, attemptRecords[1], "provider_message_id", "mock_"+task.Message.MessageID)
}

func TestWorkerFailoverPathWithFakeProvider(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-failover")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected send response 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	task, err := runtime.Queue().Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue queued task: %v", err)
	}
	fake := provider.NewFakeProvider(
		provider.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
		provider.Accepted("provider_backup"),
	)
	workerRuntime, err := worker.New(worker.Config{
		Queue:                 runtime.Queue(),
		MessageLog:            runtime.MessageLog(),
		Stats:                 runtime.Stats(),
		ProviderResolver:      fakeFailoverResolver(fake),
		MaxAttemptsPerMessage: runtime.Defaults().MaxAttemptsPerMessage,
		RetryBackoffSeconds:   []int{0, 0, 0},
		ProviderTimeout:       time.Duration(runtime.Defaults().ProviderTimeoutSeconds) * time.Second,
		WorkerConcurrency:     1,
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	if err := workerRuntime.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process first task: %v", err)
	}
	retryTask, err := runtime.Queue().Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue retry task: %v", err)
	}
	if err := workerRuntime.ProcessTask(context.Background(), retryTask); err != nil {
		t.Fatalf("process retry task: %v", err)
	}

	requests := fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(requests))
	}
	if requests[0].Channel.Code != "mock_auth_api" || requests[1].Channel.Code != "mock_auth_backup" {
		t.Fatalf("expected primary then backup, got %s then %s", requests[0].Channel.Code, requests[1].Channel.Code)
	}

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	lastMessage := messageRecords[len(messageRecords)-1]
	assertRecordValue(t, lastMessage, "status", string(domain.MessageStatusSent))

	attemptRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-attempts.jsonl"))
	if len(attemptRecords) != 4 {
		t.Fatalf("expected two attempt state pairs, got %d", len(attemptRecords))
	}
	assertRecordValue(t, attemptRecords[1], "status", string(domain.AttemptStatusFailed))
	assertRecordValue(t, attemptRecords[1], "failure_class", string(domain.FailureClassTemporary))
	assertRecordValue(t, attemptRecords[3], "status", string(domain.AttemptStatusSent))
	assertRecordValue(t, attemptRecords[3], "provider_message_id", "provider_backup")
}

func TestSendAPIIdempotencyReplayIntegration(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-integration-replay")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first response 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performSend(t, runtime, testSendBody(), "idem-integration-replay")
	if second.Code != http.StatusAccepted {
		t.Fatalf("expected replay response 202, got %d: %s", second.Code, second.Body.String())
	}

	var firstResponse sendResponse
	var secondResponse sendResponse
	decodeJSON(t, first.Body.String(), &firstResponse)
	decodeJSON(t, second.Body.String(), &secondResponse)
	if firstResponse.RequestID == secondResponse.RequestID {
		t.Fatalf("expected replay to return a new request_id")
	}
	if firstResponse.MessageID != secondResponse.MessageID {
		t.Fatalf("expected replay to return original message_id, got %s then %s", firstResponse.MessageID, secondResponse.MessageID)
	}
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected replay not to enqueue again, got %d", got)
	}

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 1 {
		t.Fatalf("expected one queued message record, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusQueued))
}

func TestSendAPIRateLimitRejectionIntegration(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 1
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-rate-integration-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first response 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performSend(t, runtime, testSendBody(), "idem-rate-integration-2")
	assertErrorResponse(t, second, http.StatusTooManyRequests, domain.ErrorCodeRateLimited)

	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected rate-limited request not to enqueue, got %d", got)
	}
	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 1 {
		t.Fatalf("expected only first queued message record, got %d", len(messageRecords))
	}
}

func TestSendAPISuppressionRejectionIntegration(t *testing.T) {
	store, err := lite.LoadSuppressionStore(writeSuppressionYAML(t, "project_a", "user@example.com", "hard_bounce"))
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}
	cfg := testRuntimeConfig(t, "off")
	runtime, err := NewRuntime(cfg, config.NewSecretResolver(), WithSuppressionStore(store))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-suppression-integration")
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeSuppressedRecipient)

	if got := runtime.Queue().PendingCount(); got != 0 {
		t.Fatalf("expected suppressed request not to enqueue, got %d", got)
	}
	messagePath := filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl")
	if data, err := os.ReadFile(messagePath); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Fatalf("expected no queued message records for suppressed request, got %q", string(data))
	}
}

func mockProviderResolver(mock provider.Provider) worker.ProviderResolver {
	return worker.NewStaticProviderResolver(worker.ProviderChannelRuntime{
		Account: domain.ProviderAccount{
			Code:     "mock_main",
			Provider: domain.ProviderMock,
			Enabled:  true,
		},
		Channel: domain.ProviderChannel{
			Code:      "mock_auth_api",
			Account:   "mock_main",
			Transport: domain.TransportAPI,
			Enabled:   true,
		},
		Provider: mock,
	})
}

func fakeFailoverResolver(fake provider.Provider) worker.ProviderResolver {
	account := domain.ProviderAccount{
		Code:     "mock_main",
		Provider: domain.ProviderMock,
		Enabled:  true,
	}
	return worker.NewStaticProviderResolver(
		worker.ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_auth_api",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		},
		worker.ProviderChannelRuntime{
			Account: account,
			Channel: domain.ProviderChannel{
				Code:      "mock_auth_backup",
				Account:   "mock_main",
				Transport: domain.TransportAPI,
				Enabled:   true,
			},
			Provider: fake,
		},
	)
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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

func TestSendRequestQueuesMessage(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-1")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response sendResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.RequestID == "" || response.MessageID == "" || response.Status != domain.MessageStatusQueued {
		t.Fatalf("unexpected response: %+v", response)
	}
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected one queued task, got %d", got)
	}

	messageLine := readSingleLine(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if !strings.Contains(messageLine, `"status":"queued"`) || !strings.Contains(messageLine, `"api_key_name":"default"`) {
		t.Fatalf("unexpected message log line: %s", messageLine)
	}
}

func TestSendRequestIdempotentReplay(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-replay")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	var firstResponse sendResponse
	decodeJSON(t, first.Body.String(), &firstResponse)

	second := performSend(t, runtime, testSendBody(), "idem-replay")
	if second.Code != http.StatusAccepted {
		t.Fatalf("expected replay 202, got %d: %s", second.Code, second.Body.String())
	}
	var secondResponse sendResponse
	decodeJSON(t, second.Body.String(), &secondResponse)

	if secondResponse.MessageID != firstResponse.MessageID {
		t.Fatalf("expected replay message_id %s, got %s", firstResponse.MessageID, secondResponse.MessageID)
	}
	if secondResponse.RequestID == firstResponse.RequestID {
		t.Fatalf("expected replay to use a new request_id")
	}
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected replay not to enqueue again, got pending %d", got)
	}
}

func TestSendRequestIdempotencyConflict(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-conflict")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}

	body := strings.Replace(testSendBody(), `"code":"123456"`, `"code":"654321"`, 1)
	second := performSend(t, runtime, body, "idem-conflict")
	assertErrorResponse(t, second, http.StatusConflict, domain.ErrorCodeIdempotencyConflict)
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected conflict not to enqueue again, got pending %d", got)
	}
}

func TestSendRequestSuppressedRecipient(t *testing.T) {
	store, err := lite.LoadSuppressionStore(writeSuppressionYAML(t, "project_a", "user@example.com", "manual"))
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}
	runtime := openTestRuntimeWithOptions(t, "off", WithSuppressionStore(store))
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-suppressed")
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeSuppressedRecipient)
	if got := runtime.Queue().PendingCount(); got != 0 {
		t.Fatalf("expected suppressed recipient not to enqueue, got %d", got)
	}
}

func TestSendRequestRouteNotFound(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()
	runtime.auth.apps[0].Scenes[0].RoutePolicy = domain.RoutePolicy{}

	recorder := performSend(t, runtime, testSendBody(), "idem-route")
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeRouteNotFound)
}

func TestSendRequestRateLimited(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 1
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-rate-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performSend(t, runtime, testSendBody(), "idem-rate-2")
	assertErrorResponse(t, second, http.StatusTooManyRequests, domain.ErrorCodeRateLimited)
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected rate-limited request not to enqueue, got %d", got)
	}
}

func TestSendRequestUsesTrustedProxyCallerIP(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Server.TrustedProxies = []string{"127.0.0.1"}
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	request := newSendRequest(testSendBody(), "idem-proxy")
	request.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.1")
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	messageLine := readSingleLine(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if !strings.Contains(messageLine, `"caller_ip":"203.0.113.10"`) {
		t.Fatalf("expected caller_ip from trusted proxy header, got %s", messageLine)
	}
}

func TestSendRequestQueueFull(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Defaults.MemoryQueueSize = 1
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-queue-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performSend(t, runtime, testSendBody(), "idem-queue-2")
	assertErrorResponse(t, second, http.StatusServiceUnavailable, domain.ErrorCodeQueueFull)
}

func TestSendRequestMessageLogFailure(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()
	if err := runtime.messageLog.Close(); err != nil {
		t.Fatalf("close message log: %v", err)
	}

	recorder := performSend(t, runtime, testSendBody(), "idem-log-fail")
	assertErrorResponse(t, recorder, http.StatusInternalServerError, domain.ErrorCodeInternal)
}

func TestSendRequestInvalidJSON(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performSend(t, runtime, `{"scene":`, "idem-invalid-json")
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestSendRequestUnsupportedMediaType(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", strings.NewReader(testSendBody()))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	request.Header.Set("Idempotency-Key", "idem-media")
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	assertErrorResponse(t, recorder, http.StatusUnsupportedMediaType, domain.ErrorCodeUnsupportedMediaType)
}

func performSend(t *testing.T, runtime *Runtime, body string, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := newSendRequest(body, idempotencyKey)
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func newSendRequest(body string, idempotencyKey string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	request.Header.Set("Idempotency-Key", idempotencyKey)

	return request
}

func testSendBody() string {
	return `{"scene":"register_code","to":"user@example.com","locale":"en-US","vars":{"code":"123456"},"context":{"user_ip":"1.2.3.4","user_id":"10001","request_id":"biz-123"}}`
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, code domain.ErrorCode) {
	t.Helper()

	if recorder.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, recorder.Code, recorder.Body.String())
	}
	var response errorResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.Error.Code != code {
		t.Fatalf("expected error code %s, got %+v", code, response.Error)
	}
	if response.Error.RequestID == "" {
		t.Fatalf("expected error request_id")
	}
}

func decodeJSON(t *testing.T, body string, target any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), target); err != nil {
		t.Fatalf("decode JSON %q: %v", body, err)
	}
}

func readSingleLine(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one line in %s, got %d: %q", path, len(lines), string(data))
	}

	return lines[0]
}

func openRuntimeFromConfig(t *testing.T, cfg *config.Config) *Runtime {
	t.Helper()

	runtime, err := NewRuntime(cfg, config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}

	return runtime
}

func openTestRuntimeWithOptions(t *testing.T, statsMode string, options ...RuntimeOption) *Runtime {
	t.Helper()

	runtime, err := NewRuntime(testRuntimeConfig(t, statsMode), config.NewSecretResolver(), options...)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}

	return runtime
}

func writeSuppressionYAML(t *testing.T, app string, email string, reason string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "suppression.yaml")
	content := "entries:\n  - app: " + app + "\n    email: " + email + "\n    reason: " + reason + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write suppression yaml: %v", err)
	}

	return path
}

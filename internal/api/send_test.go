package api

import (
	"context"
	"encoding/json"
	"errors"
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

func TestSendRequestIdempotentReplayCanonicalizesJSONNumbers(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	firstBody := strings.Replace(testSendBody(), `"vars":{"code":"123456"}`, `"vars":{"amount":1,"code":"123456"}`, 1)
	first := performSend(t, runtime, firstBody, "idem-number-replay")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	var firstResponse sendResponse
	decodeJSON(t, first.Body.String(), &firstResponse)

	secondBody := strings.Replace(testSendBody(), `"vars":{"code":"123456"}`, `"vars":{"amount":1.0,"code":"123456"}`, 1)
	second := performSend(t, runtime, secondBody, "idem-number-replay")
	if second.Code != http.StatusAccepted {
		t.Fatalf("expected replay 202, got %d: %s", second.Code, second.Body.String())
	}
	var secondResponse sendResponse
	decodeJSON(t, second.Body.String(), &secondResponse)

	if secondResponse.MessageID != firstResponse.MessageID {
		t.Fatalf("expected replay message_id %s, got %s", firstResponse.MessageID, secondResponse.MessageID)
	}
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected semantically equal JSON numbers not to enqueue again, got pending %d", got)
	}
}

func TestSendRequestPendingIdempotencyDoesNotDuplicateQueue(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	reservation := reserveTestSendIdempotency(t, runtime, "idem-concurrent")
	second := performSend(t, runtime, testSendBody(), "idem-concurrent")
	assertErrorResponse(t, second, http.StatusConflict, domain.ErrorCodeIdempotencyConflict)
	if got := runtime.Queue().PendingCount(); got != 0 {
		t.Fatalf("expected pending duplicate not to enqueue, got %d", got)
	}

	reservation.Release()
	first := performSend(t, runtime, testSendBody(), "idem-concurrent")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected request after release 202, got %d: %s", first.Code, first.Body.String())
	}
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected released idempotency key to enqueue once, got %d", got)
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

func TestSendRequestRejectsInvalidSceneCodeBeforeLookup(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	body := strings.Replace(testSendBody(), `"register_code"`, `"Register Code"`, 1)
	recorder := performSend(t, runtime, body, "idem-invalid-scene")

	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
	if got := runtime.Queue().PendingCount(); got != 0 {
		t.Fatalf("expected invalid scene request not to enqueue, got %d", got)
	}
}

func TestSendRequestRejectsInvalidRenderedSubjectBeforeQueue(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	body := strings.Replace(testSendBody(), `"code":"123456"`, `"code":"123456\r\nBcc: attacker@example.com"`, 1)
	recorder := performSend(t, runtime, body, "idem-subject-injection")

	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeTemplateRenderFailed)
	if got := runtime.Queue().PendingCount(); got != 0 {
		t.Fatalf("expected invalid rendered subject not to enqueue, got %d", got)
	}
	info, err := os.Stat(filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if err != nil {
		t.Fatalf("stat message log: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected invalid rendered subject not to append a message record, got size %d", info.Size())
	}
}

func TestSendRequestRejectsDisabledApp(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	disabled := false
	cfg.Apps[0].Enabled = &disabled
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := performSend(t, runtime, testSendBody(), "idem-disabled-app")
	assertErrorResponse(t, recorder, http.StatusForbidden, domain.ErrorCodeAppDisabled)
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

func TestSendRequestRateLimitsNormalizedContextUserIP(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 10
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerDay = 10
	cfg.Apps[0].Scenes[0].RateLimit.SameUserIPPerHour = 1
	cfg.Apps[0].Scenes[0].RateLimit.SameCallerIPPerHour = 10
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	firstBody := strings.Replace(testSendBody(), `"1.2.3.4"`, `"2001:0db8:0000:0000:0000:0000:0000:0001"`, 1)
	first := performSend(t, runtime, firstBody, "idem-user-ip-normalized-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	messageLine := readSingleLine(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if !strings.Contains(messageLine, `"user_ip":"2001:db8::1"`) {
		t.Fatalf("expected normalized user_ip in message log, got %s", messageLine)
	}

	secondBody := strings.Replace(testSendBody(), `"1.2.3.4"`, `"2001:db8::1"`, 1)
	second := performSend(t, runtime, secondBody, "idem-user-ip-normalized-2")
	assertErrorResponse(t, second, http.StatusTooManyRequests, domain.ErrorCodeRateLimited)
	if got := runtime.Queue().PendingCount(); got != 1 {
		t.Fatalf("expected normalized user_ip rate limit not to enqueue, got %d", got)
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

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 1 {
		t.Fatalf("expected queue_full request not to append a queued message record, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusQueued))

	if _, err := runtime.Queue().Dequeue(context.Background()); err != nil {
		t.Fatalf("dequeue first task: %v", err)
	}
	retry := performSend(t, runtime, testSendBody(), "idem-queue-2")
	if retry.Code != http.StatusAccepted {
		t.Fatalf("expected queue_full idempotency key to be reusable after capacity returns, got %d: %s", retry.Code, retry.Body.String())
	}
}

func TestSendRequestQueueFullDoesNotConsumeRateLimit(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Defaults.MemoryQueueSize = 1
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 2
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	first := performSend(t, runtime, testSendBody(), "idem-queue-rate-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performSend(t, runtime, testSendBody(), "idem-queue-rate-2")
	assertErrorResponse(t, second, http.StatusServiceUnavailable, domain.ErrorCodeQueueFull)

	if _, err := runtime.Queue().Dequeue(context.Background()); err != nil {
		t.Fatalf("dequeue first task: %v", err)
	}
	third := performSend(t, runtime, testSendBody(), "idem-queue-rate-3")
	if third.Code != http.StatusAccepted {
		t.Fatalf("expected third request to still have rate limit capacity, got %d: %s", third.Code, third.Body.String())
	}
}

func TestSendRequestMessageLogFailureDoesNotConsumeRateLimit(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 1
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()
	if err := runtime.messageLog.Close(); err != nil {
		t.Fatalf("close message log: %v", err)
	}

	recorder := performSend(t, runtime, testSendBody(), "idem-log-fail")
	assertErrorResponse(t, recorder, http.StatusInternalServerError, domain.ErrorCodeInternal)

	reopened, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:           cfg.Logging.Dir,
		MaxBytes:      int64(cfg.Logging.MaxFileSizeMB) * 1024 * 1024,
		MaxBackups:    cfg.Logging.MaxBackups,
		EventsEnabled: cfg.Webhooks.Enabled,
	})
	if err != nil {
		t.Fatalf("reopen message log: %v", err)
	}
	runtime.messageLog = reopened

	retry := performSend(t, runtime, testSendBody(), "idem-log-fail-retry")
	if retry.Code != http.StatusAccepted {
		t.Fatalf("expected retry to keep rate limit capacity after log failure, got %d: %s", retry.Code, retry.Body.String())
	}
}

func TestCommitInitialQueueTaskMarksQueuedMessageFailedWhenQueueCloses(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	message := domain.Message{
		RequestID: "req_commit_closed",
		MessageID: "msg_commit_closed",
		AppCode:   "project_a",
		SceneCode: "register_code",
		ToDomain:  "example.com",
		ToHash:    "hash",
		Locale:    "en-US",
		Status:    domain.MessageStatusQueued,
	}
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append queued message: %v", err)
	}
	reservation, err := runtime.Queue().Reserve()
	if err != nil {
		t.Fatalf("reserve queue slot: %v", err)
	}
	if err := runtime.Queue().Close(); err != nil {
		t.Fatalf("close queue before commit: %v", err)
	}

	err = commitInitialQueueTask(runtime.MessageLog(), reservation, message)
	if !errors.Is(err, lite.ErrMemoryQueueClosed) {
		t.Fatalf("expected closed queue error, got %v", err)
	}

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 2 {
		t.Fatalf("expected queued and failed records, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusQueued))
	assertRecordValue(t, messageRecords[1], "status", string(domain.MessageStatusFailed))
	assertRecordValue(t, messageRecords[1], "error_code", string(domain.ErrorCodeInternal))
	assertRecordValue(t, messageRecords[1], "error_message", "queue commit failed")
}

func TestSendRequestInvalidJSON(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performSend(t, runtime, `{"scene":`, "idem-invalid-json")
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestSendRequestAuthenticatesBeforeBodyValidation(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", strings.NewReader(`{"scene":`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "idem-auth-before-body")
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestSendRequestAuthenticatesBeforeSceneValidation(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", strings.NewReader(`{"to":"user@example.com","vars":{"code":"123456"}}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "idem-auth-before-scene")
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestSendRequestRejectsNonStringContextUserIP(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	body := strings.Replace(testSendBody(), `"1.2.3.4"`, `1234`, 1)
	recorder := performSend(t, runtime, body, "idem-context-user-ip")

	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidContext)
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

func TestSendRequestAcceptsCaseInsensitiveJSONContentType(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	request := newSendRequest(testSendBody(), "idem-media-case")
	request.Header.Set("Content-Type", "Application/JSON; Charset=UTF-8")
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSendRequestValidatesContentTypeBeforeBodySize(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", strings.NewReader(strings.Repeat("x", runtime.defaults.MaxRequestBodyBytes+1)))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	request.Header.Set("Idempotency-Key", "idem-media-before-size")
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

func reserveTestSendIdempotency(t *testing.T, runtime *Runtime, idempotencyKey string) *lite.IdempotencyReservation {
	t.Helper()

	fingerprint, err := domain.RequestFingerprint("user@example.com", "en-US", map[string]any{"code": "123456"})
	if err != nil {
		t.Fatalf("build fingerprint: %v", err)
	}
	reservation, decision, err := runtime.idempotent.Reserve(
		"project_a",
		"register_code",
		domain.IdempotencyHash("project_a", "register_code", idempotencyKey),
		fingerprint,
	)
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}
	if decision.State != lite.IdempotencyDecisionNew {
		t.Fatalf("expected new idempotency decision, got %+v", decision)
	}

	return reservation
}

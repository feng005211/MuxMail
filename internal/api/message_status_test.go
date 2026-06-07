package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

func TestMessageStatusReturnsLatestMessage(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	sendRecorder := performSend(t, runtime, testSendBody(), "idem-status")
	if sendRecorder.Code != http.StatusAccepted {
		t.Fatalf("expected send 202, got %d: %s", sendRecorder.Code, sendRecorder.Body.String())
	}
	var sendOutput sendResponse
	decodeJSON(t, sendRecorder.Body.String(), &sendOutput)

	message := testStatusMessage(sendOutput.MessageID)
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append latest status: %v", err)
	}

	statusRecorder := performMessageStatus(t, runtime, sendOutput.MessageID, testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}

	var response messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &response)
	if response.MessageID != sendOutput.MessageID || response.Status != domain.MessageStatusSent {
		t.Fatalf("unexpected status response: %+v", response)
	}
	if response.App != "project_a" || response.Scene != "register_code" || response.ToDomain != "example.com" {
		t.Fatalf("unexpected safe metadata: %+v", response)
	}
	if response.UpdatedAt == "" || response.ToHash == "" {
		t.Fatalf("expected updated_at and to_hash, got %+v", response)
	}
	if strings.Contains(statusRecorder.Body.String(), "user@example.com") ||
		strings.Contains(statusRecorder.Body.String(), "Your code") ||
		strings.Contains(statusRecorder.Body.String(), "123456") {
		t.Fatalf("message status leaked sensitive content: %s", statusRecorder.Body.String())
	}
}

func TestMessageStatusRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performMessageStatus(t, runtime, "msg_missing", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestMessageStatusHidesOtherApps(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	otherApp := cfg.Apps[0]
	otherApp.Code = "project_b"
	otherApp.APIKeys = []config.APIKeyConfig{{Name: "default", KeyRef: "plain:mk_test_other_api_key_123456789"}}
	cfg.Apps = append(cfg.Apps, otherApp)
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	message := testStatusMessage("msg_cross_app")
	message.AppCode = "project_b"
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append other app message: %v", err)
	}

	recorder := performMessageStatus(t, runtime, "msg_cross_app", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusNotFound, domain.ErrorCodeMessageNotFound)
}

func TestMessageStatusMissingMessageReturnsNotFound(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performMessageStatus(t, runtime, "msg_missing", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusNotFound, domain.ErrorCodeMessageNotFound)
}

func TestMessageListReturnsLatestSnapshots(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := testStatusMessage("msg_list_a")
	first.Status = domain.MessageStatusQueued
	second := testStatusMessage("msg_list_b")
	second.SceneCode = "reset_password"
	second.Status = domain.MessageStatusFailed
	if err := runtime.MessageLog().AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	recorder := performMessageList(t, runtime, "", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.App != "project_a" || response.Limit != defaultMessageListLimit {
		t.Fatalf("unexpected message list response: %+v", response)
	}
	if len(response.Messages) != 2 {
		t.Fatalf("expected two messages, got %+v", response.Messages)
	}
	if response.Messages[0].MessageID != "msg_list_b" || response.Messages[1].MessageID != "msg_list_a" {
		t.Fatalf("expected descending latest messages, got %+v", response.Messages)
	}
}

func TestMessageListSupportsStatusAndSceneFilters(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := testStatusMessage("msg_list_filter_a")
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusQueued
	second := testStatusMessage("msg_list_filter_b")
	second.SceneCode = "register_code"
	second.Status = domain.MessageStatusFailed
	third := testStatusMessage("msg_list_filter_c")
	third.SceneCode = "reset_password"
	third.Status = domain.MessageStatusFailed
	if err := runtime.MessageLog().AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(third); err != nil {
		t.Fatalf("append third message: %v", err)
	}

	recorder := performMessageList(t, runtime, "limit=1&status=failed&scene=register_code", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.Limit != 1 || len(response.Messages) != 1 || response.Messages[0].MessageID != "msg_list_filter_b" {
		t.Fatalf("unexpected filtered message list: %+v", response)
	}
}

func TestMessageListRejectsInvalidQuery(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performMessageList(t, runtime, "limit=0", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)

	recorder = performMessageList(t, runtime, "status=opening", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)
}

func TestMessageListRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performMessageList(t, runtime, "", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestFailedMessageListReturnsOnlyFailedMessages(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := testStatusMessage("msg_failed_a")
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusFailed
	second := testStatusMessage("msg_failed_b")
	second.SceneCode = "reset_password"
	second.Status = domain.MessageStatusFailed
	third := testStatusMessage("msg_sent_c")
	third.SceneCode = "register_code"
	third.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(first); err != nil {
		t.Fatalf("append first failed message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(second); err != nil {
		t.Fatalf("append second failed message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(third); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	recorder := performFailedMessageList(t, runtime, "", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if len(response.Messages) != 2 {
		t.Fatalf("expected two failed messages, got %+v", response.Messages)
	}
	for _, message := range response.Messages {
		if message.Status != domain.MessageStatusFailed {
			t.Fatalf("expected failed-only list, got %+v", response.Messages)
		}
	}
}

func TestFailedMessageListSupportsSceneAndLimit(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	first := testStatusMessage("msg_failed_filter_a")
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusFailed
	second := testStatusMessage("msg_failed_filter_b")
	second.SceneCode = "register_code"
	second.Status = domain.MessageStatusFailed
	third := testStatusMessage("msg_failed_filter_c")
	third.SceneCode = "reset_password"
	third.Status = domain.MessageStatusFailed
	if err := runtime.MessageLog().AppendMessage(first); err != nil {
		t.Fatalf("append first failed message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(second); err != nil {
		t.Fatalf("append second failed message: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(third); err != nil {
		t.Fatalf("append third failed message: %v", err)
	}

	recorder := performFailedMessageList(t, runtime, "limit=1&scene=register_code", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.Limit != 1 || len(response.Messages) != 1 {
		t.Fatalf("unexpected failed message filter response: %+v", response)
	}
	if response.Messages[0].Scene != "register_code" || response.Messages[0].Status != domain.MessageStatusFailed {
		t.Fatalf("unexpected failed message entry: %+v", response.Messages[0])
	}
}

func TestFailedMessageListRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performFailedMessageList(t, runtime, "", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestSuppressionListReturnsAppScopedEntries(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	_, err := runtime.suppressed.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "bounce@example.com",
		Reason:  domain.SuppressionReasonHardBounce,
	})
	if err != nil {
		t.Fatalf("add suppression entry: %v", err)
	}
	_, err = runtime.suppressed.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "complaint@example.com",
		Reason:  domain.SuppressionReasonComplaint,
	})
	if err != nil {
		t.Fatalf("add suppression entry: %v", err)
	}
	_, err = runtime.suppressed.Add(domain.SuppressionEntry{
		AppCode: "project_b",
		Email:   "foreign@example.com",
		Reason:  domain.SuppressionReasonComplaint,
	})
	if err != nil {
		t.Fatalf("add suppression entry: %v", err)
	}

	recorder := performSuppressionList(t, runtime, "limit=1&reason=complaint", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response suppressionListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.App != "project_a" || response.Limit != 1 {
		t.Fatalf("unexpected suppression response: %+v", response)
	}
	if len(response.Suppressions) != 1 {
		t.Fatalf("expected one suppression entry, got %+v", response.Suppressions)
	}
	entry := response.Suppressions[0]
	if entry.Email != "complaint@example.com" || entry.Reason != domain.SuppressionReasonComplaint {
		t.Fatalf("unexpected suppression entry: %+v", entry)
	}
}

func TestSuppressionListSupportsEmailFilter(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	_, err := runtime.suppressed.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "bounce@example.com",
		Reason:  domain.SuppressionReasonHardBounce,
	})
	if err != nil {
		t.Fatalf("add suppression entry: %v", err)
	}

	recorder := performSuppressionList(t, runtime, "email=bounce@example.com", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response suppressionListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if len(response.Suppressions) != 1 || response.Suppressions[0].NormalizedEmail != "bounce@example.com" {
		t.Fatalf("unexpected suppression email filter response: %+v", response)
	}
}

func TestSuppressionListRejectsInvalidQuery(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performSuppressionList(t, runtime, "limit=0", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)

	recorder = performSuppressionList(t, runtime, "reason=unsubscribed", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)

	recorder = performSuppressionList(t, runtime, "email=not-an-email", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)
}

func TestSuppressionListRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performSuppressionList(t, runtime, "", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestMessageEventsReturnsAppScopedTimeline(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_events")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_events",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"source":"resend"}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}); err != nil {
		t.Fatalf("append provider event: %v", err)
	}

	recorder := performMessageEvents(t, runtime, "msg_events", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageEventsResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.MessageID != "msg_events" || response.App != "project_a" {
		t.Fatalf("unexpected message events response: %+v", response)
	}
	if len(response.Events) != 1 {
		t.Fatalf("expected one event, got %+v", response.Events)
	}
	event := response.Events[0]
	if event.Provider != domain.ProviderResend || event.EventType != domain.ProviderEventDelivered {
		t.Fatalf("unexpected event response: %+v", event)
	}
	if strings.Contains(recorder.Body.String(), "user@example.com") {
		t.Fatalf("message events response leaked recipient: %s", recorder.Body.String())
	}
}

func TestMessageEventsReturnsEmptyListWhenNoEvents(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	message := testStatusMessage("msg_no_events")
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}

	recorder := performMessageEvents(t, runtime, "msg_no_events", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageEventsResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if len(response.Events) != 0 {
		t.Fatalf("expected empty event list, got %+v", response.Events)
	}
}

func TestMessageEventsHidesOtherApps(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	otherApp := cfg.Apps[0]
	otherApp.Code = "project_b"
	otherApp.APIKeys = []config.APIKeyConfig{{Name: "default", KeyRef: "plain:mk_test_other_api_key_123456789"}}
	cfg.Apps = append(cfg.Apps, otherApp)
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		SharedSecretRef: "plain:" + testWebhookSecret,
	}
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	message := testStatusMessage("msg_cross_app_events")
	message.AppCode = "project_b"
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append other app message: %v", err)
	}
	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_cross_app_events",
		AppCode:             "project_b",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"source":"resend"}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}); err != nil {
		t.Fatalf("append other app provider event: %v", err)
	}

	recorder := performMessageEvents(t, runtime, "msg_cross_app_events", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusNotFound, domain.ErrorCodeMessageNotFound)
}

func TestMessageAttemptsReturnsTimeline(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	message := testStatusMessage("msg_attempts")
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_attempts",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSending,
		FailureClass:        domain.FailureClassNone,
	}); err != nil {
		t.Fatalf("append sending attempt: %v", err)
	}
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_attempts",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_123",
		DurationMS:          42,
	}); err != nil {
		t.Fatalf("append sent attempt: %v", err)
	}

	recorder := performMessageAttempts(t, runtime, "msg_attempts", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageAttemptsResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.MessageID != "msg_attempts" || response.App != "project_a" {
		t.Fatalf("unexpected attempt response: %+v", response)
	}
	if len(response.Attempts) != 2 {
		t.Fatalf("expected two attempt records, got %+v", response.Attempts)
	}
	if response.Attempts[0].Status != domain.AttemptStatusSending || response.Attempts[1].Status != domain.AttemptStatusSent {
		t.Fatalf("unexpected attempt sequence: %+v", response.Attempts)
	}
	if response.Attempts[1].ProviderMessageID != "provider_123" || response.Attempts[1].DurationMS != 42 {
		t.Fatalf("unexpected attempt metadata: %+v", response.Attempts[1])
	}
}

func TestMessageAttemptsReturnsEmptyListWhenNoAttempts(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	message := testStatusMessage("msg_no_attempts")
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}

	recorder := performMessageAttempts(t, runtime, "msg_no_attempts", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response messageAttemptsResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if len(response.Attempts) != 0 {
		t.Fatalf("expected empty attempt list, got %+v", response.Attempts)
	}
}

func TestMessageAttemptsHidesOtherApps(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	otherApp := cfg.Apps[0]
	otherApp.Code = "project_b"
	otherApp.APIKeys = []config.APIKeyConfig{{Name: "default", KeyRef: "plain:mk_test_other_api_key_123456789"}}
	cfg.Apps = append(cfg.Apps, otherApp)
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	message := testStatusMessage("msg_cross_app_attempts")
	message.AppCode = "project_b"
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append other app message: %v", err)
	}
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_cross_app_attempts",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_123",
		DurationMS:          42,
	}); err != nil {
		t.Fatalf("append other app attempt: %v", err)
	}

	recorder := performMessageAttempts(t, runtime, "msg_cross_app_attempts", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusNotFound, domain.ErrorCodeMessageNotFound)
}

func performMessageStatus(t *testing.T, runtime *Runtime, messageID string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages/"+messageID, nil)
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performMessageList(t *testing.T, runtime *Runtime, rawQuery string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages", nil)
	if rawQuery != "" {
		request.URL.RawQuery = rawQuery
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performFailedMessageList(t *testing.T, runtime *Runtime, rawQuery string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages/failed", nil)
	if rawQuery != "" {
		request.URL.RawQuery = rawQuery
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performSuppressionList(t *testing.T, runtime *Runtime, rawQuery string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/suppressions", nil)
	if rawQuery != "" {
		request.URL.RawQuery = rawQuery
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performMessageEvents(t *testing.T, runtime *Runtime, messageID string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages/"+messageID+"/events", nil)
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performMessageAttempts(t *testing.T, runtime *Runtime, messageID string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages/"+messageID+"/attempts", nil)
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func testStatusMessage(messageID string) domain.Message {
	normalizedEmail := domain.NormalizeEmail("user@example.com")
	return domain.Message{
		RequestID:          "req_status",
		BusinessRequestID:  "biz_status",
		MessageID:          messageID,
		AppCode:            "project_a",
		APIKeyName:         "default",
		SceneCode:          "register_code",
		ToEmail:            "user@example.com",
		NormalizedToEmail:  normalizedEmail,
		ToDomain:           "example.com",
		ToHash:             domain.ToHash("project_a", normalizedEmail),
		Locale:             "en-US",
		Subject:            "Your code is 123456",
		TextBody:           "Your code is 123456.",
		Status:             domain.MessageStatusQueued,
		IdempotencyHash:    "idem_hash",
		RequestFingerprint: "fingerprint",
	}
}

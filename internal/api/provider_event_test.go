package api

import (
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

const testWebhookSecret = "mk_webhook_test_secret_123456789"

func TestProviderEventDisabledByDefault(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_01ABC", "delivered"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusServiceUnavailable, domain.ErrorCodeWebhookDisabled)
}

func TestProviderEventRejectsInvalidSecret(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_01ABC", "delivered"), "wrong-secret")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestProviderEventAppendsEventAndMessageStatus(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_webhook")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_webhook", "delivered"), testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_webhook", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}

	eventData, err := os.ReadFile(filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	if !strings.Contains(string(eventData), `"event_type":"delivered"`) ||
		!strings.Contains(string(eventData), `"message_id":"msg_webhook"`) {
		t.Fatalf("expected delivered event log, got %s", string(eventData))
	}
}

func TestProviderEventComplaintAddsSuppression(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_complaint")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBodyWithRecipient("project_a", "msg_complaint", "complained", "user@example.com"), testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	reloaded, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	if _, ok := reloaded.Contains("project_a", domain.NormalizeEmail("user@example.com")); !ok {
		t.Fatal("expected complaint webhook to persist suppression")
	}

	blocked := performSend(t, runtime, testSendBody(), "idem-after-complaint")
	assertErrorResponse(t, blocked, http.StatusUnprocessableEntity, domain.ErrorCodeSuppressedRecipient)
}

func TestProviderEventDuplicateComplaintIsIgnored(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_complaint_duplicate")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	body := providerEventBodyWithRecipient("project_a", "msg_complaint_duplicate", "complained", "user@example.com")
	first := performProviderEvent(t, runtime, body, testWebhookSecret)
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first 202, got %d: %s", first.Code, first.Body.String())
	}
	second := performProviderEvent(t, runtime, body, testWebhookSecret)
	if second.Code != http.StatusAccepted {
		t.Fatalf("expected duplicate 202, got %d: %s", second.Code, second.Body.String())
	}

	eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if len(eventRecords) != 1 {
		t.Fatalf("expected one provider event record, got %d", len(eventRecords))
	}
	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 2 {
		t.Fatalf("expected sent + complained message records, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[1], "status", string(domain.MessageStatusComplained))

	data, err := os.ReadFile(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("read suppression file: %v", err)
	}
	if strings.Count(string(data), "app: project_a") != 1 {
		t.Fatalf("expected one suppression entry, got %s", string(data))
	}
}

func TestProviderEventHidesOtherAppMessage(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_other_app")
	message.AppCode = "project_b"
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append other app message: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBodyWithRecipient("project_a", "msg_other_app", "bounced", "user@example.com"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusNotFound, domain.ErrorCodeMessageNotFound)
}

func TestProviderEventListReturnsRecentEvents(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_recent_a",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_111",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"source":"resend"}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}); err != nil {
		t.Fatalf("append first recent event: %v", err)
	}
	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_recent_b",
		AppCode:             "project_a",
		Provider:            domain.ProviderBrevo,
		ProviderAccountCode: "brevo_main",
		ProviderChannelCode: "brevo_auth_api",
		ProviderMessageID:   "provider_222",
		EventType:           domain.ProviderEventBounced,
		EventPayload:        `{"source":"brevo"}`,
		OccurredAt:          "2026-05-28T03:10:00Z",
	}); err != nil {
		t.Fatalf("append second recent event: %v", err)
	}

	recorder := performProviderEventList(t, runtime, "limit=1&provider=brevo&event_type=bounced", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response providerEventListResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.App != "project_a" || response.Limit != 1 {
		t.Fatalf("unexpected provider event list response: %+v", response)
	}
	if len(response.Events) != 1 {
		t.Fatalf("expected one recent event, got %+v", response.Events)
	}
	event := response.Events[0]
	if event.MessageID != "msg_recent_b" || event.Provider != domain.ProviderBrevo || event.EventType != domain.ProviderEventBounced {
		t.Fatalf("unexpected recent event: %+v", event)
	}
}

func TestProviderEventListRejectsInvalidQuery(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performProviderEventList(t, runtime, "limit=0", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)

	recorder = performProviderEventList(t, runtime, "provider=mailgun", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)

	recorder = performProviderEventList(t, runtime, "event_type=open", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)
}

func TestProviderEventListRequiresAuthorization(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performProviderEventList(t, runtime, "", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestProviderEventListHidesRawPayloadAndRecipient(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_recent_payload",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_444",
		RecipientEmail:      "user@example.com",
		EventType:           domain.ProviderEventComplained,
		EventPayload:        `{"source":"resend","secret":"hidden"}`,
		OccurredAt:          "2026-05-28T03:20:00Z",
	}); err != nil {
		t.Fatalf("append payload-bearing event: %v", err)
	}

	recorder := performProviderEventList(t, runtime, "", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "user@example.com") || strings.Contains(recorder.Body.String(), "hidden") {
		t.Fatalf("provider event list leaked sensitive data: %s", recorder.Body.String())
	}
}

func openWebhookRuntime(t *testing.T) (*Runtime, *config.Config) {
	t.Helper()

	cfg := testRuntimeConfig(t, "off")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		SharedSecretRef: "plain:" + testWebhookSecret,
	}
	runtime, err := NewRuntime(cfg, config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open webhook runtime: %v", err)
	}

	return runtime, cfg
}

func performProviderEvent(t *testing.T, runtime *Runtime, body string, secret string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/v1/provider-events", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performProviderEventList(t *testing.T, runtime *Runtime, rawQuery string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/provider-events", nil)
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

func providerEventBody(app string, messageID string, eventType string) string {
	return `{"app":"` + app + `","message_id":"` + messageID + `","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"` + eventType + `","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`
}

func providerEventBodyWithRecipient(app string, messageID string, eventType string, recipient string) string {
	return `{"app":"` + app + `","message_id":"` + messageID + `","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","recipient_email":"` + recipient + `","event_type":"` + eventType + `","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`
}

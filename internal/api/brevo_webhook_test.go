package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const testBrevoWebhookToken = "mk_brevo_webhook_token_123456789"

func TestBrevoWebhookDisabledWithoutToken(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBody("project_a", "msg_01ABC", "delivered"), testBrevoWebhookToken)
	assertErrorResponse(t, recorder, http.StatusServiceUnavailable, domain.ErrorCodeWebhookDisabled)
}

func TestBrevoWebhookRejectsInvalidToken(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBody("project_a", "msg_01ABC", "delivered"), "wrong-token")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestBrevoWebhookRejectsInvalidIdentityBeforeLogQuery(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	messageLog := runtime.messageLog
	runtime.messageLog = nil
	defer func() {
		runtime.messageLog = messageLog
	}()

	for _, body := range []string{
		brevoWebhookBody("ProjectA", "msg_01ABC", "delivered"),
		brevoWebhookBody("project_a", "bad_01ABC", "delivered"),
	} {
		recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
		assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
	}
}

func TestBrevoWebhookAppendsDeliveredEvent(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBody("project_a", "msg_brevo", "delivered"), testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_brevo", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestBrevoWebhookMapsSpamToComplained(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_spam")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_spam", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBodyWithRecipient("project_a", "msg_brevo_spam", "spam", "user@example.com"), testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_brevo_spam", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusComplained {
		t.Fatalf("expected complained status, got %+v", status)
	}
}

func TestBrevoWebhookCombinesTagFields(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_combined_tags")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_combined_tags", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	body := `{"event":"delivered","message-id":"provider_123","ts_event":1779937200,"tag":["source:brevo"],"tags":["app:project_a","message_id:msg_brevo_combined_tags","provider_account:brevo_main","provider_channel:brevo_auth_api"]}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_brevo_combined_tags", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestBrevoWebhookAcceptsStringEncodedTagArray(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_string_tag")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_string_tag", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	body := `{"event":"delivered","message-id":"provider_123","ts_event":1779937200,"tag":"[\"app:project_a\",\"message_id:msg_brevo_string_tag\",\"provider_account:brevo_main\",\"provider_channel:brevo_auth_api\"]"}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_brevo_string_tag", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestBrevoWebhookTrimsMetadataTagFields(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_spaced_tags")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_spaced_tags", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	body := `{"event":"delivered","message-id":"provider_123","ts_event":1779937200,"tag":[" app : project_a "," message_id : msg_brevo_spaced_tags "," provider_account : brevo_main "," provider_channel : brevo_auth_api "]}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_brevo_spaced_tags", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestBrevoWebhookBounceAddsSuppression(t *testing.T) {
	runtime, cfg := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_bounce")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_bounce", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBodyWithRecipient("project_a", "msg_brevo_bounce", "hardBounce", "user@example.com"), testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	reloaded, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	if _, ok := reloaded.Contains("project_a", domain.NormalizeEmail("user@example.com")); !ok {
		t.Fatal("expected brevo bounce to persist suppression")
	}

	blocked := performSend(t, runtime, testSendBody(), "idem-after-brevo-bounce")
	assertErrorResponse(t, blocked, http.StatusUnprocessableEntity, domain.ErrorCodeSuppressedRecipient)
}

func TestBrevoWebhookRejectsInvalidSuppressionRecipient(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBodyWithRecipient("project_a", "msg_brevo_invalid_recipient", "hardBounce", "not an email"), testBrevoWebhookToken)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestBrevoWebhookRequiresMuxMailTags(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	body := `{"event":"delivered","message-id":"provider_123","date":"2026-05-28 03:00:00","tag":["app:project_a"]}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestBrevoWebhookRequiresProviderIdentityTags(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	body := `{"event":"delivered","message-id":"provider_123","date":"2026-05-28 03:00:00","tag":["app:project_a","message_id:msg_missing_identity","provider_account:brevo_main"]}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestBrevoWebhookRequiresEventTimestamp(t *testing.T) {
	runtime, _ := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	body := `{"event":"delivered","message-id":"provider_123","tag":["app:project_a","message_id:msg_missing_date","provider_account:brevo_main","provider_channel:brevo_auth_api"]}`
	recorder := performBrevoWebhook(t, runtime, body, testBrevoWebhookToken)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestBrevoWebhookUsesEventTimestampAsUTC(t *testing.T) {
	runtime, cfg := openBrevoWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_brevo_ts")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_brevo_ts", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	recorder := performBrevoWebhook(t, runtime, brevoWebhookBody("project_a", "msg_brevo_ts", "delivered"), testBrevoWebhookToken)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if len(eventRecords) != 1 {
		t.Fatalf("expected one provider event record, got %d", len(eventRecords))
	}
	assertRecordValue(t, eventRecords[0], "occurred_at", "2026-05-28T03:00:00Z")
}

func openBrevoWebhookRuntime(t *testing.T) (*Runtime, *config.Config) {
	t.Helper()

	cfg := testRuntimeConfig(t, "off")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:       true,
		BrevoTokenRef: "plain:" + testBrevoWebhookToken,
	}
	runtime, err := NewRuntime(cfg, config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open brevo webhook runtime: %v", err)
	}

	return runtime, cfg
}

func performBrevoWebhook(t *testing.T, runtime *Runtime, body string, token string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/v1/provider-events/brevo", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func brevoWebhookBody(app string, messageID string, eventType string) string {
	return brevoWebhookBodyWithRecipient(app, messageID, eventType, "")
}

func brevoWebhookBodyWithRecipient(app string, messageID string, eventType string, recipient string) string {
	emailField := ``
	if recipient != "" {
		emailField = `,"email":"` + recipient + `"`
	}
	return `{"event":"` + eventType + `","message-id":"provider_123","date":"2026-05-28 03:00:00","ts_event":1779937200` + emailField + `,"tag":["app:` + app + `","message_id:` + messageID + `","provider_account:brevo_main","provider_channel:brevo_auth_api"]}`
}

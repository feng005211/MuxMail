package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestProviderEventSharedSecretDisabledWhenOnlyNativeWebhookConfigured(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		ResendSecretRef: "plain:" + testResendWebhookSecret,
	}
	runtime := openRuntimeFromConfig(t, cfg)
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

func TestProviderEventRejectsInvalidIdentityBeforeLogQuery(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	messageLog := runtime.messageLog
	runtime.messageLog = nil
	defer func() {
		runtime.messageLog = messageLog
	}()

	for _, body := range []string{
		providerEventBody("ProjectA", "msg_01ABC", "delivered"),
		providerEventBody("project_a", "bad_01ABC", "delivered"),
		`{"app":"project_a","message_id":"msg_01ABC","provider":"resend","provider_account":"resend main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","occurred_at":"2026-05-28T03:00:00Z"}`,
		`{"app":"project_a","message_id":"msg_01ABC","provider":"resend","provider_account":"resend_main","provider_channel":"resend/auth","provider_message_id":"provider_123","event_type":"delivered","occurred_at":"2026-05-28T03:00:00Z"}`,
	} {
		recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
		assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
	}
}

func TestProviderEventAppendsEventAndMessageStatus(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_webhook")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_webhook", domain.ProviderResend, "resend_main", "resend_auth_api")

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

func TestProviderEventMatchesSentAttemptWithoutProviderMessageID(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_webhook_missing_provider_id")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_webhook_missing_provider_id",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		DurationMS:          42,
	}); err != nil {
		t.Fatalf("append sent attempt without provider message id: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_webhook_missing_provider_id", "delivered"), testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_webhook_missing_provider_id", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestProviderEventNormalizesPayloadBeforeLogging(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_payload")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_payload", domain.ProviderResend, "resend_main", "resend_auth_api")

	body := providerEventBodyWithPayload("project_a", "msg_payload", `{"recipient":"user@example.com","secret":"mk_live_secret"}`)
	recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	eventData, err := os.ReadFile(filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	eventLog := string(eventData)
	if strings.Contains(eventLog, "user@example.com") || strings.Contains(eventLog, "mk_live_secret") {
		t.Fatalf("provider event log leaked request payload: %s", eventLog)
	}
	if !strings.Contains(eventLog, `"event_payload":"{\"source\":\"generic\"}"`) {
		t.Fatalf("expected normalized event payload, got %s", eventLog)
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
	appendSentAttempt(t, runtime, "msg_complaint", domain.ProviderResend, "resend_main", "resend_auth_api")

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

func TestProviderEventComplaintUpgradesBounceSuppression(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_suppression_upgrade")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_suppression_upgrade", domain.ProviderResend, "resend_main", "resend_auth_api")

	bounced := performProviderEvent(t, runtime, providerEventBodyWithRecipient("project_a", "msg_suppression_upgrade", "bounced", "user@example.com"), testWebhookSecret)
	if bounced.Code != http.StatusAccepted {
		t.Fatalf("expected bounced 202, got %d: %s", bounced.Code, bounced.Body.String())
	}
	complainedBody := strings.Replace(
		providerEventBodyWithRecipient("project_a", "msg_suppression_upgrade", "complained", "user@example.com"),
		`"occurred_at":"2026-05-28T03:00:00Z"`,
		`"occurred_at":"2026-05-28T03:05:00Z"`,
		1,
	)
	complained := performProviderEvent(t, runtime, complainedBody, testWebhookSecret)
	if complained.Code != http.StatusAccepted {
		t.Fatalf("expected complained 202, got %d: %s", complained.Code, complained.Body.String())
	}

	reloaded, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	entry, ok := reloaded.Contains("project_a", domain.NormalizeEmail("user@example.com"))
	if !ok {
		t.Fatal("expected suppression entry")
	}
	if entry.Reason != domain.SuppressionReasonComplaint {
		t.Fatalf("expected complaint suppression to replace hard bounce, got %+v", entry)
	}
}

func TestProviderEventDuplicateComplaintIsIgnored(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_complaint_duplicate")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_complaint_duplicate", domain.ProviderResend, "resend_main", "resend_auth_api")

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

func TestProviderEventRecordsStatsOnlyForNewEvent(t *testing.T) {
	cfg := testRuntimeConfig(t, "file")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		SharedSecretRef: "plain:" + testWebhookSecret,
	}
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	message := testStatusMessage("msg_event_stats")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_event_stats", domain.ProviderResend, "resend_main", "resend_auth_api")

	body := providerEventBody("project_a", "msg_event_stats", "delivered")
	first := performProviderEvent(t, runtime, body, testWebhookSecret)
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first event 202, got %d: %s", first.Code, first.Body.String())
	}
	duplicate := performProviderEvent(t, runtime, body, testWebhookSecret)
	if duplicate.Code != http.StatusAccepted {
		t.Fatalf("expected duplicate event 202, got %d: %s", duplicate.Code, duplicate.Body.String())
	}

	recorder := performStatsSummary(t, runtime, "24h", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected stats summary 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response statsSummaryResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if got := response.Metrics[lite.MetricProviderEventsDelivered]; got != 1 {
		t.Fatalf("expected delivered event stat to count only the first event, got %g in %+v", got, response.Metrics)
	}
}

func TestProviderEventConcurrentDuplicateWritesOnce(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_concurrent_duplicate")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_concurrent_duplicate", domain.ProviderResend, "resend_main", "resend_auth_api")

	body := providerEventBody("project_a", "msg_concurrent_duplicate", "delivered")
	start := make(chan struct{})
	type eventResult struct {
		code int
		body string
	}
	results := make(chan eventResult, 16)
	var workers sync.WaitGroup
	for index := 0; index < cap(results); index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			request := httptest.NewRequest(http.MethodPost, "/v1/provider-events", strings.NewReader(body))
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+testWebhookSecret)
			recorder := httptest.NewRecorder()
			runtime.Handler().ServeHTTP(recorder, request)
			results <- eventResult{code: recorder.Code, body: recorder.Body.String()}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	for result := range results {
		if result.code != http.StatusAccepted {
			t.Fatalf("expected all duplicate events to be accepted, got %d: %s", result.code, result.body)
		}
	}
	eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if len(eventRecords) != 1 {
		t.Fatalf("expected one provider event record, got %d", len(eventRecords))
	}
	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 2 {
		t.Fatalf("expected sent + delivered message records, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[1], "status", string(domain.MessageStatusDelivered))
}

func TestProviderEventDuplicateOlderEventDoesNotRollBackStatus(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_duplicate_old_event")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_duplicate_old_event", domain.ProviderResend, "resend_main", "resend_auth_api")

	deliveredBody := providerEventBody("project_a", "msg_duplicate_old_event", "delivered")
	delivered := performProviderEvent(t, runtime, deliveredBody, testWebhookSecret)
	if delivered.Code != http.StatusAccepted {
		t.Fatalf("expected delivered 202, got %d: %s", delivered.Code, delivered.Body.String())
	}

	bouncedBody := providerEventBodyWithRecipient("project_a", "msg_duplicate_old_event", "bounced", "user@example.com")
	bounced := performProviderEvent(t, runtime, bouncedBody, testWebhookSecret)
	if bounced.Code != http.StatusAccepted {
		t.Fatalf("expected bounced 202, got %d: %s", bounced.Code, bounced.Body.String())
	}

	duplicateDelivered := performProviderEvent(t, runtime, deliveredBody, testWebhookSecret)
	if duplicateDelivered.Code != http.StatusAccepted {
		t.Fatalf("expected duplicate delivered 202, got %d: %s", duplicateDelivered.Code, duplicateDelivered.Body.String())
	}
	var duplicateResponse providerEventResponse
	decodeJSON(t, duplicateDelivered.Body.String(), &duplicateResponse)
	if duplicateResponse.Status != domain.MessageStatusBounced {
		t.Fatalf("expected duplicate older delivered event to keep bounced status, got %+v", duplicateResponse)
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_duplicate_old_event", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusBounced {
		t.Fatalf("expected bounced status after duplicate older event, got %+v", status)
	}
}

func TestProviderEventDuplicateOlderBounceCanRepairMissingStatus(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_duplicate_old_bounce_repair")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_duplicate_old_bounce_repair", domain.ProviderResend, "resend_main", "resend_auth_api")

	bounceEvent := domain.ProviderEvent{
		MessageID:           "msg_duplicate_old_bounce_repair",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventBounced,
		EventPayload:        normalizedProviderEventPayload,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := runtime.MessageLog().AppendProviderEvent(bounceEvent); err != nil {
		t.Fatalf("append bounce provider event without status: %v", err)
	}
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_duplicate_old_bounce_repair",
		AppCode:             "project_a",
		AttemptNo:           2,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_456",
		DurationMS:          43,
	}); err != nil {
		t.Fatalf("append later sent attempt: %v", err)
	}
	deliveredEvent := bounceEvent
	deliveredEvent.ProviderMessageID = "provider_456"
	deliveredEvent.EventType = domain.ProviderEventDelivered
	deliveredEvent.OccurredAt = "2026-05-28T03:05:00Z"
	if err := runtime.MessageLog().AppendProviderEvent(deliveredEvent); err != nil {
		t.Fatalf("append later delivered provider event: %v", err)
	}

	body := providerEventBodyWithRecipient("project_a", "msg_duplicate_old_bounce_repair", "bounced", "user@example.com")
	recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected duplicate bounce repair 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_duplicate_old_bounce_repair", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusBounced {
		t.Fatalf("expected duplicate older bounce to repair bounced status, got %+v", status)
	}
}

func TestProviderEventLateDeliveredDoesNotOverwriteBounce(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_late_delivered")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_late_delivered", domain.ProviderResend, "resend_main", "resend_auth_api")

	bouncedBody := providerEventBodyWithRecipient("project_a", "msg_late_delivered", "bounced", "user@example.com")
	bounced := performProviderEvent(t, runtime, bouncedBody, testWebhookSecret)
	if bounced.Code != http.StatusAccepted {
		t.Fatalf("expected bounced 202, got %d: %s", bounced.Code, bounced.Body.String())
	}

	deliveredBody := providerEventBody("project_a", "msg_late_delivered", "delivered")
	delivered := performProviderEvent(t, runtime, deliveredBody, testWebhookSecret)
	if delivered.Code != http.StatusAccepted {
		t.Fatalf("expected delivered 202, got %d: %s", delivered.Code, delivered.Body.String())
	}
	var deliveredResponse providerEventResponse
	decodeJSON(t, delivered.Body.String(), &deliveredResponse)
	if deliveredResponse.Status != domain.MessageStatusBounced {
		t.Fatalf("expected late delivered event to keep bounced status, got %+v", deliveredResponse)
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_late_delivered", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusBounced {
		t.Fatalf("expected bounced status after late delivered event, got %+v", status)
	}

	eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if len(eventRecords) != 2 {
		t.Fatalf("expected bounced and delivered event records, got %d", len(eventRecords))
	}
	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 2 {
		t.Fatalf("expected sent + bounced message records, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[1], "status", string(domain.MessageStatusBounced))
}

func TestProviderEventDuplicateLatestEventCanRepairMissingStatus(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_duplicate_repair")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_duplicate_repair", domain.ProviderResend, "resend_main", "resend_auth_api")
	if err := runtime.MessageLog().AppendProviderEvent(domain.ProviderEvent{
		MessageID:           "msg_duplicate_repair",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        normalizedProviderEventPayload,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}); err != nil {
		t.Fatalf("append provider event without status: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_duplicate_repair", "delivered"), testWebhookSecret)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected duplicate repair 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_duplicate_repair", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected duplicate latest event to repair delivered status, got %+v", status)
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

func TestProviderEventRejectsMismatchedSentAttempt(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_mismatch")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_mismatch", domain.ProviderBrevo, "brevo_main", "brevo_auth_api")

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_mismatch", "delivered"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)

	if _, err := os.Stat(filepath.Join(cfg.Logging.Dir, "mail-events.jsonl")); err == nil {
		eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
		if len(eventRecords) != 0 {
			t.Fatalf("expected no provider event records, got %d", len(eventRecords))
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat event log: %v", err)
	}

	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 1 {
		t.Fatalf("expected only original sent message record, got %d", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusSent))
}

func TestProviderEventRejectsStaleSentAttemptRecord(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_stale_attempt")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_stale_attempt", domain.ProviderResend, "resend_main", "resend_auth_api")
	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           "msg_stale_attempt",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusFailed,
		FailureClass:        domain.FailureClassTemporary,
		ErrorCode:           domain.ErrorCodeProviderUnavailable,
		ErrorMessage:        "provider request failed",
		DurationMS:          43,
	}); err != nil {
		t.Fatalf("append latest failed attempt: %v", err)
	}

	recorder := performProviderEvent(t, runtime, providerEventBody("project_a", "msg_stale_attempt", "delivered"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)

	eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
	if len(eventRecords) != 0 {
		t.Fatalf("expected stale sent attempt not to append provider events, got %d", len(eventRecords))
	}
	messageRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-messages.jsonl"))
	if len(messageRecords) != 1 {
		t.Fatalf("expected no webhook status update, got %d message records", len(messageRecords))
	}
	assertRecordValue(t, messageRecords[0], "status", string(domain.MessageStatusSent))
}

func TestProviderEventRejectsSuppressionRecipientMismatch(t *testing.T) {
	runtime, cfg := openWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_recipient_mismatch")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_recipient_mismatch", domain.ProviderResend, "resend_main", "resend_auth_api")

	recorder := performProviderEvent(t, runtime, providerEventBodyWithRecipient("project_a", "msg_recipient_mismatch", "bounced", "other@example.com"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)

	if _, err := os.Stat(filepath.Join(cfg.Logging.Dir, "mail-events.jsonl")); err == nil {
		eventRecords := readJSONLLines(t, filepath.Join(cfg.Logging.Dir, "mail-events.jsonl"))
		if len(eventRecords) != 0 {
			t.Fatalf("expected no provider event records, got %d", len(eventRecords))
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat event log: %v", err)
	}
	reloaded, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	if _, ok := reloaded.Contains("project_a", domain.NormalizeEmail("other@example.com")); ok {
		t.Fatal("expected mismatched recipient not to be suppressed")
	}
}

func TestProviderEventRequiresProviderIdentity(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	body := `{"app":"project_a","message_id":"msg_identity","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","event_type":"delivered","occurred_at":"2026-05-28T03:00:00Z"}`
	recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestProviderEventRequiresOccurredAt(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	body := `{"app":"project_a","message_id":"msg_identity","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered"}`
	recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestProviderEventRejectsInvalidOccurredAt(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	body := strings.Replace(providerEventBody("project_a", "msg_invalid_occurred_at", "delivered"), "2026-05-28T03:00:00Z", "2026-05-28 03:00:00", 1)
	recorder := performProviderEvent(t, runtime, body, testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestProviderEventRejectsInvalidSuppressionRecipient(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performProviderEvent(t, runtime, providerEventBodyWithRecipient("project_a", "msg_invalid_recipient", "bounced", "not an email"), testWebhookSecret)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
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

func providerEventBodyWithPayload(app string, messageID string, payload string) string {
	encodedPayload := strings.ReplaceAll(payload, `"`, `\"`)
	return `{"app":"` + app + `","message_id":"` + messageID + `","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","event_payload":"` + encodedPayload + `","occurred_at":"2026-05-28T03:00:00Z"}`
}

func appendSentAttempt(t *testing.T, runtime *Runtime, messageID string, provider domain.Provider, account string, channel string) {
	t.Helper()

	if err := runtime.MessageLog().AppendAttempt(domain.Attempt{
		MessageID:           messageID,
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            provider,
		ProviderAccountCode: account,
		ProviderChannelCode: channel,
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_123",
		DurationMS:          42,
	}); err != nil {
		t.Fatalf("append sent attempt: %v", err)
	}
}

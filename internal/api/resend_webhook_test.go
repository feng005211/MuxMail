package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const testResendWebhookSecret = "whsec_bXV4bWFpbF9yZXNlbmRfd2ViaG9va19zZWNyZXQ="

func TestResendWebhookDisabledWithoutSecret(t *testing.T) {
	runtime, _ := openWebhookRuntime(t)
	defer runtime.Close()

	recorder := performResendWebhook(t, runtime, resendWebhookBody("project_a", "msg_01ABC", "email.delivered"), testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusServiceUnavailable, domain.ErrorCodeWebhookDisabled)
}

func TestResendWebhookRejectsInvalidSignature(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	recorder := performResendWebhookWithSignature(t, runtime, resendWebhookBody("project_a", "msg_01ABC", "email.delivered"), "v1,bad")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestResendWebhookVerifierUsesSingleClockSnapshot(t *testing.T) {
	body := resendWebhookBody("project_a", "msg_clock_snapshot", "email.delivered")
	signedAt := time.Date(2026, 6, 9, 12, 30, 0, 0, time.UTC)
	secret, err := decodeSvixSecret(testResendWebhookSecret)
	if err != nil {
		t.Fatalf("decode test secret: %v", err)
	}
	calls := 0
	verifier := resendWebhookVerifier{
		enabled: true,
		secret:  secret,
		now: func() time.Time {
			calls++
			if calls == 1 {
				return signedAt
			}
			return signedAt.Add(-resendWebhookTolerance - time.Nanosecond)
		},
	}
	request := newResendWebhookRequest(body)
	request.Header.Set("svix-id", "msg_123")
	request.Header.Set("svix-timestamp", strconvFormatUnix(signedAt))
	request.Header.Set("svix-signature", signSvixTestPayload(t, "msg_123", signedAt, body, testResendWebhookSecret))

	if err := verifier.verify(request.Header, []byte(body)); err != nil {
		t.Fatalf("expected verifier to use one clock snapshot, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one clock read, got %d", calls)
	}
}

func TestResendWebhookRejectsInvalidIdentityBeforeLogQuery(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	messageLog := runtime.messageLog
	runtime.messageLog = nil
	defer func() {
		runtime.messageLog = messageLog
	}()

	for _, body := range []string{
		resendWebhookBody("ProjectA", "msg_01ABC", "email.delivered"),
		resendWebhookBody("project_a", "bad_01ABC", "email.delivered"),
	} {
		recorder := performResendWebhook(t, runtime, body, testResendWebhookSecret, time.Now())
		assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
	}
}

func TestResendWebhookAcceptsCommaSeparatedMultipleSignatures(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_resend_multi_sig")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_resend_multi_sig", domain.ProviderResend, "resend_main", "resend_auth_api")

	body := resendWebhookBody("project_a", "msg_resend_multi_sig", "email.delivered")
	signedAt := time.Now()
	validSignature := signSvixTestPayload(t, "msg_123", signedAt, body, testResendWebhookSecret)
	request := newResendWebhookRequest(body)
	request.Header.Set("svix-id", "msg_123")
	request.Header.Set("svix-timestamp", strconvFormatUnix(signedAt))
	request.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString([]byte("bad signature"))+","+validSignature)
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestResendWebhookAppendsDeliveredEvent(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_resend")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_resend", domain.ProviderResend, "resend_main", "resend_auth_api")

	recorder := performResendWebhook(t, runtime, resendWebhookBody("project_a", "msg_resend", "email.delivered"), testResendWebhookSecret, time.Now())
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	statusRecorder := performMessageStatus(t, runtime, "msg_resend", testAPIKey)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("expected status query 200, got %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status messageStatusResponse
	decodeJSON(t, statusRecorder.Body.String(), &status)
	if status.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected delivered status, got %+v", status)
	}
}

func TestResendWebhookBounceAddsSuppression(t *testing.T) {
	runtime, cfg := openResendWebhookRuntime(t)
	defer runtime.Close()

	message := testStatusMessage("msg_resend_bounce")
	message.Status = domain.MessageStatusSent
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	appendSentAttempt(t, runtime, "msg_resend_bounce", domain.ProviderResend, "resend_main", "resend_auth_api")

	recorder := performResendWebhook(t, runtime, resendWebhookBodyWithRecipient("project_a", "msg_resend_bounce", "email.bounced", "user@example.com"), testResendWebhookSecret, time.Now())
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	reloaded, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	if _, ok := reloaded.Contains("project_a", domain.NormalizeEmail("user@example.com")); !ok {
		t.Fatal("expected resend bounce to persist suppression")
	}

	blocked := performSend(t, runtime, testSendBody(), "idem-after-resend-bounce")
	assertErrorResponse(t, blocked, http.StatusUnprocessableEntity, domain.ErrorCodeSuppressedRecipient)
}

func TestResendWebhookRejectsInvalidSuppressionRecipient(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	recorder := performResendWebhook(t, runtime, resendWebhookBodyWithRecipient("project_a", "msg_resend_invalid_recipient", "email.bounced", "not an email"), testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestResendWebhookRequiresMuxMailTags(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	body := `{"type":"email.delivered","data":{"email_id":"provider_123","tags":{"app":"project_a"}},"created_at":"2026-05-28T03:00:00Z"}`
	recorder := performResendWebhook(t, runtime, body, testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestResendWebhookRequiresProviderIdentityTags(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	body := `{"type":"email.delivered","data":{"email_id":"provider_123","tags":{"app":"project_a","message_id":"msg_missing_identity","provider_account":"resend_main"}},"created_at":"2026-05-28T03:00:00Z"}`
	recorder := performResendWebhook(t, runtime, body, testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestResendWebhookRequiresCreatedAt(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	body := `{"type":"email.delivered","data":{"email_id":"provider_123","tags":{"app":"project_a","message_id":"msg_missing_created_at","provider_account":"resend_main","provider_channel":"resend_auth_api"}}}`
	recorder := performResendWebhook(t, runtime, body, testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func TestResendWebhookRejectsInvalidCreatedAt(t *testing.T) {
	runtime, _ := openResendWebhookRuntime(t)
	defer runtime.Close()

	body := strings.Replace(resendWebhookBody("project_a", "msg_invalid_created_at", "email.delivered"), "2026-05-28T03:00:00Z", "2026-05-28 03:00:00", 1)
	recorder := performResendWebhook(t, runtime, body, testResendWebhookSecret, time.Now())
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidJSON)
}

func openResendWebhookRuntime(t *testing.T) (*Runtime, *config.Config) {
	t.Helper()

	cfg := testRuntimeConfig(t, "off")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		ResendSecretRef: "plain:" + testResendWebhookSecret,
	}
	runtime, err := NewRuntime(cfg, config.NewSecretResolver(), WithNow(time.Now))
	if err != nil {
		t.Fatalf("open resend webhook runtime: %v", err)
	}

	return runtime, cfg
}

func performResendWebhook(t *testing.T, runtime *Runtime, body string, secret string, signedAt time.Time) *httptest.ResponseRecorder {
	t.Helper()

	signature := signSvixTestPayload(t, "msg_123", signedAt, body, secret)
	request := newResendWebhookRequest(body)
	request.Header.Set("svix-id", "msg_123")
	request.Header.Set("svix-timestamp", strconvFormatUnix(signedAt))
	request.Header.Set("svix-signature", signature)
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func performResendWebhookWithSignature(t *testing.T, runtime *Runtime, body string, signature string) *httptest.ResponseRecorder {
	t.Helper()

	signedAt := time.Now()
	request := newResendWebhookRequest(body)
	request.Header.Set("svix-id", "msg_123")
	request.Header.Set("svix-timestamp", strconvFormatUnix(signedAt))
	request.Header.Set("svix-signature", signature)
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}

func newResendWebhookRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/provider-events/resend", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	return request
}

func signSvixTestPayload(t *testing.T, id string, signedAt time.Time, body string, secret string) string {
	t.Helper()

	decodedSecret, err := decodeSvixSecret(secret)
	if err != nil {
		t.Fatalf("decode test secret: %v", err)
	}
	timestamp := strconvFormatUnix(signedAt)
	mac := hmac.New(sha256.New, decodedSecret)
	_, _ = mac.Write([]byte(id + "." + timestamp + "." + body))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func resendWebhookBody(app string, messageID string, eventType string) string {
	return resendWebhookBodyWithRecipient(app, messageID, eventType, "")
}

func resendWebhookBodyWithRecipient(app string, messageID string, eventType string, recipient string) string {
	toField := ``
	if recipient != "" {
		toField = `,"to":["` + recipient + `"]`
	}
	return `{"type":"` + eventType + `","data":{"email_id":"provider_123","tags":{"app":"` + app + `","message_id":"` + messageID + `","provider_account":"resend_main","provider_channel":"resend_auth_api"}` + toField + `},"created_at":"2026-05-28T03:00:00Z"}`
}

func strconvFormatUnix(value time.Time) string {
	return strconv.FormatInt(value.Unix(), 10)
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestBrevoAPIProviderAcceptedResponse(t *testing.T) {
	var captured brevoEmailPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/smtp/email" {
			t.Fatalf("unexpected request target: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("api-key") != "secret" {
			t.Fatalf("unexpected api-key header: %q", request.Header.Get("api-key"))
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"brevo_msg_123"}`))
	}))
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	if !result.IsAccepted() || result.Accepted.ProviderMessageID != "brevo_msg_123" {
		t.Fatalf("expected accepted response, got %+v", result)
	}
	if captured.Sender.Name != "MuxMail" || captured.Sender.Email != "no-reply@auth.example.com" {
		t.Fatalf("unexpected sender: %+v", captured.Sender)
	}
	if len(captured.To) != 1 || captured.To[0].Email != "user@example.com" {
		t.Fatalf("unexpected recipients: %+v", captured.To)
	}
	if captured.HTMLContent == "" || captured.TextContent == "" {
		t.Fatalf("expected html and text payload bodies, got %+v", captured)
	}
	assertBrevoTag(t, captured.Tags, "app:project_a")
	assertBrevoTag(t, captured.Tags, "message_id:msg_01ABC")
	assertBrevoTag(t, captured.Tags, "provider_account:brevo_main")
	assertBrevoTag(t, captured.Tags, "provider_channel:brevo_auth_api")
}

func TestBrevoAPIProviderTreats2xxWithoutMessageIDAsAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	if !result.IsAccepted() {
		t.Fatalf("expected accepted response, got %+v", result)
	}
	if result.Accepted.ProviderMessageID != "" {
		t.Fatalf("expected empty provider message id, got %q", result.Accepted.ProviderMessageID)
	}
}

func TestBrevoAPIProviderMaps429ToTemporaryFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusTooManyRequests, "too_many_requests", "slow down", "90")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
	if result.RetryAfterSeconds != 90 {
		t.Fatalf("expected retry-after 90, got %d", result.RetryAfterSeconds)
	}
}

func TestBrevoAPIProviderMapsHTTPDateRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 30, 0, 0, time.UTC)
	retryAfter := now.Add(2 * time.Second).Format(http.TimeFormat)
	server := brevoErrorServer(http.StatusTooManyRequests, "too_many_requests", "slow down", retryAfter)
	defer server.Close()

	provider := NewBrevoAPIProvider(
		StaticSecretResolver{"brevo_key": "secret"},
		WithBrevoBaseURL(server.URL),
		WithBrevoHTTPClient(server.Client()),
		WithBrevoNow(func() time.Time { return now }),
	)
	result, err := provider.Send(context.Background(), brevoTestRequest(brevoTestMessage()))
	if err != nil {
		t.Fatalf("brevo send returned error: %v", err)
	}

	assertBrevoFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
	if result.RetryAfterSeconds != 2 {
		t.Fatalf("expected retry-after 2, got %d", result.RetryAfterSeconds)
	}
}

func TestBrevoAPIProviderMaps5xxToTemporaryFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusBadGateway, "internal_error", "try later", "")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
}

func TestBrevoAPIProviderMapsNetworkTimeoutToTemporaryFailure(t *testing.T) {
	result, err := NewBrevoAPIProvider(
		StaticSecretResolver{"brevo_key": "secret"},
		WithBrevoBaseURL("https://brevo.test"),
		WithBrevoHTTPClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}).client()),
	).Send(context.Background(), brevoTestRequest(brevoTestMessage()))
	if err != nil {
		t.Fatalf("brevo send returned error: %v", err)
	}
	assertBrevoFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
}

func TestBrevoAPIProviderHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewBrevoAPIProvider(StaticSecretResolver{"brevo_key": "secret"}).Send(ctx, brevoTestRequest(brevoTestMessage()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestBrevoAPIProviderMapsAuthFailureToChannelFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusUnauthorized, "unauthorized", "invalid api key", "")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestBrevoAPIProviderRejectsInvalidAPIKeyValueBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"brevo_msg_123"}`))
	}))
	defer server.Close()

	result, err := NewBrevoAPIProvider(
		StaticSecretResolver{"brevo_key": "secret\n"},
		WithBrevoBaseURL(server.URL),
		WithBrevoHTTPClient(server.Client()),
	).Send(context.Background(), brevoTestRequest(brevoTestMessage()))
	if err != nil {
		t.Fatalf("brevo send returned error: %v", err)
	}
	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
	if requestCount != 0 {
		t.Fatalf("expected invalid api key to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestBrevoAPIProviderRejectsInvalidRecipientBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"brevo_msg_123"}`))
	}))
	defer server.Close()

	message := brevoTestMessage()
	message.ToEmail = "victim@example.com\r\nBcc: attacker@example.com"
	result := sendThroughBrevoTestServer(t, server, message, nil)

	assertBrevoFailure(t, result, domain.FailureClassMessagePermanent, domain.ErrorCodeInvalidRecipient)
	if requestCount != 0 {
		t.Fatalf("expected invalid recipient to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestBrevoAPIProviderRejectsInvalidFromNameBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"brevo_msg_123"}`))
	}))
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), func(request *SendRequest) {
		request.Channel.FromName = "MuxMail\r\nBcc: attacker@example.com"
	})

	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
	if requestCount != 0 {
		t.Fatalf("expected invalid from name to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestBrevoAPIProviderMapsDomainFailureToChannelFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusBadRequest, "invalid_parameter", "sender domain is not verified", "")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestBrevoAPIProviderMapsSenderFailureToChannelFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusBadRequest, "invalid_parameter", "invalid sender address", "")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestBrevoAPIProviderMapsInvalidRecipientToPermanentFailure(t *testing.T) {
	server := brevoErrorServer(http.StatusBadRequest, "invalid_parameter", "invalid recipient", "")
	defer server.Close()

	result := sendThroughBrevoTestServer(t, server, brevoTestMessage(), nil)
	assertBrevoFailure(t, result, domain.FailureClassMessagePermanent, domain.ErrorCodeInvalidRecipient)
}

func TestBrevoAPIProviderRejectsUnsupportedTransport(t *testing.T) {
	provider := NewBrevoAPIProvider(StaticSecretResolver{"brevo_key": "secret"})
	request := brevoTestRequest(brevoTestMessage())
	request.Channel.Transport = domain.TransportSMTP

	result, err := provider.Send(context.Background(), request)
	if err != nil {
		t.Fatalf("brevo send returned error: %v", err)
	}
	assertBrevoFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func sendThroughBrevoTestServer(t *testing.T, server *httptest.Server, message domain.Message, mutate func(*SendRequest)) SendResult {
	t.Helper()

	provider := NewBrevoAPIProvider(
		StaticSecretResolver{"brevo_key": "secret"},
		WithBrevoBaseURL(server.URL),
		WithBrevoHTTPClient(server.Client()),
	)
	request := brevoTestRequest(message)
	if mutate != nil {
		mutate(&request)
	}
	result, err := provider.Send(context.Background(), request)
	if err != nil {
		t.Fatalf("brevo send returned error: %v", err)
	}

	return result
}

func brevoTestRequest(message domain.Message) SendRequest {
	return SendRequest{
		Message: message,
		Account: domain.ProviderAccount{
			Code:     "brevo_main",
			Provider: domain.ProviderBrevo,
			Enabled:  true,
			CredentialRefs: map[string]string{
				"api_key": "brevo_key",
			},
		},
		Channel: domain.ProviderChannel{
			Code:      "brevo_auth_api",
			Account:   "brevo_main",
			Transport: domain.TransportAPI,
			Enabled:   true,
			FromName:  "MuxMail",
			From:      "no-reply@auth.example.com",
		},
	}
}

func brevoTestMessage() domain.Message {
	return domain.Message{
		AppCode:   "project_a",
		MessageID: "msg_01ABC",
		ToEmail:   "user@example.com",
		Subject:   "Your verification code",
		HTMLBody:  "<p>Your code is 123456.</p>",
		TextBody:  "Your code is 123456.",
	}
}

func assertBrevoTag(t *testing.T, tags []string, want string) {
	t.Helper()

	for _, tag := range tags {
		if tag == want {
			return
		}
	}

	t.Fatalf("expected tag %q in %+v", want, tags)
}

func brevoErrorServer(status int, code string, message string, retryAfter string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.TrimSpace(request.Header.Get("api-key")) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    code,
			"message": message,
		})
	}))
}

func assertBrevoFailure(t *testing.T, result SendResult, failureClass domain.FailureClass, errorCode domain.ErrorCode) {
	t.Helper()

	if !result.IsFailed() {
		t.Fatalf("expected failed result, got %+v", result)
	}
	if result.Failed.FailureClass != failureClass {
		t.Fatalf("expected failure class %s, got %+v", failureClass, result.Failed)
	}
	if result.Failed.ErrorCode != errorCode {
		t.Fatalf("expected error code %s, got %+v", errorCode, result.Failed)
	}
}

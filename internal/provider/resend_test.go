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

func TestResendAPIProviderAcceptedResponse(t *testing.T) {
	var captured resendEmailPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/emails" {
			t.Fatalf("unexpected request target: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected authorization header: %q", request.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resend_msg_123"}`))
	}))
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	if !result.IsAccepted() || result.Accepted.ProviderMessageID != "resend_msg_123" {
		t.Fatalf("expected accepted response, got %+v", result)
	}
	if captured.From != `"MuxMail" <no-reply@auth.example.com>` {
		t.Fatalf("unexpected from: %q", captured.From)
	}
	if len(captured.To) != 1 || captured.To[0] != "user@example.com" {
		t.Fatalf("unexpected recipients: %+v", captured.To)
	}
	if captured.HTML == "" || captured.Text == "" {
		t.Fatalf("expected html and text payload bodies, got %+v", captured)
	}
	assertResendTag(t, captured.Tags, "app", "project_a")
	assertResendTag(t, captured.Tags, "message_id", "msg_01ABC")
	assertResendTag(t, captured.Tags, "provider_account", "resend_main")
	assertResendTag(t, captured.Tags, "provider_channel", "resend_auth_api")
}

func TestResendAPIProviderTreats2xxWithoutMessageIDAsAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	if !result.IsAccepted() {
		t.Fatalf("expected accepted response, got %+v", result)
	}
	if result.Accepted.ProviderMessageID != "" {
		t.Fatalf("expected empty provider message id, got %q", result.Accepted.ProviderMessageID)
	}
}

func TestResendAPIProviderMaps429ToTemporaryFailure(t *testing.T) {
	server := resendErrorServer(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", "120")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
	if result.RetryAfterSeconds != 120 {
		t.Fatalf("expected retry-after 120, got %d", result.RetryAfterSeconds)
	}
}

func TestResendAPIProviderMapsHTTPDateRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 30, 0, 0, time.UTC)
	retryAfter := now.Add(2 * time.Second).Format(http.TimeFormat)
	server := resendErrorServer(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", retryAfter)
	defer server.Close()

	provider := NewResendAPIProvider(
		StaticSecretResolver{"resend_key": "secret"},
		WithResendBaseURL(server.URL),
		WithResendHTTPClient(server.Client()),
		WithResendNow(func() time.Time { return now }),
	)
	result, err := provider.Send(context.Background(), resendTestRequest(resendTestMessage()))
	if err != nil {
		t.Fatalf("resend send returned error: %v", err)
	}

	assertResendFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
	if result.RetryAfterSeconds != 2 {
		t.Fatalf("expected retry-after 2, got %d", result.RetryAfterSeconds)
	}
}

func TestResendAPIProviderMaps5xxToTemporaryFailure(t *testing.T) {
	server := resendErrorServer(http.StatusBadGateway, "internal_server_error", "try later", "")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
}

func TestResendAPIProviderMapsNetworkTimeoutToTemporaryFailure(t *testing.T) {
	result, err := NewResendAPIProvider(
		StaticSecretResolver{"resend_key": "secret"},
		WithResendBaseURL("https://resend.test"),
		WithResendHTTPClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}).client()),
	).Send(context.Background(), resendTestRequest(resendTestMessage()))
	if err != nil {
		t.Fatalf("resend send returned error: %v", err)
	}
	assertResendFailure(t, result, domain.FailureClassTemporary, domain.ErrorCodeProviderUnavailable)
}

func TestResendAPIProviderHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewResendAPIProvider(StaticSecretResolver{"resend_key": "secret"}).Send(ctx, resendTestRequest(resendTestMessage()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestResendAPIProviderMapsAuthFailureToChannelFailure(t *testing.T) {
	server := resendErrorServer(http.StatusUnauthorized, "invalid_api_key", "invalid key", "")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestResendAPIProviderRejectsInvalidAPIKeyValueBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resend_msg_123"}`))
	}))
	defer server.Close()

	result, err := NewResendAPIProvider(
		StaticSecretResolver{"resend_key": "secret\n"},
		WithResendBaseURL(server.URL),
		WithResendHTTPClient(server.Client()),
	).Send(context.Background(), resendTestRequest(resendTestMessage()))
	if err != nil {
		t.Fatalf("resend send returned error: %v", err)
	}
	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
	if requestCount != 0 {
		t.Fatalf("expected invalid api key to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestResendAPIProviderRejectsInvalidRecipientBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resend_msg_123"}`))
	}))
	defer server.Close()

	message := resendTestMessage()
	message.ToEmail = "victim@example.com\r\nBcc: attacker@example.com"
	result := sendThroughResendTestServer(t, server, message, nil)

	assertResendFailure(t, result, domain.FailureClassMessagePermanent, domain.ErrorCodeInvalidRecipient)
	if requestCount != 0 {
		t.Fatalf("expected invalid recipient to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestResendAPIProviderRejectsInvalidFromNameBeforeRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resend_msg_123"}`))
	}))
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), func(request *SendRequest) {
		request.Channel.FromName = "MuxMail\r\nBcc: attacker@example.com"
	})

	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
	if requestCount != 0 {
		t.Fatalf("expected invalid from name to stop before HTTP request, got %d requests", requestCount)
	}
}

func TestResendAPIProviderMapsDomainFailureToChannelFailure(t *testing.T) {
	server := resendErrorServer(http.StatusForbidden, "validation_error", "domain is not verified", "")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestResendAPIProviderMapsSenderFailureToChannelFailure(t *testing.T) {
	server := resendErrorServer(http.StatusBadRequest, "validation_error", "from address is not allowed", "")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func TestResendAPIProviderMapsInvalidRecipientToPermanentFailure(t *testing.T) {
	server := resendErrorServer(http.StatusUnprocessableEntity, "validation_error", "invalid recipient", "")
	defer server.Close()

	result := sendThroughResendTestServer(t, server, resendTestMessage(), nil)
	assertResendFailure(t, result, domain.FailureClassMessagePermanent, domain.ErrorCodeInvalidRecipient)
}

func TestResendAPIProviderRejectsUnsupportedTransport(t *testing.T) {
	provider := NewResendAPIProvider(StaticSecretResolver{"resend_key": "secret"})
	request := resendTestRequest(resendTestMessage())
	request.Channel.Transport = domain.TransportSMTP

	result, err := provider.Send(context.Background(), request)
	if err != nil {
		t.Fatalf("resend send returned error: %v", err)
	}
	assertResendFailure(t, result, domain.FailureClassChannel, domain.ErrorCodeProviderUnavailable)
}

func sendThroughResendTestServer(t *testing.T, server *httptest.Server, message domain.Message, mutate func(*SendRequest)) SendResult {
	t.Helper()

	provider := NewResendAPIProvider(
		StaticSecretResolver{"resend_key": "secret"},
		WithResendBaseURL(server.URL),
		WithResendHTTPClient(server.Client()),
	)
	request := resendTestRequest(message)
	if mutate != nil {
		mutate(&request)
	}
	result, err := provider.Send(context.Background(), request)
	if err != nil {
		t.Fatalf("resend send returned error: %v", err)
	}

	return result
}

func resendTestRequest(message domain.Message) SendRequest {
	return SendRequest{
		Message: message,
		Account: domain.ProviderAccount{
			Code:     "resend_main",
			Provider: domain.ProviderResend,
			Enabled:  true,
			CredentialRefs: map[string]string{
				"api_key": "resend_key",
			},
		},
		Channel: domain.ProviderChannel{
			Code:      "resend_auth_api",
			Account:   "resend_main",
			Transport: domain.TransportAPI,
			Enabled:   true,
			FromName:  "MuxMail",
			From:      "no-reply@auth.example.com",
		},
	}
}

func resendTestMessage() domain.Message {
	return domain.Message{
		AppCode:   "project_a",
		MessageID: "msg_01ABC",
		ToEmail:   "user@example.com",
		Subject:   "Your verification code",
		HTMLBody:  "<p>Your code is 123456.</p>",
		TextBody:  "Your code is 123456.",
	}
}

func assertResendTag(t *testing.T, tags []resendTag, name string, value string) {
	t.Helper()

	for _, tag := range tags {
		if tag.Name == name {
			if tag.Value != value {
				t.Fatalf("expected tag %s=%q, got %q", name, value, tag.Value)
			}
			return
		}
	}

	t.Fatalf("expected tag %s in %+v", name, tags)
}

func resendErrorServer(status int, name string, message string, retryAfter string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":       name,
			"message":    message,
			"statusCode": status,
		})
	}))
}

func assertResendFailure(t *testing.T, result SendResult, failureClass domain.FailureClass, errorCode domain.ErrorCode) {
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

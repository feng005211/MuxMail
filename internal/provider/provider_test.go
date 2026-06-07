package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestAcceptedResultMapping(t *testing.T) {
	result := Accepted("provider_123")

	if !result.IsAccepted() || result.IsFailed() {
		t.Fatalf("expected accepted result, got %+v", result)
	}
	if result.Accepted.ProviderMessageID != "provider_123" {
		t.Fatalf("unexpected provider message id: %q", result.Accepted.ProviderMessageID)
	}
}

func TestFailureResultMapping(t *testing.T) {
	tests := []struct {
		name         string
		result       SendResult
		failureClass domain.FailureClass
	}{
		{
			name:         "temporary",
			result:       TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
			failureClass: domain.FailureClassTemporary,
		},
		{
			name:         "channel",
			result:       ChannelFailure(domain.ErrorCodeProviderUnavailable, "channel failed"),
			failureClass: domain.FailureClassChannel,
		},
		{
			name:         "message permanent",
			result:       MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "invalid recipient"),
			failureClass: domain.FailureClassMessagePermanent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.result.IsFailed() || tt.result.IsAccepted() {
				t.Fatalf("expected failed result, got %+v", tt.result)
			}
			if tt.result.Failed.FailureClass != tt.failureClass {
				t.Fatalf("expected failure class %s, got %s", tt.failureClass, tt.result.Failed.FailureClass)
			}
		})
	}
}

func TestResultWithRetryAfter(t *testing.T) {
	result := WithRetryAfter(TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"), 120)

	if result.RetryAfterSeconds != 120 {
		t.Fatalf("expected retry_after 120, got %d", result.RetryAfterSeconds)
	}
}

func TestFakeProviderReturnsScriptedResults(t *testing.T) {
	fake := NewFakeProvider(
		TemporaryFailure(domain.ErrorCodeProviderUnavailable, "temporary provider failure"),
		ChannelFailure(domain.ErrorCodeProviderUnavailable, "channel failed"),
		MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "invalid recipient"),
		Accepted("provider_123"),
	)

	results := make([]SendResult, 0, 4)
	for attempt := 1; attempt <= 4; attempt++ {
		result, err := fake.Send(context.Background(), SendRequest{
			Message: testProviderMessage(),
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
			Attempt: attempt,
		})
		if err != nil {
			t.Fatalf("send attempt %d: %v", attempt, err)
		}
		results = append(results, result)
	}

	if results[0].Failed.FailureClass != domain.FailureClassTemporary {
		t.Fatalf("expected temporary failure, got %+v", results[0])
	}
	if results[1].Failed.FailureClass != domain.FailureClassChannel {
		t.Fatalf("expected channel failure, got %+v", results[1])
	}
	if results[2].Failed.FailureClass != domain.FailureClassMessagePermanent {
		t.Fatalf("expected permanent failure, got %+v", results[2])
	}
	if results[3].Accepted.ProviderMessageID != "provider_123" {
		t.Fatalf("expected accepted result, got %+v", results[3])
	}

	requests := fake.Requests()
	if len(requests) != 4 {
		t.Fatalf("expected 4 recorded requests, got %d", len(requests))
	}
	if requests[3].Attempt != 4 || requests[3].Message.MessageID != "msg_01ABC" {
		t.Fatalf("unexpected recorded request: %+v", requests[3])
	}
}

func TestFakeProviderHonorsCanceledContext(t *testing.T) {
	fake := NewFakeProvider(Accepted("provider_123"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fake.Send(ctx, SendRequest{Message: testProviderMessage()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if len(fake.Requests()) != 0 {
		t.Fatalf("expected canceled request not to be recorded")
	}
}

func testProviderMessage() domain.Message {
	return domain.Message{
		MessageID:         "msg_01ABC",
		AppCode:           "project_a",
		SceneCode:         "register_code",
		ToEmail:           "user@example.com",
		NormalizedToEmail: "user@example.com",
		Subject:           "Your code",
		TextBody:          "Your code is 123456.",
	}
}

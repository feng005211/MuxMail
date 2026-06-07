package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestMockProviderSendSuccess(t *testing.T) {
	mock := NewMockProvider()

	result, err := mock.Send(context.Background(), SendRequest{
		Message: domain.Message{MessageID: "msg_01ABC"},
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
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("mock send: %v", err)
	}
	if !result.IsAccepted() {
		t.Fatalf("expected accepted result, got %+v", result)
	}
	if result.Accepted.ProviderMessageID != "mock_msg_01ABC" {
		t.Fatalf("unexpected provider message id: %s", result.Accepted.ProviderMessageID)
	}
}

func TestMockProviderRejectsUnsupportedTransport(t *testing.T) {
	mock := NewMockProvider()

	result, err := mock.Send(context.Background(), SendRequest{
		Message: domain.Message{MessageID: "msg_01ABC"},
		Channel: domain.ProviderChannel{
			Code:      "mock_auth_smtp",
			Account:   "mock_main",
			Transport: domain.TransportSMTP,
			Enabled:   true,
		},
	})
	if err != nil {
		t.Fatalf("mock send: %v", err)
	}
	if !result.IsFailed() || result.Failed.FailureClass != domain.FailureClassChannel {
		t.Fatalf("expected channel failure, got %+v", result)
	}
}

func TestMockProviderHonorsCanceledContext(t *testing.T) {
	mock := NewMockProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mock.Send(ctx, SendRequest{
		Message: domain.Message{MessageID: "msg_01ABC"},
		Channel: domain.ProviderChannel{
			Transport: domain.TransportAPI,
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

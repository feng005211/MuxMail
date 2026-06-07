package provider

import (
	"context"
	"fmt"

	"github.com/muxmail/muxmail/internal/domain"
)

// MockProvider is the network-free mock API provider for local use and tests.
type MockProvider struct{}

// NewMockProvider creates the mock API provider.
func NewMockProvider() MockProvider {
	return MockProvider{}
}

// Send accepts every valid mock API request without network access.
func (p MockProvider) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	if request.Channel.Transport != domain.TransportAPI {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "mock provider only supports api transport"), nil
	}
	if request.Message.MessageID == "" {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "message id is required"), nil
	}

	return Accepted(fmt.Sprintf("mock_%s", request.Message.MessageID)), nil
}

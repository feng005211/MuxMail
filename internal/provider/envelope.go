package provider

import (
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
)

type providerEnvelope struct {
	from string
	to   string
}

func normalizeProviderEnvelope(request SendRequest) (providerEnvelope, SendResult, bool) {
	to := strings.TrimSpace(request.Message.ToEmail)
	if !domain.IsAddrSpecEmail(to) {
		return providerEnvelope{}, MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "recipient invalid"), true
	}

	from := strings.TrimSpace(request.Channel.From)
	if !domain.IsAddrSpecEmail(from) {
		return providerEnvelope{}, ChannelFailure(domain.ErrorCodeProviderUnavailable, "provider sender invalid"), true
	}
	if !domain.IsSafeEmailDisplayName(request.Channel.FromName) {
		return providerEnvelope{}, ChannelFailure(domain.ErrorCodeProviderUnavailable, "provider sender name invalid"), true
	}

	return providerEnvelope{from: from, to: to}, SendResult{}, false
}

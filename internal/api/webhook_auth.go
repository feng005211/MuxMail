package api

import (
	"fmt"
	"strings"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

type webhookAuthenticator struct {
	enabled    bool
	secretHash string
}

func newWebhookAuthenticator(webhooks config.WebhookConfig, resolver config.SecretResolver) (webhookAuthenticator, error) {
	if !webhooks.Enabled || strings.TrimSpace(webhooks.SharedSecretRef) == "" {
		return webhookAuthenticator{}, nil
	}
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}

	resolved, err := resolver.Resolve(webhooks.SharedSecretRef)
	if err != nil {
		return webhookAuthenticator{}, fmt.Errorf("resolve webhook shared secret: %w", err)
	}

	return webhookAuthenticator{
		enabled:    true,
		secretHash: domain.APIKeyHash(resolved.Value),
	}, nil
}

func (a webhookAuthenticator) authenticate(header string) error {
	if !a.enabled {
		return AuthError{Code: domain.ErrorCodeWebhookDisabled, Message: "webhook receiver disabled"}
	}
	secret, ok := parseBearerToken(header)
	if !ok {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}
	if !domain.ConstantTimeEqualHex(domain.APIKeyHash(secret), a.secretHash) {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}

	return nil
}

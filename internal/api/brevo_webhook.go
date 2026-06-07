package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

type brevoWebhookVerifier struct {
	enabled   bool
	tokenHash string
}

type brevoWebhookPayload struct {
	Event     string   `json:"event"`
	MessageID string   `json:"message-id"`
	Date      string   `json:"date"`
	Email     string   `json:"email"`
	Tag       []string `json:"tag"`
	Tags      []string `json:"tags"`
}

func newBrevoWebhookVerifier(webhooks config.WebhookConfig, resolver config.SecretResolver) (brevoWebhookVerifier, error) {
	if !webhooks.Enabled || strings.TrimSpace(webhooks.BrevoTokenRef) == "" {
		return brevoWebhookVerifier{}, nil
	}
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}

	resolved, err := resolver.Resolve(webhooks.BrevoTokenRef)
	if err != nil {
		return brevoWebhookVerifier{}, fmt.Errorf("resolve brevo webhook token: %w", err)
	}

	return brevoWebhookVerifier{
		enabled:   true,
		tokenHash: domain.APIKeyHash(resolved.Value),
	}, nil
}

func (r *Runtime) handleBrevoProviderEvent(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodPost {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processBrevoProviderEvent(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processBrevoProviderEvent(httpRequest *http.Request) (providerEventResponse, error) {
	if !r.brevoHook.enabled {
		return providerEventResponse{}, AuthError{Code: domain.ErrorCodeWebhookDisabled, Message: "brevo webhook receiver disabled"}
	}
	if err := r.brevoHook.authenticate(httpRequest.Header.Get("Authorization")); err != nil {
		return providerEventResponse{}, err
	}
	if err := validateSendContentType(httpRequest.Header.Get("Content-Type")); err != nil {
		return providerEventResponse{}, err
	}

	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, int64(r.defaults.MaxRequestBodyBytes)+1))
	if err != nil || len(body) > r.defaults.MaxRequestBodyBytes {
		return providerEventResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeRequestTooLarge, Message: "request body is too large"}
	}
	event, err := decodeBrevoWebhookEvent(body)
	if err != nil {
		return providerEventResponse{}, err
	}

	return r.applyProviderEvent(event)
}

func (v brevoWebhookVerifier) authenticate(header string) error {
	if !v.enabled {
		return AuthError{Code: domain.ErrorCodeWebhookDisabled, Message: "brevo webhook receiver disabled"}
	}
	token, ok := parseBearerToken(header)
	if !ok {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}
	if !domain.ConstantTimeEqualHex(domain.APIKeyHash(token), v.tokenHash) {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}

	return nil
}

func decodeBrevoWebhookEvent(body []byte) (domain.ProviderEvent, error) {
	var payload brevoWebhookPayload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&payload); err != nil {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must be a JSON object"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	} else if err != io.EOF {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	}

	eventType, ok := mapBrevoEventType(payload.Event)
	if !ok {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "event is invalid"}
	}
	tags := payload.Tag
	if len(tags) == 0 {
		tags = payload.Tags
	}

	values := parseBrevoMetadataTags(tags)
	event := domain.ProviderEvent{
		MessageID:           values["message_id"],
		AppCode:             values["app"],
		Provider:            domain.ProviderBrevo,
		ProviderAccountCode: values["provider_account"],
		ProviderChannelCode: values["provider_channel"],
		ProviderMessageID:   strings.TrimSpace(payload.MessageID),
		RecipientEmail:      strings.TrimSpace(payload.Email),
		EventType:           eventType,
		EventPayload:        `{"source":"brevo"}`,
		OccurredAt:          strings.TrimSpace(payload.Date),
	}
	if event.AppCode == "" || event.MessageID == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app and message_id tags are required"}
	}
	if requiresSuppression(event.EventType) && domain.NormalizeEmail(event.RecipientEmail) == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "email is required for bounce and complaint events"}
	}

	return event, nil
}

func parseBrevoMetadataTags(tags []string) map[string]string {
	values := make(map[string]string, len(tags))
	for _, tag := range tags {
		name, value, ok := strings.Cut(strings.TrimSpace(tag), ":")
		if !ok || name == "" || value == "" {
			continue
		}
		if _, exists := values[name]; exists {
			continue
		}
		values[name] = value
	}

	return values
}

func mapBrevoEventType(value string) (domain.ProviderEventType, bool) {
	switch strings.TrimSpace(value) {
	case "delivered":
		return domain.ProviderEventDelivered, true
	case "hardBounce", "hard_bounce", "invalid_email":
		return domain.ProviderEventBounced, true
	case "spam":
		return domain.ProviderEventComplained, true
	default:
		return "", false
	}
}

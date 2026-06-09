package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

const resendWebhookTolerance = 5 * time.Minute

type resendWebhookVerifier struct {
	enabled bool
	secret  []byte
	now     func() time.Time
}

type resendWebhookPayload struct {
	Type string `json:"type"`
	Data struct {
		EmailID string   `json:"email_id"`
		To      []string `json:"to"`
		Tags    struct {
			App             string `json:"app"`
			MessageID       string `json:"message_id"`
			ProviderAccount string `json:"provider_account"`
			ProviderChannel string `json:"provider_channel"`
		} `json:"tags"`
	} `json:"data"`
	CreatedAt string `json:"created_at"`
}

func newResendWebhookVerifier(webhooks config.WebhookConfig, resolver config.SecretResolver) (resendWebhookVerifier, error) {
	verifier := resendWebhookVerifier{now: time.Now}
	if !webhooks.Enabled || strings.TrimSpace(webhooks.ResendSecretRef) == "" {
		return verifier, nil
	}
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}
	resolved, err := resolver.Resolve(webhooks.ResendSecretRef)
	if err != nil {
		return resendWebhookVerifier{}, fmt.Errorf("resolve resend webhook secret: %w", err)
	}
	secret, err := decodeSvixSecret(resolved.Value)
	if err != nil {
		return resendWebhookVerifier{}, err
	}

	return resendWebhookVerifier{
		enabled: true,
		secret:  secret,
		now:     time.Now,
	}, nil
}

func (r *Runtime) handleResendProviderEvent(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodPost {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processResendProviderEvent(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processResendProviderEvent(httpRequest *http.Request) (providerEventResponse, error) {
	if !r.resendHook.enabled {
		return providerEventResponse{}, AuthError{Code: domain.ErrorCodeWebhookDisabled, Message: "resend webhook receiver disabled"}
	}
	if err := validateSendContentType(httpRequest.Header.Get("Content-Type")); err != nil {
		return providerEventResponse{}, err
	}

	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, int64(r.defaults.MaxRequestBodyBytes)+1))
	if err != nil || len(body) > r.defaults.MaxRequestBodyBytes {
		return providerEventResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeRequestTooLarge, Message: "request body is too large"}
	}
	if err := r.resendHook.verify(httpRequest.Header, body); err != nil {
		return providerEventResponse{}, err
	}
	event, err := decodeResendWebhookEvent(body)
	if err != nil {
		return providerEventResponse{}, err
	}

	return r.applyProviderEvent(event)
}

func (v resendWebhookVerifier) verify(header http.Header, body []byte) error {
	id := strings.TrimSpace(header.Get("svix-id"))
	timestamp := strings.TrimSpace(header.Get("svix-timestamp"))
	signature := strings.TrimSpace(header.Get("svix-signature"))
	if id == "" || timestamp == "" || signature == "" {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}

	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}
	signedAt := time.Unix(seconds, 0)
	now := time.Now
	if v.now != nil {
		now = v.now
	}
	verifiedAt := now()
	if signedAt.Before(verifiedAt.Add(-resendWebhookTolerance)) || signedAt.After(verifiedAt.Add(resendWebhookTolerance)) {
		return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}

	signedPayload := id + "." + timestamp + "." + string(body)
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(signedPayload))
	expected := mac.Sum(nil)
	for _, encoded := range svixV1SignatureValues(signature) {
		actual, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		if hmac.Equal(actual, expected) {
			return nil
		}
	}

	return AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
}

func svixV1SignatureValues(header string) []string {
	tokens := strings.Fields(strings.ReplaceAll(header, ",", " "))
	values := make([]string, 0, len(tokens)/2)
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index] == "v1" {
			values = append(values, tokens[index+1])
		}
	}

	return values
}

func decodeSvixSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "whsec_")
	secret, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		secret, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, fmt.Errorf("decode resend webhook secret: %w", err)
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("decode resend webhook secret: empty secret")
	}

	return secret, nil
}

func decodeResendWebhookEvent(body []byte) (domain.ProviderEvent, error) {
	var payload resendWebhookPayload
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

	eventType, ok := mapResendEventType(payload.Type)
	if !ok {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "event_type is invalid"}
	}
	event := domain.ProviderEvent{
		MessageID:           strings.TrimSpace(payload.Data.Tags.MessageID),
		AppCode:             strings.TrimSpace(payload.Data.Tags.App),
		Provider:            domain.ProviderResend,
		ProviderAccountCode: strings.TrimSpace(payload.Data.Tags.ProviderAccount),
		ProviderChannelCode: strings.TrimSpace(payload.Data.Tags.ProviderChannel),
		ProviderMessageID:   strings.TrimSpace(payload.Data.EmailID),
		RecipientEmail:      firstNonEmptyString(payload.Data.To),
		EventType:           eventType,
		EventPayload:        `{"source":"resend"}`,
		OccurredAt:          strings.TrimSpace(payload.CreatedAt),
	}
	if event.AppCode == "" || event.MessageID == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app and message_id tags are required"}
	}
	if !isValidIdentifierFilter(event.AppCode) {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app is invalid"}
	}
	if !isValidMessageIDValue(event.MessageID) {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "message_id is invalid"}
	}
	if err := validateProviderEventIdentity(&event); err != nil {
		return domain.ProviderEvent{}, err
	}
	if err := validateProviderEventRecipientEmail(event); err != nil {
		return domain.ProviderEvent{}, err
	}

	return event, nil
}

func mapResendEventType(value string) (domain.ProviderEventType, bool) {
	switch value {
	case "email.delivered":
		return domain.ProviderEventDelivered, true
	case "email.bounced":
		return domain.ProviderEventBounced, true
	case "email.complained":
		return domain.ProviderEventComplained, true
	default:
		return "", false
	}
}

func firstNonEmptyString(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}

	return ""
}

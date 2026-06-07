package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

const defaultResendBaseURL = "https://api.resend.com"
const defaultResendHTTPTimeout = 10 * time.Second

// ResendAPIProvider sends mail through the Resend Email API.
type ResendAPIProvider struct {
	secrets SecretResolver
	client  *http.Client
	baseURL string
}

// ResendAPIOption customizes the Resend API provider.
type ResendAPIOption func(*ResendAPIProvider)

// NewResendAPIProvider creates a Resend API provider.
func NewResendAPIProvider(secrets SecretResolver, options ...ResendAPIOption) *ResendAPIProvider {
	provider := &ResendAPIProvider{
		secrets: secrets,
		client:  &http.Client{Timeout: defaultResendHTTPTimeout},
		baseURL: defaultResendBaseURL,
	}
	for _, option := range options {
		option(provider)
	}

	return provider
}

// WithResendHTTPClient overrides the HTTP client used by the Resend API provider.
func WithResendHTTPClient(client *http.Client) ResendAPIOption {
	return func(provider *ResendAPIProvider) {
		if client != nil {
			provider.client = client
		}
	}
}

// WithResendBaseURL overrides the Resend API base URL for tests.
func WithResendBaseURL(baseURL string) ResendAPIOption {
	return func(provider *ResendAPIProvider) {
		if strings.TrimSpace(baseURL) != "" {
			provider.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

// Send sends one message through the Resend Email API.
func (p *ResendAPIProvider) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	if request.Account.Provider != domain.ProviderResend || request.Channel.Transport != domain.TransportAPI {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend api transport required"), nil
	}

	apiKey, err := p.apiKey(request)
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend api key unavailable"), nil
	}
	payload, err := buildResendPayload(request)
	if err != nil {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "resend message build failed"), nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "resend message encode failed"), nil
	}

	endpoint, err := p.emailEndpoint()
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend endpoint invalid"), nil
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend request build failed"), nil
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if request.Message.MessageID != "" {
		httpRequest.Header.Set("Idempotency-Key", request.Message.MessageID)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "resend request failed"), nil
	}
	defer response.Body.Close()

	return decodeResendResponse(response), nil
}

func (p *ResendAPIProvider) apiKey(request SendRequest) (string, error) {
	ref := request.Account.CredentialRefs["api_key"]
	if ref == "" || p.secrets == nil {
		return "", fmt.Errorf("resend api key reference is required")
	}

	return p.secrets.ResolveSecret(ref)
}

func (p *ResendAPIProvider) emailEndpoint() (string, error) {
	base, err := url.Parse(p.baseURL)
	if err != nil {
		return "", err
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("base URL must include scheme and host")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/emails"
	base.RawQuery = ""
	base.Fragment = ""

	return base.String(), nil
}

type resendEmailPayload struct {
	From    string      `json:"from"`
	To      []string    `json:"to"`
	Subject string      `json:"subject"`
	HTML    string      `json:"html,omitempty"`
	Text    string      `json:"text,omitempty"`
	Tags    []resendTag `json:"tags,omitempty"`
}

type resendTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func buildResendPayload(request SendRequest) (resendEmailPayload, error) {
	if request.Message.HTMLBody == "" && request.Message.TextBody == "" {
		return resendEmailPayload{}, fmt.Errorf("message body is required")
	}
	if request.Channel.From == "" || request.Message.ToEmail == "" || request.Message.Subject == "" {
		return resendEmailPayload{}, fmt.Errorf("message address and subject are required")
	}

	return resendEmailPayload{
		From: (&mail.Address{
			Name:    request.Channel.FromName,
			Address: request.Channel.From,
		}).String(),
		To:      []string{strings.TrimSpace(request.Message.ToEmail)},
		Subject: request.Message.Subject,
		HTML:    request.Message.HTMLBody,
		Text:    request.Message.TextBody,
		Tags: []resendTag{
			{Name: "app", Value: request.Message.AppCode},
			{Name: "message_id", Value: request.Message.MessageID},
			{Name: "provider_account", Value: request.Account.Code},
			{Name: "provider_channel", Value: request.Channel.Code},
		},
	}, nil
}

type resendAcceptedResponse struct {
	ID string `json:"id"`
}

type resendErrorResponse struct {
	Name       string `json:"name"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
}

func decodeResendResponse(response *http.Response) SendResult {
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		var accepted resendAcceptedResponse
		if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil || accepted.ID == "" {
			return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "resend accepted response invalid")
		}

		return Accepted(accepted.ID)
	}

	errorResponse := readResendError(response)
	result := classifyResendHTTPFailure(response.StatusCode, errorResponse)
	if retryAfter := parseRetryAfterSeconds(response.Header.Get("Retry-After")); retryAfter > 0 {
		result = WithRetryAfter(result, retryAfter)
	}

	return result
}

func readResendError(response *http.Response) resendErrorResponse {
	var errorResponse resendErrorResponse
	_ = json.NewDecoder(response.Body).Decode(&errorResponse)
	return errorResponse
}

func classifyResendHTTPFailure(statusCode int, errorResponse resendErrorResponse) SendResult {
	errorName := strings.ToLower(errorResponse.Name)
	switch {
	case statusCode == http.StatusTooManyRequests || statusCode >= 500:
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "resend temporary failure")
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend channel failure")
	case errorName == "invalid_api_key" || errorName == "missing_api_key" || errorName == "restricted_api_key":
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend channel failure")
	case errorName == "invalid_from_address" || errorName == "invalid_access":
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend channel failure")
	case containsDomainVerificationHint(errorResponse.Message):
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "resend channel failure")
	case statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity:
		return MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "resend message rejected")
	default:
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "resend request failed")
	}
}

func containsDomainVerificationHint(message string) bool {
	normalized := strings.ToLower(message)
	return strings.Contains(normalized, "domain") && strings.Contains(normalized, "verif")
}

func parseRetryAfterSeconds(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return seconds
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		seconds := int(time.Until(retryAt).Seconds())
		if seconds > 0 {
			return seconds
		}
	}

	return 0
}

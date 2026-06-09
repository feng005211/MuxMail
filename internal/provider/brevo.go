package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

const defaultBrevoBaseURL = "https://api.brevo.com/v3"
const defaultBrevoHTTPTimeout = 10 * time.Second

// BrevoAPIProvider sends mail through the Brevo transactional email API.
type BrevoAPIProvider struct {
	secrets SecretResolver
	client  *http.Client
	baseURL string
	now     func() time.Time
}

// BrevoAPIOption customizes the Brevo API provider.
type BrevoAPIOption func(*BrevoAPIProvider)

// NewBrevoAPIProvider creates a Brevo API provider.
func NewBrevoAPIProvider(secrets SecretResolver, options ...BrevoAPIOption) *BrevoAPIProvider {
	provider := &BrevoAPIProvider{
		secrets: secrets,
		client:  &http.Client{Timeout: defaultBrevoHTTPTimeout},
		baseURL: defaultBrevoBaseURL,
		now:     time.Now,
	}
	for _, option := range options {
		option(provider)
	}

	return provider
}

// WithBrevoHTTPClient overrides the HTTP client used by the Brevo API provider.
func WithBrevoHTTPClient(client *http.Client) BrevoAPIOption {
	return func(provider *BrevoAPIProvider) {
		if client != nil {
			provider.client = client
		}
	}
}

// WithBrevoBaseURL overrides the Brevo API base URL for tests.
func WithBrevoBaseURL(baseURL string) BrevoAPIOption {
	return func(provider *BrevoAPIProvider) {
		if strings.TrimSpace(baseURL) != "" {
			provider.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

// WithBrevoNow overrides the clock used to interpret HTTP-date Retry-After headers.
func WithBrevoNow(now func() time.Time) BrevoAPIOption {
	return func(provider *BrevoAPIProvider) {
		if now != nil {
			provider.now = now
		}
	}
}

// Send sends one message through the Brevo transactional email API.
func (p *BrevoAPIProvider) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	if request.Account.Provider != domain.ProviderBrevo || request.Channel.Transport != domain.TransportAPI {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo api transport required"), nil
	}
	envelope, result, failed := normalizeProviderEnvelope(request)
	if failed {
		return result, nil
	}

	apiKey, err := p.apiKey(request)
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo api key unavailable"), nil
	}
	if !isVisibleASCIIWithoutWhitespaceSecret(apiKey) {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo api key invalid"), nil
	}
	payload, err := buildBrevoPayload(request, envelope)
	if err != nil {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "brevo message build failed"), nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "brevo message encode failed"), nil
	}

	endpoint, err := p.emailEndpoint()
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo endpoint invalid"), nil
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo request build failed"), nil
	}
	httpRequest.Header.Set("api-key", apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "brevo request failed"), nil
	}
	defer response.Body.Close()

	return decodeBrevoResponse(response, p.now), nil
}

func (p *BrevoAPIProvider) apiKey(request SendRequest) (string, error) {
	ref := request.Account.CredentialRefs["api_key"]
	if ref == "" || p.secrets == nil {
		return "", fmt.Errorf("brevo api key reference is required")
	}

	return p.secrets.ResolveSecret(ref)
}

func (p *BrevoAPIProvider) emailEndpoint() (string, error) {
	base, err := url.Parse(p.baseURL)
	if err != nil {
		return "", err
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("base URL must include scheme and host")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/smtp/email"
	base.RawQuery = ""
	base.Fragment = ""

	return base.String(), nil
}

type brevoEmailPayload struct {
	Sender      brevoAddress   `json:"sender"`
	To          []brevoAddress `json:"to"`
	Subject     string         `json:"subject"`
	HTMLContent string         `json:"htmlContent,omitempty"`
	TextContent string         `json:"textContent,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
}

type brevoAddress struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

func buildBrevoPayload(request SendRequest, envelope providerEnvelope) (brevoEmailPayload, error) {
	if request.Message.HTMLBody == "" && request.Message.TextBody == "" {
		return brevoEmailPayload{}, fmt.Errorf("message body is required")
	}
	if request.Message.Subject == "" {
		return brevoEmailPayload{}, fmt.Errorf("message subject is required")
	}

	return brevoEmailPayload{
		Sender: brevoAddress{
			Name:  request.Channel.FromName,
			Email: envelope.from,
		},
		To: []brevoAddress{
			{Email: envelope.to},
		},
		Subject:     request.Message.Subject,
		HTMLContent: request.Message.HTMLBody,
		TextContent: request.Message.TextBody,
		Tags: []string{
			"app:" + request.Message.AppCode,
			"message_id:" + request.Message.MessageID,
			"provider_account:" + request.Account.Code,
			"provider_channel:" + request.Channel.Code,
		},
	}, nil
}

type brevoAcceptedResponse struct {
	MessageID  string   `json:"messageId"`
	MessageIDs []string `json:"messageIds"`
}

type brevoErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeBrevoResponse(response *http.Response, now func() time.Time) SendResult {
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		var accepted brevoAcceptedResponse
		if err := json.NewDecoder(response.Body).Decode(&accepted); err == nil {
			if accepted.MessageID != "" {
				return Accepted(accepted.MessageID)
			}
			if len(accepted.MessageIDs) > 0 && accepted.MessageIDs[0] != "" {
				return Accepted(accepted.MessageIDs[0])
			}
		}

		// A 2xx response means Brevo accepted the request; retrying here can duplicate delivery.
		return Accepted("")
	}

	errorResponse := readBrevoError(response)
	result := classifyBrevoHTTPFailure(response.StatusCode, errorResponse)
	if retryAfter := parseRetryAfterSeconds(response.Header.Get("Retry-After"), now); retryAfter > 0 {
		result = WithRetryAfter(result, retryAfter)
	}

	return result
}

func readBrevoError(response *http.Response) brevoErrorResponse {
	var errorResponse brevoErrorResponse
	_ = json.NewDecoder(response.Body).Decode(&errorResponse)
	return errorResponse
}

func classifyBrevoHTTPFailure(statusCode int, errorResponse brevoErrorResponse) SendResult {
	errorCode := strings.ToLower(errorResponse.Code)
	message := strings.ToLower(errorResponse.Message)
	switch {
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500:
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "brevo temporary failure")
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden || statusCode == http.StatusPaymentRequired:
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo channel failure")
	case strings.Contains(errorCode, "unauthorized") || strings.Contains(errorCode, "permission"):
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo channel failure")
	case containsSenderConfigurationHint(errorCode) || containsSenderConfigurationHint(errorResponse.Message):
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo channel failure")
	case containsBrevoDomainFailure(message):
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "brevo channel failure")
	case statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity:
		return MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "brevo message rejected")
	default:
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "brevo request failed")
	}
}

func containsBrevoDomainFailure(message string) bool {
	return (strings.Contains(message, "domain") || strings.Contains(message, "sender")) &&
		(strings.Contains(message, "verif") || strings.Contains(message, "unauthor") || strings.Contains(message, "not allowed"))
}

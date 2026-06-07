package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSendRequestSuccess(t *testing.T) {
	req, err := ValidateSendRequest(validSendRequestInput(), SendRequestValidationOptions{})
	if err != nil {
		t.Fatalf("expected request to validate: %v", err)
	}

	if req.Scene != "register_code" {
		t.Fatalf("expected scene register_code, got %q", req.Scene)
	}
	if req.To != " User@Example.COM " {
		t.Fatalf("expected original recipient to be preserved, got %q", req.To)
	}
	if req.NormalizedToEmail != "user@example.com" {
		t.Fatalf("expected normalized recipient, got %q", req.NormalizedToEmail)
	}
	if req.Locale != "en-US" {
		t.Fatalf("expected locale en-US, got %q", req.Locale)
	}
	if req.UserIP != "1.2.3.4" || req.UserID != "10001" || req.BusinessRequestID != "abc123" {
		t.Fatalf("expected context fields to be extracted, got %+v", req)
	}
	if req.IdempotencyKey != "business_request_id" {
		t.Fatalf("expected idempotency key, got %q", req.IdempotencyKey)
	}
}

func TestValidateSendRequestFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SendRequestValidationInput, *SendRequestValidationOptions)
		code   ErrorCode
	}{
		{
			name: "missing content type",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.ContentType = ""
			},
			code: ErrorCodeUnsupportedMediaType,
		},
		{
			name: "unsupported content type",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.ContentType = "text/plain"
			},
			code: ErrorCodeUnsupportedMediaType,
		},
		{
			name: "body too large",
			mutate: func(input *SendRequestValidationInput, options *SendRequestValidationOptions) {
				options.MaxRequestBodyBytes = 2
			},
			code: ErrorCodeRequestTooLarge,
		},
		{
			name: "invalid json",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(`{"scene":`)
			},
			code: ErrorCodeInvalidJSON,
		},
		{
			name: "json array",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(`[]`)
			},
			code: ErrorCodeInvalidJSON,
		},
		{
			name: "multiple json values",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(validSendRequestBody() + `{}`)
			},
			code: ErrorCodeInvalidJSON,
		},
		{
			name: "missing idempotency key",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.IdempotencyKey = ""
			},
			code: ErrorCodeMissingIdempotencyKey,
		},
		{
			name: "invalid idempotency key whitespace",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.IdempotencyKey = "bad key"
			},
			code: ErrorCodeInvalidIdempotencyKey,
		},
		{
			name: "missing scene",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(`{"to":"user@example.com","vars":{}}`)
			},
			code: ErrorCodeInvalidJSON,
		},
		{
			name: "invalid recipient display name",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `" User@Example.COM "`, `"User <user@example.com>"`, 1))
			},
			code: ErrorCodeInvalidRecipient,
		},
		{
			name: "invalid recipient non ascii",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `" User@Example.COM "`, `"usér@example.com"`, 1))
			},
			code: ErrorCodeInvalidRecipient,
		},
		{
			name: "invalid locale casing",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"en-US"`, `"zh-cn"`, 1))
			},
			code: ErrorCodeInvalidLocale,
		},
		{
			name: "locale not allowed",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"en-US"`, `"fr-FR"`, 1))
			},
			code: ErrorCodeInvalidLocale,
		},
		{
			name: "vars too large",
			mutate: func(input *SendRequestValidationInput, options *SendRequestValidationOptions) {
				options.MaxTemplateVarBytes = 2
			},
			code: ErrorCodeRequestTooLarge,
		},
		{
			name: "vars nested",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"code":"123456"`, `"code":{"nested":true}`, 1))
			},
			code: ErrorCodeInvalidTemplateVars,
		},
		{
			name: "vars field name dot",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"code":"123456"`, `"code.value":"123456"`, 1))
			},
			code: ErrorCodeInvalidTemplateVars,
		},
		{
			name: "vars null",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"code":"123456"`, `"code":null`, 1))
			},
			code: ErrorCodeInvalidTemplateVars,
		},
		{
			name: "context too large",
			mutate: func(input *SendRequestValidationInput, options *SendRequestValidationOptions) {
				options.MaxContextBytes = 2
			},
			code: ErrorCodeInvalidContext,
		},
		{
			name: "context nested",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"user_id":"10001"`, `"user_id":{"nested":true}`, 1))
			},
			code: ErrorCodeInvalidContext,
		},
		{
			name: "context request id whitespace",
			mutate: func(input *SendRequestValidationInput, _ *SendRequestValidationOptions) {
				input.Body = []byte(strings.Replace(validSendRequestBody(), `"abc123"`, `"abc 123"`, 1))
			},
			code: ErrorCodeInvalidContext,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validSendRequestInput()
			options := SendRequestValidationOptions{}
			tt.mutate(&input, &options)

			_, err := ValidateSendRequest(input, options)
			assertRequestValidationCode(t, err, tt.code)
		})
	}
}

func TestValidateSendRequestAllowsCharsetContentType(t *testing.T) {
	input := validSendRequestInput()
	input.ContentType = "application/json; charset=utf-8"

	if _, err := ValidateSendRequest(input, SendRequestValidationOptions{}); err != nil {
		t.Fatalf("expected charset content type to validate: %v", err)
	}
}

func TestValidateSendRequestAllowsMissingLocaleAndContext(t *testing.T) {
	input := validSendRequestInput()
	input.Body = []byte(`{"scene":"register_code","to":"user@example.com","vars":{"code":"123456"}}`)

	req, err := ValidateSendRequest(input, SendRequestValidationOptions{})
	if err != nil {
		t.Fatalf("expected request to validate: %v", err)
	}
	if req.Locale != "" {
		t.Fatalf("expected empty locale for later default resolution, got %q", req.Locale)
	}
	if req.Context == nil || len(req.Context) != 0 {
		t.Fatalf("expected empty context map, got %+v", req.Context)
	}
}

func validSendRequestInput() SendRequestValidationInput {
	return SendRequestValidationInput{
		ContentType:    "application/json",
		IdempotencyKey: "business_request_id",
		AllowedLocales: []string{"en-US", "zh-CN"},
		Body:           []byte(validSendRequestBody()),
	}
}

func validSendRequestBody() string {
	return `{"scene":"register_code","to":" User@Example.COM ","locale":"en-US","vars":{"code":"123456","expire_minutes":10,"urgent":true},"context":{"user_ip":"1.2.3.4","user_id":"10001","request_id":"abc123","attempt":1}}`
}

func assertRequestValidationCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()

	var validationError RequestValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("expected request validation error, got %v", err)
	}
	if validationError.Code != code {
		t.Fatalf("expected error code %s, got %s", code, validationError.Code)
	}
}

package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxRequestBodyBytes = 65536
	defaultMaxTemplateVarBytes = 8192
	defaultMaxContextBytes     = 4096
	maxRecipientBytes          = 254
	maxRecipientLocalBytes     = 64
	maxIdempotencyKeyBytes     = 128
	maxContextFields           = 16
	maxContextFieldNameBytes   = 64
	maxContextStringBytes      = 256
	maxBusinessRequestIDBytes  = 128
	maxTemplateVars            = 32
	maxTemplateVarNameBytes    = 64
	maxTemplateVarStringBytes  = 1024
)

// SendRequestValidationOptions defines request validation size limits.
type SendRequestValidationOptions struct {
	MaxRequestBodyBytes int
	MaxTemplateVarBytes int
	MaxContextBytes     int
}

// SendRequestValidationInput contains the raw request data needed for pure validation.
type SendRequestValidationInput struct {
	ContentType    string
	IdempotencyKey string
	Body           []byte
	AllowedLocales []string
}

// SendRequest is the parsed and normalized send request payload.
type SendRequest struct {
	Scene             string
	To                string
	NormalizedToEmail string
	Locale            string
	Vars              map[string]any
	Context           map[string]any
	UserIP            string
	UserID            string
	BusinessRequestID string
	IdempotencyKey    string
}

// RequestValidationError is a stable validation failure for a send request.
type RequestValidationError struct {
	Code    ErrorCode
	Message string
}

// Error returns the stable validation code as an error string.
func (e RequestValidationError) Error() string {
	return string(e.Code)
}

// ValidateSendRequest validates the raw /v1/mail/send request without side effects.
func ValidateSendRequest(input SendRequestValidationInput, options SendRequestValidationOptions) (SendRequest, error) {
	options = applySendRequestValidationDefaults(options)
	if err := validateContentType(input.ContentType); err != nil {
		return SendRequest{}, err
	}
	if len(input.Body) > options.MaxRequestBodyBytes {
		return SendRequest{}, requestValidationError(ErrorCodeRequestTooLarge, "request body is too large")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(input.Body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return SendRequest{}, requestValidationError(ErrorCodeInvalidJSON, "request body must be a JSON object")
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return SendRequest{}, err
	}
	if raw == nil {
		return SendRequest{}, requestValidationError(ErrorCodeInvalidJSON, "request body must be a JSON object")
	}

	idempotencyKey, err := validateIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return SendRequest{}, err
	}

	scene, err := decodeStringField(raw, "scene", ErrorCodeInvalidJSON)
	if err != nil {
		return SendRequest{}, err
	}
	to, normalizedToEmail, err := validateRecipientField(raw)
	if err != nil {
		return SendRequest{}, err
	}
	locale, err := validateLocaleField(raw, input.AllowedLocales)
	if err != nil {
		return SendRequest{}, err
	}
	vars, err := validateVarsField(raw["vars"], options.MaxTemplateVarBytes)
	if err != nil {
		return SendRequest{}, err
	}
	context, err := validateContextField(raw["context"], options.MaxContextBytes)
	if err != nil {
		return SendRequest{}, err
	}

	return SendRequest{
		Scene:             scene,
		To:                to,
		NormalizedToEmail: normalizedToEmail,
		Locale:            locale,
		Vars:              vars,
		Context:           context,
		UserIP:            stringValue(context["user_ip"]),
		UserID:            stringValue(context["user_id"]),
		BusinessRequestID: stringValue(context["request_id"]),
		IdempotencyKey:    idempotencyKey,
	}, nil
}

func applySendRequestValidationDefaults(options SendRequestValidationOptions) SendRequestValidationOptions {
	if options.MaxRequestBodyBytes <= 0 {
		options.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}
	if options.MaxTemplateVarBytes <= 0 {
		options.MaxTemplateVarBytes = defaultMaxTemplateVarBytes
	}
	if options.MaxContextBytes <= 0 {
		options.MaxContextBytes = defaultMaxContextBytes
	}

	return options
}

func validateContentType(contentType string) error {
	if strings.TrimSpace(contentType) == "" {
		return requestValidationError(ErrorCodeUnsupportedMediaType, "content type must be application/json")
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return requestValidationError(ErrorCodeUnsupportedMediaType, "content type must be application/json")
	}

	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return requestValidationError(ErrorCodeInvalidJSON, "request body must contain a single JSON object")
	} else if err != io.EOF {
		return requestValidationError(ErrorCodeInvalidJSON, "request body must contain a single JSON object")
	}

	return nil
}

func validateIdempotencyKey(value string) (string, error) {
	if value == "" {
		return "", requestValidationError(ErrorCodeMissingIdempotencyKey, "idempotency key is required")
	}
	if len(value) > maxIdempotencyKeyBytes || !isVisibleASCIIWithoutWhitespace(value) {
		return "", requestValidationError(ErrorCodeInvalidIdempotencyKey, "idempotency key is invalid")
	}

	return value, nil
}

func validateRecipientField(raw map[string]json.RawMessage) (string, string, error) {
	to, err := decodeStringField(raw, "to", ErrorCodeInvalidRecipient)
	if err != nil {
		return "", "", err
	}

	trimmed := strings.TrimSpace(to)
	if trimmed == "" || len(trimmed) > maxRecipientBytes || !isASCII(trimmed) {
		return "", "", requestValidationError(ErrorCodeInvalidRecipient, "recipient is invalid")
	}
	if strings.ContainsAny(trimmed, "<>") || strings.Contains(trimmed, " ") {
		return "", "", requestValidationError(ErrorCodeInvalidRecipient, "recipient must be a single addr-spec")
	}
	if _, err := mail.ParseAddress(trimmed); err != nil {
		return "", "", requestValidationError(ErrorCodeInvalidRecipient, "recipient is invalid")
	}
	parts := strings.Split(trimmed, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(parts[0]) > maxRecipientLocalBytes {
		return "", "", requestValidationError(ErrorCodeInvalidRecipient, "recipient is invalid")
	}

	return to, NormalizeEmail(trimmed), nil
}

func validateLocaleField(raw map[string]json.RawMessage, allowedLocales []string) (string, error) {
	value, exists := raw["locale"]
	if !exists {
		return "", nil
	}

	var locale string
	if err := json.Unmarshal(value, &locale); err != nil {
		return "", requestValidationError(ErrorCodeInvalidLocale, "locale is invalid")
	}
	if !isRequestLocaleFormat(locale) {
		return "", requestValidationError(ErrorCodeInvalidLocale, "locale is invalid")
	}
	if len(allowedLocales) == 0 {
		return locale, nil
	}
	for _, allowed := range allowedLocales {
		if locale == allowed {
			return locale, nil
		}
	}

	return "", requestValidationError(ErrorCodeInvalidLocale, "locale is not allowed")
}

func validateVarsField(raw json.RawMessage, maxBytes int) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if len(raw) > maxBytes {
		return nil, requestValidationError(ErrorCodeRequestTooLarge, "template variables are too large")
	}

	var vars map[string]any
	if err := decodeObject(raw, &vars); err != nil {
		return nil, requestValidationError(ErrorCodeInvalidTemplateVars, "template variables must be a flat object")
	}
	if len(vars) > maxTemplateVars {
		return nil, requestValidationError(ErrorCodeInvalidTemplateVars, "template variables contain too many fields")
	}
	for name, value := range vars {
		if name == "" || len(name) > maxTemplateVarNameBytes || strings.Contains(name, ".") || containsWhitespace(name) {
			return nil, requestValidationError(ErrorCodeInvalidTemplateVars, "template variable name is invalid")
		}
		if err := validateFlatJSONValue(value, maxTemplateVarStringBytes); err != nil {
			return nil, requestValidationError(ErrorCodeInvalidTemplateVars, "template variable value is invalid")
		}
	}

	return vars, nil
}

func validateContextField(raw json.RawMessage, maxBytes int) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if len(raw) > maxBytes {
		return nil, requestValidationError(ErrorCodeInvalidContext, "context is too large")
	}

	var context map[string]any
	if err := decodeObject(raw, &context); err != nil {
		return nil, requestValidationError(ErrorCodeInvalidContext, "context must be a flat object")
	}
	if len(context) > maxContextFields {
		return nil, requestValidationError(ErrorCodeInvalidContext, "context contains too many fields")
	}
	for name, value := range context {
		if name == "" || len(name) > maxContextFieldNameBytes {
			return nil, requestValidationError(ErrorCodeInvalidContext, "context field name is invalid")
		}
		if err := validateFlatJSONValue(value, maxContextStringBytes); err != nil {
			return nil, requestValidationError(ErrorCodeInvalidContext, "context value is invalid")
		}
	}

	if requestID := stringValue(context["request_id"]); requestID != "" {
		if len(requestID) > maxBusinessRequestIDBytes || !isVisibleASCIIWithoutWhitespace(requestID) {
			return nil, requestValidationError(ErrorCodeInvalidContext, "context.request_id is invalid")
		}
	}

	return context, nil
}

func decodeStringField(raw map[string]json.RawMessage, field string, code ErrorCode) (string, error) {
	value, exists := raw[field]
	if !exists {
		return "", requestValidationError(code, fmt.Sprintf("%s is required", field))
	}

	var result string
	if err := json.Unmarshal(value, &result); err != nil {
		return "", requestValidationError(code, fmt.Sprintf("%s must be a string", field))
	}

	return result, nil
}

func decodeObject(raw json.RawMessage, target *map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if *target == nil {
		return fmt.Errorf("object is null")
	}

	return ensureSingleJSONValue(decoder)
}

func validateFlatJSONValue(value any, maxStringBytes int) error {
	switch typed := value.(type) {
	case string:
		if len(typed) > maxStringBytes {
			return fmt.Errorf("string value is too long")
		}
	case bool, json.Number:
		return nil
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}

	return nil
}

func requestValidationError(code ErrorCode, message string) RequestValidationError {
	return RequestValidationError{Code: code, Message: message}
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
	}

	return true
}

func isVisibleASCIIWithoutWhitespace(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7E || unicode.IsSpace(r) {
			return false
		}
	}

	return true
}

func containsWhitespace(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) {
			return true
		}
	}

	return false
}

func isRequestLocaleFormat(locale string) bool {
	if len(locale) != 5 {
		return false
	}

	return locale[0] >= 'a' && locale[0] <= 'z' &&
		locale[1] >= 'a' && locale[1] <= 'z' &&
		locale[2] == '-' &&
		locale[3] >= 'A' && locale[3] <= 'Z' &&
		locale[4] >= 'A' && locale[4] <= 'Z'
}

func stringValue(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}

	return ""
}

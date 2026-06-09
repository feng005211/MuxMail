package domain

// MessageStatus represents the lifecycle state of a mail message.
type MessageStatus string

const (
	// MessageStatusQueued means MuxMail accepted the request and queued the message.
	MessageStatusQueued MessageStatus = "queued"
	// MessageStatusSending means a worker is attempting delivery through a provider channel.
	MessageStatusSending MessageStatus = "sending"
	// MessageStatusRetrying means delivery will retry through the next eligible provider channel.
	MessageStatusRetrying MessageStatus = "retrying"
	// MessageStatusSent means the provider accepted the message.
	MessageStatusSent MessageStatus = "sent"
	// MessageStatusFailed means all attempts failed or the message hit a permanent failure.
	MessageStatusFailed MessageStatus = "failed"
	// MessageStatusDelivered means a provider webhook confirmed successful delivery.
	MessageStatusDelivered MessageStatus = "delivered"
	// MessageStatusBounced means a provider webhook reported a bounce.
	MessageStatusBounced MessageStatus = "bounced"
	// MessageStatusComplained means a provider webhook reported a complaint.
	MessageStatusComplained MessageStatus = "complained"
)

// IsValid reports whether s is one of the supported message statuses.
func (s MessageStatus) IsValid() bool {
	switch s {
	case MessageStatusQueued,
		MessageStatusSending,
		MessageStatusRetrying,
		MessageStatusSent,
		MessageStatusFailed,
		MessageStatusDelivered,
		MessageStatusBounced,
		MessageStatusComplained:
		return true
	default:
		return false
	}
}

// AttemptStatus represents the lifecycle state of one provider delivery attempt.
type AttemptStatus string

const (
	// AttemptStatusSending means a provider request is in progress.
	AttemptStatusSending AttemptStatus = "sending"
	// AttemptStatusSent means the provider accepted the attempt.
	AttemptStatusSent AttemptStatus = "sent"
	// AttemptStatusFailed means the attempt failed.
	AttemptStatusFailed AttemptStatus = "failed"
)

// IsValid reports whether s is one of the supported attempt statuses.
func (s AttemptStatus) IsValid() bool {
	switch s {
	case AttemptStatusSending, AttemptStatusSent, AttemptStatusFailed:
		return true
	default:
		return false
	}
}

// FailureClass describes how a failed provider attempt should affect retry behavior.
type FailureClass string

const (
	// FailureClassNone means the attempt succeeded or no failure classification applies.
	FailureClassNone FailureClass = ""
	// FailureClassTemporary means the message can move to another candidate channel.
	FailureClassTemporary FailureClass = "temporary_failure"
	// FailureClassChannel means the current provider channel is unusable for this message.
	FailureClassChannel FailureClass = "channel_failure"
	// FailureClassMessagePermanent means the message should fail without trying more channels.
	FailureClassMessagePermanent FailureClass = "message_permanent_failure"
)

// IsValid reports whether c is empty or one of the supported failure classes.
func (c FailureClass) IsValid() bool {
	switch c {
	case FailureClassNone,
		FailureClassTemporary,
		FailureClassChannel,
		FailureClassMessagePermanent:
		return true
	default:
		return false
	}
}

// ProviderEventType represents a normalized provider webhook event.
type ProviderEventType string

const (
	// ProviderEventDelivered means the provider confirmed delivery.
	ProviderEventDelivered ProviderEventType = "delivered"
	// ProviderEventBounced means the provider reported a bounce.
	ProviderEventBounced ProviderEventType = "bounced"
	// ProviderEventComplained means the provider reported a complaint.
	ProviderEventComplained ProviderEventType = "complained"
)

// IsValid reports whether e is one of the supported provider event types.
func (e ProviderEventType) IsValid() bool {
	switch e {
	case ProviderEventDelivered, ProviderEventBounced, ProviderEventComplained:
		return true
	default:
		return false
	}
}

// MessageStatus maps a normalized provider event to its message status.
func (e ProviderEventType) MessageStatus() MessageStatus {
	switch e {
	case ProviderEventDelivered:
		return MessageStatusDelivered
	case ProviderEventBounced:
		return MessageStatusBounced
	case ProviderEventComplained:
		return MessageStatusComplained
	default:
		return ""
	}
}

// Provider identifies a supported email service provider.
type Provider string

const (
	// ProviderMock is the local network-free provider used for development and tests.
	ProviderMock Provider = "mock"
	// ProviderResend identifies the Resend provider adapter.
	ProviderResend Provider = "resend"
	// ProviderBrevo identifies the Brevo provider adapter.
	ProviderBrevo Provider = "brevo"
)

// IsValid reports whether p is supported by the MVP.
func (p Provider) IsValid() bool {
	switch p {
	case ProviderMock, ProviderResend, ProviderBrevo:
		return true
	default:
		return false
	}
}

// Transport identifies how a provider channel sends email.
type Transport string

const (
	// TransportAPI sends mail through the provider HTTP API.
	TransportAPI Transport = "api"
	// TransportSMTP sends mail through the provider SMTP submission endpoint.
	TransportSMTP Transport = "smtp"
)

// IsValid reports whether t is supported by the MVP.
func (t Transport) IsValid() bool {
	switch t {
	case TransportAPI, TransportSMTP:
		return true
	default:
		return false
	}
}

// SupportsTransport reports whether provider supports transport in the MVP allowlist.
func SupportsTransport(provider Provider, transport Transport) bool {
	switch provider {
	case ProviderMock:
		return transport == TransportAPI
	case ProviderResend, ProviderBrevo:
		return transport == TransportAPI || transport == TransportSMTP
	default:
		return false
	}
}

const (
	// MinAPIKeyBytes is the minimum accepted byte length for an App API key.
	MinAPIKeyBytes = 24
	// MaxAPIKeyBytes is the maximum accepted byte length for an App API key.
	MaxAPIKeyBytes = 128
)

// IsValidAPIKeyValue reports whether value can be used as an App API key.
func IsValidAPIKeyValue(value string) bool {
	if len(value) < MinAPIKeyBytes || len(value) > MaxAPIKeyBytes {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7E {
			return false
		}
	}

	return true
}

// ErrorCode is a stable public API error code returned by MuxMail.
type ErrorCode string

const (
	// ErrorCodeUnauthorized means the API key is missing or invalid.
	ErrorCodeUnauthorized ErrorCode = "unauthorized"
	// ErrorCodeAppDisabled means the resolved App is disabled.
	ErrorCodeAppDisabled ErrorCode = "app_disabled"
	// ErrorCodeSceneNotFound means the requested Scene does not exist.
	ErrorCodeSceneNotFound ErrorCode = "scene_not_found"
	// ErrorCodeSceneDisabled means the requested Scene is disabled.
	ErrorCodeSceneDisabled ErrorCode = "scene_disabled"
	// ErrorCodeMessageNotFound means the requested message does not exist for the App.
	ErrorCodeMessageNotFound ErrorCode = "message_not_found"
	// ErrorCodeWebhookDisabled means the provider webhook receiver is not enabled.
	ErrorCodeWebhookDisabled ErrorCode = "webhook_disabled"
	// ErrorCodeMissingIdempotencyKey means the Idempotency-Key header is missing.
	ErrorCodeMissingIdempotencyKey ErrorCode = "missing_idempotency_key"
	// ErrorCodeInvalidIdempotencyKey means the Idempotency-Key header is malformed.
	ErrorCodeInvalidIdempotencyKey ErrorCode = "invalid_idempotency_key"
	// ErrorCodeRequestTooLarge means the request body or template variables are too large.
	ErrorCodeRequestTooLarge ErrorCode = "request_too_large"
	// ErrorCodeUnsupportedMediaType means Content-Type is not application/json.
	ErrorCodeUnsupportedMediaType ErrorCode = "unsupported_media_type"
	// ErrorCodeInvalidJSON means the request body is invalid JSON or not an object.
	ErrorCodeInvalidJSON ErrorCode = "invalid_json"
	// ErrorCodeInvalidQuery means one or more query parameters are invalid.
	ErrorCodeInvalidQuery ErrorCode = "invalid_query"
	// ErrorCodeInvalidRecipient means the recipient email address is invalid.
	ErrorCodeInvalidRecipient ErrorCode = "invalid_recipient"
	// ErrorCodeInvalidContext means the context object is invalid.
	ErrorCodeInvalidContext ErrorCode = "invalid_context"
	// ErrorCodeInvalidLocale means the requested locale is not allowed by the App.
	ErrorCodeInvalidLocale ErrorCode = "invalid_locale"
	// ErrorCodeInvalidTemplateVars means the template variables object is invalid.
	ErrorCodeInvalidTemplateVars ErrorCode = "invalid_template_vars"
	// ErrorCodeMissingTemplateVar means a required template variable is missing.
	ErrorCodeMissingTemplateVar ErrorCode = "missing_template_var"
	// ErrorCodeTemplateLocaleNotFound means no usable template exists for the resolved locale.
	ErrorCodeTemplateLocaleNotFound ErrorCode = "template_locale_not_found"
	// ErrorCodeTemplateRenderFailed means template rendering failed.
	ErrorCodeTemplateRenderFailed ErrorCode = "template_render_failed"
	// ErrorCodeRateLimited means a rate limit rule rejected the request.
	ErrorCodeRateLimited ErrorCode = "rate_limited"
	// ErrorCodeSuppressedRecipient means the recipient is on the suppression list.
	ErrorCodeSuppressedRecipient ErrorCode = "suppressed_recipient"
	// ErrorCodeIdempotencyConflict means the same idempotency key was reused with different content.
	ErrorCodeIdempotencyConflict ErrorCode = "idempotency_conflict"
	// ErrorCodeRouteNotFound means no provider route is available.
	ErrorCodeRouteNotFound ErrorCode = "route_not_found"
	// ErrorCodeProviderUnavailable means all eligible provider channels failed.
	ErrorCodeProviderUnavailable ErrorCode = "provider_unavailable"
	// ErrorCodeQueueFull means the in-memory queue has reached capacity.
	ErrorCodeQueueFull ErrorCode = "queue_full"
	// ErrorCodeInternal means MuxMail hit an uncategorized internal error.
	ErrorCodeInternal ErrorCode = "internal_error"
)

// IsValid reports whether c is one of the stable public API error codes.
func (c ErrorCode) IsValid() bool {
	switch c {
	case ErrorCodeUnauthorized,
		ErrorCodeAppDisabled,
		ErrorCodeSceneNotFound,
		ErrorCodeSceneDisabled,
		ErrorCodeMessageNotFound,
		ErrorCodeWebhookDisabled,
		ErrorCodeMissingIdempotencyKey,
		ErrorCodeInvalidIdempotencyKey,
		ErrorCodeRequestTooLarge,
		ErrorCodeUnsupportedMediaType,
		ErrorCodeInvalidJSON,
		ErrorCodeInvalidQuery,
		ErrorCodeInvalidRecipient,
		ErrorCodeInvalidContext,
		ErrorCodeInvalidLocale,
		ErrorCodeInvalidTemplateVars,
		ErrorCodeMissingTemplateVar,
		ErrorCodeTemplateLocaleNotFound,
		ErrorCodeTemplateRenderFailed,
		ErrorCodeRateLimited,
		ErrorCodeSuppressedRecipient,
		ErrorCodeIdempotencyConflict,
		ErrorCodeRouteNotFound,
		ErrorCodeProviderUnavailable,
		ErrorCodeQueueFull,
		ErrorCodeInternal:
		return true
	default:
		return false
	}
}

// SuppressionReason identifies why an address is suppressed.
type SuppressionReason string

const (
	// SuppressionReasonHardBounce means a provider reported a hard bounce.
	SuppressionReasonHardBounce SuppressionReason = "hard_bounce"
	// SuppressionReasonComplaint means a recipient complaint suppressed the address.
	SuppressionReasonComplaint SuppressionReason = "complaint"
	// SuppressionReasonManual means the address was manually suppressed.
	SuppressionReasonManual SuppressionReason = "manual"
)

// IsValid reports whether r is one of the supported suppression reasons.
func (r SuppressionReason) IsValid() bool {
	switch r {
	case SuppressionReasonHardBounce,
		SuppressionReasonComplaint,
		SuppressionReasonManual:
		return true
	default:
		return false
	}
}

// RoutePolicyWildcard is the fallback route key used when no recipient domain matches.
const RoutePolicyWildcard = "*"

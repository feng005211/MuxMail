package domain

import "testing"

func TestMessageStatusValidation(t *testing.T) {
	valid := []MessageStatus{
		MessageStatusQueued,
		MessageStatusSending,
		MessageStatusRetrying,
		MessageStatusSent,
		MessageStatusFailed,
		MessageStatusDelivered,
		MessageStatusBounced,
		MessageStatusComplained,
	}

	for _, status := range valid {
		if !status.IsValid() {
			t.Fatalf("expected message status %q to be valid", status)
		}
	}

	for _, status := range []MessageStatus{"", "unknown", "queued "} {
		if status.IsValid() {
			t.Fatalf("expected message status %q to be invalid", status)
		}
	}
}

func TestAttemptStatusValidation(t *testing.T) {
	valid := []AttemptStatus{
		AttemptStatusSending,
		AttemptStatusSent,
		AttemptStatusFailed,
	}

	for _, status := range valid {
		if !status.IsValid() {
			t.Fatalf("expected attempt status %q to be valid", status)
		}
	}

	for _, status := range []AttemptStatus{"", "unknown", "sent "} {
		if status.IsValid() {
			t.Fatalf("expected attempt status %q to be invalid", status)
		}
	}
}

func TestFailureClassValidation(t *testing.T) {
	valid := []FailureClass{
		FailureClassNone,
		FailureClassTemporary,
		FailureClassChannel,
		FailureClassMessagePermanent,
	}

	for _, failureClass := range valid {
		if !failureClass.IsValid() {
			t.Fatalf("expected failure class %q to be valid", failureClass)
		}
	}

	for _, failureClass := range []FailureClass{"unknown", "temporary_failure "} {
		if failureClass.IsValid() {
			t.Fatalf("expected failure class %q to be invalid", failureClass)
		}
	}
}

func TestProviderValidation(t *testing.T) {
	valid := []Provider{ProviderMock, ProviderResend, ProviderBrevo}

	for _, provider := range valid {
		if !provider.IsValid() {
			t.Fatalf("expected provider %q to be valid", provider)
		}
	}

	for _, provider := range []Provider{"", "mailgun", "resend "} {
		if provider.IsValid() {
			t.Fatalf("expected provider %q to be invalid", provider)
		}
	}
}

func TestTransportValidation(t *testing.T) {
	valid := []Transport{TransportAPI, TransportSMTP}

	for _, transport := range valid {
		if !transport.IsValid() {
			t.Fatalf("expected transport %q to be valid", transport)
		}
	}

	for _, transport := range []Transport{"", "http", "api "} {
		if transport.IsValid() {
			t.Fatalf("expected transport %q to be invalid", transport)
		}
	}
}

func TestSupportsTransport(t *testing.T) {
	tests := []struct {
		name      string
		provider  Provider
		transport Transport
		want      bool
	}{
		{name: "mock api", provider: ProviderMock, transport: TransportAPI, want: true},
		{name: "mock smtp", provider: ProviderMock, transport: TransportSMTP, want: false},
		{name: "resend api", provider: ProviderResend, transport: TransportAPI, want: true},
		{name: "resend smtp", provider: ProviderResend, transport: TransportSMTP, want: true},
		{name: "brevo api", provider: ProviderBrevo, transport: TransportAPI, want: true},
		{name: "brevo smtp", provider: ProviderBrevo, transport: TransportSMTP, want: true},
		{name: "unknown provider", provider: Provider("mailgun"), transport: TransportAPI, want: false},
		{name: "unknown transport", provider: ProviderResend, transport: Transport("http"), want: false},
	}

	for _, tt := range tests {
		got := SupportsTransport(tt.provider, tt.transport)
		if got != tt.want {
			t.Fatalf("%s: expected %t, got %t", tt.name, tt.want, got)
		}
	}
}

func TestErrorCodeValidation(t *testing.T) {
	valid := []ErrorCode{
		ErrorCodeUnauthorized,
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
		ErrorCodeInternal,
	}

	for _, code := range valid {
		if !code.IsValid() {
			t.Fatalf("expected error code %q to be valid", code)
		}
	}

	for _, code := range []ErrorCode{"", "bad_request", "internal_error "} {
		if code.IsValid() {
			t.Fatalf("expected error code %q to be invalid", code)
		}
	}
}

func TestSuppressionReasonValidation(t *testing.T) {
	valid := []SuppressionReason{
		SuppressionReasonHardBounce,
		SuppressionReasonComplaint,
		SuppressionReasonManual,
	}

	for _, reason := range valid {
		if !reason.IsValid() {
			t.Fatalf("expected suppression reason %q to be valid", reason)
		}
	}

	for _, reason := range []SuppressionReason{"", "unsubscribe", "manual "} {
		if reason.IsValid() {
			t.Fatalf("expected suppression reason %q to be invalid", reason)
		}
	}
}

func TestProviderEventTypeValidationAndStatusMapping(t *testing.T) {
	tests := []struct {
		event  ProviderEventType
		status MessageStatus
	}{
		{event: ProviderEventDelivered, status: MessageStatusDelivered},
		{event: ProviderEventBounced, status: MessageStatusBounced},
		{event: ProviderEventComplained, status: MessageStatusComplained},
	}

	for _, tt := range tests {
		if !tt.event.IsValid() {
			t.Fatalf("expected event %q to be valid", tt.event)
		}
		if got := tt.event.MessageStatus(); got != tt.status {
			t.Fatalf("expected event %q to map to %q, got %q", tt.event, tt.status, got)
		}
	}

	if ProviderEventType("opened").IsValid() {
		t.Fatal("expected opened event to be invalid")
	}
	if got := ProviderEventType("opened").MessageStatus(); got != "" {
		t.Fatalf("expected unknown event to map to empty status, got %q", got)
	}
}

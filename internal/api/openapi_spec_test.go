package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

type openAPISpec struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]any    `yaml:"paths"`
	Components openAPIComponents `yaml:"components"`
}

type openAPIComponents struct {
	Schemas map[string]openAPISchema `yaml:"schemas"`
}

type openAPISchema struct {
	Enum []string `yaml:"enum"`
}

func TestOpenAPISpecParsesAndCoversCurrentRoutes(t *testing.T) {
	spec := loadOpenAPISpec(t)

	if !strings.HasPrefix(spec.OpenAPI, "3.1.") {
		t.Fatalf("expected OpenAPI 3.1.x, got %q", spec.OpenAPI)
	}
	if spec.Info.Title != "MuxMail API" {
		t.Fatalf("unexpected spec title: %q", spec.Info.Title)
	}
	if spec.Info.Version == "" {
		t.Fatal("expected spec version")
	}

	requiredPaths := []string{
		"/healthz",
		"/readyz",
		"/v1/mail/send",
		"/v1/mail/messages",
		"/v1/mail/messages/failed",
		"/v1/mail/messages/{message_id}",
		"/v1/mail/messages/{message_id}/events",
		"/v1/mail/messages/{message_id}/attempts",
		"/v1/suppressions",
		"/v1/provider-events",
		"/v1/provider-events/resend",
		"/v1/provider-events/brevo",
		"/v1/stats/summary",
	}
	for _, path := range requiredPaths {
		if _, exists := spec.Paths[path]; !exists {
			t.Fatalf("expected spec path %q", path)
		}
	}
}

func TestOpenAPIEnumsMatchDomainConstants(t *testing.T) {
	spec := loadOpenAPISpec(t)

	assertOpenAPIEnum(t, spec, "MessageStatus", []string{
		string(domain.MessageStatusQueued),
		string(domain.MessageStatusSending),
		string(domain.MessageStatusRetrying),
		string(domain.MessageStatusSent),
		string(domain.MessageStatusFailed),
		string(domain.MessageStatusDelivered),
		string(domain.MessageStatusBounced),
		string(domain.MessageStatusComplained),
	})
	assertOpenAPIEnum(t, spec, "AttemptStatus", []string{
		string(domain.AttemptStatusSending),
		string(domain.AttemptStatusSent),
		string(domain.AttemptStatusFailed),
	})
	assertOpenAPIEnum(t, spec, "FailureClass", []string{
		string(domain.FailureClassNone),
		string(domain.FailureClassTemporary),
		string(domain.FailureClassChannel),
		string(domain.FailureClassMessagePermanent),
	})
	assertOpenAPIEnum(t, spec, "Provider", []string{
		string(domain.ProviderMock),
		string(domain.ProviderResend),
		string(domain.ProviderBrevo),
	})
	assertOpenAPIEnum(t, spec, "Transport", []string{
		string(domain.TransportAPI),
		string(domain.TransportSMTP),
	})
	assertOpenAPIEnum(t, spec, "ProviderEventType", []string{
		string(domain.ProviderEventDelivered),
		string(domain.ProviderEventBounced),
		string(domain.ProviderEventComplained),
	})
	assertOpenAPIEnum(t, spec, "SuppressionReason", []string{
		string(domain.SuppressionReasonHardBounce),
		string(domain.SuppressionReasonComplaint),
		string(domain.SuppressionReasonManual),
	})
	assertOpenAPIEnum(t, spec, "ErrorCode", []string{
		string(domain.ErrorCodeUnauthorized),
		string(domain.ErrorCodeAppDisabled),
		string(domain.ErrorCodeSceneNotFound),
		string(domain.ErrorCodeSceneDisabled),
		string(domain.ErrorCodeMessageNotFound),
		string(domain.ErrorCodeWebhookDisabled),
		string(domain.ErrorCodeMissingIdempotencyKey),
		string(domain.ErrorCodeInvalidIdempotencyKey),
		string(domain.ErrorCodeRequestTooLarge),
		string(domain.ErrorCodeUnsupportedMediaType),
		string(domain.ErrorCodeInvalidJSON),
		string(domain.ErrorCodeInvalidQuery),
		string(domain.ErrorCodeInvalidRecipient),
		string(domain.ErrorCodeInvalidContext),
		string(domain.ErrorCodeInvalidLocale),
		string(domain.ErrorCodeInvalidTemplateVars),
		string(domain.ErrorCodeMissingTemplateVar),
		string(domain.ErrorCodeTemplateLocaleNotFound),
		string(domain.ErrorCodeTemplateRenderFailed),
		string(domain.ErrorCodeRateLimited),
		string(domain.ErrorCodeSuppressedRecipient),
		string(domain.ErrorCodeIdempotencyConflict),
		string(domain.ErrorCodeRouteNotFound),
		string(domain.ErrorCodeProviderUnavailable),
		string(domain.ErrorCodeQueueFull),
		string(domain.ErrorCodeInternal),
	})
}

func loadOpenAPISpec(t *testing.T) openAPISpec {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("expected runtime caller to resolve current file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	specPath := filepath.Join(repoRoot, "docs", "openapi.yaml")

	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}

	var spec openAPISpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}

	return spec
}

func assertOpenAPIEnum(t *testing.T, spec openAPISpec, schemaName string, want []string) {
	t.Helper()

	schema, exists := spec.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("expected OpenAPI schema %q", schemaName)
	}
	if len(schema.Enum) != len(want) {
		t.Fatalf("expected %s enum %v, got %v", schemaName, want, schema.Enum)
	}
	for index, value := range want {
		if schema.Enum[index] != value {
			t.Fatalf("expected %s enum %v, got %v", schemaName, want, schema.Enum)
		}
	}
}

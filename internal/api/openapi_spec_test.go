package api

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/muxmail/muxmail"
	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

type openAPISpec struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]openAPIPathItem `yaml:"paths"`
	Components openAPIComponents          `yaml:"components"`
}

type openAPIPathItem struct {
	Get  openAPIOperation `yaml:"get"`
	Post openAPIOperation `yaml:"post"`
}

type openAPIOperation struct {
	Parameters []openAPIParameter `yaml:"parameters"`
}

type openAPIParameter struct {
	In     string        `yaml:"in"`
	Name   string        `yaml:"name"`
	Schema openAPISchema `yaml:"schema"`
}

type openAPIComponents struct {
	Schemas map[string]openAPISchema `yaml:"schemas"`
}

type openAPISchema struct {
	Required   []string                 `yaml:"required"`
	Properties map[string]openAPISchema `yaml:"properties"`
	Enum       []string                 `yaml:"enum"`
	Type       string                   `yaml:"type"`
	Format     string                   `yaml:"format"`
	MaxLength  int                      `yaml:"maxLength"`
	Pattern    string                   `yaml:"pattern"`
}

func TestOpenAPISpecParsesAndCoversCurrentRoutes(t *testing.T) {
	spec := loadOpenAPISpec(t)

	if !strings.HasPrefix(spec.OpenAPI, "3.1.") {
		t.Fatalf("expected OpenAPI 3.1.x, got %q", spec.OpenAPI)
	}
	if spec.Info.Title != "MuxMail API" {
		t.Fatalf("unexpected spec title: %q", spec.Info.Title)
	}
	if spec.Info.Version != muxmail.Version() {
		t.Fatalf("expected spec version %q, got %q", muxmail.Version(), spec.Info.Version)
	}

	requiredPaths := []string{
		"/healthz",
		"/readyz",
		"/version",
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
		"/v1/admin/config-summary",
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

func TestOpenAPIEmailFieldsMatchAddrSpecValidator(t *testing.T) {
	spec := loadOpenAPISpec(t)

	sendRequest, exists := spec.Components.Schemas["SendRequest"]
	if !exists {
		t.Fatal("expected SendRequest schema")
	}
	assertAddrSpecEmailPattern(t, sendRequest.Properties["to"])

	providerEventRequest, exists := spec.Components.Schemas["ProviderEventRequest"]
	if !exists {
		t.Fatal("expected ProviderEventRequest schema")
	}
	assertAddrSpecEmailPattern(t, providerEventRequest.Properties["recipient_email"])

	suppressionList := spec.Paths["/v1/suppressions"].Get
	for _, parameter := range suppressionList.Parameters {
		if parameter.In == "query" && parameter.Name == "email" {
			assertAddrSpecEmailPattern(t, parameter.Schema)
			return
		}
	}
	t.Fatal("expected /v1/suppressions email query parameter")
}

func TestOpenAPISendRequestLocalePatternMatchesValidator(t *testing.T) {
	spec := loadOpenAPISpec(t)

	sendRequest, exists := spec.Components.Schemas["SendRequest"]
	if !exists {
		t.Fatal("expected SendRequest schema")
	}
	locale, exists := sendRequest.Properties["locale"]
	if !exists {
		t.Fatal("expected SendRequest.locale property")
	}
	if locale.Pattern != "^[a-z]{2,3}-[A-Z]{2}$" {
		t.Fatalf("expected locale pattern to allow two or three language letters, got %q", locale.Pattern)
	}
}

func TestOpenAPIProviderEventOccurredAtFieldsUseDateTime(t *testing.T) {
	spec := loadOpenAPISpec(t)

	assertOpenAPIDateTimeField(t, spec, "MessageEventEntry", "occurred_at")
	assertOpenAPIDateTimeField(t, spec, "ProviderEventListEntry", "occurred_at")
	assertOpenAPIDateTimeField(t, spec, "ProviderEventRequest", "occurred_at")
	assertOpenAPIRequiresField(t, spec, "MessageEventEntry", "occurred_at")
	assertOpenAPIRequiresField(t, spec, "ProviderEventListEntry", "occurred_at")
}

func TestOpenAPIAdminSchemasCoverSafeConfigSummaryShape(t *testing.T) {
	spec := loadOpenAPISpec(t)

	assertOpenAPIRequired(t, spec, "AdminConfigSummaryResponse", []string{
		"app",
		"runtime",
		"defaults",
		"provider_accounts",
		"provider_channels",
	})
	assertOpenAPIRequired(t, spec, "AdminAppSummary", []string{
		"code",
		"name",
		"enabled",
		"default_locale",
		"allowed_locales",
		"api_keys",
		"scenes",
		"templates",
	})
	assertOpenAPIRequired(t, spec, "AdminSceneSummary", []string{
		"code",
		"name",
		"enabled",
		"template",
		"rate_limit",
		"route_policy",
	})
	assertOpenAPIRequired(t, spec, "AdminTemplateSummary", []string{
		"code",
		"locale",
		"enabled",
		"subject",
		"required_vars",
		"has_html",
		"has_text",
	})
	assertOpenAPIRequired(t, spec, "AdminProviderChannelSummary", []string{
		"code",
		"account",
		"provider",
		"transport",
		"enabled",
		"sender_domain",
		"from_name",
		"from",
	})
	assertOpenAPIPropertiesAbsent(t, spec, "AdminAPIKeySummary", []string{"key_ref", "key_hash"})
	assertOpenAPIPropertiesAbsent(t, spec, "AdminProviderAccountSummary", []string{"credentials"})
	assertOpenAPIPropertiesAbsent(t, spec, "AdminRuntimeSummary", []string{
		"shared_secret_ref",
		"resend_secret_ref",
		"brevo_token_ref",
		"logging_dir",
	})
	assertOpenAPIPropertiesAbsent(t, spec, "AdminSMTPSummary", []string{"username", "password_ref"})

	for _, schemaName := range []string{
		"AdminConfigSummaryResponse",
		"AdminAppSummary",
		"AdminSceneSummary",
		"AdminRateLimitSummary",
		"AdminTemplateSummary",
		"AdminRuntimeSummary",
		"AdminDefaultsSummary",
		"AdminProviderAccountSummary",
		"AdminProviderChannelSummary",
		"AdminSMTPSummary",
	} {
		if _, exists := spec.Components.Schemas[schemaName]; !exists {
			t.Fatalf("expected OpenAPI admin schema %q", schemaName)
		}
	}
}

func assertOpenAPIPropertiesAbsent(t *testing.T, spec openAPISpec, schemaName string, blocked []string) {
	t.Helper()

	schema, exists := spec.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("expected OpenAPI schema %q", schemaName)
	}
	for _, property := range blocked {
		if _, exists := schema.Properties[property]; exists {
			t.Fatalf("expected %s property %q to be omitted", schemaName, property)
		}
	}
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

func assertOpenAPIRequired(t *testing.T, spec openAPISpec, schemaName string, want []string) {
	t.Helper()

	schema, exists := spec.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("expected OpenAPI schema %q", schemaName)
	}
	if len(schema.Required) != len(want) {
		t.Fatalf("expected %s required fields %v, got %v", schemaName, want, schema.Required)
	}
	for index, value := range want {
		if schema.Required[index] != value {
			t.Fatalf("expected %s required fields %v, got %v", schemaName, want, schema.Required)
		}
		if _, exists := schema.Properties[value]; !exists {
			t.Fatalf("expected %s property %q", schemaName, value)
		}
	}
}

func assertOpenAPIRequiresField(t *testing.T, spec openAPISpec, schemaName string, fieldName string) {
	t.Helper()

	schema, exists := spec.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("expected OpenAPI schema %q", schemaName)
	}
	for _, required := range schema.Required {
		if required == fieldName {
			return
		}
	}
	t.Fatalf("expected %s to require %q, got %v", schemaName, fieldName, schema.Required)
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

func assertOpenAPIDateTimeField(t *testing.T, spec openAPISpec, schemaName string, fieldName string) {
	t.Helper()

	schema, exists := spec.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("expected OpenAPI schema %q", schemaName)
	}
	field, exists := schema.Properties[fieldName]
	if !exists {
		t.Fatalf("expected %s property %q", schemaName, fieldName)
	}
	if field.Type != "string" || field.Format != "date-time" {
		t.Fatalf("expected %s.%s to be string date-time, got type=%q format=%q", schemaName, fieldName, field.Type, field.Format)
	}
}

func assertAddrSpecEmailPattern(t *testing.T, schema openAPISchema) {
	t.Helper()

	if schema.Type != "string" || schema.Format != "email" || schema.MaxLength != 254 {
		t.Fatalf("expected email schema type=string format=email maxLength=254, got type=%q format=%q maxLength=%d", schema.Type, schema.Format, schema.MaxLength)
	}
	if schema.Pattern == "" {
		t.Fatal("expected email schema pattern")
	}
	pattern := regexp.MustCompile(schema.Pattern)
	for _, value := range []string{
		"User@Example.COM",
		"user.name+tag@example.co",
		"user@localhost",
	} {
		if !pattern.MatchString(value) {
			t.Fatalf("expected OpenAPI email pattern to accept %q", value)
		}
		if !domain.IsAddrSpecEmail(value) {
			t.Fatalf("test fixture %q should be valid according to the domain validator", value)
		}
	}
	for _, value := range []string{
		"User <user@example.com>",
		"user example@example.com",
		"user\n@example.com",
		"usér@example.com",
		"user@example.com (comment)",
		"user@bad..example.com",
		"user@-bad.example.com",
		"user@example-.com",
		"user@bad_domain.example.com",
		"not-an-email",
	} {
		if pattern.MatchString(value) {
			t.Fatalf("expected OpenAPI email pattern to reject %q", value)
		}
		if domain.IsAddrSpecEmail(value) {
			t.Fatalf("test fixture %q should be invalid according to the domain validator", value)
		}
	}
}

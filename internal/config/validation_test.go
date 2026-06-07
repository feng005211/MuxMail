package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateValidConfigPasses(t *testing.T) {
	cfg := validConfig(t)

	report := Validate(cfg, NewSecretResolver())
	if report.HasErrors() {
		t.Fatalf("expected valid config, got errors: %+v", report.Errors)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", report.Warnings)
	}
}

func TestValidateWarnsForPlainSecret(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].APIKeys[0].KeyRef = "plain:muxmail_example_key"

	report := Validate(cfg, NewSecretResolver())
	if report.HasErrors() {
		t.Fatalf("expected plain secret to pass with warning, got errors: %+v", report.Errors)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("expected one warning, got %+v", report.Warnings)
	}
	if report.Warnings[0].Code != "plain_secret_ref" {
		t.Fatalf("expected plain secret warning, got %+v", report.Warnings[0])
	}
}

func TestValidateWithOptionsRejectsPlainSecretInStrictMode(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].APIKeys[0].KeyRef = "plain:muxmail_example_key"

	report := ValidateWithOptions(cfg, NewSecretResolver(), ValidationOptions{StrictPlainSecrets: true})
	assertReportCode(t, report, "plain_secret_ref_forbidden")
	if len(report.Warnings) != 0 {
		t.Fatalf("expected strict plain secret to become an error only, got warnings: %+v", report.Warnings)
	}
}

func TestExampleConfigValidatesWithPlainSecretWarningsOnly(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("expected runtime caller to resolve current file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	examplePath := filepath.Join(repoRoot, "config.example.yaml")

	cfg, err := LoadFile(examplePath)
	if err != nil {
		t.Fatalf("expected example config to load: %v", err)
	}

	report := Validate(cfg, NewSecretResolver())
	if report.HasErrors() {
		t.Fatalf("expected example config to validate, got errors: %+v", report.Errors)
	}
	if len(report.Warnings) != 4 {
		t.Fatalf("expected four plain secret warnings, got %+v", report.Warnings)
	}
	for _, warning := range report.Warnings {
		if warning.Code != "plain_secret_ref" {
			t.Fatalf("expected only plain secret warnings, got %+v", report.Warnings)
		}
	}
}

func TestContainerExampleConfigValidatesInStrictMode(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("expected runtime caller to resolve current file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	examplePath := filepath.Join(repoRoot, "config.container.example.yaml")

	t.Setenv("PROJECT_A_MUXMAIL_KEY", "mk_test_123456789012345678901234")
	t.Setenv("BREVO_API_KEY", "muxmail_example_brevo_key")
	t.Setenv("RESEND_API_KEY", "muxmail_example_resend_key")

	cfg, err := LoadFile(examplePath)
	if err != nil {
		t.Fatalf("expected container example config to load: %v", err)
	}

	report := ValidateWithOptions(cfg, NewSecretResolver(), ValidationOptions{StrictPlainSecrets: true})
	if report.HasErrors() {
		t.Fatalf("expected container example config to validate in strict mode, got errors: %+v", report.Errors)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("expected no strict-mode warnings, got %+v", report.Warnings)
	}
}

func TestValidateRejectsDuplicateAppCode(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps = append(cfg.Apps, cfg.Apps[0])

	assertValidationCode(t, cfg, "app_duplicate")
}

func TestValidateRejectsInvalidAppLocale(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].DefaultLocale = "zh-cn"

	assertValidationCode(t, cfg, "locale_invalid")
}

func TestValidateRejectsDuplicateAPIKeyName(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].APIKeys = append(cfg.Apps[0].APIKeys, cfg.Apps[0].APIKeys[0])

	assertValidationCode(t, cfg, "api_key_duplicate")
}

func TestValidateRejectsDuplicateTemplateLocale(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].Templates = append(cfg.Apps[0].Templates, cfg.Apps[0].Templates[0])

	assertValidationCode(t, cfg, "template_duplicate")
}

func TestValidateRejectsTemplateLocaleOutsideApp(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].Templates[0].Locale = "fr-FR"

	assertValidationCode(t, cfg, "template_locale_not_allowed")
}

func TestValidateRejectsDuplicateSceneCode(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].Scenes = append(cfg.Apps[0].Scenes, cfg.Apps[0].Scenes[0])

	assertValidationCode(t, cfg, "scene_duplicate")
}

func TestValidateRejectsSceneTemplateFromWrongApp(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].Scenes[0].Template = "missing_template"

	assertValidationCode(t, cfg, "scene_template_not_found")
}

func TestValidateRejectsDuplicateProviderAccount(t *testing.T) {
	cfg := validConfig(t)
	cfg.ProviderAccounts = append(cfg.ProviderAccounts, cfg.ProviderAccounts[0])

	assertValidationCode(t, cfg, "provider_account_duplicate")
}

func TestValidateRejectsDuplicateProviderChannel(t *testing.T) {
	cfg := validConfig(t)
	cfg.ProviderChannels = append(cfg.ProviderChannels, cfg.ProviderChannels[0])

	assertValidationCode(t, cfg, "provider_channel_duplicate")
}

func TestValidateRejectsInvalidProviderAndTransport(t *testing.T) {
	cfg := validConfig(t)
	cfg.ProviderAccounts[0].Provider = "mailgun"
	cfg.ProviderChannels[0].Transport = "smtp"

	report := Validate(cfg, NewSecretResolver())
	assertReportCode(t, report, "provider_invalid")
	assertReportCode(t, report, "transport_invalid")
}

func TestValidateRejectsMissingRouteWildcard(t *testing.T) {
	cfg := validConfig(t)
	delete(cfg.Apps[0].Scenes[0].RoutePolicy, "*")

	assertValidationCode(t, cfg, "route_policy_missing_wildcard")
}

func TestValidateRejectsMissingRouteChannel(t *testing.T) {
	cfg := validConfig(t)
	cfg.Apps[0].Scenes[0].RoutePolicy["*"] = []string{"missing_channel"}

	assertValidationCode(t, cfg, "route_channel_not_found")
}

func TestValidateRejectsDisabledRouteChannel(t *testing.T) {
	cfg := validConfig(t)
	disabled := false
	cfg.ProviderChannels[0].Enabled = &disabled

	assertValidationCode(t, cfg, "route_channel_disabled")
}

func TestValidateRejectsFromDomainMismatch(t *testing.T) {
	cfg := validConfig(t)
	cfg.ProviderChannels[0].From = "no-reply@other.example.com"

	assertValidationCode(t, cfg, "from_domain_mismatch")
}

func TestValidateRejectsInvalidSMTPConfig(t *testing.T) {
	cfg := validConfig(t)
	cfg.ProviderAccounts[0].Provider = "resend"
	cfg.ProviderChannels[0].Transport = "smtp"
	cfg.ProviderChannels[0].SMTP = &SMTPConfig{Host: "", Port: 25, Username: ""}

	report := Validate(cfg, NewSecretResolver())
	assertReportCode(t, report, "smtp_invalid")
}

func TestValidateRejectsMissingEnvSecret(t *testing.T) {
	cfg := validConfig(t)
	os.Unsetenv("MUXMAIL_MISSING_KEY")
	cfg.ProviderAccounts[0].Credentials["api_key"] = "env:MUXMAIL_MISSING_KEY"

	assertValidationCode(t, cfg, "secret_ref_invalid")
}

func TestValidateRejectsInvalidSuppressionReason(t *testing.T) {
	cfg := validConfig(t)
	writeTestConfig(t, cfg.SuppressionFile, `
entries:
  - app: project_a
    email: bounced@example.com
    reason: unsubscribe
`)

	assertValidationCode(t, cfg, "suppression_reason_invalid")
}

func TestValidateAllowsMissingSuppressionFile(t *testing.T) {
	cfg := validConfig(t)
	cfg.SuppressionFile = filepath.Join(t.TempDir(), "missing.yaml")

	report := Validate(cfg, NewSecretResolver())
	if report.HasErrors() {
		t.Fatalf("expected missing suppression file to be allowed, got %+v", report.Errors)
	}
}

func TestValidateRejectsInvalidNumericDefaults(t *testing.T) {
	cfg := validConfig(t)
	cfg.Defaults.MemoryQueueSize = -1
	cfg.Defaults.MaxAttemptsPerMessage = 4
	cfg.Defaults.RetryBackoffSeconds = []int{0, 30, 120}
	cfg.Logging.MaxBackups = 0
	cfg.Server.ReadTimeoutSeconds = -1
	cfg.Apps[0].Scenes[0].RateLimit.SameEmailPerMinute = 0

	report := Validate(cfg, NewSecretResolver())
	assertReportCode(t, report, "default_invalid")
	assertReportCode(t, report, "logging_invalid")
	assertReportCode(t, report, "server_timeout_invalid")
	assertReportCode(t, report, "rate_limit_invalid")
}

func TestValidateRejectsInvalidTrustedProxy(t *testing.T) {
	cfg := validConfig(t)
	cfg.Server.TrustedProxies = []string{"127.0.0.1", "not-an-ip"}

	assertValidationCode(t, cfg, "trusted_proxy_invalid")
}

func TestValidateRequiresWebhookSecretWhenEnabled(t *testing.T) {
	cfg := validConfig(t)
	cfg.Webhooks.Enabled = true
	cfg.Webhooks.SharedSecretRef = ""

	assertValidationCode(t, cfg, "secret_ref_required")
}

func validConfig(t *testing.T) *Config {
	t.Helper()

	dir := t.TempDir()
	apiKeyPath := filepath.Join(dir, "api.key")
	writeTestConfig(t, apiKeyPath, "mk_test_123456789012345678901234\n")
	providerKeyPath := filepath.Join(dir, "provider.key")
	writeTestConfig(t, providerKeyPath, "provider-secret\n")

	return &Config{
		SourcePath:      filepath.Join(dir, "config.yaml"),
		BaseDir:         dir,
		SuppressionFile: filepath.Join(dir, "suppression.yaml"),
		Server: ServerConfig{
			Listen:                   ":8080",
			ReadTimeoutSeconds:       10,
			ReadHeaderTimeoutSeconds: 5,
			WriteTimeoutSeconds:      15,
			IdleTimeoutSeconds:       60,
		},
		Runtime: RuntimeConfig{
			ConfigStore: "file",
			Queue:       "memory",
			RateLimiter: "memory",
			MessageLog:  "file",
			Stats:       "off",
			Suppression: "file",
		},
		Defaults: DefaultsConfig{
			ProviderTimeoutSeconds: 10,
			MaxAttemptsPerMessage:  3,
			RetryBackoffSeconds:    []int{0, 30, 120},
			MemoryQueueSize:        1000,
			WorkerConcurrency:      4,
			IdempotencyCacheSize:   10000,
			IdempotencyTTLHours:    24,
			MaxRequestBodyBytes:    65536,
			MaxTemplateVarBytes:    8192,
			MaxContextBytes:        4096,
		},
		Apps: []AppConfig{
			{
				Code:           "project_a",
				Name:           "Project A",
				DefaultLocale:  "en-US",
				AllowedLocales: []string{"en-US", "zh-CN"},
				APIKeys: []APIKeyConfig{
					{Name: "default", KeyRef: "file:" + apiKeyPath},
				},
				Templates: []TemplateConfig{
					{
						Code:         "register_code_v1",
						Locale:       "en-US",
						Subject:      "Your verification code",
						RequiredVars: []string{"code"},
						TextBody:     "Your verification code is {{ .code }}.",
					},
					{
						Code:         "register_code_v1",
						Locale:       "zh-CN",
						Subject:      "Verification code",
						RequiredVars: []string{"code"},
						TextBody:     "Verification code is {{ .code }}.",
					},
				},
				Scenes: []SceneConfig{
					{
						Code:     "register_code",
						Name:     "Register verification code",
						Template: "register_code_v1",
						RateLimit: RateLimitConfig{
							SameEmailPerMinute:  1,
							SameEmailPerDay:     10,
							SameUserIPPerHour:   20,
							SameCallerIPPerHour: 200,
						},
						RoutePolicy: RoutePolicy{
							"*": []string{"mock_auth_api"},
						},
					},
				},
			},
		},
		ProviderAccounts: []ProviderAccountConfig{
			{
				Code:     "mock_main",
				Provider: "mock",
				Credentials: map[string]string{
					"api_key": "file:" + providerKeyPath,
				},
			},
		},
		ProviderChannels: []ProviderChannelConfig{
			{
				Code:         "mock_auth_api",
				Account:      "mock_main",
				Transport:    "api",
				SenderDomain: "auth.example.com",
				FromName:     "MuxMail",
				From:         "no-reply@auth.example.com",
			},
		},
		Logging: LoggingConfig{
			Dir:           filepath.Join(dir, "logs"),
			MaxFileSizeMB: 100,
			MaxBackups:    5,
		},
	}
}

func assertValidationCode(t *testing.T, cfg *Config, code string) {
	t.Helper()

	report := Validate(cfg, NewSecretResolver())
	assertReportCode(t, report, code)
}

func assertReportCode(t *testing.T, report ValidationReport, code string) {
	t.Helper()

	for _, validationError := range report.Errors {
		if validationError.Code == code {
			return
		}
	}

	t.Fatalf("expected validation code %q, got errors: %+v", code, report.Errors)
}

func TestValidationErrorListSummary(t *testing.T) {
	err := ValidationErrorList{
		{Code: "one", Message: "first"},
		{Code: "two", Message: "second"},
	}

	if got := err.Error(); got != "configuration validation failed: 2 errors" {
		t.Fatalf("expected multi-error summary, got %q", got)
	}

	single := ValidationErrorList{{Code: "one", Message: "first"}}
	if got := single.Error(); got != "configuration validation failed: first" {
		t.Fatalf("expected single-error summary, got %q", got)
	}
}

package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"
	texttemplate "text/template"

	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

const (
	maxMVPAttemptsPerMessage  = 3
	maxMVPRetryBackoffSeconds = 300
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$`)
	localePattern     = regexp.MustCompile(`^[a-z]{2,3}-[A-Z]{2}$`)
)

// ValidationReport contains all warnings and errors found during config validation.
type ValidationReport struct {
	Warnings []ValidationWarning
	Errors   []ValidationError
}

// ValidationOptions controls optional validation behavior.
type ValidationOptions struct {
	StrictPlainSecrets bool
}

// HasErrors reports whether validation found at least one blocking error.
func (r ValidationReport) HasErrors() bool {
	return len(r.Errors) > 0
}

// Err returns a single error value when the report contains validation errors.
func (r ValidationReport) Err() error {
	if !r.HasErrors() {
		return nil
	}

	return ValidationErrorList(r.Errors)
}

// ValidationError describes one blocking configuration validation error.
type ValidationError struct {
	Code    string
	Path    string
	Message string
}

// ValidationErrorList is the error form of one or more validation errors.
type ValidationErrorList []ValidationError

// Error returns a short summary suitable for command-line output.
func (l ValidationErrorList) Error() string {
	if len(l) == 1 {
		return fmt.Sprintf("configuration validation failed: %s", l[0].Message)
	}

	return fmt.Sprintf("configuration validation failed: %d errors", len(l))
}

// Validate checks all MVP configuration rules that do not require starting the runtime.
func Validate(cfg *Config, resolver SecretResolver) ValidationReport {
	return ValidateWithOptions(cfg, resolver, ValidationOptions{})
}

// ValidateWithOptions checks configuration rules with additional validation options.
func ValidateWithOptions(cfg *Config, resolver SecretResolver, options ValidationOptions) ValidationReport {
	if resolver == nil {
		resolver = NewSecretResolver()
	}

	v := configValidator{
		cfg:      cfg,
		resolver: resolver,
		options:  options,
	}
	v.validate()

	return v.report
}

type configValidator struct {
	cfg                *Config
	resolver           SecretResolver
	options            ValidationOptions
	report             ValidationReport
	accountsByCode     map[string]ProviderAccountConfig
	channelsByCode     map[string]ProviderChannelConfig
	duplicateAccount   map[string]bool
	duplicateChannel   map[string]bool
	enabledChannelCode map[string]bool
	apiKeyHashPaths    map[string]string
	appCodes           map[string]struct{}
}

func (v *configValidator) validate() {
	if v.cfg == nil {
		v.addError("config_missing", "", "config is required")
		return
	}

	v.validateServer()
	v.validateRuntime()
	v.validateDefaults()
	v.validateLogging()
	v.validateProviderAccounts()
	v.validateProviderChannels()
	v.validateApps()
	v.validateWebhooks()
	v.validateSuppressionFile()
}

func (v *configValidator) validateServer() {
	if v.cfg.Server.Listen == "" {
		v.addError("server_listen_required", "server.listen", "server.listen is required")
	}
	if v.cfg.Server.ReadTimeoutSeconds <= 0 {
		v.addError("server_timeout_invalid", "server.read_timeout_seconds", "server.read_timeout_seconds must be greater than 0")
	}
	if v.cfg.Server.ReadHeaderTimeoutSeconds <= 0 {
		v.addError("server_timeout_invalid", "server.read_header_timeout_seconds", "server.read_header_timeout_seconds must be greater than 0")
	}
	if v.cfg.Server.WriteTimeoutSeconds <= 0 {
		v.addError("server_timeout_invalid", "server.write_timeout_seconds", "server.write_timeout_seconds must be greater than 0")
	}
	if v.cfg.Server.IdleTimeoutSeconds <= 0 {
		v.addError("server_timeout_invalid", "server.idle_timeout_seconds", "server.idle_timeout_seconds must be greater than 0")
	}
	for index, proxy := range v.cfg.Server.TrustedProxies {
		if !isValidTrustedProxy(proxy) {
			v.addError("trusted_proxy_invalid", fmt.Sprintf("server.trusted_proxies[%d]", index), "trusted proxy must be an IP address or CIDR prefix")
		}
	}
}

func (v *configValidator) validateRuntime() {
	v.requireValue("runtime.config_store", v.cfg.Runtime.ConfigStore, []string{"file"})
	v.requireValue("runtime.queue", v.cfg.Runtime.Queue, []string{"memory"})
	v.requireValue("runtime.rate_limiter", v.cfg.Runtime.RateLimiter, []string{"memory"})
	v.requireValue("runtime.message_log", v.cfg.Runtime.MessageLog, []string{"file"})
	v.requireValue("runtime.stats", v.cfg.Runtime.Stats, []string{"off", "file"})
	v.requireValue("runtime.suppression", v.cfg.Runtime.Suppression, []string{"file"})
}

func (v *configValidator) validateDefaults() {
	if v.cfg.Defaults.ProviderTimeoutSeconds <= 0 {
		v.addError("default_invalid", "defaults.provider_timeout_seconds", "defaults.provider_timeout_seconds must be greater than 0")
	}
	if v.cfg.Defaults.MaxAttemptsPerMessage <= 0 || v.cfg.Defaults.MaxAttemptsPerMessage > maxMVPAttemptsPerMessage {
		v.addError("default_invalid", "defaults.max_attempts_per_message", "defaults.max_attempts_per_message must be between 1 and 3")
	}
	if len(v.cfg.Defaults.RetryBackoffSeconds) != v.cfg.Defaults.MaxAttemptsPerMessage {
		v.addError("retry_backoff_invalid", "defaults.retry_backoff_seconds", "defaults.retry_backoff_seconds length must equal defaults.max_attempts_per_message")
	}
	for index, backoffSeconds := range v.cfg.Defaults.RetryBackoffSeconds {
		path := fmt.Sprintf("defaults.retry_backoff_seconds[%d]", index)
		if backoffSeconds < 0 || backoffSeconds > maxMVPRetryBackoffSeconds {
			v.addError("retry_backoff_invalid", path, "defaults.retry_backoff_seconds values must be between 0 and 300")
			continue
		}
		if index == 0 && backoffSeconds != 0 {
			v.addError("retry_backoff_invalid", path, "defaults.retry_backoff_seconds[0] must be 0")
		}
	}
	if v.cfg.Defaults.MemoryQueueSize <= 0 {
		v.addError("default_invalid", "defaults.memory_queue_size", "defaults.memory_queue_size must be greater than 0")
	}
	if v.cfg.Defaults.WorkerConcurrency <= 0 {
		v.addError("default_invalid", "defaults.worker_concurrency", "defaults.worker_concurrency must be greater than 0")
	}
	if v.cfg.Defaults.IdempotencyCacheSize <= 0 {
		v.addError("default_invalid", "defaults.idempotency_cache_size", "defaults.idempotency_cache_size must be greater than 0")
	}
	if v.cfg.Defaults.IdempotencyTTLHours <= 0 {
		v.addError("default_invalid", "defaults.idempotency_ttl_hours", "defaults.idempotency_ttl_hours must be greater than 0")
	}
	if v.cfg.Defaults.MaxRequestBodyBytes <= 0 {
		v.addError("default_invalid", "defaults.max_request_body_bytes", "defaults.max_request_body_bytes must be greater than 0")
	}
	if v.cfg.Defaults.MaxTemplateVarBytes <= 0 {
		v.addError("default_invalid", "defaults.max_template_var_bytes", "defaults.max_template_var_bytes must be greater than 0")
	}
	if v.cfg.Defaults.MaxContextBytes <= 0 {
		v.addError("default_invalid", "defaults.max_context_bytes", "defaults.max_context_bytes must be greater than 0")
	}
}

func (v *configValidator) validateLogging() {
	if v.cfg.Logging.Dir == "" {
		v.addError("logging_dir_required", "logging.dir", "logging.dir is required")
	}
	if v.cfg.Logging.MaxFileSizeMB <= 0 {
		v.addError("logging_invalid", "logging.max_file_size_mb", "logging.max_file_size_mb must be greater than 0")
	}
	if v.cfg.Logging.MaxBackups < 1 {
		v.addError("logging_invalid", "logging.max_backups", "logging.max_backups must be at least 1")
	}
}

func (v *configValidator) validateProviderAccounts() {
	v.accountsByCode = make(map[string]ProviderAccountConfig)
	v.duplicateAccount = make(map[string]bool)

	for index, account := range v.cfg.ProviderAccounts {
		path := fmt.Sprintf("provider_accounts[%d]", index)
		v.validateIdentifier(path+".code", account.Code)
		if account.Code != "" {
			if _, exists := v.accountsByCode[account.Code]; exists {
				v.duplicateAccount[account.Code] = true
				v.addError("provider_account_duplicate", path+".code", "provider account code must be unique")
			} else {
				v.accountsByCode[account.Code] = account
			}
		}

		if !account.Provider.IsValid() {
			v.addError("provider_invalid", path+".provider", "provider must be one of mock, resend, or brevo")
		}

		for name, ref := range account.Credentials {
			credentialPath := path + ".credentials." + name
			resolved, ok := v.validateSecretRef(credentialPath, ref)
			if ok && name == "api_key" && providerAPIKeyUsesHTTPHeader(account.Provider) && !isVisibleASCIIWithoutWhitespaceSecret(resolved.Value) {
				v.addError("secret_value_invalid", credentialPath, "provider api key secret value must be visible ASCII without whitespace")
			}
		}
	}
}

func (v *configValidator) validateProviderChannels() {
	v.channelsByCode = make(map[string]ProviderChannelConfig)
	v.duplicateChannel = make(map[string]bool)
	v.enabledChannelCode = make(map[string]bool)

	for index, channel := range v.cfg.ProviderChannels {
		path := fmt.Sprintf("provider_channels[%d]", index)
		v.validateIdentifier(path+".code", channel.Code)
		if channel.Code != "" {
			if _, exists := v.channelsByCode[channel.Code]; exists {
				v.duplicateChannel[channel.Code] = true
				v.addError("provider_channel_duplicate", path+".code", "provider channel code must be unique")
			} else {
				v.channelsByCode[channel.Code] = channel
			}
		}

		account, accountExists := v.accountsByCode[channel.Account]
		if channel.Account == "" {
			v.addError("provider_account_required", path+".account", "provider channel account is required")
		} else if !accountExists {
			v.addError("provider_account_not_found", path+".account", "provider channel account must reference an existing provider account")
		} else {
			if !domain.SupportsTransport(account.Provider, channel.Transport) {
				v.addError("transport_invalid", path+".transport", "provider channel transport is not supported by the provider")
			}
			if EnabledValue(channel.Enabled) && !EnabledValue(account.Enabled) {
				v.addError("provider_account_disabled", path+".account", "enabled provider channel cannot reference a disabled provider account")
			}
		}

		if !channel.Transport.IsValid() {
			v.addError("transport_invalid", path+".transport", "transport must be api or smtp")
		}
		if channel.Transport == domain.TransportAPI && providerAPIKeyUsesHTTPHeader(account.Provider) && strings.TrimSpace(account.Credentials["api_key"]) == "" {
			v.addError("secret_ref_required", path+".account", "api transport provider channel requires provider account credentials.api_key")
		}
		if !domain.IsSafeEmailDisplayName(channel.FromName) {
			v.addError("from_name_invalid", path+".from_name", "from_name must not contain control characters")
		}
		v.validateFromDomain(path+".from", channel.From, channel.SenderDomain)
		v.validateSMTP(path, channel, account, accountExists)

		if channel.Code != "" && EnabledValue(channel.Enabled) && accountExists && EnabledValue(account.Enabled) {
			v.enabledChannelCode[channel.Code] = true
		}
	}
}

func (v *configValidator) validateSMTP(path string, channel ProviderChannelConfig, account ProviderAccountConfig, accountExists bool) {
	if channel.SMTP != nil && channel.SMTP.PasswordRef != "" {
		v.validateSecretRef(path+".smtp.password_ref", channel.SMTP.PasswordRef)
	}
	if channel.Transport != domain.TransportSMTP {
		if channel.SMTP != nil {
			v.addError("smtp_invalid", path+".smtp", "smtp settings are only allowed for smtp transport")
		}
		return
	}

	if channel.SMTP == nil {
		v.addError("smtp_required", path+".smtp", "smtp settings are required for smtp transport")
		return
	}
	if channel.SMTP.Host == "" {
		v.addError("smtp_invalid", path+".smtp.host", "smtp.host is required")
	}
	if channel.SMTP.Port != 587 {
		v.addError("smtp_invalid", path+".smtp.port", "smtp.port must be 587")
	}
	if channel.SMTP.Username == "" {
		v.addError("smtp_invalid", path+".smtp.username", "smtp.username is required")
	}
	if channel.SMTP.PasswordRef != "" {
		return
	}
	if accountExists && strings.TrimSpace(account.Credentials["api_key"]) == "" {
		v.addError("secret_ref_required", path+".smtp.password_ref", "smtp.password_ref is required when provider account credentials.api_key is missing")
	}
}

func (v *configValidator) validateApps() {
	v.appCodes = make(map[string]struct{})
	v.apiKeyHashPaths = make(map[string]string)

	if len(v.cfg.Apps) == 0 {
		v.addError("app_required", "apps", "at least one app is required")
	}
	for appIndex, app := range v.cfg.Apps {
		appPath := fmt.Sprintf("apps[%d]", appIndex)
		v.validateIdentifier(appPath+".code", app.Code)
		if app.Code != "" {
			if _, exists := v.appCodes[app.Code]; exists {
				v.addError("app_duplicate", appPath+".code", "app code must be unique")
			} else {
				v.appCodes[app.Code] = struct{}{}
			}
		}

		if len(app.APIKeys) == 0 {
			v.addError("api_key_required", appPath+".api_keys", "app must define at least one api key")
		}
		if len(app.Templates) == 0 {
			v.addError("template_required", appPath+".templates", "app must define at least one template")
		}
		if len(app.Scenes) == 0 {
			v.addError("scene_required", appPath+".scenes", "app must define at least one scene")
		}

		allowedLocales := v.validateAppLocales(appPath, app)
		v.validateAPIKeys(appPath, app)
		templates := v.validateTemplates(appPath, app, allowedLocales)
		v.validateScenes(appPath, app, templates)
	}
}

func (v *configValidator) validateWebhooks() {
	if !v.cfg.Webhooks.Enabled {
		return
	}
	if strings.TrimSpace(v.cfg.Webhooks.SharedSecretRef) == "" &&
		strings.TrimSpace(v.cfg.Webhooks.ResendSecretRef) == "" &&
		strings.TrimSpace(v.cfg.Webhooks.BrevoTokenRef) == "" {
		v.addError("secret_ref_required", "webhooks", "at least one webhook secret reference is required when webhooks are enabled")
		return
	}
	if strings.TrimSpace(v.cfg.Webhooks.SharedSecretRef) != "" {
		v.validateBearerSecretRef("webhooks.shared_secret_ref", v.cfg.Webhooks.SharedSecretRef)
	}
	if strings.TrimSpace(v.cfg.Webhooks.ResendSecretRef) != "" {
		resolved, ok := v.validateSecretRef("webhooks.resend_secret_ref", v.cfg.Webhooks.ResendSecretRef)
		if ok && !isValidResendWebhookSecretValue(resolved.Value) {
			v.addError("resend_webhook_secret_invalid", "webhooks.resend_secret_ref", "resend webhook secret must be a non-empty whsec_ or base64 Svix secret")
		}
	}
	if strings.TrimSpace(v.cfg.Webhooks.BrevoTokenRef) != "" {
		v.validateBearerSecretRef("webhooks.brevo_token_ref", v.cfg.Webhooks.BrevoTokenRef)
	}
}

func (v *configValidator) validateAppLocales(appPath string, app AppConfig) map[string]struct{} {
	allowed := make(map[string]struct{})
	for localeIndex, locale := range app.AllowedLocales {
		path := fmt.Sprintf("%s.allowed_locales[%d]", appPath, localeIndex)
		if !isValidLocale(locale) {
			v.addError("locale_invalid", path, "allowed locale must use language-region format such as en-US")
			continue
		}
		if _, exists := allowed[locale]; exists {
			v.addError("locale_duplicate", path, "allowed locale must be unique within an app")
			continue
		}
		allowed[locale] = struct{}{}
	}

	if !isValidLocale(app.DefaultLocale) {
		v.addError("locale_invalid", appPath+".default_locale", "default locale must use language-region format such as en-US")
	}
	if _, exists := allowed[app.DefaultLocale]; !exists {
		v.addError("default_locale_not_allowed", appPath+".default_locale", "app default_locale must be included in allowed_locales")
	}

	return allowed
}

func (v *configValidator) validateAPIKeys(appPath string, app AppConfig) {
	names := make(map[string]struct{})
	for keyIndex, key := range app.APIKeys {
		path := fmt.Sprintf("%s.api_keys[%d]", appPath, keyIndex)
		v.validateIdentifier(path+".name", key.Name)
		if key.Name != "" {
			if _, exists := names[key.Name]; exists {
				v.addError("api_key_duplicate", path+".name", "api key name must be unique within an app")
			} else {
				names[key.Name] = struct{}{}
			}
		}
		resolved, ok := v.resolveSecretRef(path+".key_ref", key.KeyRef)
		if !ok {
			continue
		}
		if !domain.IsValidAPIKeyValue(resolved.Value) {
			v.addError("api_key_value_invalid", path+".key_ref", "api key secret value must be 24 to 128 visible ASCII bytes without whitespace")
		}
		keyHash := domain.APIKeyHash(resolved.Value)
		if firstPath, exists := v.apiKeyHashPaths[keyHash]; exists {
			v.addError("api_key_value_duplicate", path+".key_ref", "api key secret value must be unique across all apps; first used at "+firstPath)
			continue
		}
		v.apiKeyHashPaths[keyHash] = path + ".key_ref"
	}
}

func (v *configValidator) validateTemplates(appPath string, app AppConfig, allowedLocales map[string]struct{}) map[string]TemplateConfig {
	templatesByKey := make(map[string]TemplateConfig)
	for templateIndex, tmpl := range app.Templates {
		path := fmt.Sprintf("%s.templates[%d]", appPath, templateIndex)
		v.validateIdentifier(path+".code", tmpl.Code)
		if !isValidLocale(tmpl.Locale) {
			v.addError("locale_invalid", path+".locale", "template locale must use language-region format such as en-US")
		}
		if _, exists := allowedLocales[tmpl.Locale]; !exists {
			v.addError("template_locale_not_allowed", path+".locale", "template locale must be included in app allowed_locales")
		}
		if tmpl.Subject == "" {
			v.addError("template_subject_required", path+".subject", "template subject is required")
		} else {
			if !domain.IsSafeEmailHeaderValue(tmpl.Subject) {
				v.addError("template_subject_invalid", path+".subject", "template subject must not contain control characters")
			}
			v.validateTextTemplate(path+".subject", "template_subject_invalid", tmpl.Subject)
		}
		if tmpl.HTMLBody == "" && tmpl.TextBody == "" {
			v.addError("template_body_required", path, "template must define html_body or text_body")
		}
		if tmpl.HTMLBody != "" {
			v.validateHTMLTemplate(path+".html_body", tmpl.HTMLBody)
		}
		if tmpl.TextBody != "" {
			v.validateTextTemplate(path+".text_body", "template_text_body_invalid", tmpl.TextBody)
		}
		for requiredIndex, name := range tmpl.RequiredVars {
			if !domain.IsTemplateVarName(name) {
				v.addError("template_required_var_invalid", fmt.Sprintf("%s.required_vars[%d]", path, requiredIndex), "required var name must match send request variable name rules")
			}
		}

		key := templateKey(tmpl.Code, tmpl.Locale)
		if tmpl.Code != "" && tmpl.Locale != "" {
			if _, exists := templatesByKey[key]; exists {
				v.addError("template_duplicate", path+".code", "template code and locale must be unique within an app")
			} else {
				templatesByKey[key] = tmpl
			}
		}
	}

	return templatesByKey
}

func (v *configValidator) validateTextTemplate(path string, code string, source string) {
	if _, err := texttemplate.New(path).Parse(source); err != nil {
		v.addError(code, path, "template text must parse successfully")
	}
}

func (v *configValidator) validateHTMLTemplate(path string, source string) {
	if _, err := htmltemplate.New(path).Parse(source); err != nil {
		v.addError("template_html_body_invalid", path, "template html_body must parse successfully")
	}
}

func (v *configValidator) validateScenes(appPath string, app AppConfig, templates map[string]TemplateConfig) {
	sceneCodes := make(map[string]struct{})
	for sceneIndex, scene := range app.Scenes {
		path := fmt.Sprintf("%s.scenes[%d]", appPath, sceneIndex)
		v.validateIdentifier(path+".code", scene.Code)
		if scene.Code != "" {
			if _, exists := sceneCodes[scene.Code]; exists {
				v.addError("scene_duplicate", path+".code", "scene code must be unique within an app")
			} else {
				sceneCodes[scene.Code] = struct{}{}
			}
		}

		defaultTemplate, hasDefaultTemplate := templates[templateKey(scene.Template, app.DefaultLocale)]
		if scene.Template == "" {
			v.addError("scene_template_required", path+".template", "scene template is required")
		} else if !hasDefaultTemplate {
			v.addError("scene_template_not_found", path+".template", "scene template must exist in the same app for app default_locale")
		} else if !EnabledValue(defaultTemplate.Enabled) {
			v.addError("scene_template_disabled", path+".template", "scene template default locale version must be enabled")
		}

		v.validateRateLimit(path+".rate_limit", scene.RateLimit)
		v.validateRoutePolicy(path+".route_policy", scene.RoutePolicy)
	}
}

func (v *configValidator) validateRateLimit(path string, limit RateLimitConfig) {
	if limit.SameEmailPerMinute <= 0 {
		v.addError("rate_limit_invalid", path+".same_email_per_minute", "same_email_per_minute must be greater than 0")
	}
	if limit.SameEmailPerDay <= 0 {
		v.addError("rate_limit_invalid", path+".same_email_per_day", "same_email_per_day must be greater than 0")
	}
	if limit.SameUserIPPerHour <= 0 {
		v.addError("rate_limit_invalid", path+".same_user_ip_per_hour", "same_user_ip_per_hour must be greater than 0")
	}
	if limit.SameCallerIPPerHour <= 0 {
		v.addError("rate_limit_invalid", path+".same_caller_ip_per_hour", "same_caller_ip_per_hour must be greater than 0")
	}
}

func (v *configValidator) validateRoutePolicy(path string, policy RoutePolicy) {
	if _, exists := policy[domain.RoutePolicyWildcard]; !exists {
		v.addError("route_policy_missing_wildcard", path, "route_policy must include the * fallback route")
	}

	for routeKey, channels := range policy {
		if routeKey == "" || (routeKey != domain.RoutePolicyWildcard && !isValidRouteDomain(routeKey)) {
			v.addError("route_domain_invalid", path, "route key must be a recipient domain or *")
		}
		if len(channels) == 0 {
			v.addError("route_policy_empty", path+"."+routeKey, "route policy entry must include at least one provider channel")
		}
		seenChannels := make(map[string]int, len(channels))
		for channelIndex, channelCode := range channels {
			channelPath := fmt.Sprintf("%s.%s[%d]", path, routeKey, channelIndex)
			if firstIndex, exists := seenChannels[channelCode]; exists {
				v.addError("route_channel_duplicate", channelPath, fmt.Sprintf("route policy entry repeats provider channel from index %d", firstIndex))
				continue
			}
			seenChannels[channelCode] = channelIndex

			channel, exists := v.channelsByCode[channelCode]
			if !exists {
				v.addError("route_channel_not_found", channelPath, "route policy references a missing provider channel")
				continue
			}
			if !EnabledValue(channel.Enabled) {
				v.addError("route_channel_disabled", channelPath, "route policy references a disabled provider channel")
				continue
			}
			if !v.enabledChannelCode[channelCode] {
				v.addError("route_channel_unavailable", channelPath, "route policy references a provider channel whose account is unavailable")
			}
		}
	}
}

func (v *configValidator) validateSuppressionFile() {
	if v.cfg.SuppressionFile == "" {
		return
	}

	data, err := os.ReadFile(v.cfg.SuppressionFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		v.addError("suppression_file_unreadable", "suppression", "suppression file could not be read")
		return
	}

	file, err := decodeSuppressionFile(data)
	if err != nil {
		v.addError("suppression_file_invalid", "suppression", "suppression file must be valid YAML")
		return
	}

	seenEntries := make(map[string]int, len(file.Entries))
	for index, entry := range file.Entries {
		appCode := strings.TrimSpace(entry.App)
		email := strings.TrimSpace(entry.Email)
		normalizedEmail, emailOK := domain.NormalizeAddrSpecEmail(email)
		if appCode == "" {
			v.addError("suppression_app_required", fmt.Sprintf("suppression.entries[%d].app", index), "suppression app is required")
		} else if !identifierPattern.MatchString(appCode) {
			v.addError("suppression_app_invalid", fmt.Sprintf("suppression.entries[%d].app", index), "suppression app must match an existing App code")
		} else if _, exists := v.appCodes[appCode]; !exists {
			v.addError("suppression_app_not_found", fmt.Sprintf("suppression.entries[%d].app", index), "suppression app must reference an existing App")
		}
		if !emailOK {
			v.addError("suppression_email_invalid", fmt.Sprintf("suppression.entries[%d].email", index), "suppression email must be a valid single addr-spec")
		} else if appCode != "" && identifierPattern.MatchString(appCode) {
			key := suppressionEntryKey(appCode, normalizedEmail)
			if firstIndex, exists := seenEntries[key]; exists {
				v.addError("suppression_duplicate", fmt.Sprintf("suppression.entries[%d]", index), fmt.Sprintf("suppression entry duplicates entries[%d] for the same App and email", firstIndex))
			} else {
				seenEntries[key] = index
			}
		}
		if !domain.SuppressionReason(entry.Reason).IsValid() {
			v.addError("suppression_reason_invalid", fmt.Sprintf("suppression.entries[%d].reason", index), "suppression reason must be hard_bounce, complaint, or manual")
		}
	}
}

func (v *configValidator) validateFromDomain(path string, from string, senderDomain string) {
	if senderDomain == "" {
		v.addError("sender_domain_required", path, "sender_domain is required")
		return
	}

	fromDomain, ok := domain.AddrSpecEmailDomain(from)
	if !ok {
		v.addError("from_invalid", path, "from must be a valid email address")
		return
	}
	if !strings.EqualFold(fromDomain, senderDomain) {
		v.addError("from_domain_mismatch", path, "from domain must equal sender_domain")
	}
}

func (v *configValidator) validateIdentifier(path string, value string) {
	if !identifierPattern.MatchString(value) {
		v.addError("identifier_invalid", path, "identifier must be 1 to 64 characters and contain only lowercase letters, digits, underscores, and hyphens")
	}
}

func (v *configValidator) validateSecretRef(path string, ref string) (ResolvedSecret, bool) {
	return v.resolveSecretRef(path, ref)
}

func (v *configValidator) validateBearerSecretRef(path string, ref string) {
	resolved, ok := v.validateSecretRef(path, ref)
	if ok && !isVisibleASCIIWithoutWhitespaceSecret(resolved.Value) {
		v.addError("secret_value_invalid", path, "webhook bearer secret value must be visible ASCII without whitespace")
	}
}

func (v *configValidator) resolveSecretRef(path string, ref string) (ResolvedSecret, bool) {
	if strings.TrimSpace(ref) == "" {
		v.addError("secret_ref_required", path, "secret reference is required")
		return ResolvedSecret{}, false
	}

	resolved, err := v.resolver.Resolve(ref)
	if err != nil {
		v.addError("secret_ref_invalid", path, err.Error())
		return ResolvedSecret{}, false
	}

	for _, warning := range resolved.Warnings {
		warning.Path = path
		if v.options.StrictPlainSecrets && warning.Code == "plain_secret_ref" {
			v.addError("plain_secret_ref_forbidden", path, "plain secret references are forbidden in strict validation")
			continue
		}
		v.report.Warnings = append(v.report.Warnings, warning)
	}
	if resolved.Value == "" {
		v.addError("secret_value_empty", path, "secret value must not be empty")
		return ResolvedSecret{}, false
	}

	return resolved, true
}

func (v *configValidator) requireValue(path string, value string, allowed []string) {
	for _, allowedValue := range allowed {
		if value == allowedValue {
			return
		}
	}

	v.addError("runtime_invalid", path, fmt.Sprintf("%s must be one of %s", path, strings.Join(allowed, ", ")))
}

func (v *configValidator) addError(code string, path string, message string) {
	v.report.Errors = append(v.report.Errors, ValidationError{
		Code:    code,
		Path:    path,
		Message: message,
	})
}

type suppressionFile struct {
	Entries []suppressionEntry `yaml:"entries"`
}

type suppressionEntry struct {
	App    string `yaml:"app"`
	Email  string `yaml:"email"`
	Reason string `yaml:"reason"`
}

func decodeSuppressionFile(data []byte) (suppressionFile, error) {
	var file suppressionFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return file, nil
		}
		return suppressionFile{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return suppressionFile{}, fmt.Errorf("suppression file must contain a single YAML document")
		}
		return suppressionFile{}, err
	}

	return file, nil
}

func isValidLocale(locale string) bool {
	return localePattern.MatchString(locale)
}

func templateKey(code string, locale string) string {
	return code + "\x00" + locale
}

func suppressionEntryKey(appCode string, normalizedEmail string) string {
	return appCode + "\x00" + normalizedEmail
}

func isASCII(value string) bool {
	for _, char := range value {
		if char > 127 {
			return false
		}
	}

	return true
}

func providerAPIKeyUsesHTTPHeader(provider domain.Provider) bool {
	return provider == domain.ProviderResend || provider == domain.ProviderBrevo
}

func isVisibleASCIIWithoutWhitespaceSecret(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7E {
			return false
		}
	}

	return true
}

func isValidResendWebhookSecretValue(value string) bool {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "whsec_")
	if value == "" {
		return false
	}
	if secret, err := base64.StdEncoding.DecodeString(value); err == nil && len(secret) > 0 {
		return true
	}
	if secret, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(secret) > 0 {
		return true
	}

	return false
}

func isValidRouteDomain(value string) bool {
	if value == "" || len(value) > 253 || strings.TrimSpace(value) != value || value != strings.ToLower(value) || !isASCII(value) {
		return false
	}

	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		for index := 0; index < len(label); index++ {
			char := label[index]
			valid := (char >= 'a' && char <= 'z') ||
				(char >= '0' && char <= '9') ||
				char == '-'
			if !valid {
				return false
			}
			if (index == 0 || index == len(label)-1) && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') {
				return false
			}
		}
	}

	return true
}

func isValidTrustedProxy(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return false
		}
		return isValidTrustedProxyPrefix(prefix)
	}

	_, err := netip.ParseAddr(value)
	return err == nil
}

func isValidTrustedProxyPrefix(prefix netip.Prefix) bool {
	if prefix.Addr().Is4In6() {
		return prefix.Bits() > 96
	}

	return prefix.Bits() > 0
}

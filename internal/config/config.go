package config

import (
	"fmt"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

const (
	defaultListen                   = ":8080"
	defaultReadTimeoutSeconds       = 10
	defaultReadHeaderTimeoutSeconds = 5
	defaultWriteTimeoutSeconds      = 15
	defaultIdleTimeoutSeconds       = 60
	defaultConfigStore              = "file"
	defaultQueue                    = "memory"
	defaultRateLimiter              = "memory"
	defaultMessageLog               = "file"
	defaultStats                    = "off"
	defaultSuppression              = "file"
	defaultProviderTimeoutSeconds   = 10
	defaultMaxAttemptsPerMessage    = 3
	defaultMemoryQueueSize          = 1000
	defaultWorkerConcurrency        = 4
	defaultIdempotencyCacheSize     = 10000
	defaultIdempotencyTTLHours      = 24
	defaultMaxRequestBodyBytes      = 65536
	defaultMaxTemplateVarBytes      = 8192
	defaultMaxContextBytes          = 4096
	defaultLoggingDir               = "data/logs"
	defaultMaxFileSizeMB            = 100
	defaultMaxBackups               = 5
	defaultSuppressionFile          = "data/suppression.yaml"
	fileSecretPrefix                = "file:"
)

var defaultRetryBackoffSeconds = []int{0, 30, 120}

// Config is the root file configuration for a MuxMail process.
type Config struct {
	SourcePath       string                  `yaml:"-"`
	BaseDir          string                  `yaml:"-"`
	SuppressionFile  string                  `yaml:"suppression_file"`
	Server           ServerConfig            `yaml:"server"`
	Runtime          RuntimeConfig           `yaml:"runtime"`
	Defaults         DefaultsConfig          `yaml:"defaults"`
	Apps             []AppConfig             `yaml:"apps"`
	ProviderAccounts []ProviderAccountConfig `yaml:"provider_accounts"`
	ProviderChannels []ProviderChannelConfig `yaml:"provider_channels"`
	Webhooks         WebhookConfig           `yaml:"webhooks"`
	Logging          LoggingConfig           `yaml:"logging"`
}

// ServerConfig contains HTTP server settings.
type ServerConfig struct {
	Listen                   string   `yaml:"listen"`
	ReadTimeoutSeconds       int      `yaml:"read_timeout_seconds"`
	ReadHeaderTimeoutSeconds int      `yaml:"read_header_timeout_seconds"`
	WriteTimeoutSeconds      int      `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds       int      `yaml:"idle_timeout_seconds"`
	TrustedProxies           []string `yaml:"trusted_proxies"`
}

// RuntimeConfig selects the infrastructure implementations used by MuxMail.
type RuntimeConfig struct {
	ConfigStore string `yaml:"config_store"`
	Queue       string `yaml:"queue"`
	RateLimiter string `yaml:"rate_limiter"`
	MessageLog  string `yaml:"message_log"`
	Stats       string `yaml:"stats"`
	Suppression string `yaml:"suppression"`
}

// DefaultsConfig contains operational defaults shared by the API and worker.
type DefaultsConfig struct {
	ProviderTimeoutSeconds int   `yaml:"provider_timeout_seconds"`
	MaxAttemptsPerMessage  int   `yaml:"max_attempts_per_message"`
	RetryBackoffSeconds    []int `yaml:"retry_backoff_seconds"`
	MemoryQueueSize        int   `yaml:"memory_queue_size"`
	WorkerConcurrency      int   `yaml:"worker_concurrency"`
	IdempotencyCacheSize   int   `yaml:"idempotency_cache_size"`
	IdempotencyTTLHours    int   `yaml:"idempotency_ttl_hours"`
	MaxRequestBodyBytes    int   `yaml:"max_request_body_bytes"`
	MaxTemplateVarBytes    int   `yaml:"max_template_var_bytes"`
	MaxContextBytes        int   `yaml:"max_context_bytes"`
}

// AppConfig configures one business application.
type AppConfig struct {
	Code           string           `yaml:"code"`
	Name           string           `yaml:"name"`
	Enabled        *bool            `yaml:"enabled"`
	DefaultLocale  string           `yaml:"default_locale"`
	AllowedLocales []string         `yaml:"allowed_locales"`
	APIKeys        []APIKeyConfig   `yaml:"api_keys"`
	Templates      []TemplateConfig `yaml:"templates"`
	Scenes         []SceneConfig    `yaml:"scenes"`
}

// APIKeyConfig configures one App API key reference.
type APIKeyConfig struct {
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"`
	KeyRef  string `yaml:"key_ref"`
}

// TemplateConfig configures one locale-specific email template.
type TemplateConfig struct {
	Code         string   `yaml:"code"`
	Locale       string   `yaml:"locale"`
	Enabled      *bool    `yaml:"enabled"`
	Subject      string   `yaml:"subject"`
	RequiredVars []string `yaml:"required_vars"`
	HTMLBody     string   `yaml:"html_body"`
	TextBody     string   `yaml:"text_body"`
}

// SceneConfig configures one sending scenario.
type SceneConfig struct {
	Code        string          `yaml:"code"`
	Name        string          `yaml:"name"`
	Enabled     *bool           `yaml:"enabled"`
	Template    string          `yaml:"template"`
	RateLimit   RateLimitConfig `yaml:"rate_limit"`
	RoutePolicy RoutePolicy     `yaml:"route_policy"`
}

// RateLimitConfig defines fixed-window limits for one Scene.
type RateLimitConfig struct {
	SameEmailPerMinute  int `yaml:"same_email_per_minute"`
	SameEmailPerDay     int `yaml:"same_email_per_day"`
	SameUserIPPerHour   int `yaml:"same_user_ip_per_hour"`
	SameCallerIPPerHour int `yaml:"same_caller_ip_per_hour"`
}

// RoutePolicy maps recipient domains to ordered provider channel codes.
type RoutePolicy map[string][]string

// ProviderAccountConfig configures one provider account.
type ProviderAccountConfig struct {
	Code        string            `yaml:"code"`
	Provider    domain.Provider   `yaml:"provider"`
	Enabled     *bool             `yaml:"enabled"`
	Credentials map[string]string `yaml:"credentials"`
}

// ProviderChannelConfig configures one routable provider channel.
type ProviderChannelConfig struct {
	Code         string           `yaml:"code"`
	Account      string           `yaml:"account"`
	Transport    domain.Transport `yaml:"transport"`
	Enabled      *bool            `yaml:"enabled"`
	SenderDomain string           `yaml:"sender_domain"`
	FromName     string           `yaml:"from_name"`
	From         string           `yaml:"from"`
	SMTP         *SMTPConfig      `yaml:"smtp"`
}

// SMTPConfig contains SMTP submission settings for a provider channel.
type SMTPConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Username    string `yaml:"username"`
	PasswordRef string `yaml:"password_ref"`
}

// WebhookConfig contains provider webhook receiver settings.
type WebhookConfig struct {
	Enabled         bool   `yaml:"enabled"`
	SharedSecretRef string `yaml:"shared_secret_ref"`
	ResendSecretRef string `yaml:"resend_secret_ref"`
	BrevoTokenRef   string `yaml:"brevo_token_ref"`
}

// LoggingConfig contains JSONL log output settings.
type LoggingConfig struct {
	Dir           string `yaml:"dir"`
	MaxFileSizeMB int    `yaml:"max_file_size_mb"`
	MaxBackups    int    `yaml:"max_backups"`
}

// LoadFile reads a YAML configuration file, applies MVP defaults, and resolves relative paths.
func LoadFile(configPath string) (*Config, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, fmt.Errorf("config path is required")
	}

	absolutePath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	cfg.SourcePath = absolutePath
	cfg.BaseDir = filepath.Dir(absolutePath)
	cfg.applyDefaults()
	cfg.resolveRelativePaths()

	return &cfg, nil
}

// EnabledValue returns true when an optional enabled field is omitted.
func EnabledValue(enabled *bool) bool {
	if enabled == nil {
		return true
	}

	return *enabled
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = defaultListen
	}
	if c.Server.ReadTimeoutSeconds == 0 {
		c.Server.ReadTimeoutSeconds = defaultReadTimeoutSeconds
	}
	if c.Server.ReadHeaderTimeoutSeconds == 0 {
		c.Server.ReadHeaderTimeoutSeconds = defaultReadHeaderTimeoutSeconds
	}
	if c.Server.WriteTimeoutSeconds == 0 {
		c.Server.WriteTimeoutSeconds = defaultWriteTimeoutSeconds
	}
	if c.Server.IdleTimeoutSeconds == 0 {
		c.Server.IdleTimeoutSeconds = defaultIdleTimeoutSeconds
	}

	if c.Runtime.ConfigStore == "" {
		c.Runtime.ConfigStore = defaultConfigStore
	}
	if c.Runtime.Queue == "" {
		c.Runtime.Queue = defaultQueue
	}
	if c.Runtime.RateLimiter == "" {
		c.Runtime.RateLimiter = defaultRateLimiter
	}
	if c.Runtime.MessageLog == "" {
		c.Runtime.MessageLog = defaultMessageLog
	}
	if c.Runtime.Stats == "" {
		c.Runtime.Stats = defaultStats
	}
	if c.Runtime.Suppression == "" {
		c.Runtime.Suppression = defaultSuppression
	}

	if c.Defaults.ProviderTimeoutSeconds == 0 {
		c.Defaults.ProviderTimeoutSeconds = defaultProviderTimeoutSeconds
	}
	if c.Defaults.MaxAttemptsPerMessage == 0 {
		c.Defaults.MaxAttemptsPerMessage = defaultMaxAttemptsPerMessage
	}
	if len(c.Defaults.RetryBackoffSeconds) == 0 {
		c.Defaults.RetryBackoffSeconds = append([]int(nil), defaultRetryBackoffSeconds...)
	}
	if c.Defaults.MemoryQueueSize == 0 {
		c.Defaults.MemoryQueueSize = defaultMemoryQueueSize
	}
	if c.Defaults.WorkerConcurrency == 0 {
		c.Defaults.WorkerConcurrency = defaultWorkerConcurrency
	}
	if c.Defaults.IdempotencyCacheSize == 0 {
		c.Defaults.IdempotencyCacheSize = defaultIdempotencyCacheSize
	}
	if c.Defaults.IdempotencyTTLHours == 0 {
		c.Defaults.IdempotencyTTLHours = defaultIdempotencyTTLHours
	}
	if c.Defaults.MaxRequestBodyBytes == 0 {
		c.Defaults.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}
	if c.Defaults.MaxTemplateVarBytes == 0 {
		c.Defaults.MaxTemplateVarBytes = defaultMaxTemplateVarBytes
	}
	if c.Defaults.MaxContextBytes == 0 {
		c.Defaults.MaxContextBytes = defaultMaxContextBytes
	}

	if c.Logging.Dir == "" {
		c.Logging.Dir = defaultLoggingDir
	}
	if c.Logging.MaxFileSizeMB == 0 {
		c.Logging.MaxFileSizeMB = defaultMaxFileSizeMB
	}
	if c.Logging.MaxBackups == 0 {
		c.Logging.MaxBackups = defaultMaxBackups
	}
	if c.SuppressionFile == "" {
		c.SuppressionFile = defaultSuppressionFile
	}
}

func (c *Config) resolveRelativePaths() {
	c.Logging.Dir = resolvePath(c.BaseDir, c.Logging.Dir)
	c.SuppressionFile = resolvePath(c.BaseDir, c.SuppressionFile)
	c.Webhooks.SharedSecretRef = resolveSecretFileRef(c.BaseDir, c.Webhooks.SharedSecretRef)
	c.Webhooks.ResendSecretRef = resolveSecretFileRef(c.BaseDir, c.Webhooks.ResendSecretRef)
	c.Webhooks.BrevoTokenRef = resolveSecretFileRef(c.BaseDir, c.Webhooks.BrevoTokenRef)

	for appIndex := range c.Apps {
		for keyIndex := range c.Apps[appIndex].APIKeys {
			key := &c.Apps[appIndex].APIKeys[keyIndex]
			key.KeyRef = resolveSecretFileRef(c.BaseDir, key.KeyRef)
		}
	}

	for accountIndex := range c.ProviderAccounts {
		for name, value := range c.ProviderAccounts[accountIndex].Credentials {
			c.ProviderAccounts[accountIndex].Credentials[name] = resolveSecretFileRef(c.BaseDir, value)
		}
	}

	for channelIndex := range c.ProviderChannels {
		smtp := c.ProviderChannels[channelIndex].SMTP
		if smtp == nil {
			continue
		}
		smtp.PasswordRef = resolveSecretFileRef(c.BaseDir, smtp.PasswordRef)
	}
}

func resolveSecretFileRef(baseDir string, ref string) string {
	if !strings.HasPrefix(ref, fileSecretPrefix) {
		return ref
	}

	secretPath := strings.TrimPrefix(ref, fileSecretPrefix)
	if secretPath == "" {
		return ref
	}

	return fileSecretPrefix + resolvePath(baseDir, secretPath)
}

func resolvePath(baseDir string, targetPath string) string {
	if targetPath == "" {
		return targetPath
	}
	if filepath.IsAbs(targetPath) {
		return filepath.Clean(targetPath)
	}
	if strings.HasPrefix(targetPath, "/") {
		return slashpath.Clean(targetPath)
	}

	return filepath.Clean(filepath.Join(baseDir, targetPath))
}

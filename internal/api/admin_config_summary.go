package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

type adminConfigSummaryResponse struct {
	App              adminAppSummary               `json:"app"`
	Runtime          adminRuntimeSummary           `json:"runtime"`
	Defaults         adminDefaultsSummary          `json:"defaults"`
	ProviderAccounts []adminProviderAccountSummary `json:"provider_accounts"`
	ProviderChannels []adminProviderChannelSummary `json:"provider_channels"`
}

type adminAppSummary struct {
	Code           string                 `json:"code"`
	Name           string                 `json:"name"`
	Enabled        bool                   `json:"enabled"`
	DefaultLocale  string                 `json:"default_locale"`
	AllowedLocales []string               `json:"allowed_locales"`
	APIKeys        []adminAPIKeySummary   `json:"api_keys"`
	Scenes         []adminSceneSummary    `json:"scenes"`
	Templates      []adminTemplateSummary `json:"templates"`
}

type adminAPIKeySummary struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type adminSceneSummary struct {
	Code        string                `json:"code"`
	Name        string                `json:"name"`
	Enabled     bool                  `json:"enabled"`
	Template    string                `json:"template"`
	RateLimit   adminRateLimitSummary `json:"rate_limit"`
	RoutePolicy domain.RoutePolicy    `json:"route_policy"`
}

type adminTemplateSummary struct {
	Code         string   `json:"code"`
	Locale       string   `json:"locale"`
	Enabled      bool     `json:"enabled"`
	Subject      string   `json:"subject"`
	RequiredVars []string `json:"required_vars"`
	HasHTML      bool     `json:"has_html"`
	HasText      bool     `json:"has_text"`
}

type adminRuntimeSummary struct {
	ConfigStore string `json:"config_store"`
	Queue       string `json:"queue"`
	RateLimiter string `json:"rate_limiter"`
	MessageLog  string `json:"message_log"`
	Stats       string `json:"stats"`
	Suppression string `json:"suppression"`
	Webhooks    bool   `json:"webhooks"`
}

type adminDefaultsSummary struct {
	ProviderTimeoutSeconds int   `json:"provider_timeout_seconds"`
	MaxAttemptsPerMessage  int   `json:"max_attempts_per_message"`
	RetryBackoffSeconds    []int `json:"retry_backoff_seconds"`
	MemoryQueueSize        int   `json:"memory_queue_size"`
	WorkerConcurrency      int   `json:"worker_concurrency"`
	IdempotencyCacheSize   int   `json:"idempotency_cache_size"`
	IdempotencyTTLHours    int   `json:"idempotency_ttl_hours"`
	MaxRequestBodyBytes    int   `json:"max_request_body_bytes"`
	MaxTemplateVarBytes    int   `json:"max_template_var_bytes"`
	MaxContextBytes        int   `json:"max_context_bytes"`
}

type adminRateLimitSummary struct {
	SameEmailPerMinute  int `json:"same_email_per_minute"`
	SameEmailPerDay     int `json:"same_email_per_day"`
	SameUserIPPerHour   int `json:"same_user_ip_per_hour"`
	SameCallerIPPerHour int `json:"same_caller_ip_per_hour"`
}

type adminProviderAccountSummary struct {
	Code     string          `json:"code"`
	Provider domain.Provider `json:"provider"`
	Enabled  bool            `json:"enabled"`
}

type adminProviderChannelSummary struct {
	Code         string            `json:"code"`
	Account      string            `json:"account"`
	Provider     domain.Provider   `json:"provider"`
	Transport    domain.Transport  `json:"transport"`
	Enabled      bool              `json:"enabled"`
	SenderDomain string            `json:"sender_domain"`
	FromName     string            `json:"from_name"`
	From         string            `json:"from"`
	SMTP         *adminSMTPSummary `json:"smtp,omitempty"`
}

type adminSMTPSummary struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// handleAdminConfigSummary returns App-scoped Lite admin configuration metadata without secrets.
func (r *Runtime) handleAdminConfigSummary(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processAdminConfigSummary(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processAdminConfigSummary(httpRequest *http.Request) (adminConfigSummaryResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return adminConfigSummaryResponse{}, err
	}

	visibleChannels := appRouteChannels(auth.App)
	accountProviders := make(map[string]domain.Provider, len(r.cfg.ProviderAccounts))
	for _, account := range r.cfg.ProviderAccounts {
		accountProviders[account.Code] = account.Provider
	}

	visibleAccounts := make(map[string]struct{})
	channels := make([]adminProviderChannelSummary, 0, len(r.cfg.ProviderChannels))
	for _, channel := range r.cfg.ProviderChannels {
		if _, ok := visibleChannels[channel.Code]; !ok {
			continue
		}
		visibleAccounts[channel.Account] = struct{}{}
		channels = append(channels, adminProviderChannelSummary{
			Code:         channel.Code,
			Account:      channel.Account,
			Provider:     accountProviders[channel.Account],
			Transport:    channel.Transport,
			Enabled:      config.EnabledValue(channel.Enabled),
			SenderDomain: channel.SenderDomain,
			FromName:     channel.FromName,
			From:         channel.From,
			SMTP:         adminSMTPFromConfig(channel.SMTP),
		})
	}
	sort.Slice(channels, func(left int, right int) bool {
		return channels[left].Code < channels[right].Code
	})

	accounts := make([]adminProviderAccountSummary, 0, len(visibleAccounts))
	for _, account := range r.cfg.ProviderAccounts {
		if _, ok := visibleAccounts[account.Code]; !ok {
			continue
		}
		accounts = append(accounts, adminProviderAccountSummary{
			Code:     account.Code,
			Provider: account.Provider,
			Enabled:  config.EnabledValue(account.Enabled),
		})
	}
	sort.Slice(accounts, func(left int, right int) bool {
		return accounts[left].Code < accounts[right].Code
	})

	return adminConfigSummaryResponse{
		App:              adminAppFromDomain(auth.App),
		Runtime:          adminRuntimeFromConfig(r.cfg),
		Defaults:         adminDefaultsFromConfig(r.cfg.Defaults),
		ProviderAccounts: accounts,
		ProviderChannels: channels,
	}, nil
}

func appRouteChannels(app domain.App) map[string]struct{} {
	channels := make(map[string]struct{})
	for _, scene := range app.Scenes {
		for _, routeChannels := range scene.RoutePolicy {
			for _, channel := range routeChannels {
				if channel != "" {
					channels[channel] = struct{}{}
				}
			}
		}
	}

	return channels
}

func adminAppFromDomain(app domain.App) adminAppSummary {
	keys := make([]adminAPIKeySummary, 0, len(app.APIKeys))
	for _, key := range app.APIKeys {
		keys = append(keys, adminAPIKeySummary{
			Name:    key.Name,
			Enabled: key.Enabled,
		})
	}
	sort.Slice(keys, func(left int, right int) bool {
		return keys[left].Name < keys[right].Name
	})

	scenes := make([]adminSceneSummary, 0, len(app.Scenes))
	for _, scene := range app.Scenes {
		scenes = append(scenes, adminSceneSummary{
			Code:        scene.Code,
			Name:        scene.Name,
			Enabled:     scene.Enabled,
			Template:    scene.Template,
			RateLimit:   adminRateLimitFromDomain(scene.RateLimit),
			RoutePolicy: copyRoutePolicy(scene.RoutePolicy),
		})
	}
	sort.Slice(scenes, func(left int, right int) bool {
		return scenes[left].Code < scenes[right].Code
	})

	templates := make([]adminTemplateSummary, 0, len(app.Templates))
	for _, template := range app.Templates {
		templates = append(templates, adminTemplateSummary{
			Code:         template.Code,
			Locale:       template.Locale,
			Enabled:      template.Enabled,
			Subject:      template.Subject,
			RequiredVars: append([]string(nil), template.RequiredVars...),
			HasHTML:      template.HTMLBody != "",
			HasText:      template.TextBody != "",
		})
	}
	sort.Slice(templates, func(left int, right int) bool {
		if templates[left].Code == templates[right].Code {
			return templates[left].Locale < templates[right].Locale
		}
		return templates[left].Code < templates[right].Code
	})

	return adminAppSummary{
		Code:           app.Code,
		Name:           app.Name,
		Enabled:        app.Enabled,
		DefaultLocale:  app.DefaultLocale,
		AllowedLocales: append([]string(nil), app.AllowedLocales...),
		APIKeys:        keys,
		Scenes:         scenes,
		Templates:      templates,
	}
}

func adminDefaultsFromConfig(defaults config.DefaultsConfig) adminDefaultsSummary {
	return adminDefaultsSummary{
		ProviderTimeoutSeconds: defaults.ProviderTimeoutSeconds,
		MaxAttemptsPerMessage:  defaults.MaxAttemptsPerMessage,
		RetryBackoffSeconds:    append([]int(nil), defaults.RetryBackoffSeconds...),
		MemoryQueueSize:        defaults.MemoryQueueSize,
		WorkerConcurrency:      defaults.WorkerConcurrency,
		IdempotencyCacheSize:   defaults.IdempotencyCacheSize,
		IdempotencyTTLHours:    defaults.IdempotencyTTLHours,
		MaxRequestBodyBytes:    defaults.MaxRequestBodyBytes,
		MaxTemplateVarBytes:    defaults.MaxTemplateVarBytes,
		MaxContextBytes:        defaults.MaxContextBytes,
	}
}

func adminRateLimitFromDomain(policy domain.RateLimitPolicy) adminRateLimitSummary {
	return adminRateLimitSummary{
		SameEmailPerMinute:  policy.SameEmailPerMinute,
		SameEmailPerDay:     policy.SameEmailPerDay,
		SameUserIPPerHour:   policy.SameUserIPPerHour,
		SameCallerIPPerHour: policy.SameCallerIPPerHour,
	}
}

func adminRuntimeFromConfig(cfg *config.Config) adminRuntimeSummary {
	return adminRuntimeSummary{
		ConfigStore: cfg.Runtime.ConfigStore,
		Queue:       cfg.Runtime.Queue,
		RateLimiter: cfg.Runtime.RateLimiter,
		MessageLog:  cfg.Runtime.MessageLog,
		Stats:       cfg.Runtime.Stats,
		Suppression: cfg.Runtime.Suppression,
		Webhooks:    cfg.Webhooks.Enabled,
	}
}

func adminSMTPFromConfig(smtp *config.SMTPConfig) *adminSMTPSummary {
	if smtp == nil {
		return nil
	}

	return &adminSMTPSummary{
		Host: smtp.Host,
		Port: smtp.Port,
	}
}

func copyRoutePolicy(policy domain.RoutePolicy) domain.RoutePolicy {
	copied := make(domain.RoutePolicy, len(policy))
	for key, channels := range policy {
		copied[key] = append([]string(nil), channels...)
	}

	return copied
}

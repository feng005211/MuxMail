package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/muxmail/muxmail/internal/config"
)

func TestRuntimeHealthAndReadyEndpoints(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	tests := []struct {
		path string
		code int
		body string
	}{
		{path: "/healthz", code: http.StatusOK, body: `{"status":"ok"}`},
		{path: "/readyz", code: http.StatusOK, body: `{"status":"ok"}`},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)

			runtime.Handler().ServeHTTP(recorder, request)

			if recorder.Code != tt.code {
				t.Fatalf("expected status %d, got %d", tt.code, recorder.Code)
			}
			if recorder.Body.String() != tt.body {
				t.Fatalf("expected body %q, got %q", tt.body, recorder.Body.String())
			}
			if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("expected CORS headers to be absent")
			}
		})
	}
}

func TestRuntimeReadyAfterCloseReturnsNotReady(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", recorder.Code)
	}
	if recorder.Body.String() != `{"status":"not_ready"}` {
		t.Fatalf("unexpected body: %q", recorder.Body.String())
	}
}

func TestRuntimeOptionsReturnsNotFound(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected OPTIONS to return 404, got %d", recorder.Code)
	}
}

func TestRuntimeInitializesStatsFileWhenEnabled(t *testing.T) {
	cfg := testRuntimeConfig(t, "file")
	runtime, err := NewRuntime(cfg, config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	if _, err := filepath.Abs(cfg.Logging.Dir); err != nil {
		t.Fatalf("expected logging dir to be usable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Logging.Dir, "mail-stats.jsonl")); err != nil {
		t.Fatalf("expected stats file to be created: %v", err)
	}
}

func openTestRuntime(t *testing.T, statsMode string) *Runtime {
	t.Helper()

	runtime, err := NewRuntime(testRuntimeConfig(t, statsMode), config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}

	return runtime
}

func testRuntimeConfig(t *testing.T, statsMode string) *config.Config {
	t.Helper()

	return &config.Config{
		SuppressionFile: filepath.Join(t.TempDir(), "suppression.yaml"),
		Server: config.ServerConfig{
			Listen:                   "127.0.0.1:0",
			ReadTimeoutSeconds:       10,
			ReadHeaderTimeoutSeconds: 5,
			WriteTimeoutSeconds:      15,
			IdleTimeoutSeconds:       60,
		},
		Runtime: config.RuntimeConfig{
			ConfigStore: "file",
			Queue:       "memory",
			RateLimiter: "memory",
			MessageLog:  "file",
			Stats:       statsMode,
			Suppression: "file",
		},
		Defaults: config.DefaultsConfig{
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
		Logging: config.LoggingConfig{
			Dir:           t.TempDir(),
			MaxFileSizeMB: 100,
			MaxBackups:    5,
		},
		Apps: []config.AppConfig{
			{
				Code:           "project_a",
				Name:           "Project A",
				DefaultLocale:  "en-US",
				AllowedLocales: []string{"en-US", "zh-CN"},
				APIKeys: []config.APIKeyConfig{
					{Name: "default", KeyRef: "plain:" + testAPIKey},
				},
				Templates: []config.TemplateConfig{
					{
						Code:         "register_code_v1",
						Locale:       "en-US",
						Subject:      "Your code is {{ .code }}",
						RequiredVars: []string{"code"},
						TextBody:     "Your code is {{ .code }}.",
					},
					{
						Code:         "register_code_v1",
						Locale:       "zh-CN",
						Subject:      "Code {{ .code }}",
						RequiredVars: []string{"code"},
						TextBody:     "Code {{ .code }}.",
					},
				},
				Scenes: []config.SceneConfig{
					{
						Code:     "register_code",
						Name:     "Register verification code",
						Template: "register_code_v1",
						RateLimit: config.RateLimitConfig{
							SameEmailPerMinute:  10,
							SameEmailPerDay:     10,
							SameUserIPPerHour:   10,
							SameCallerIPPerHour: 10,
						},
						RoutePolicy: config.RoutePolicy{
							"*": []string{"mock_auth_api", "mock_auth_backup"},
						},
					},
				},
			},
		},
		ProviderAccounts: []config.ProviderAccountConfig{
			{
				Code:     "mock_main",
				Provider: "mock",
			},
		},
		ProviderChannels: []config.ProviderChannelConfig{
			{
				Code:         "mock_auth_api",
				Account:      "mock_main",
				Transport:    "api",
				SenderDomain: "auth.example.com",
				FromName:     "MuxMail",
				From:         "no-reply@auth.example.com",
			},
			{
				Code:         "mock_auth_backup",
				Account:      "mock_main",
				Transport:    "api",
				SenderDomain: "auth-bak.example.com",
				FromName:     "MuxMail",
				From:         "no-reply@auth-bak.example.com",
			},
		},
	}
}

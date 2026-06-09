package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muxmail/muxmail"
	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

func TestRuntimeHealthEndpoint(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	tests := []struct {
		path string
		code int
		body string
	}{
		{path: "/healthz", code: http.StatusOK, body: `{"status":"ok"}`},
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

func TestRuntimeReadyBeforeServeReturnsNotReady(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

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

func TestRuntimeVersionEndpointReturnsEmbeddedVersion(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/version", nil)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if body.Version != muxmail.Version() {
		t.Fatalf("expected version %q, got %q", muxmail.Version(), body.Version)
	}
}

func TestRuntimeV1ResponsesAreNotCached(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected v1 no-store cache control, got %q", recorder.Header().Get("Cache-Control"))
	}
	if recorder.Header().Get("Pragma") != "no-cache" || recorder.Header().Get("Expires") != "0" {
		t.Fatalf("expected legacy no-cache headers, got pragma=%q expires=%q", recorder.Header().Get("Pragma"), recorder.Header().Get("Expires"))
	}
}

func TestRuntimeAdminIndexUsesNoStoreHeaders(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected admin HTML no-store cache control, got %q", recorder.Header().Get("Cache-Control"))
	}
	if recorder.Header().Get("Pragma") != "no-cache" || recorder.Header().Get("Expires") != "0" {
		t.Fatalf("expected admin HTML legacy no-cache headers, got pragma=%q expires=%q", recorder.Header().Get("Pragma"), recorder.Header().Get("Expires"))
	}
}

func TestRuntimeAdminRejectsUnsafePathsBeforeMuxRedirects(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	for _, path := range []string{
		"/admin/../config.yaml",
		"/admin/%2e%2e/config.yaml",
		"/admin/%252e%252e/config.yaml",
		"/admin//assets/app.js",
		"/admin/assets%2fapp.js",
		"/admin/assets\\app.js",
		"/admin/assets%5capp.js",
		"/admin/assets/app.js:stream",
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			runtime.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d", recorder.Code)
			}
			if recorder.Header().Get("Location") != "" {
				t.Fatalf("expected unsafe admin path not to redirect, got %q", recorder.Header().Get("Location"))
			}
			if recorder.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatalf("expected admin security headers, got %q", recorder.Header().Get("X-Frame-Options"))
			}
		})
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

func TestRuntimeWithNowControlsLiteComponentClocks(t *testing.T) {
	fixed := time.Date(2026, 5, 28, 4, 4, 5, 123456789, time.UTC)
	cfg := testRuntimeConfig(t, "file")
	runtime, err := NewRuntime(cfg, config.NewSecretResolver(), WithNow(func() time.Time {
		return fixed
	}))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	message := domain.Message{
		RequestID: "req_clock",
		MessageID: "msg_clock",
		AppCode:   "project_a",
		SceneCode: "register_code",
		Status:    domain.MessageStatusQueued,
	}
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}
	snapshot, found, err := runtime.MessageLog().FindLatestMessage("project_a", "msg_clock")
	if err != nil {
		t.Fatalf("find message: %v", err)
	}
	if !found {
		t.Fatal("expected message snapshot")
	}
	if !snapshot.Timestamp.Equal(fixed) {
		t.Fatalf("expected message log timestamp %s, got %s", fixed, snapshot.Timestamp)
	}

	if err := runtime.Stats().Record(lite.StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    lite.MetricMessagesQueued,
		Value:     1,
	}); err != nil {
		t.Fatalf("record stat: %v", err)
	}
	summary, err := runtime.Stats().Summary("project_a", fixed.Add(-time.Minute), fixed.Add(time.Minute))
	if err != nil {
		t.Fatalf("summarize stats: %v", err)
	}
	if got := summary.Metrics[lite.MetricMessagesQueued]; got != 1 {
		t.Fatalf("expected fixed-clock stat to be included, got %g in %+v", got, summary.Metrics)
	}

	if err := runtime.Queue().Enqueue(lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	task, err := runtime.Queue().Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue task: %v", err)
	}
	if !task.AvailableAt.Equal(fixed) {
		t.Fatalf("expected queue available_at %s, got %s", fixed, task.AvailableAt)
	}
}

func TestRuntimeServeContextCancelKeepsLiteResourcesOpen(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Serve(ctx); err != nil {
		t.Fatalf("expected canceled serve to shut down HTTP cleanly: %v", err)
	}
	if runtime.ready.Load() {
		t.Fatal("expected runtime readiness to be disabled after HTTP shutdown")
	}

	message := domain.Message{
		RequestID: "req_after_http_shutdown",
		MessageID: "msg_after_http_shutdown",
		AppCode:   "project_a",
		SceneCode: "register_code",
		Status:    domain.MessageStatusQueued,
	}
	if err := runtime.Queue().Enqueue(lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("expected queue to remain open until runtime close: %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(message); err != nil {
		t.Fatalf("expected message log to remain open until runtime close: %v", err)
	}
}

func TestRuntimeShutdownNilContextClosesLiteResources(t *testing.T) {
	runtime := openTestRuntime(t, "off")

	if err := runtime.Shutdown(nil); err != nil {
		t.Fatalf("shutdown with nil context: %v", err)
	}

	message := domain.Message{
		RequestID: "req_after_shutdown",
		MessageID: "msg_after_shutdown",
		AppCode:   "project_a",
		SceneCode: "register_code",
		Status:    domain.MessageStatusQueued,
	}
	if err := runtime.Queue().Enqueue(lite.QueueTask{Message: message, AttemptNo: 1}); !errors.Is(err, lite.ErrMemoryQueueClosed) {
		t.Fatalf("expected shutdown to close queue, got %v", err)
	}
	if err := runtime.MessageLog().AppendMessage(message); err == nil {
		t.Fatal("expected shutdown to close message log")
	}
}

func TestRuntimeServeReadyCallbackReportsBoundAddress(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- runtime.Serve(ctx, func(address string) {
			ready <- address
		})
	}()

	var address string
	select {
	case address = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected runtime serve ready callback")
	}
	if address == "" || strings.HasSuffix(address, ":0") {
		t.Fatalf("expected actual bound listen address, got %q", address)
	}
	if !runtime.ready.Load() {
		t.Fatal("expected runtime to be ready after serve callback")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected canceled serve to shut down cleanly: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected runtime serve to stop after cancel")
	}
}

func TestRuntimeServeMarksNotReadyWhenServerStops(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runtime.Serve(context.Background(), func(string) {
			ready <- struct{}{}
		})
	}()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected runtime serve ready callback")
	}
	if err := runtime.server.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected server close to stop serve cleanly: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected runtime serve to stop after server close")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readyz 503 after server stop, got %d", recorder.Code)
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

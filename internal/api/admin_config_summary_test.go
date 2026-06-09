package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

func TestAdminConfigSummaryReturnsAppScopedSafeConfig(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response adminConfigSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.App.Code != "project_a" {
		t.Fatalf("expected project_a app, got %q", response.App.Code)
	}
	if len(response.App.APIKeys) != 1 || response.App.APIKeys[0].Name != "default" {
		t.Fatalf("expected API key metadata without secret values, got %#v", response.App.APIKeys)
	}
	if !response.App.APIKeys[0].Enabled {
		t.Fatalf("expected omitted API key enabled field to default to true")
	}
	if len(response.ProviderChannels) != 2 {
		t.Fatalf("expected provider channels, got %#v", response.ProviderChannels)
	}

	body := recorder.Body.String()
	blockedFragments := []string{
		testAPIKey,
		"key_hash",
		"key_ref",
		"credentials",
		"password_ref",
		"logging_dir",
		"plain:",
	}
	for _, fragment := range blockedFragments {
		if strings.Contains(body, fragment) {
			t.Fatalf("expected response to omit %q: %s", fragment, body)
		}
	}
}

func TestAdminConfigSummaryRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)

	runtime.Handler().ServeHTTP(recorder, request)

	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func TestAdminConfigSummaryRejectsDisabledApp(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	disabled := false
	cfg.Apps[0].Enabled = &disabled
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)

	runtime.Handler().ServeHTTP(recorder, request)

	assertErrorResponse(t, recorder, http.StatusForbidden, domain.ErrorCodeAppDisabled)
}

func TestAdminStaticServesEmbeddedIndex(t *testing.T) {
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, t.TempDir())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff security header, got %q", recorder.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("expected admin CSP header, got %q", recorder.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(recorder.Body.String(), "MuxMail Admin") {
		t.Fatalf("expected embedded admin fallback, got %q", recorder.Body.String())
	}
}

func TestAdminStaticServesLocalDistBeforeEmbeddedFallback(t *testing.T) {
	localRoot := t.TempDir()
	localDist := filepath.Join(localRoot, "dist")
	if err := os.Mkdir(localDist, 0o700); err != nil {
		t.Fatalf("create local dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "package.json"), []byte(`{"name":"@muxmail/admin"}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "vite.config.ts"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("write vite config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDist, "index.html"), []byte("<!doctype html><title>Local Admin</title>"), 0o600); err != nil {
		t.Fatalf("write local index: %v", err)
	}
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, localDist)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Local Admin") {
		t.Fatalf("expected local admin dist to be served, got %q", body)
	}
	if strings.Contains(body, "has not been built") {
		t.Fatalf("expected local dist before embedded placeholder, got %q", body)
	}
}

func TestAdminStaticIgnoresLocalDistOutsideSourceTree(t *testing.T) {
	localDist := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDist, "index.html"), []byte("<!doctype html><title>Local Admin</title>"), 0o600); err != nil {
		t.Fatalf("write local index: %v", err)
	}
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, localDist)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "Local Admin") {
		t.Fatalf("expected non-source-tree local dist to be ignored, got %q", body)
	}
	if !strings.Contains(body, "MuxMail Admin") {
		t.Fatalf("expected embedded fallback, got %q", body)
	}
}

func TestAdminStaticIgnoresLocalDistWithWrongPackageName(t *testing.T) {
	localRoot := t.TempDir()
	localDist := filepath.Join(localRoot, "dist")
	if err := os.Mkdir(localDist, 0o700); err != nil {
		t.Fatalf("create local dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "package.json"), []byte(`{"name":"other-admin"}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "vite.config.ts"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("write vite config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDist, "index.html"), []byte("<!doctype html><title>Local Admin</title>"), 0o600); err != nil {
		t.Fatalf("write local index: %v", err)
	}
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, localDist)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "Local Admin") {
		t.Fatalf("expected local dist with wrong package name to be ignored, got %q", body)
	}
	if !strings.Contains(body, "MuxMail Admin") {
		t.Fatalf("expected embedded fallback, got %q", body)
	}
}

func TestAdminStaticDoesNotOverrideBuiltEmbeddedDist(t *testing.T) {
	localRoot := t.TempDir()
	localDist := filepath.Join(localRoot, "dist")
	if err := os.Mkdir(localDist, 0o700); err != nil {
		t.Fatalf("create local dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "package.json"), []byte(`{"name":"@muxmail/admin"}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "vite.config.ts"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("write vite config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDist, "index.html"), []byte("<!doctype html><title>Local Admin</title>"), 0o600); err != nil {
		t.Fatalf("write local index: %v", err)
	}
	embedded := fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>Embedded Admin</title>"),
		},
	}

	if shouldUseLocalAdminDist(embedded, localDist) {
		t.Fatal("expected built embedded admin assets to take precedence over local dist")
	}
}

func TestAdminRedirectSupportsHeadWithoutBody(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodHead, "/admin", nil)

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("expected status 301, got %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected HEAD response body to be empty, got %q", recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location != "/admin/" {
		t.Fatalf("expected redirect location /admin/, got %q", location)
	}
	if recorder.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected redirect security header, got %q", recorder.Header().Get("X-Frame-Options"))
	}
	if recorder.Header().Get("Permissions-Policy") != "camera=(), microphone=(), geolocation=(), payment=()" {
		t.Fatalf("expected redirect permissions policy header, got %q", recorder.Header().Get("Permissions-Policy"))
	}
}

func TestAdminStaticServesEmbeddedAssets(t *testing.T) {
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, t.TempDir())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/assets/admin-placeholder.txt", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "embedded asset placeholder") {
		t.Fatalf("expected embedded asset body, got %q", recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") == "no-store" {
		t.Fatalf("expected asset cache header not to use admin HTML no-store")
	}
}

func TestAdminStaticServesIndexForSPARoutes(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	for _, path := range []string{"/admin/", "/admin/messages"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)

			runtime.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), "MuxMail Admin") {
				t.Fatalf("expected admin index, got %q", recorder.Body.String())
			}
		})
	}
}

func TestAdminStaticServesDirectIndexWithNoStoreHeaders(t *testing.T) {
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, t.TempDir())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/index.html", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "MuxMail Admin") {
		t.Fatalf("expected admin index, got %q", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected html content type, got %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected direct index no-store cache control, got %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestAdminStaticServesHeadIndexWithoutBody(t *testing.T) {
	handler := adminFileHandlerWithLocalDist(embeddedAdminDist, t.TempDir())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodHead, "/admin/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected HEAD response body to be empty, got %q", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected html content type, got %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected HEAD index no-store cache control, got %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestAdminStaticServesExistingAssets(t *testing.T) {
	handler := http.StripPrefix("/admin/", spaFileServer(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
		"assets/app.js": {
			Data: []byte("console.log('muxmail admin');"),
		},
	}, http.FileServer(http.FS(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
		"assets/app.js": {
			Data: []byte("console.log('muxmail admin');"),
		},
	}))))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "muxmail admin") {
		t.Fatalf("expected asset body, got %q", recorder.Body.String())
	}
}

func TestAdminStaticRejectsUnsafePaths(t *testing.T) {
	handler := http.StripPrefix("/admin/", spaFileServer(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
	}, http.FileServer(http.FS(fstest.MapFS{}))))

	for _, path := range []string{"/admin/../config.yaml", "/admin/%2e%2e/config.yaml", "/admin/%252e%252e/config.yaml", "/admin/assets\\app.js", "/admin/assets%5capp.js", "/admin/assets/app.js:stream"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d", recorder.Code)
			}
		})
	}
}

func TestAdminStaticReturnsNotFoundForMissingAssets(t *testing.T) {
	handler := http.StripPrefix("/admin/", spaFileServer(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
	}, http.FileServer(http.FS(fstest.MapFS{}))))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/assets/missing.js", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
}

func TestAdminStaticReturnsNotFoundForAssetDirectories(t *testing.T) {
	handler := http.StripPrefix("/admin/", spaFileServer(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
		"assets/app.js": {
			Data: []byte("console.log('muxmail admin');"),
		},
	}, http.FileServer(http.FS(fstest.MapFS{}))))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/assets", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
}

func TestAdminStaticReturnsNotFoundForTrailingSlashAssetDirectory(t *testing.T) {
	handler := http.StripPrefix("/admin/", spaFileServer(fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><title>MuxMail Admin</title>"),
		},
		"assets/app.js": {
			Data: []byte("console.log('muxmail admin');"),
		},
	}, http.FileServer(http.FS(fstest.MapFS{}))))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/assets/", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
}

func TestAdminConfigSummaryOnlyReturnsRoutedChannelsForAuthenticatedApp(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.ProviderAccounts = append(cfg.ProviderAccounts, config.ProviderAccountConfig{
		Code:     "unused_account",
		Provider: "mock",
	})
	cfg.ProviderChannels = append(cfg.ProviderChannels, config.ProviderChannelConfig{
		Code:         "unused_channel",
		Account:      "unused_account",
		Transport:    "api",
		SenderDomain: "unused.example.com",
		From:         "no-reply@unused.example.com",
	})
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "unused_channel") || strings.Contains(recorder.Body.String(), "unused_account") {
		t.Fatalf("expected unrouted provider resources to be omitted: %s", recorder.Body.String())
	}
}

func TestAdminConfigSummaryReturnsOnlyAuthenticatedAppResources(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps = append(cfg.Apps, config.AppConfig{
		Code:           "project_b",
		Name:           "Project B",
		DefaultLocale:  "en-US",
		AllowedLocales: []string{"en-US"},
		APIKeys: []config.APIKeyConfig{
			{Name: "default", KeyRef: "plain:mk_test_project_b_key_123456"},
		},
		Templates: []config.TemplateConfig{
			{
				Code:         "reset_password_v1",
				Locale:       "en-US",
				Subject:      "Reset password",
				RequiredVars: []string{"link"},
				TextBody:     "Reset password: {{ .link }}",
			},
		},
		Scenes: []config.SceneConfig{
			{
				Code:     "reset_password",
				Name:     "Reset password",
				Template: "reset_password_v1",
				RateLimit: config.RateLimitConfig{
					SameEmailPerMinute:  10,
					SameEmailPerDay:     10,
					SameUserIPPerHour:   10,
					SameCallerIPPerHour: 10,
				},
				RoutePolicy: config.RoutePolicy{
					"*": []string{"project_b_auth_api"},
				},
			},
		},
	})
	cfg.ProviderAccounts = append(cfg.ProviderAccounts, config.ProviderAccountConfig{
		Code:     "project_b_account",
		Provider: "mock",
	})
	cfg.ProviderChannels = append(cfg.ProviderChannels, config.ProviderChannelConfig{
		Code:         "project_b_auth_api",
		Account:      "project_b_account",
		Transport:    "api",
		SenderDomain: "auth-b.example.com",
		From:         "no-reply@auth-b.example.com",
	})
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer mk_test_project_b_key_123456")

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response adminConfigSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.App.Code != "project_b" {
		t.Fatalf("expected project_b app, got %q", response.App.Code)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{"project_a", "register_code", "register_code_v1", "mock_auth_api", "mock_auth_backup"} {
		if strings.Contains(body, fragment) {
			t.Fatalf("expected project_a resources to be omitted, found %q in %s", fragment, body)
		}
	}
	if !strings.Contains(body, "project_b_auth_api") || !strings.Contains(body, "reset_password") {
		t.Fatalf("expected authenticated app resources in response: %s", body)
	}
}

func TestAdminConfigSummaryOmitsSMTPCredentialMetadata(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Apps[0].Scenes[0].RoutePolicy["*"] = []string{"resend_auth_smtp"}
	cfg.ProviderAccounts = append(cfg.ProviderAccounts, config.ProviderAccountConfig{
		Code:     "resend_main",
		Provider: "resend",
		Credentials: map[string]string{
			"api_key": "plain:resend_example_secret",
		},
	})
	cfg.ProviderChannels = append(cfg.ProviderChannels, config.ProviderChannelConfig{
		Code:         "resend_auth_smtp",
		Account:      "resend_main",
		Transport:    "smtp",
		SenderDomain: "auth-smtp.example.com",
		FromName:     "MuxMail",
		From:         "no-reply@auth-smtp.example.com",
		SMTP: &config.SMTPConfig{
			Host:        "smtp.resend.com",
			Port:        587,
			Username:    "resend",
			PasswordRef: "plain:smtp_example_secret",
		},
	})
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, fragment := range []string{"username", "password_ref", "credentials", "resend_example_secret", "smtp_example_secret", "plain:"} {
		if strings.Contains(body, fragment) {
			t.Fatalf("expected SMTP credential metadata to be omitted, found %q in %s", fragment, body)
		}
	}
	if !strings.Contains(body, "smtp.resend.com") {
		t.Fatalf("expected safe SMTP host metadata to be present: %s", body)
	}
}

func TestAdminConfigSummaryOmitsWebhookSecretMetadata(t *testing.T) {
	cfg := testRuntimeConfig(t, "off")
	cfg.Webhooks = config.WebhookConfig{
		Enabled:         true,
		SharedSecretRef: "plain:webhook_shared_secret",
		ResendSecretRef: "plain:" + testResendWebhookSecret,
		BrevoTokenRef:   "plain:brevo_webhook_token",
	}
	runtime := openRuntimeFromConfig(t, cfg)
	defer runtime.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config-summary", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)

	runtime.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, fragment := range []string{
		"shared_secret_ref",
		"resend_secret_ref",
		"brevo_token_ref",
		"webhook_shared_secret",
		testResendWebhookSecret,
		"brevo_webhook_token",
		"plain:",
	} {
		if strings.Contains(body, fragment) {
			t.Fatalf("expected webhook secret metadata to be omitted, found %q in %s", fragment, body)
		}
	}
	if !strings.Contains(body, `"webhooks":true`) {
		t.Fatalf("expected safe webhook enabled metadata to be present: %s", body)
	}
}

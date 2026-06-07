package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeTestConfig(t, configPath, "apps: []\n")

	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}

	if cfg.SourcePath != configPath {
		t.Fatalf("expected source path %q, got %q", configPath, cfg.SourcePath)
	}
	if cfg.BaseDir != dir {
		t.Fatalf("expected base dir %q, got %q", dir, cfg.BaseDir)
	}
	if cfg.Server.Listen != ":8080" {
		t.Fatalf("expected default listen :8080, got %q", cfg.Server.Listen)
	}
	if cfg.Server.ReadTimeoutSeconds != 10 {
		t.Fatalf("expected default read timeout 10, got %d", cfg.Server.ReadTimeoutSeconds)
	}
	if cfg.Server.ReadHeaderTimeoutSeconds != 5 {
		t.Fatalf("expected default read header timeout 5, got %d", cfg.Server.ReadHeaderTimeoutSeconds)
	}
	if cfg.Server.WriteTimeoutSeconds != 15 {
		t.Fatalf("expected default write timeout 15, got %d", cfg.Server.WriteTimeoutSeconds)
	}
	if cfg.Server.IdleTimeoutSeconds != 60 {
		t.Fatalf("expected default idle timeout 60, got %d", cfg.Server.IdleTimeoutSeconds)
	}
	if cfg.Runtime.ConfigStore != "file" || cfg.Runtime.Queue != "memory" || cfg.Runtime.RateLimiter != "memory" {
		t.Fatalf("expected Lite runtime defaults, got %+v", cfg.Runtime)
	}
	if cfg.Runtime.MessageLog != "file" || cfg.Runtime.Stats != "off" || cfg.Runtime.Suppression != "file" {
		t.Fatalf("expected Lite log/stat/suppression defaults, got %+v", cfg.Runtime)
	}
	if cfg.Defaults.ProviderTimeoutSeconds != 10 {
		t.Fatalf("expected provider timeout 10, got %d", cfg.Defaults.ProviderTimeoutSeconds)
	}
	if cfg.Defaults.MaxAttemptsPerMessage != 3 {
		t.Fatalf("expected max attempts 3, got %d", cfg.Defaults.MaxAttemptsPerMessage)
	}
	if got := cfg.Defaults.RetryBackoffSeconds; len(got) != 3 || got[0] != 0 || got[1] != 30 || got[2] != 120 {
		t.Fatalf("expected default retry backoff [0 30 120], got %v", got)
	}
	if cfg.Defaults.MemoryQueueSize != 1000 || cfg.Defaults.WorkerConcurrency != 4 {
		t.Fatalf("expected Lite queue defaults, got %+v", cfg.Defaults)
	}
	if cfg.Defaults.IdempotencyCacheSize != 10000 || cfg.Defaults.IdempotencyTTLHours != 24 {
		t.Fatalf("expected idempotency defaults, got %+v", cfg.Defaults)
	}
	if cfg.Defaults.MaxRequestBodyBytes != 65536 || cfg.Defaults.MaxTemplateVarBytes != 8192 || cfg.Defaults.MaxContextBytes != 4096 {
		t.Fatalf("expected request size defaults, got %+v", cfg.Defaults)
	}
	if cfg.Logging.MaxFileSizeMB != 100 || cfg.Logging.MaxBackups != 5 {
		t.Fatalf("expected logging defaults, got %+v", cfg.Logging)
	}
}

func TestLoadFileFailsOnMissingFile(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing config file to fail")
	}
}

func TestLoadFileRequiresPath(t *testing.T) {
	_, err := LoadFile("")
	if err == nil {
		t.Fatal("expected empty config path to fail")
	}
}

func TestLoadFileResolvesRelativePathsFromConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeTestConfig(t, configPath, `
logging:
  dir: logs
suppression_file: suppressions/custom.yaml
apps:
  - code: project_a
    api_keys:
      - name: default
        key_ref: file:secrets/app.key
provider_accounts:
  - code: resend_main
    provider: resend
    credentials:
      api_key: file:secrets/resend.key
      public_label: plain:example
provider_channels:
  - code: resend_auth_smtp
    account: resend_main
    transport: smtp
    smtp:
      host: smtp.resend.com
      port: 587
      username: resend
      password_ref: file:secrets/smtp.key
`)

	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}

	wantLoggingDir := filepath.Join(dir, "logs")
	if cfg.Logging.Dir != wantLoggingDir {
		t.Fatalf("expected logging dir %q, got %q", wantLoggingDir, cfg.Logging.Dir)
	}

	wantSuppressionFile := filepath.Join(dir, "suppressions", "custom.yaml")
	if cfg.SuppressionFile != wantSuppressionFile {
		t.Fatalf("expected suppression file %q, got %q", wantSuppressionFile, cfg.SuppressionFile)
	}

	wantAPIKeyRef := "file:" + filepath.Join(dir, "secrets", "app.key")
	if cfg.Apps[0].APIKeys[0].KeyRef != wantAPIKeyRef {
		t.Fatalf("expected app key ref %q, got %q", wantAPIKeyRef, cfg.Apps[0].APIKeys[0].KeyRef)
	}

	wantCredentialRef := "file:" + filepath.Join(dir, "secrets", "resend.key")
	if cfg.ProviderAccounts[0].Credentials["api_key"] != wantCredentialRef {
		t.Fatalf("expected provider credential ref %q, got %q", wantCredentialRef, cfg.ProviderAccounts[0].Credentials["api_key"])
	}
	if cfg.ProviderAccounts[0].Credentials["public_label"] != "plain:example" {
		t.Fatalf("expected plain credential ref to remain unchanged")
	}

	wantSMTPPasswordRef := "file:" + filepath.Join(dir, "secrets", "smtp.key")
	if cfg.ProviderChannels[0].SMTP.PasswordRef != wantSMTPPasswordRef {
		t.Fatalf("expected SMTP password ref %q, got %q", wantSMTPPasswordRef, cfg.ProviderChannels[0].SMTP.PasswordRef)
	}
}

func TestEnabledValueDefaultsToTrue(t *testing.T) {
	if !EnabledValue(nil) {
		t.Fatal("expected nil enabled value to default to true")
	}

	disabled := false
	if EnabledValue(&disabled) {
		t.Fatal("expected explicit false enabled value to stay false")
	}
}

func writeTestConfig(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
}

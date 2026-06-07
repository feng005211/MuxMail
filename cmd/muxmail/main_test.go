package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muxmail/muxmail"
)

func TestRunConfigValidateRequiresConfigPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{"config", "validate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected missing config path to fail")
	}
	if !strings.Contains(err.Error(), "-c or --config") {
		t.Fatalf("expected missing config path error, got %q", err.Error())
	}
}

func TestRunVersionOutputsEmbeddedVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("expected version command to succeed: %v", err)
	}
	want := "muxmail " + muxmail.Version() + "\n"
	if stdout.String() != want {
		t.Fatalf("expected version output %q, got %q", want, stdout.String())
	}
}

func TestRunServeRequiresConfigPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{"serve"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected missing config path to fail")
	}
	if !strings.Contains(err.Error(), "serve requires -c or --config") {
		t.Fatalf("expected missing config path error, got %q", err.Error())
	}
}

func TestRunConfigValidateLoadsConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, configPath, "apps: []\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{"config", "validate", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected config validate to load config: %v", err)
	}
	if stdout.String() != "configuration valid\n" {
		t.Fatalf("expected success output, got %q", stdout.String())
	}
}

func TestRunConfigValidateStrictRejectsPlainSecret(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, configPath, dryRunTestConfig(t, dir))

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{"config", "validate", "--config", configPath, "--strict"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected strict config validate to fail")
	}
	if !strings.Contains(stderr.String(), "plain_secret_ref_forbidden") {
		t.Fatalf("expected strict plain secret error, got stderr=%q err=%v", stderr.String(), err)
	}
}

func TestRunServeReturnsWhenListenFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen occupied port: %v", err)
	}
	defer listener.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := strings.Replace(dryRunTestConfig(t, dir), `listen: "127.0.0.1:0"`, `listen: "`+listener.Addr().String()+`"`, 1)
	writeConfigFile(t, configPath, configBody)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run([]string{"serve", "-c", configPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected serve to fail on occupied port")
	}
	if !strings.Contains(err.Error(), "bind") && !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("expected listen failure, got %q", err.Error())
	}
}

func TestRunSendDryRunOutputsRouteAndRenderPreview(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, configPath, dryRunTestConfig(t, dir))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"send", "dry-run",
		"--config", configPath,
		"--app", "project_a",
		"--scene", "register_code",
		"--to", "user@example.com",
		"--locale", "en-US",
		"--var", "code=123456",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected dry-run to succeed: %v\nstderr=%s", err, stderr.String())
	}

	var output dryRunOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode dry-run output: %v\n%s", err, stdout.String())
	}
	if output.App != "project_a" || output.Scene != "register_code" || output.Locale != "en-US" {
		t.Fatalf("unexpected dry-run identity output: %+v", output)
	}
	if output.Template != "register_code_v1" || output.ToDomain != "example.com" {
		t.Fatalf("unexpected dry-run template/recipient output: %+v", output)
	}
	if len(output.SelectedChannels) != 2 || output.SelectedChannels[0] != "mock_auth_api" || output.SelectedChannels[1] != "mock_auth_backup" {
		t.Fatalf("unexpected dry-run selected channels: %+v", output.SelectedChannels)
	}
	if output.SubjectPreview != "Your verification code is 123456" {
		t.Fatalf("unexpected subject preview: %q", output.SubjectPreview)
	}
	if !output.HTMLRendered || !output.TextRendered {
		t.Fatalf("expected both html and text rendered, got %+v", output)
	}
	if strings.Contains(stdout.String(), "user@example.com") {
		t.Fatalf("dry-run output must not include full recipient: %s", stdout.String())
	}
}

func TestRunSendDryRunFailsWhenTemplateVarMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, configPath, dryRunTestConfig(t, dir))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"send", "dry-run",
		"-c", configPath,
		"--app", "project_a",
		"--scene", "register_code",
		"--to", "user@example.com",
		"--locale", "en-US",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected dry-run to fail")
	}
	if !strings.Contains(err.Error(), "missing_template_var") {
		t.Fatalf("expected missing_template_var, got %q", err.Error())
	}
}

func TestRunSendDryRunFailsWhenRouteIsMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, configPath, strings.Replace(dryRunTestConfig(t, dir), `"*":`, "qq.com:", 1))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"send", "dry-run",
		"-c", configPath,
		"--app", "project_a",
		"--scene", "register_code",
		"--to", "user@example.com",
		"--var", "code=123456",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected dry-run to fail")
	}
	if !strings.Contains(err.Error(), "configuration validation failed") {
		t.Fatalf("expected config validation failure, got %q", err.Error())
	}
}

func writeConfigFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func dryRunTestConfig(t *testing.T, dir string) string {
	t.Helper()

	suppressionPath := filepath.Join(dir, "data", "suppression.yaml")
	if err := os.MkdirAll(filepath.Dir(suppressionPath), 0o700); err != nil {
		t.Fatalf("create suppression dir: %v", err)
	}
	writeConfigFile(t, suppressionPath, "entries: []\n")
	loggingDir := filepath.Join(dir, "logs")

	return `server:
  listen: "127.0.0.1:0"
runtime:
  config_store: file
  queue: memory
  rate_limiter: memory
  message_log: file
  stats: off
  suppression: file
defaults:
  provider_timeout_seconds: 10
  max_attempts_per_message: 3
  retry_backoff_seconds: [0, 0, 0]
  memory_queue_size: 1000
  worker_concurrency: 1
  idempotency_cache_size: 100
  idempotency_ttl_hours: 24
  max_request_body_bytes: 65536
  max_template_var_bytes: 8192
  max_context_bytes: 4096
apps:
  - code: project_a
    name: Project A
    enabled: true
    default_locale: en-US
    allowed_locales: [en-US]
    api_keys:
      - name: default
        enabled: true
        key_ref: plain:test_key
    templates:
      - code: register_code_v1
        locale: en-US
        enabled: true
        subject: "Your verification code is {{ .code }}"
        required_vars: [code]
        html_body: "<p>Your verification code is {{ .code }}</p>"
        text_body: "Your verification code is {{ .code }}"
    scenes:
      - code: register_code
        name: Register code
        enabled: true
        template: register_code_v1
        rate_limit:
          same_email_per_minute: 1
          same_email_per_day: 10
          same_user_ip_per_hour: 20
          same_caller_ip_per_hour: 200
        route_policy:
          "*": [mock_auth_api, mock_auth_backup]
provider_accounts:
  - code: mock_main
    provider: mock
    enabled: true
provider_channels:
  - code: mock_auth_api
    account: mock_main
    transport: api
    enabled: true
    sender_domain: auth.example.com
    from_name: MuxMail
    from: no-reply@auth.example.com
  - code: mock_auth_backup
    account: mock_main
    transport: api
    enabled: true
    sender_domain: auth-bak.example.com
    from_name: MuxMail
    from: no-reply@auth-bak.example.com
logging:
  dir: ` + strconvQuote(loggingDir) + `
  max_file_size_mb: 100
  max_backups: 5
`
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

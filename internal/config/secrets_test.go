package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretResolverResolvesPlainWithWarning(t *testing.T) {
	resolver := NewSecretResolver()

	resolved, err := resolver.Resolve("plain:super-secret")
	if err != nil {
		t.Fatalf("expected plain secret to resolve: %v", err)
	}
	if resolved.Value != "super-secret" {
		t.Fatalf("expected plain secret value, got %q", resolved.Value)
	}
	if len(resolved.Warnings) != 1 {
		t.Fatalf("expected one plain secret warning, got %d", len(resolved.Warnings))
	}
	if resolved.Warnings[0].Code != "plain_secret_ref" {
		t.Fatalf("expected plain secret warning code, got %q", resolved.Warnings[0].Code)
	}
}

func TestSecretResolverResolvesEnvWithoutTrim(t *testing.T) {
	t.Setenv("MUXMAIL_TEST_SECRET", " value with spaces \n")
	resolver := NewSecretResolver()

	resolved, err := resolver.Resolve("env:MUXMAIL_TEST_SECRET")
	if err != nil {
		t.Fatalf("expected env secret to resolve: %v", err)
	}
	if resolved.Value != " value with spaces \n" {
		t.Fatalf("expected env secret to remain untrimmed, got %q", resolved.Value)
	}
	if len(resolved.Warnings) != 0 {
		t.Fatalf("expected no env warnings, got %d", len(resolved.Warnings))
	}
}

func TestSecretResolverFailsOnMissingEnv(t *testing.T) {
	os.Unsetenv("MUXMAIL_MISSING_SECRET")
	resolver := NewSecretResolver()

	_, err := resolver.Resolve("env:MUXMAIL_MISSING_SECRET")
	if err == nil {
		t.Fatal("expected missing env secret to fail")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("expected error not to contain secret value, got %q", err.Error())
	}
}

func TestSecretResolverResolvesFileAndTrimsOneTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	writeSecretFile(t, path, "secret-value\n\n")

	resolver := NewSecretResolver()
	resolved, err := resolver.Resolve("file:" + path)
	if err != nil {
		t.Fatalf("expected file secret to resolve: %v", err)
	}
	if resolved.Value != "secret-value\n" {
		t.Fatalf("expected one trailing newline to be trimmed, got %q", resolved.Value)
	}
}

func TestSecretResolverTrimsCRLFAsOneTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	writeSecretFile(t, path, "secret-value\r\n")

	resolver := NewSecretResolver()
	resolved, err := resolver.Resolve("file:" + path)
	if err != nil {
		t.Fatalf("expected file secret to resolve: %v", err)
	}
	if resolved.Value != "secret-value" {
		t.Fatalf("expected CRLF newline to be trimmed, got %q", resolved.Value)
	}
}

func TestSecretResolverFailsOnUnreadableFileWithoutSecretLeak(t *testing.T) {
	resolver := NewSecretResolver()

	_, err := resolver.Resolve("file:" + filepath.Join(t.TempDir(), "missing-secret-value"))
	if err == nil {
		t.Fatal("expected missing file secret to fail")
	}
	if strings.Contains(err.Error(), "actual-secret") {
		t.Fatalf("expected error not to contain secret value, got %q", err.Error())
	}
}

func TestSecretResolverRejectsUnsupportedScheme(t *testing.T) {
	resolver := NewSecretResolver()

	_, err := resolver.Resolve("vault:secret/path")
	if err == nil {
		t.Fatal("expected unsupported secret scheme to fail")
	}
	if strings.Contains(err.Error(), "vault:secret/path") {
		t.Fatalf("expected unsupported scheme error not to echo full ref, got %q", err.Error())
	}
}

func writeSecretFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
}

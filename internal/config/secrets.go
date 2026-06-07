package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	plainSecretPrefix = "plain:"
	envSecretPrefix   = "env:"
)

// SecretResolver resolves config secret references into secret values.
type SecretResolver interface {
	Resolve(ref string) (ResolvedSecret, error)
}

// ResolvedSecret is the value and warnings produced by resolving a secret reference.
type ResolvedSecret struct {
	Value    string
	Warnings []ValidationWarning
}

// ValidationWarning describes a non-fatal configuration concern.
type ValidationWarning struct {
	Code    string
	Path    string
	Message string
}

// FileSecretResolver resolves plain, environment, and file secret references.
type FileSecretResolver struct{}

// NewSecretResolver creates the default secret resolver used by file-based config validation.
func NewSecretResolver() SecretResolver {
	return FileSecretResolver{}
}

// Resolve returns the secret value for a plain, env, or file reference.
func (r FileSecretResolver) Resolve(ref string) (ResolvedSecret, error) {
	switch {
	case strings.HasPrefix(ref, plainSecretPrefix):
		return resolvePlainSecret(ref), nil
	case strings.HasPrefix(ref, envSecretPrefix):
		return resolveEnvSecret(ref)
	case strings.HasPrefix(ref, fileSecretPrefix):
		return resolveFileSecret(ref)
	default:
		return ResolvedSecret{}, fmt.Errorf("unsupported secret reference scheme")
	}
}

func resolvePlainSecret(ref string) ResolvedSecret {
	return ResolvedSecret{
		Value: strings.TrimPrefix(ref, plainSecretPrefix),
		Warnings: []ValidationWarning{
			{
				Code:    "plain_secret_ref",
				Message: "plain secret references are allowed for local examples but should not be used in production",
			},
		},
	}
}

func resolveEnvSecret(ref string) (ResolvedSecret, error) {
	name := strings.TrimPrefix(ref, envSecretPrefix)
	if name == "" {
		return ResolvedSecret{}, fmt.Errorf("environment secret reference is missing a variable name")
	}

	value, ok := os.LookupEnv(name)
	if !ok {
		return ResolvedSecret{}, fmt.Errorf("environment variable %q is not set", name)
	}

	return ResolvedSecret{Value: value}, nil
}

func resolveFileSecret(ref string) (ResolvedSecret, error) {
	path := strings.TrimPrefix(ref, fileSecretPrefix)
	if path == "" {
		return ResolvedSecret{}, fmt.Errorf("file secret reference is missing a path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ResolvedSecret{}, fmt.Errorf("read file secret: %w", err)
	}

	return ResolvedSecret{Value: trimOneTrailingNewline(string(data))}, nil
}

func trimOneTrailingNewline(value string) string {
	if strings.HasSuffix(value, "\r\n") {
		return strings.TrimSuffix(value, "\r\n")
	}
	if strings.HasSuffix(value, "\n") {
		return strings.TrimSuffix(value, "\n")
	}

	return value
}

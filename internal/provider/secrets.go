package provider

import "fmt"

// SecretResolver resolves provider credential references.
type SecretResolver interface {
	ResolveSecret(ref string) (string, error)
}

// SecretResolverFunc adapts a function into a SecretResolver.
type SecretResolverFunc func(ref string) (string, error)

// ResolveSecret resolves one provider credential reference.
func (f SecretResolverFunc) ResolveSecret(ref string) (string, error) {
	return f(ref)
}

// StaticSecretResolver resolves credentials from an in-memory map.
type StaticSecretResolver map[string]string

// ResolveSecret resolves one provider credential reference from the map.
func (r StaticSecretResolver) ResolveSecret(ref string) (string, error) {
	value, ok := r[ref]
	if !ok {
		return "", fmt.Errorf("secret reference not found")
	}

	return value, nil
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

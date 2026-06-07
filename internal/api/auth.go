package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

const (
	minAPIKeyBytes = 24
	maxAPIKeyBytes = 128
)

type authContextKey struct{}

// AuthContext contains the App and API key metadata resolved from Authorization.
type AuthContext struct {
	App    domain.App
	APIKey domain.APIKeyMetadata
}

// AuthError is a stable authentication or authorization failure.
type AuthError struct {
	Code    domain.ErrorCode
	Message string
}

// Error returns the stable public API error code.
func (e AuthError) Error() string {
	return string(e.Code)
}

// Authenticator matches Bearer API keys to configured Apps.
type Authenticator struct {
	apps []domain.App
}

// NewAuthenticator resolves App API keys and stores only their SHA-256 hashes.
func NewAuthenticator(apps []config.AppConfig, resolver config.SecretResolver) (*Authenticator, error) {
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}

	authenticator := &Authenticator{
		apps: make([]domain.App, 0, len(apps)),
	}
	for appIndex, appConfig := range apps {
		apiKeys := make([]domain.APIKeyMetadata, 0, len(appConfig.APIKeys))
		for keyIndex, keyConfig := range appConfig.APIKeys {
			resolved, err := resolver.Resolve(keyConfig.KeyRef)
			if err != nil {
				return nil, fmt.Errorf("resolve api key apps[%d].api_keys[%d]: %w", appIndex, keyIndex, err)
			}
			apiKeys = append(apiKeys, domain.APIKeyMetadata{
				Name:    keyConfig.Name,
				Enabled: config.EnabledValue(keyConfig.Enabled),
				KeyHash: domain.APIKeyHash(resolved.Value),
			})
		}
		authenticator.apps = append(authenticator.apps, domainAppFromConfig(appConfig, apiKeys))
	}

	return authenticator, nil
}

// AuthenticateHeader resolves the App and API key metadata from an Authorization header.
func (a *Authenticator) AuthenticateHeader(header string) (AuthContext, error) {
	apiKey, ok := parseBearerToken(header)
	if !ok || len(apiKey) < minAPIKeyBytes || len(apiKey) > maxAPIKeyBytes {
		return AuthContext{}, AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
	}

	keyHash := domain.APIKeyHash(apiKey)
	for _, app := range a.apps {
		for _, key := range app.APIKeys {
			if !domain.ConstantTimeEqualHex(key.KeyHash, keyHash) {
				continue
			}
			if !key.Enabled {
				return AuthContext{}, AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
			}
			if !app.Enabled {
				return AuthContext{}, AuthError{Code: domain.ErrorCodeAppDisabled, Message: "app disabled"}
			}

			return AuthContext{App: app, APIKey: key}, nil
		}
	}

	return AuthContext{}, AuthError{Code: domain.ErrorCodeUnauthorized, Message: "unauthorized"}
}

// Middleware authenticates HTTP requests and stores AuthContext on the request context.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authContext, err := a.AuthenticateHeader(request.Header.Get("Authorization"))
		if err != nil {
			writeAuthError(w, err)
			return
		}

		next.ServeHTTP(w, request.WithContext(ContextWithAuth(request.Context(), authContext)))
	})
}

// ContextWithAuth returns a context carrying resolved authentication metadata.
func ContextWithAuth(ctx context.Context, auth AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, auth)
}

// AuthFromContext returns resolved authentication metadata from ctx.
func AuthFromContext(ctx context.Context) (AuthContext, bool) {
	auth, ok := ctx.Value(authContextKey{}).(AuthContext)
	return auth, ok
}

func parseBearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}

	token := strings.TrimPrefix(header, prefix)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}

	return token, true
}

func writeAuthError(w http.ResponseWriter, err error) {
	code := domain.ErrorCodeUnauthorized
	status := http.StatusUnauthorized
	message := "unauthorized"

	if authError, ok := err.(AuthError); ok {
		code = authError.Code
		message = authError.Message
		if authError.Code == domain.ErrorCodeAppDisabled {
			status = http.StatusForbidden
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":"%s","message":"%s","request_id":""}}`, code, message)
}

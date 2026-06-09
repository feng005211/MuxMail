package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
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
	seenKeyHashes := make(map[string]string)
	for appIndex, appConfig := range apps {
		apiKeys := make([]domain.APIKeyMetadata, 0, len(appConfig.APIKeys))
		for keyIndex, keyConfig := range appConfig.APIKeys {
			resolved, err := resolver.Resolve(keyConfig.KeyRef)
			if err != nil {
				return nil, fmt.Errorf("resolve api key apps[%d].api_keys[%d]: %w", appIndex, keyIndex, err)
			}
			if !domain.IsValidAPIKeyValue(resolved.Value) {
				return nil, fmt.Errorf("api key apps[%d].api_keys[%d] has invalid value", appIndex, keyIndex)
			}
			keyHash := domain.APIKeyHash(resolved.Value)
			keyPath := fmt.Sprintf("apps[%d].api_keys[%d]", appIndex, keyIndex)
			if firstPath, exists := seenKeyHashes[keyHash]; exists {
				return nil, fmt.Errorf("api key %s duplicates %s", keyPath, firstPath)
			}
			seenKeyHashes[keyHash] = keyPath
			apiKeys = append(apiKeys, domain.APIKeyMetadata{
				Name:    keyConfig.Name,
				Enabled: config.EnabledValue(keyConfig.Enabled),
				KeyHash: keyHash,
			})
		}
		authenticator.apps = append(authenticator.apps, domainAppFromConfig(appConfig, apiKeys))
	}

	return authenticator, nil
}

// AuthenticateHeader resolves the App and API key metadata from an Authorization header.
func (a *Authenticator) AuthenticateHeader(header string) (AuthContext, error) {
	apiKey, ok := parseBearerToken(header)
	if !ok || !domain.IsValidAPIKeyValue(apiKey) {
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
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return "", false
	}

	token := fields[1]
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
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorPayload{
			Code:      code,
			Message:   message,
			RequestID: "",
		},
	})
}

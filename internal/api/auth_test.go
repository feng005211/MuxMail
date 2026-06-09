package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

const (
	testAPIKey         = "mk_test_project_a_key_123456"
	testDisabledAPIKey = "mk_test_project_a_key_disabled"
)

func TestAuthenticatorAuthenticatesValidKey(t *testing.T) {
	authenticator := openTestAuthenticator(t, nil, nil)

	auth, err := authenticator.AuthenticateHeader("Bearer " + testAPIKey)
	if err != nil {
		t.Fatalf("authenticate valid key: %v", err)
	}
	if auth.App.Code != "project_a" || auth.APIKey.Name != "default" {
		t.Fatalf("unexpected auth context: %+v", auth)
	}
	if auth.APIKey.KeyHash == testAPIKey {
		t.Fatalf("expected API key hash, got plaintext key")
	}
}

func TestAuthenticatorAcceptsCaseInsensitiveBearerScheme(t *testing.T) {
	authenticator := openTestAuthenticator(t, nil, nil)

	auth, err := authenticator.AuthenticateHeader("bearer " + testAPIKey)
	if err != nil {
		t.Fatalf("authenticate lower-case bearer key: %v", err)
	}
	if auth.App.Code != "project_a" || auth.APIKey.Name != "default" {
		t.Fatalf("unexpected auth context: %+v", auth)
	}
}

func TestAuthenticatorRejectsMissingAndInvalidAuth(t *testing.T) {
	authenticator := openTestAuthenticator(t, nil, nil)

	tests := []string{
		"",
		"Basic " + testAPIKey,
		"Bearer short",
		"Bearer " + testAPIKey + " extra",
		"Bearer mk_test_invalid_key_123456_中文",
		"Bearer mk_test_wrong_key_123456789",
	}

	for _, header := range tests {
		t.Run(header, func(t *testing.T) {
			_, err := authenticator.AuthenticateHeader(header)
			assertAuthError(t, err, domain.ErrorCodeUnauthorized)
		})
	}
}

func TestNewAuthenticatorRejectsInvalidResolvedAPIKeyValue(t *testing.T) {
	_, err := NewAuthenticator([]config.AppConfig{
		{
			Code:          "project_a",
			Name:          "Project A",
			DefaultLocale: "en-US",
			AllowedLocales: []string{
				"en-US",
			},
			APIKeys: []config.APIKeyConfig{
				{Name: "default", KeyRef: "plain:short"},
			},
		},
	}, config.NewSecretResolver())
	if err == nil {
		t.Fatal("expected invalid API key value to be rejected")
	}
	if !strings.Contains(err.Error(), "apps[0].api_keys[0]") {
		t.Fatalf("expected error path without secret value, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "short") {
		t.Fatalf("expected invalid API key error to omit secret value, got %q", err.Error())
	}
}

func TestNewAuthenticatorRejectsDuplicateResolvedAPIKeyValue(t *testing.T) {
	_, err := NewAuthenticator([]config.AppConfig{
		{
			Code:          "project_a",
			Name:          "Project A",
			DefaultLocale: "en-US",
			AllowedLocales: []string{
				"en-US",
			},
			APIKeys: []config.APIKeyConfig{
				{Name: "default", KeyRef: "plain:" + testAPIKey},
			},
		},
		{
			Code:          "project_b",
			Name:          "Project B",
			DefaultLocale: "en-US",
			AllowedLocales: []string{
				"en-US",
			},
			APIKeys: []config.APIKeyConfig{
				{Name: "default", KeyRef: "plain:" + testAPIKey},
			},
		},
	}, config.NewSecretResolver())
	if err == nil {
		t.Fatal("expected duplicate API key value to be rejected")
	}
	if !strings.Contains(err.Error(), "apps[1].api_keys[0]") || !strings.Contains(err.Error(), "apps[0].api_keys[0]") {
		t.Fatalf("expected duplicate error to identify both key paths, got %q", err.Error())
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("expected duplicate API key error to omit secret value, got %q", err.Error())
	}
}

func TestAuthenticatorRejectsDisabledApp(t *testing.T) {
	disabled := false
	authenticator := openTestAuthenticator(t, &disabled, nil)

	_, err := authenticator.AuthenticateHeader("Bearer " + testAPIKey)
	assertAuthError(t, err, domain.ErrorCodeAppDisabled)
}

func TestAuthenticatorRejectsDisabledKey(t *testing.T) {
	disabled := false
	authenticator := openTestAuthenticator(t, nil, &disabled)

	_, err := authenticator.AuthenticateHeader("Bearer " + testDisabledAPIKey)
	assertAuthError(t, err, domain.ErrorCodeUnauthorized)
}

func TestAuthenticatorMiddlewareStoresAuthContext(t *testing.T) {
	authenticator := openTestAuthenticator(t, nil, nil)
	var resolved AuthContext

	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var ok bool
		resolved, ok = AuthFromContext(request.Context())
		if !ok {
			t.Fatalf("expected auth context")
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", recorder.Code)
	}
	if resolved.App.Code != "project_a" || resolved.APIKey.Name != "default" {
		t.Fatalf("unexpected auth context: %+v", resolved)
	}
}

func TestAuthenticatorMiddlewareWritesUnauthorized(t *testing.T) {
	authenticator := openTestAuthenticator(t, nil, nil)
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Fatalf("handler should not run")
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", recorder.Code)
	}
	if recorder.Body.String() != "{\"error\":{\"code\":\"unauthorized\",\"message\":\"unauthorized\",\"request_id\":\"\"}}\n" {
		t.Fatalf("unexpected unauthorized body: %q", recorder.Body.String())
	}
}

func TestAuthenticatorMiddlewareWritesAppDisabled(t *testing.T) {
	disabled := false
	authenticator := openTestAuthenticator(t, &disabled, nil)
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Fatalf("handler should not run")
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/send", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", recorder.Code)
	}
	if recorder.Body.String() != "{\"error\":{\"code\":\"app_disabled\",\"message\":\"app disabled\",\"request_id\":\"\"}}\n" {
		t.Fatalf("unexpected app disabled body: %q", recorder.Body.String())
	}
}

func assertAuthError(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()

	var authError AuthError
	if !errors.As(err, &authError) {
		t.Fatalf("expected auth error, got %v", err)
	}
	if authError.Code != code {
		t.Fatalf("expected auth code %s, got %s", code, authError.Code)
	}
}

func openTestAuthenticator(t *testing.T, appEnabled *bool, disabledKeyEnabled *bool) *Authenticator {
	t.Helper()

	authenticator, err := NewAuthenticator([]config.AppConfig{
		{
			Code:          "project_a",
			Name:          "Project A",
			Enabled:       appEnabled,
			DefaultLocale: "en-US",
			AllowedLocales: []string{
				"en-US",
			},
			APIKeys: []config.APIKeyConfig{
				{Name: "default", KeyRef: "plain:" + testAPIKey},
				{Name: "disabled", Enabled: disabledKeyEnabled, KeyRef: "plain:" + testDisabledAPIKey},
			},
		},
	}, config.NewSecretResolver())
	if err != nil {
		t.Fatalf("open authenticator: %v", err)
	}

	return authenticator
}

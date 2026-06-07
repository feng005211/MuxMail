package config

import (
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestDomainAppFromConfigCopiesNestedSlices(t *testing.T) {
	cfg := AppConfig{
		Code:           "project_a",
		Name:           "Project A",
		DefaultLocale:  "en-US",
		AllowedLocales: []string{"en-US"},
		Scenes: []SceneConfig{
			{
				Code:     "register_code",
				Template: "register_code_v1",
				RoutePolicy: RoutePolicy{
					"*": []string{"mock_auth_api"},
				},
			},
		},
		Templates: []TemplateConfig{
			{
				Code:         "register_code_v1",
				Locale:       "en-US",
				Subject:      "Your code",
				RequiredVars: []string{"code"},
				TextBody:     "Your code is {{ .code }}",
			},
		},
	}
	apiKeys := []domain.APIKeyMetadata{{Name: "default", Enabled: true, KeyHash: "hash"}}

	app := DomainAppFromConfig(cfg, apiKeys)
	cfg.AllowedLocales[0] = "zh-CN"
	cfg.Scenes[0].RoutePolicy["*"][0] = "changed"
	cfg.Templates[0].RequiredVars[0] = "changed"
	apiKeys[0].Name = "changed"

	if app.AllowedLocales[0] != "en-US" {
		t.Fatalf("expected allowed locales to be copied, got %+v", app.AllowedLocales)
	}
	if app.Scenes[0].RoutePolicy["*"][0] != "mock_auth_api" {
		t.Fatalf("expected route policy to be copied, got %+v", app.Scenes[0].RoutePolicy)
	}
	if app.Templates[0].RequiredVars[0] != "code" {
		t.Fatalf("expected required vars to be copied, got %+v", app.Templates[0].RequiredVars)
	}
	if app.APIKeys[0].Name != "default" {
		t.Fatalf("expected api keys to be copied, got %+v", app.APIKeys)
	}
}

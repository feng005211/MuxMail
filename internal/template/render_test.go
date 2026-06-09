package template

import (
	"errors"
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestRenderSuccessEnglish(t *testing.T) {
	result, err := Render(testApp(), testScene(), testRequest("en-US"))
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}

	if result.TemplateCode != "register_code_v1" {
		t.Fatalf("expected template code, got %q", result.TemplateCode)
	}
	if result.Locale != "en-US" {
		t.Fatalf("expected en-US locale, got %q", result.Locale)
	}
	if result.Subject != "Your code is 123456" {
		t.Fatalf("unexpected subject: %q", result.Subject)
	}
	if result.HTMLBody != "<p>Your code is 123456.</p>" {
		t.Fatalf("unexpected html body: %q", result.HTMLBody)
	}
	if result.TextBody != "Your code is 123456." {
		t.Fatalf("unexpected text body: %q", result.TextBody)
	}
	if !result.HasHTML || !result.HasText || !result.MultipartAlternative {
		t.Fatalf("expected multipart alternative metadata, got %+v", result)
	}
}

func TestRenderSuccessChineseLocale(t *testing.T) {
	result, err := Render(testApp(), testScene(), testRequest("zh-CN"))
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}

	if result.Locale != "zh-CN" {
		t.Fatalf("expected zh-CN locale, got %q", result.Locale)
	}
	if result.Subject != "Code 123456 for zh-CN" {
		t.Fatalf("unexpected subject: %q", result.Subject)
	}
}

func TestRenderUsesAppDefaultLocaleWhenRequestLocaleMissing(t *testing.T) {
	result, err := Render(testApp(), testScene(), testRequest(""))
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}

	if result.Locale != "en-US" {
		t.Fatalf("expected default locale en-US, got %q", result.Locale)
	}
}

func TestRenderFallsBackToDefaultLocale(t *testing.T) {
	app := testApp()
	app.Templates = []domain.Template{app.Templates[0]}

	result, err := Render(app, testScene(), testRequest("zh-CN"))
	if err != nil {
		t.Fatalf("expected render to fallback: %v", err)
	}

	if result.Locale != "en-US" {
		t.Fatalf("expected fallback locale en-US, got %q", result.Locale)
	}
}

func TestRenderMissingDefaultLocaleTemplateFails(t *testing.T) {
	app := testApp()
	app.Templates = []domain.Template{app.Templates[1]}

	_, err := Render(app, testScene(), testRequest("fr-FR"))
	assertRenderErrorCode(t, err, domain.ErrorCodeTemplateLocaleNotFound)
}

func TestRenderMissingRequiredVarFails(t *testing.T) {
	request := testRequest("en-US")
	delete(request.Vars, "code")

	_, err := Render(testApp(), testScene(), request)
	assertRenderErrorCode(t, err, domain.ErrorCodeMissingTemplateVar)
}

func TestRenderFailureUsesStableErrorCode(t *testing.T) {
	app := testApp()
	app.Templates[0].Subject = "{{"

	_, err := Render(app, testScene(), testRequest("en-US"))
	assertRenderErrorCode(t, err, domain.ErrorCodeTemplateRenderFailed)
}

func TestRenderRejectsInvalidRenderedSubject(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		code    string
	}{
		{name: "empty", subject: "{{ .code }}", code: ""},
		{name: "newline", subject: "Your code {{ .code }}", code: "123456\r\nBcc: attacker@example.com"},
		{name: "tab", subject: "Your code {{ .code }}", code: "123456\tops"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApp()
			app.Templates[0].Subject = tt.subject
			request := testRequest("en-US")
			request.Vars["code"] = tt.code

			_, err := Render(app, testScene(), request)
			assertRenderErrorCode(t, err, domain.ErrorCodeTemplateRenderFailed)
		})
	}
}

func TestRenderEscapesHTMLBody(t *testing.T) {
	request := testRequest("en-US")
	request.Vars["code"] = `<b>123456</b>`

	result, err := Render(testApp(), testScene(), request)
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}

	if result.HTMLBody != "<p>Your code is &lt;b&gt;123456&lt;/b&gt;.</p>" {
		t.Fatalf("expected html escaping, got %q", result.HTMLBody)
	}
	if result.TextBody != "Your code is <b>123456</b>." {
		t.Fatalf("expected text template not to HTML-escape, got %q", result.TextBody)
	}
}

func testApp() domain.App {
	return domain.App{
		Code:           "project_a",
		Enabled:        true,
		DefaultLocale:  "en-US",
		AllowedLocales: []string{"en-US", "zh-CN"},
		Templates: []domain.Template{
			{
				Code:         "register_code_v1",
				Locale:       "en-US",
				Enabled:      true,
				Subject:      "Your code is {{ .code }}",
				RequiredVars: []string{"code", "expire_minutes"},
				HTMLBody:     "<p>Your code is {{ .code }}.</p>",
				TextBody:     "Your code is {{ .code }}.",
			},
			{
				Code:         "register_code_v1",
				Locale:       "zh-CN",
				Enabled:      true,
				Subject:      "Code {{ .code }} for zh-CN",
				RequiredVars: []string{"code", "expire_minutes"},
				TextBody:     "Code {{ .code }} expires in {{ .expire_minutes }} minutes.",
			},
		},
	}
}

func testScene() domain.Scene {
	return domain.Scene{
		Code:     "register_code",
		Enabled:  true,
		Template: "register_code_v1",
	}
}

func testRequest(locale string) domain.SendRequest {
	return domain.SendRequest{
		Scene:  "register_code",
		To:     "user@example.com",
		Locale: locale,
		Vars: map[string]any{
			"code":           "123456",
			"expire_minutes": 10,
		},
	}
}

func assertRenderErrorCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()

	var renderErr RenderError
	if !errors.As(err, &renderErr) {
		t.Fatalf("expected render error, got %v", err)
	}
	if renderErr.Code != code {
		t.Fatalf("expected render error code %s, got %s", code, renderErr.Code)
	}
}

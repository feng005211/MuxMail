package template

import (
	"bytes"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/muxmail/muxmail/internal/domain"
)

// RenderedMail contains the rendered mail content selected for one request.
type RenderedMail struct {
	TemplateCode         string
	Locale               string
	Subject              string
	HTMLBody             string
	TextBody             string
	HasHTML              bool
	HasText              bool
	MultipartAlternative bool
}

// RenderError is a stable template rendering failure.
type RenderError struct {
	Code    domain.ErrorCode
	Message string
}

// Error returns the stable error code as an error string.
func (e RenderError) Error() string {
	return string(e.Code)
}

// Render resolves and renders a Scene template for the current App and request.
func Render(app domain.App, scene domain.Scene, request domain.SendRequest) (RenderedMail, error) {
	tmpl, locale, err := resolveTemplate(app, scene, request.Locale)
	if err != nil {
		return RenderedMail{}, err
	}
	if err := requireVars(tmpl, request.Vars); err != nil {
		return RenderedMail{}, err
	}

	subject, err := renderTextTemplate("subject", tmpl.Subject, request.Vars)
	if err != nil {
		return RenderedMail{}, renderError(domain.ErrorCodeTemplateRenderFailed, "template subject render failed")
	}

	htmlBody := ""
	if tmpl.HTMLBody != "" {
		htmlBody, err = renderHTMLTemplate("html_body", tmpl.HTMLBody, request.Vars)
		if err != nil {
			return RenderedMail{}, renderError(domain.ErrorCodeTemplateRenderFailed, "template html body render failed")
		}
	}

	textBody := ""
	if tmpl.TextBody != "" {
		textBody, err = renderTextTemplate("text_body", tmpl.TextBody, request.Vars)
		if err != nil {
			return RenderedMail{}, renderError(domain.ErrorCodeTemplateRenderFailed, "template text body render failed")
		}
	}

	hasHTML := htmlBody != ""
	hasText := textBody != ""
	if !hasHTML && !hasText {
		return RenderedMail{}, renderError(domain.ErrorCodeTemplateRenderFailed, "template must render html or text body")
	}

	return RenderedMail{
		TemplateCode:         tmpl.Code,
		Locale:               locale,
		Subject:              subject,
		HTMLBody:             htmlBody,
		TextBody:             textBody,
		HasHTML:              hasHTML,
		HasText:              hasText,
		MultipartAlternative: hasHTML && hasText,
	}, nil
}

func resolveTemplate(app domain.App, scene domain.Scene, requestLocale string) (domain.Template, string, error) {
	targetLocale := requestLocale
	if targetLocale == "" {
		targetLocale = app.DefaultLocale
	}

	if tmpl, ok := findEnabledTemplate(app.Templates, scene.Template, targetLocale); ok {
		return tmpl, targetLocale, nil
	}
	if targetLocale != app.DefaultLocale {
		if tmpl, ok := findEnabledTemplate(app.Templates, scene.Template, app.DefaultLocale); ok {
			return tmpl, app.DefaultLocale, nil
		}
	}

	return domain.Template{}, "", renderError(domain.ErrorCodeTemplateLocaleNotFound, "template locale not found")
}

func findEnabledTemplate(templates []domain.Template, code string, locale string) (domain.Template, bool) {
	for _, tmpl := range templates {
		if tmpl.Code == code && tmpl.Locale == locale && tmpl.Enabled {
			return tmpl, true
		}
	}

	return domain.Template{}, false
}

func requireVars(tmpl domain.Template, vars map[string]any) error {
	for _, name := range tmpl.RequiredVars {
		if _, exists := vars[name]; !exists {
			return renderError(domain.ErrorCodeMissingTemplateVar, "required template variable is missing")
		}
	}

	return nil
}

func renderTextTemplate(name string, source string, vars map[string]any) (string, error) {
	tmpl, err := texttemplate.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}

	var output bytes.Buffer
	if err := tmpl.Execute(&output, vars); err != nil {
		return "", err
	}

	return output.String(), nil
}

func renderHTMLTemplate(name string, source string, vars map[string]any) (string, error) {
	tmpl, err := htmltemplate.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}

	var output bytes.Buffer
	if err := tmpl.Execute(&output, vars); err != nil {
		return "", err
	}

	return output.String(), nil
}

func renderError(code domain.ErrorCode, message string) RenderError {
	return RenderError{Code: code, Message: message}
}

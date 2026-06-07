package config

import "github.com/muxmail/muxmail/internal/domain"

// DomainAppFromConfig converts one App config into the runtime domain App shape.
func DomainAppFromConfig(appConfig AppConfig, apiKeys []domain.APIKeyMetadata) domain.App {
	app := domain.App{
		Code:           appConfig.Code,
		Name:           appConfig.Name,
		Enabled:        EnabledValue(appConfig.Enabled),
		DefaultLocale:  appConfig.DefaultLocale,
		AllowedLocales: append([]string(nil), appConfig.AllowedLocales...),
		APIKeys:        append([]domain.APIKeyMetadata(nil), apiKeys...),
		Scenes:         make([]domain.Scene, 0, len(appConfig.Scenes)),
		Templates:      make([]domain.Template, 0, len(appConfig.Templates)),
	}

	for _, sceneConfig := range appConfig.Scenes {
		app.Scenes = append(app.Scenes, domain.Scene{
			Code:        sceneConfig.Code,
			Name:        sceneConfig.Name,
			Enabled:     EnabledValue(sceneConfig.Enabled),
			Template:    sceneConfig.Template,
			RateLimit:   DomainRateLimitPolicy(sceneConfig.RateLimit),
			RoutePolicy: DomainRoutePolicy(sceneConfig.RoutePolicy),
		})
	}
	for _, templateConfig := range appConfig.Templates {
		app.Templates = append(app.Templates, domain.Template{
			Code:         templateConfig.Code,
			Locale:       templateConfig.Locale,
			Enabled:      EnabledValue(templateConfig.Enabled),
			Subject:      templateConfig.Subject,
			RequiredVars: append([]string(nil), templateConfig.RequiredVars...),
			HTMLBody:     templateConfig.HTMLBody,
			TextBody:     templateConfig.TextBody,
		})
	}

	return app
}

// DomainRateLimitPolicy converts config rate limit settings into domain settings.
func DomainRateLimitPolicy(rateLimit RateLimitConfig) domain.RateLimitPolicy {
	return domain.RateLimitPolicy{
		SameEmailPerMinute:  rateLimit.SameEmailPerMinute,
		SameEmailPerDay:     rateLimit.SameEmailPerDay,
		SameUserIPPerHour:   rateLimit.SameUserIPPerHour,
		SameCallerIPPerHour: rateLimit.SameCallerIPPerHour,
	}
}

// DomainRoutePolicy converts config route policy settings into domain settings.
func DomainRoutePolicy(policy RoutePolicy) domain.RoutePolicy {
	result := make(domain.RoutePolicy, len(policy))
	for routeKey, channels := range policy {
		result[routeKey] = append([]string(nil), channels...)
	}

	return result
}

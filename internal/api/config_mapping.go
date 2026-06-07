package api

import (
	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
)

func domainAppFromConfig(appConfig config.AppConfig, apiKeys []domain.APIKeyMetadata) domain.App {
	return config.DomainAppFromConfig(appConfig, apiKeys)
}

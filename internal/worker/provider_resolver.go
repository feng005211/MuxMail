package worker

import (
	"fmt"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	provideradapter "github.com/muxmail/muxmail/internal/provider"
)

// ProviderResolverBuildOption customizes provider resolver construction.
type ProviderResolverBuildOption func(*providerResolverBuildOptions)

type providerResolverBuildOptions struct {
	smtpOptions   []provideradapter.SMTPTransportOption
	resendOptions []provideradapter.ResendAPIOption
	brevoOptions  []provideradapter.BrevoAPIOption
}

// WithSMTPTransportOptions passes SMTP transport options into the shared SMTP client.
func WithSMTPTransportOptions(options ...provideradapter.SMTPTransportOption) ProviderResolverBuildOption {
	return func(buildOptions *providerResolverBuildOptions) {
		buildOptions.smtpOptions = append(buildOptions.smtpOptions, options...)
	}
}

// WithResendAPIOptions passes Resend API options into the Resend provider.
func WithResendAPIOptions(options ...provideradapter.ResendAPIOption) ProviderResolverBuildOption {
	return func(buildOptions *providerResolverBuildOptions) {
		buildOptions.resendOptions = append(buildOptions.resendOptions, options...)
	}
}

// WithBrevoAPIOptions passes Brevo API options into the Brevo provider.
func WithBrevoAPIOptions(options ...provideradapter.BrevoAPIOption) ProviderResolverBuildOption {
	return func(buildOptions *providerResolverBuildOptions) {
		buildOptions.brevoOptions = append(buildOptions.brevoOptions, options...)
	}
}

// NewProviderResolverFromConfig builds Provider Channel runtime bindings from file config.
func NewProviderResolverFromConfig(cfg *config.Config, resolver config.SecretResolver, options ...ProviderResolverBuildOption) (*StaticProviderResolver, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}

	buildOptions := providerResolverBuildOptions{}
	for _, option := range options {
		option(&buildOptions)
	}

	accounts := make(map[string]domain.ProviderAccount, len(cfg.ProviderAccounts))
	for _, accountConfig := range cfg.ProviderAccounts {
		account := domain.ProviderAccount{
			Code:           accountConfig.Code,
			Provider:       accountConfig.Provider,
			Enabled:        config.EnabledValue(accountConfig.Enabled),
			CredentialRefs: copyStringMap(accountConfig.Credentials),
		}
		accounts[account.Code] = account
	}

	secretResolver := configSecretResolverAdapter{resolver: resolver}
	smtpTransport := provideradapter.NewSMTPTransport(secretResolver, buildOptions.smtpOptions...)
	resendAPI := provideradapter.NewResendAPIProvider(secretResolver, buildOptions.resendOptions...)
	brevoAPI := provideradapter.NewBrevoAPIProvider(secretResolver, buildOptions.brevoOptions...)
	runtimes := make([]ProviderChannelRuntime, 0, len(cfg.ProviderChannels))
	for _, channelConfig := range cfg.ProviderChannels {
		account, ok := accounts[channelConfig.Account]
		if !ok {
			return nil, fmt.Errorf("provider channel %q references missing account %q", channelConfig.Code, channelConfig.Account)
		}

		channel := domain.ProviderChannel{
			Code:         channelConfig.Code,
			Account:      channelConfig.Account,
			Transport:    channelConfig.Transport,
			Enabled:      config.EnabledValue(channelConfig.Enabled),
			SenderDomain: channelConfig.SenderDomain,
			FromName:     channelConfig.FromName,
			From:         channelConfig.From,
			SMTP:         domainSMTPSettings(channelConfig.SMTP),
		}
		adapter, err := providerAdapterFor(account.Provider, channel.Transport, smtpTransport, resendAPI, brevoAPI)
		if err != nil {
			return nil, fmt.Errorf("provider channel %q: %w", channel.Code, err)
		}

		runtimes = append(runtimes, ProviderChannelRuntime{
			Account:  account,
			Channel:  channel,
			Provider: adapter,
		})
	}

	return NewStaticProviderResolver(runtimes...), nil
}

func providerAdapterFor(provider domain.Provider, transport domain.Transport, smtpTransport provideradapter.Provider, resendAPI provideradapter.Provider, brevoAPI provideradapter.Provider) (provideradapter.Provider, error) {
	switch {
	case provider == domain.ProviderMock && transport == domain.TransportAPI:
		mock := provideradapter.NewMockProvider()
		return mock, nil
	case provider == domain.ProviderResend && transport == domain.TransportAPI:
		return resendAPI, nil
	case provider == domain.ProviderBrevo && transport == domain.TransportAPI:
		return brevoAPI, nil
	case (provider == domain.ProviderResend || provider == domain.ProviderBrevo) && transport == domain.TransportSMTP:
		return smtpTransport, nil
	default:
		return nil, fmt.Errorf("provider %q with transport %q is not wired", provider, transport)
	}
}

func domainSMTPSettings(settings *config.SMTPConfig) *domain.SMTPSettings {
	if settings == nil {
		return nil
	}

	return &domain.SMTPSettings{
		Host:        settings.Host,
		Port:        settings.Port,
		Username:    settings.Username,
		PasswordRef: settings.PasswordRef,
	}
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}

	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}

	return copy
}

type configSecretResolverAdapter struct {
	resolver config.SecretResolver
}

func (a configSecretResolverAdapter) ResolveSecret(ref string) (string, error) {
	resolved, err := a.resolver.Resolve(ref)
	if err != nil {
		return "", err
	}

	return resolved.Value, nil
}

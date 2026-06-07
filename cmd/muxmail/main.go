package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/muxmail/muxmail"
	"github.com/muxmail/muxmail/internal/api"
	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	mailtemplate "github.com/muxmail/muxmail/internal/template"
	"github.com/muxmail/muxmail/internal/worker"
)

const helpText = `MuxMail

Usage:
  muxmail help
  muxmail version
  muxmail serve -c config.yaml
  muxmail config validate -c config.yaml [--strict]
  muxmail send dry-run -c config.yaml --app project_a --scene register_code --to user@example.com --locale en-US
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, helpText)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintf(stdout, "muxmail %s\n", muxmail.Version())
		return nil
	}

	if args[0] == "config" {
		return runConfigCommand(args[1:], stdout, stderr)
	}
	if args[0] == "serve" {
		return runServe(args[1:], stdout, stderr)
	}
	if args[0] == "send" {
		return runSendCommand(args[1:], stdout, stderr)
	}

	return fmt.Errorf("muxmail: command not implemented yet: %s", args[0])
}

func runServe(args []string, stdout io.Writer, stderr io.Writer) error {
	configPath, err := parseConfigPath(args, "serve")
	if err != nil {
		return err
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return err
	}

	secretResolver := config.NewSecretResolver()
	runtime, err := api.NewRuntime(cfg, secretResolver)
	if err != nil {
		return err
	}
	defer runtime.Close()

	providerResolver, err := worker.NewProviderResolverFromConfig(cfg, secretResolver)
	if err != nil {
		return err
	}
	workerRuntime, err := worker.New(worker.Config{
		Queue:                 runtime.Queue(),
		MessageLog:            runtime.MessageLog(),
		Stats:                 runtime.Stats(),
		ProviderResolver:      providerResolver,
		MaxAttemptsPerMessage: runtime.Defaults().MaxAttemptsPerMessage,
		RetryBackoffSeconds:   runtime.Defaults().RetryBackoffSeconds,
		ProviderTimeout:       time.Duration(runtime.Defaults().ProviderTimeoutSeconds) * time.Second,
		WorkerConcurrency:     runtime.Defaults().WorkerConcurrency,
		ErrorHandler: func(err error) {
			fmt.Fprintf(stderr, "worker error: %v\n", err)
		},
	})
	if err != nil {
		return err
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- workerRuntime.Run(ctx)
	}()

	fmt.Fprintf(stdout, "muxmail %s listening on %s\n", muxmail.Version(), cfg.Server.Listen)
	if err := runtime.Serve(ctx); err != nil {
		cancel()
		_ = runtime.Close()
		<-workerDone
		return err
	}
	cancel()
	if err := <-workerDone; err != nil {
		return err
	}

	return nil
}

func runConfigCommand(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("muxmail: missing config subcommand")
	}
	if args[0] != "validate" {
		return fmt.Errorf("muxmail: unknown config subcommand: %s", args[0])
	}

	return runConfigValidate(args[1:], stdout, stderr)
}

func runConfigValidate(args []string, stdout io.Writer, stderr io.Writer) error {
	configPath, strict, err := parseConfigValidateOptions(args)
	if err != nil {
		return err
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return err
	}

	report := config.ValidateWithOptions(cfg, config.NewSecretResolver(), config.ValidationOptions{
		StrictPlainSecrets: strict,
	})
	for _, warning := range report.Warnings {
		fmt.Fprintf(stderr, "warning %s %s: %s\n", warning.Code, warning.Path, warning.Message)
	}
	if report.HasErrors() {
		for _, validationError := range report.Errors {
			fmt.Fprintf(stderr, "error %s %s: %s\n", validationError.Code, validationError.Path, validationError.Message)
		}
		return report.Err()
	}

	fmt.Fprintln(stdout, "configuration valid")
	return nil
}

func parseConfigValidateOptions(args []string) (string, bool, error) {
	var configPath string
	var strict bool
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-c", "--config":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return "", false, err
			}
			configPath = value
			index = nextIndex
		case "--strict":
			strict = true
		default:
			return "", false, fmt.Errorf("muxmail: unexpected config validate argument: %s", arg)
		}
	}

	if configPath == "" {
		return "", false, fmt.Errorf("muxmail: config validate requires -c or --config")
	}

	return configPath, strict, nil
}

func runSendCommand(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("muxmail: missing send subcommand")
	}
	if args[0] != "dry-run" {
		return fmt.Errorf("muxmail: unknown send subcommand: %s", args[0])
	}

	return runSendDryRun(args[1:], stdout, stderr)
}

type dryRunOptions struct {
	configPath string
	appCode    string
	sceneCode  string
	to         string
	locale     string
	vars       map[string]any
}

type dryRunOutput struct {
	App              string   `json:"app"`
	Scene            string   `json:"scene"`
	Locale           string   `json:"locale"`
	Template         string   `json:"template"`
	ToDomain         string   `json:"to_domain"`
	SelectedChannels []string `json:"selected_channels"`
	SubjectPreview   string   `json:"subject_preview"`
	HTMLRendered     bool     `json:"html_rendered"`
	TextRendered     bool     `json:"text_rendered"`
}

func runSendDryRun(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseDryRunOptions(args)
	if err != nil {
		return err
	}

	cfg, err := config.LoadFile(options.configPath)
	if err != nil {
		return err
	}
	report := config.Validate(cfg, config.NewSecretResolver())
	for _, warning := range report.Warnings {
		fmt.Fprintf(stderr, "warning %s %s: %s\n", warning.Code, warning.Path, warning.Message)
	}
	if report.HasErrors() {
		for _, validationError := range report.Errors {
			fmt.Fprintf(stderr, "error %s %s: %s\n", validationError.Code, validationError.Path, validationError.Message)
		}
		return report.Err()
	}

	app, err := dryRunApp(cfg, options.appCode)
	if err != nil {
		return err
	}
	scene, err := dryRunScene(app, options.sceneCode)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]any{
		"scene":  options.sceneCode,
		"to":     options.to,
		"locale": options.locale,
		"vars":   options.vars,
	})
	if err != nil {
		return fmt.Errorf("muxmail: build dry-run request: %w", err)
	}
	request, err := domain.ValidateSendRequest(domain.SendRequestValidationInput{
		ContentType:    "application/json",
		IdempotencyKey: "dry-run",
		Body:           body,
		AllowedLocales: app.AllowedLocales,
	}, domain.SendRequestValidationOptions{
		MaxRequestBodyBytes: cfg.Defaults.MaxRequestBodyBytes,
		MaxTemplateVarBytes: cfg.Defaults.MaxTemplateVarBytes,
		MaxContextBytes:     cfg.Defaults.MaxContextBytes,
	})
	if err != nil {
		return err
	}

	rendered, err := mailtemplate.Render(app, scene, request)
	if err != nil {
		return err
	}
	selection, err := domain.SelectRoute(scene, request.NormalizedToEmail, cfg.Defaults.MaxAttemptsPerMessage)
	if err != nil {
		return err
	}

	output := dryRunOutput{
		App:              app.Code,
		Scene:            scene.Code,
		Locale:           rendered.Locale,
		Template:         rendered.TemplateCode,
		ToDomain:         selection.RecipientDomain,
		SelectedChannels: selection.Channels,
		SubjectPreview:   rendered.Subject,
		HTMLRendered:     rendered.HasHTML,
		TextRendered:     rendered.HasText,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func parseDryRunOptions(args []string) (dryRunOptions, error) {
	options := dryRunOptions{vars: map[string]any{}}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-c", "--config":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			options.configPath = value
			index = nextIndex
		case "--app":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			options.appCode = value
			index = nextIndex
		case "--scene":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			options.sceneCode = value
			index = nextIndex
		case "--to":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			options.to = value
			index = nextIndex
		case "--locale":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			options.locale = value
			index = nextIndex
		case "--var":
			value, nextIndex, err := nextOptionValue(args, index, arg)
			if err != nil {
				return dryRunOptions{}, err
			}
			name, varValue, ok := strings.Cut(value, "=")
			if !ok || name == "" {
				return dryRunOptions{}, fmt.Errorf("muxmail: --var must use key=value")
			}
			options.vars[name] = varValue
			index = nextIndex
		default:
			return dryRunOptions{}, fmt.Errorf("muxmail: unexpected send dry-run argument: %s", arg)
		}
	}

	if options.configPath == "" {
		return dryRunOptions{}, fmt.Errorf("muxmail: send dry-run requires -c or --config")
	}
	if options.appCode == "" {
		return dryRunOptions{}, fmt.Errorf("muxmail: send dry-run requires --app")
	}
	if options.sceneCode == "" {
		return dryRunOptions{}, fmt.Errorf("muxmail: send dry-run requires --scene")
	}
	if options.to == "" {
		return dryRunOptions{}, fmt.Errorf("muxmail: send dry-run requires --to")
	}

	return options, nil
}

func nextOptionValue(args []string, index int, name string) (string, int, error) {
	nextIndex := index + 1
	if nextIndex >= len(args) || args[nextIndex] == "" {
		return "", index, fmt.Errorf("muxmail: %s requires a value", name)
	}

	return args[nextIndex], nextIndex, nil
}

func dryRunApp(cfg *config.Config, appCode string) (domain.App, error) {
	for _, appConfig := range cfg.Apps {
		if appConfig.Code == appCode {
			return config.DomainAppFromConfig(appConfig, nil), nil
		}
	}

	return domain.App{}, fmt.Errorf("muxmail: app not found: %s", appCode)
}

func dryRunScene(app domain.App, sceneCode string) (domain.Scene, error) {
	for _, scene := range app.Scenes {
		if scene.Code != sceneCode {
			continue
		}
		if !scene.Enabled {
			return domain.Scene{}, fmt.Errorf("muxmail: scene disabled: %s", sceneCode)
		}

		return scene, nil
	}

	return domain.Scene{}, fmt.Errorf("muxmail: scene not found: %s", sceneCode)
}

func parseConfigPath(args []string, command string) (string, error) {
	var configPath string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-c", "--config":
			index++
			if index >= len(args) || args[index] == "" {
				return "", fmt.Errorf("muxmail: %s requires a value after -c or --config", command)
			}
			configPath = args[index]
		default:
			return "", fmt.Errorf("muxmail: unexpected %s argument: %s", command, arg)
		}
	}

	if configPath == "" {
		return "", fmt.Errorf("muxmail: %s requires -c or --config", command)
	}

	return configPath, nil
}

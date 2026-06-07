package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/muxmail/muxmail"
	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/lite"
)

const shutdownTimeout = 10 * time.Second

// Runtime owns the first-phase HTTP server and Lite infrastructure.
type Runtime struct {
	server     *http.Server
	messageLog *lite.MessageLog
	stats      lite.StatsSink
	queue      *lite.MemoryQueue
	rateLimit  *lite.FixedWindowRateLimiter
	idempotent *lite.IdempotencyCache
	suppressed *lite.SuppressionStore
	callerIP   callerIPResolver
	auth       *Authenticator
	webhook    webhookAuthenticator
	resendHook resendWebhookVerifier
	brevoHook  brevoWebhookVerifier
	defaults   config.DefaultsConfig
	now        func() time.Time
	ready      bool
	closeOnce  sync.Once
}

// RuntimeOption customizes runtime dependencies for tests and controlled embeddings.
type RuntimeOption func(*Runtime)

// NewRuntime validates configuration and initializes logging, stats, and queue dependencies.
func NewRuntime(cfg *config.Config, resolver config.SecretResolver, options ...RuntimeOption) (*Runtime, error) {
	if resolver == nil {
		resolver = config.NewSecretResolver()
	}
	report := config.Validate(cfg, resolver)
	if report.HasErrors() {
		return nil, report.Err()
	}

	authenticator, err := NewAuthenticator(cfg.Apps, resolver)
	if err != nil {
		return nil, fmt.Errorf("initialize authenticator: %w", err)
	}

	messageLog, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:           cfg.Logging.Dir,
		MaxBytes:      int64(cfg.Logging.MaxFileSizeMB) * 1024 * 1024,
		MaxBackups:    cfg.Logging.MaxBackups,
		EventsEnabled: cfg.Webhooks.Enabled,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize message log: %w", err)
	}

	stats, err := newStatsSink(cfg)
	if err != nil {
		messageLog.Close()
		return nil, err
	}

	queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{Capacity: cfg.Defaults.MemoryQueueSize})
	if err != nil {
		messageLog.Close()
		stats.Close()
		return nil, fmt.Errorf("initialize memory queue: %w", err)
	}

	idempotent, err := lite.NewIdempotencyCache(lite.IdempotencyCacheConfig{
		Capacity: cfg.Defaults.IdempotencyCacheSize,
		TTL:      time.Duration(cfg.Defaults.IdempotencyTTLHours) * time.Hour,
	})
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize idempotency cache: %w", err)
	}

	suppressed, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize suppression store: %w", err)
	}

	callerIP, err := newCallerIPResolver(cfg.Server.TrustedProxies)
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize caller IP resolver: %w", err)
	}

	webhook, err := newWebhookAuthenticator(cfg.Webhooks, resolver)
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize webhook authenticator: %w", err)
	}
	resendHook, err := newResendWebhookVerifier(cfg.Webhooks, resolver)
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize resend webhook verifier: %w", err)
	}
	brevoHook, err := newBrevoWebhookVerifier(cfg.Webhooks, resolver)
	if err != nil {
		messageLog.Close()
		stats.Close()
		queue.Close()
		return nil, fmt.Errorf("initialize brevo webhook verifier: %w", err)
	}

	runtime := &Runtime{
		messageLog: messageLog,
		stats:      stats,
		queue:      queue,
		rateLimit:  lite.NewFixedWindowRateLimiter(nil),
		idempotent: idempotent,
		suppressed: suppressed,
		callerIP:   callerIP,
		auth:       authenticator,
		webhook:    webhook,
		resendHook: resendHook,
		brevoHook:  brevoHook,
		defaults:   cfg.Defaults,
		now:        time.Now,
		ready:      true,
	}
	for _, option := range options {
		option(runtime)
	}
	runtime.server = &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           runtime.Handler(),
		ReadTimeout:       seconds(cfg.Server.ReadTimeoutSeconds),
		ReadHeaderTimeout: seconds(cfg.Server.ReadHeaderTimeoutSeconds),
		WriteTimeout:      seconds(cfg.Server.WriteTimeoutSeconds),
		IdleTimeout:       seconds(cfg.Server.IdleTimeoutSeconds),
	}

	return runtime, nil
}

// WithRateLimiter overrides the runtime rate limiter.
func WithRateLimiter(rateLimiter *lite.FixedWindowRateLimiter) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.rateLimit = rateLimiter
	}
}

// WithIdempotencyCache overrides the runtime idempotency cache.
func WithIdempotencyCache(cache *lite.IdempotencyCache) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.idempotent = cache
	}
}

// WithMemoryQueue overrides the runtime queue.
func WithMemoryQueue(queue *lite.MemoryQueue) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.queue = queue
	}
}

// WithSuppressionStore overrides the runtime suppression store.
func WithSuppressionStore(store *lite.SuppressionStore) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.suppressed = store
	}
}

// WithNow overrides the runtime clock for deterministic API tests.
func WithNow(now func() time.Time) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.now = now
		runtime.resendHook.now = now
	}
}

// Handler returns the HTTP handler used by the runtime server.
func (r *Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", r.handleHealthz)
	mux.HandleFunc("/readyz", r.handleReadyz)
	mux.HandleFunc("/version", r.handleVersion)
	mux.HandleFunc("/v1/mail/send", r.handleSend)
	mux.HandleFunc("/v1/mail/messages/failed", r.handleFailedMessageList)
	mux.HandleFunc("/v1/mail/messages", r.handleMessageList)
	mux.HandleFunc("/v1/mail/messages/", r.handleMessageRoutes)
	mux.HandleFunc("/v1/suppressions", r.handleSuppressionList)
	mux.HandleFunc("/v1/stats/summary", r.handleStatsSummary)
	mux.HandleFunc("/v1/provider-events", r.handleProviderEvent)
	mux.HandleFunc("/v1/provider-events/resend", r.handleResendProviderEvent)
	mux.HandleFunc("/v1/provider-events/brevo", r.handleBrevoProviderEvent)
	return mux
}

// Serve starts the HTTP server and blocks until the context is canceled or the server exits.
func (r *Runtime) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := r.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Shutdown gracefully stops HTTP traffic, discards pending queue tasks, and flushes logs.
func (r *Runtime) Shutdown(ctx context.Context) error {
	var firstErr error
	if r.server != nil {
		if err := r.server.Shutdown(ctx); err != nil {
			firstErr = err
		}
	}
	if err := r.Close(); firstErr == nil {
		firstErr = err
	}

	return firstErr
}

// Close releases Lite runtime resources.
func (r *Runtime) Close() error {
	var firstErr error
	r.closeOnce.Do(func() {
		r.ready = false
		if r.queue != nil {
			firstErr = r.queue.Close()
		}
		if r.messageLog != nil {
			if err := r.messageLog.Close(); firstErr == nil {
				firstErr = err
			}
		}
		if r.stats != nil {
			if err := r.stats.Close(); firstErr == nil {
				firstErr = err
			}
		}
	})

	return firstErr
}

// Queue exposes the initialized Lite queue for later API and worker wiring.
func (r *Runtime) Queue() *lite.MemoryQueue {
	return r.queue
}

// MessageLog exposes the initialized Lite message log for worker wiring.
func (r *Runtime) MessageLog() *lite.MessageLog {
	return r.messageLog
}

// Stats exposes the initialized stats sink for worker wiring.
func (r *Runtime) Stats() lite.StatsSink {
	return r.stats
}

// Authenticator exposes the initialized API key authenticator for handler wiring.
func (r *Runtime) Authenticator() *Authenticator {
	return r.auth
}

// Defaults exposes operational defaults needed by worker wiring.
func (r *Runtime) Defaults() config.DefaultsConfig {
	return r.defaults
}

func (r *Runtime) handleHealthz(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(w, request)
		return
	}

	writeStatusJSON(w, http.StatusOK, `{"status":"ok"}`)
}

func (r *Runtime) handleReadyz(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(w, request)
		return
	}
	if !r.ready {
		writeStatusJSON(w, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
		return
	}

	writeStatusJSON(w, http.StatusOK, `{"status":"ok"}`)
}

func (r *Runtime) handleVersion(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(w, request)
		return
	}

	writeStatusJSON(w, http.StatusOK, fmt.Sprintf(`{"version":%q}`, muxmail.Version()))
}

func newStatsSink(cfg *config.Config) (lite.StatsSink, error) {
	switch cfg.Runtime.Stats {
	case "off":
		return lite.NewNoopStatsSink(), nil
	case "file":
		stats, err := lite.NewFileStatsSink(lite.FileStatsSinkConfig{
			Dir:        cfg.Logging.Dir,
			MaxBytes:   int64(cfg.Logging.MaxFileSizeMB) * 1024 * 1024,
			MaxBackups: cfg.Logging.MaxBackups,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize stats sink: %w", err)
		}
		return stats, nil
	default:
		return nil, fmt.Errorf("unsupported stats mode: %s", cfg.Runtime.Stats)
	}
}

func writeStatusJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func seconds(value int) time.Duration {
	return time.Duration(value) * time.Second
}

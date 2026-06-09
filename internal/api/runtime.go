package api

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/muxmail/muxmail"
	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/lite"
)

const shutdownTimeout = 10 * time.Second

//go:embed admin_dist
var embeddedAdminDist embed.FS

// Runtime owns the first-phase HTTP server and Lite infrastructure.
type Runtime struct {
	server     *http.Server
	cfg        *config.Config
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
	eventMu    sync.Mutex
	ready      atomic.Bool
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

	runtime := &Runtime{
		cfg:      cfg,
		defaults: cfg.Defaults,
		now:      time.Now,
	}
	for _, option := range options {
		option(runtime)
	}
	if runtime.now == nil {
		runtime.now = time.Now
	}

	authenticator, err := NewAuthenticator(cfg.Apps, resolver)
	if err != nil {
		return nil, fmt.Errorf("initialize authenticator: %w", err)
	}
	runtime.auth = authenticator

	messageLog, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:           cfg.Logging.Dir,
		MaxBytes:      int64(cfg.Logging.MaxFileSizeMB) * 1024 * 1024,
		MaxBackups:    cfg.Logging.MaxBackups,
		EventsEnabled: cfg.Webhooks.Enabled,
		Now:           runtime.now,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize message log: %w", err)
	}
	runtime.messageLog = messageLog

	stats, err := newStatsSink(cfg, runtime.now)
	if err != nil {
		runtime.closeInitializedDependencies()
		return nil, err
	}
	runtime.stats = stats

	if runtime.queue == nil {
		queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{
			Capacity: cfg.Defaults.MemoryQueueSize,
			Now:      runtime.now,
		})
		if err != nil {
			runtime.closeInitializedDependencies()
			return nil, fmt.Errorf("initialize memory queue: %w", err)
		}
		runtime.queue = queue
	}

	if runtime.idempotent == nil {
		idempotent, err := lite.NewIdempotencyCache(lite.IdempotencyCacheConfig{
			Capacity: cfg.Defaults.IdempotencyCacheSize,
			TTL:      time.Duration(cfg.Defaults.IdempotencyTTLHours) * time.Hour,
			Now:      runtime.now,
		})
		if err != nil {
			runtime.closeInitializedDependencies()
			return nil, fmt.Errorf("initialize idempotency cache: %w", err)
		}
		runtime.idempotent = idempotent
	}

	if runtime.suppressed == nil {
		suppressed, err := lite.LoadSuppressionStore(cfg.SuppressionFile)
		if err != nil {
			runtime.closeInitializedDependencies()
			return nil, fmt.Errorf("initialize suppression store: %w", err)
		}
		runtime.suppressed = suppressed
	}

	callerIP, err := newCallerIPResolver(cfg.Server.TrustedProxies)
	if err != nil {
		runtime.closeInitializedDependencies()
		return nil, fmt.Errorf("initialize caller IP resolver: %w", err)
	}
	runtime.callerIP = callerIP

	webhook, err := newWebhookAuthenticator(cfg.Webhooks, resolver)
	if err != nil {
		runtime.closeInitializedDependencies()
		return nil, fmt.Errorf("initialize webhook authenticator: %w", err)
	}
	runtime.webhook = webhook
	resendHook, err := newResendWebhookVerifier(cfg.Webhooks, resolver)
	if err != nil {
		runtime.closeInitializedDependencies()
		return nil, fmt.Errorf("initialize resend webhook verifier: %w", err)
	}
	resendHook.now = runtime.now
	runtime.resendHook = resendHook
	brevoHook, err := newBrevoWebhookVerifier(cfg.Webhooks, resolver)
	if err != nil {
		runtime.closeInitializedDependencies()
		return nil, fmt.Errorf("initialize brevo webhook verifier: %w", err)
	}
	runtime.brevoHook = brevoHook

	if runtime.rateLimit == nil {
		runtime.rateLimit = lite.NewFixedWindowRateLimiter(runtime.now)
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
		if now == nil {
			return
		}
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
	mux.HandleFunc("/admin", r.handleAdminRedirect)
	mux.Handle("/admin/", r.adminHandler())
	mux.HandleFunc("/v1/admin/config-summary", r.handleAdminConfigSummary)
	mux.HandleFunc("/v1/mail/send", r.handleSend)
	mux.HandleFunc("/v1/mail/messages/failed", r.handleFailedMessageList)
	mux.HandleFunc("/v1/mail/messages", r.handleMessageList)
	mux.HandleFunc("/v1/mail/messages/", r.handleMessageRoutes)
	mux.HandleFunc("/v1/suppressions", r.handleSuppressionList)
	mux.HandleFunc("/v1/stats/summary", r.handleStatsSummary)
	mux.HandleFunc("/v1/provider-events", r.handleProviderEvent)
	mux.HandleFunc("/v1/provider-events/resend", r.handleResendProviderEvent)
	mux.HandleFunc("/v1/provider-events/brevo", r.handleBrevoProviderEvent)
	return rejectUnsafeAdminPaths(noStoreAPIResponses(mux))
}

func noStoreAPIResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v1/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		next.ServeHTTP(w, request)
	})
}

func rejectUnsafeAdminPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if isUnsafeAdminRequestPath(request.URL.Path) || isUnsafeAdminRequestPath(request.URL.EscapedPath()) {
			setAdminSecurityHeaders(w)
			http.NotFound(w, request)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func isUnsafeAdminRequestPath(path string) bool {
	if path != "/admin" && !strings.HasPrefix(path, "/admin/") {
		return false
	}
	if strings.ContainsAny(path, "\\:") || strings.Contains(path, "//") {
		return true
	}
	if strings.Contains(path, "%") {
		return true
	}

	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func (r *Runtime) handleAdminRedirect(w http.ResponseWriter, request *http.Request) {
	setAdminSecurityHeaders(w)
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.NotFound(w, request)
		return
	}

	http.Redirect(w, request, "/admin/", http.StatusMovedPermanently)
}

func (r *Runtime) adminHandler() http.Handler {
	return adminFileHandler(embeddedAdminDist)
}

// Serve starts the HTTP server and blocks until the context is canceled or the server exits.
// The caller remains responsible for closing Lite resources after background workers stop.
func (r *Runtime) Serve(ctx context.Context, readyCallbacks ...func(string)) error {
	if ctx == nil {
		ctx = context.Background()
	}

	listener, err := net.Listen("tcp", r.server.Addr)
	if err != nil {
		r.ready.Store(false)
		return err
	}
	r.ready.Store(true)
	defer r.ready.Store(false)
	for _, callback := range readyCallbacks {
		if callback != nil {
			callback(listener.Addr().String())
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.server.Serve(listener)
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
		if err := r.shutdownHTTP(shutdownCtx); err != nil {
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
	if ctx == nil {
		ctx = context.Background()
	}

	var firstErr error
	if err := r.shutdownHTTP(ctx); err != nil {
		firstErr = err
	}
	if err := r.Close(); firstErr == nil {
		firstErr = err
	}

	return firstErr
}

func (r *Runtime) shutdownHTTP(ctx context.Context) error {
	r.ready.Store(false)
	if r.server == nil {
		return nil
	}

	return r.server.Shutdown(ctx)
}

// Close releases Lite runtime resources.
func (r *Runtime) Close() error {
	var firstErr error
	r.closeOnce.Do(func() {
		r.ready.Store(false)
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

func (r *Runtime) closeInitializedDependencies() {
	if r == nil {
		return
	}
	_ = r.Close()
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
	if !r.ready.Load() {
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

func newStatsSink(cfg *config.Config, now func() time.Time) (lite.StatsSink, error) {
	switch cfg.Runtime.Stats {
	case "off":
		return lite.NewNoopStatsSink(), nil
	case "file":
		stats, err := lite.NewFileStatsSink(lite.FileStatsSinkConfig{
			Dir:        cfg.Logging.Dir,
			MaxBytes:   int64(cfg.Logging.MaxFileSizeMB) * 1024 * 1024,
			MaxBackups: cfg.Logging.MaxBackups,
			Now:        now,
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

package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	provideradapter "github.com/muxmail/muxmail/internal/provider"
)

const maxRetryAfterSeconds = 300

// MessageLog records message and attempt state transitions.
type MessageLog interface {
	AppendMessage(message domain.Message) error
	AppendAttempt(attempt domain.Attempt) error
}

// Queue provides the delayed task operations used by workers.
type Queue interface {
	Dequeue(ctx context.Context) (lite.QueueTask, error)
	EnqueueDelayed(task lite.QueueTask, delay time.Duration) error
}

// ProviderChannelRuntime binds one Provider Channel to its account and adapter.
type ProviderChannelRuntime struct {
	Account  domain.ProviderAccount
	Channel  domain.ProviderChannel
	Provider provideradapter.Provider
}

// ProviderResolver resolves Provider Channel codes into runtime send dependencies.
type ProviderResolver interface {
	Resolve(channelCode string) (ProviderChannelRuntime, bool)
}

// StaticProviderResolver resolves Provider Channel dependencies from an in-memory map.
type StaticProviderResolver struct {
	channels map[string]ProviderChannelRuntime
}

// NewStaticProviderResolver creates a static Provider Channel resolver.
func NewStaticProviderResolver(channels ...ProviderChannelRuntime) *StaticProviderResolver {
	resolver := &StaticProviderResolver{channels: make(map[string]ProviderChannelRuntime, len(channels))}
	for _, channel := range channels {
		resolver.channels[channel.Channel.Code] = channel
	}

	return resolver
}

// Resolve returns the runtime Provider Channel dependencies for channelCode.
func (r *StaticProviderResolver) Resolve(channelCode string) (ProviderChannelRuntime, bool) {
	if r == nil {
		return ProviderChannelRuntime{}, false
	}

	runtime, ok := r.channels[channelCode]
	return runtime, ok
}

// Config contains Worker runtime dependencies and retry policy.
type Config struct {
	Queue                 Queue
	MessageLog            MessageLog
	Stats                 lite.StatsSink
	ProviderResolver      ProviderResolver
	MaxAttemptsPerMessage int
	RetryBackoffSeconds   []int
	ProviderTimeout       time.Duration
	WorkerConcurrency     int
	Now                   func() time.Time
	ErrorHandler          func(error)
}

// Worker consumes queued messages and performs provider attempts.
type Worker struct {
	queue            Queue
	messageLog       MessageLog
	stats            lite.StatsSink
	resolver         ProviderResolver
	maxAttempts      int
	retryBackoff     []int
	providerTimeout  time.Duration
	concurrency      int
	now              func() time.Time
	handleAsyncError func(error)
}

// New creates a Worker with explicit Lite mode dependencies.
func New(config Config) (*Worker, error) {
	if config.Queue == nil {
		return nil, fmt.Errorf("worker queue is required")
	}
	if config.MessageLog == nil {
		return nil, fmt.Errorf("worker message log is required")
	}
	if config.Stats == nil {
		config.Stats = lite.NewNoopStatsSink()
	}
	if config.ProviderResolver == nil {
		return nil, fmt.Errorf("worker provider resolver is required")
	}
	if config.MaxAttemptsPerMessage <= 0 {
		return nil, fmt.Errorf("worker max attempts must be greater than 0")
	}
	if config.ProviderTimeout <= 0 {
		return nil, fmt.Errorf("worker provider timeout must be greater than 0")
	}
	if config.WorkerConcurrency <= 0 {
		config.WorkerConcurrency = 1
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(error) {}
	}

	return &Worker{
		queue:            config.Queue,
		messageLog:       config.MessageLog,
		stats:            config.Stats,
		resolver:         config.ProviderResolver,
		maxAttempts:      config.MaxAttemptsPerMessage,
		retryBackoff:     append([]int(nil), config.RetryBackoffSeconds...),
		providerTimeout:  config.ProviderTimeout,
		concurrency:      config.WorkerConcurrency,
		now:              config.Now,
		handleAsyncError: config.ErrorHandler,
	}, nil
}

// Run starts the worker goroutine pool and blocks until ctx is canceled or the queue closes.
func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan struct{}, w.concurrency)
	for index := 0; index < w.concurrency; index++ {
		go func() {
			defer func() { done <- struct{}{} }()
			w.runOne(ctx)
		}()
	}

	for index := 0; index < w.concurrency; index++ {
		<-done
	}

	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil
	}

	return ctx.Err()
}

// ProcessTask performs one queued attempt and schedules any required retry.
func (w *Worker) ProcessTask(ctx context.Context, task lite.QueueTask) error {
	if task.AttemptNo <= 0 {
		task.AttemptNo = 1
	}

	channelCode, ok := providerChannelForAttempt(task.Message, task.AttemptNo, w.maxAttempts)
	if !ok {
		return w.appendFinalFailure(task.Message, domain.ErrorCodeProviderUnavailable, "provider unavailable")
	}

	runtime, resolved := w.resolver.Resolve(channelCode)
	attempt, logAttempt := attemptBase(task.Message, task.AttemptNo, channelCode, runtime, resolved)
	logProviderStats := logAttempt && hasCompleteAttemptMetadata(attempt)
	w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusSending, "", ""))
	if logProviderStats {
		w.appendAttempt(attemptWithStatus(attempt, domain.AttemptStatusSending, domain.FailureClassNone, "", "", "", 0))
	}

	startedAt := w.now()
	result := w.sendThroughProvider(ctx, task, channelCode, runtime, resolved)
	durationMS := roundedDurationMS(w.now().Sub(startedAt))

	if result.IsAccepted() {
		if logProviderStats {
			w.appendAttempt(attemptWithStatus(attempt, domain.AttemptStatusSent, domain.FailureClassNone, "", "", result.Accepted.ProviderMessageID, durationMS))
		}
		w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusSent, "", ""))
		if logProviderStats {
			w.recordAttemptStats(task.Message, channelCode, runtime.Channel, lite.MetricAttemptsSent, durationMS)
		}
		w.recordMessageStat(task.Message, lite.MetricMessagesSent)
		return nil
	}

	failed := normalizedFailure(result)
	if logAttempt {
		w.appendAttempt(attemptWithStatus(attempt, domain.AttemptStatusFailed, failed.FailureClass, failed.ErrorCode, failed.ErrorMessage, "", durationMS))
		if logProviderStats {
			w.recordAttemptStats(task.Message, channelCode, runtime.Channel, lite.MetricAttemptsFailed, durationMS)
		}
	}

	if failed.FailureClass == domain.FailureClassMessagePermanent {
		w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusFailed, failed.ErrorCode, failed.ErrorMessage))
		w.recordMessageStat(task.Message, lite.MetricMessagesFailed)
		return nil
	}

	nextAttempt := task.AttemptNo + 1
	if !hasProviderChannelForAttempt(task.Message, nextAttempt, w.maxAttempts) {
		w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusFailed, domain.ErrorCodeProviderUnavailable, "provider unavailable"))
		w.recordMessageStat(task.Message, lite.MetricMessagesFailed)
		return nil
	}

	w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusRetrying, failed.ErrorCode, failed.ErrorMessage))
	delay := w.retryDelay(nextAttempt, result.RetryAfterSeconds)
	retryTask := task
	retryTask.AttemptNo = nextAttempt
	if err := w.queue.EnqueueDelayed(retryTask, delay); err != nil {
		w.appendMessage(messageWithStatus(task.Message, domain.MessageStatusFailed, retryScheduleFailureCode(err), "retry enqueue failed"))
		w.recordMessageStat(task.Message, lite.MetricMessagesFailed)
		return fmt.Errorf("enqueue retry attempt %d for message %s: %w", nextAttempt, task.Message.MessageID, err)
	}

	return nil
}

func (w *Worker) runOne(ctx context.Context) {
	for {
		task, err := w.queue.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, lite.ErrMemoryQueueClosed) {
				return
			}
			w.handleAsyncError(err)
			continue
		}
		if err := w.ProcessTask(ctx, task); err != nil {
			w.handleAsyncError(err)
		}
	}
}

func (w *Worker) sendThroughProvider(ctx context.Context, task lite.QueueTask, channelCode string, runtime ProviderChannelRuntime, resolved bool) provideradapter.SendResult {
	if !resolved ||
		runtime.Provider == nil ||
		!hasRuntimeAttemptMetadata(channelCode, runtime, resolved) ||
		!runtime.Channel.Enabled ||
		!runtime.Account.Enabled {
		return provideradapter.ChannelFailure(domain.ErrorCodeProviderUnavailable, "provider channel unavailable")
	}

	sendCtx, cancel := context.WithTimeout(ctx, w.providerTimeout)
	defer cancel()

	result, err := runtime.Provider.Send(sendCtx, provideradapter.SendRequest{
		Message: task.Message,
		Account: runtime.Account,
		Channel: runtime.Channel,
		Attempt: task.AttemptNo,
	})
	if err != nil {
		return provideradapter.TemporaryFailure(domain.ErrorCodeProviderUnavailable, "provider request failed")
	}

	return result
}

func (w *Worker) retryDelay(nextAttempt int, retryAfterSeconds int) time.Duration {
	backoffSeconds := 0
	backoffIndex := nextAttempt - 1
	if backoffIndex >= 0 && backoffIndex < len(w.retryBackoff) {
		backoffSeconds = w.retryBackoff[backoffIndex]
	}

	if retryAfterSeconds > maxRetryAfterSeconds {
		retryAfterSeconds = maxRetryAfterSeconds
	}
	delaySeconds := backoffSeconds
	if retryAfterSeconds > delaySeconds {
		delaySeconds = retryAfterSeconds
	}
	if delaySeconds > maxRetryAfterSeconds {
		delaySeconds = maxRetryAfterSeconds
	}

	return time.Duration(delaySeconds) * time.Second
}

func (w *Worker) appendFinalFailure(message domain.Message, code domain.ErrorCode, errorMessage string) error {
	w.appendMessage(messageWithStatus(message, domain.MessageStatusFailed, code, errorMessage))
	w.recordMessageStat(message, lite.MetricMessagesFailed)
	return nil
}

func (w *Worker) appendMessage(message domain.Message) {
	if err := w.messageLog.AppendMessage(message); err != nil {
		w.handleAsyncError(err)
	}
}

func (w *Worker) appendAttempt(attempt domain.Attempt) {
	if err := w.messageLog.AppendAttempt(attempt); err != nil {
		w.handleAsyncError(err)
	}
}

func (w *Worker) recordMessageStat(message domain.Message, metric string) {
	_ = w.stats.Record(lite.StatsRecord{
		AppCode:   message.AppCode,
		SceneCode: message.SceneCode,
		Metric:    metric,
		Value:     1,
	})
}

func (w *Worker) recordAttemptStats(message domain.Message, channelCode string, channel domain.ProviderChannel, metric string, durationMS int) {
	_ = w.stats.Record(lite.StatsRecord{
		AppCode:             message.AppCode,
		SceneCode:           message.SceneCode,
		ProviderChannelCode: channelCode,
		Transport:           channel.Transport,
		Metric:              metric,
		Value:               1,
	})
	_ = w.stats.Record(lite.StatsRecord{
		AppCode:             message.AppCode,
		SceneCode:           message.SceneCode,
		ProviderChannelCode: channelCode,
		Transport:           channel.Transport,
		Metric:              lite.MetricProviderDurationMS,
		Value:               float64(durationMS),
	})
}

func providerChannelForAttempt(message domain.Message, attemptNo int, maxAttempts int) (string, bool) {
	if attemptNo <= 0 || attemptNo > maxAttempts || attemptNo > len(message.ProviderChannels) {
		return "", false
	}

	channelCode := message.ProviderChannels[attemptNo-1]
	return channelCode, channelCode != ""
}

func hasProviderChannelForAttempt(message domain.Message, attemptNo int, maxAttempts int) bool {
	_, ok := providerChannelForAttempt(message, attemptNo, maxAttempts)
	return ok
}

func attemptBase(message domain.Message, attemptNo int, channelCode string, runtime ProviderChannelRuntime, resolved bool) (domain.Attempt, bool) {
	if channelCode == "" {
		return domain.Attempt{}, false
	}
	accountCode := runtime.Account.Code
	provider := runtime.Account.Provider
	transport := runtime.Channel.Transport
	if !hasRuntimeAttemptMetadata(channelCode, runtime, resolved) {
		accountCode = ""
		provider = ""
		transport = ""
	}

	return domain.Attempt{
		MessageID:           message.MessageID,
		AppCode:             message.AppCode,
		AttemptNo:           attemptNo,
		Provider:            provider,
		ProviderAccountCode: accountCode,
		ProviderChannelCode: channelCode,
		Transport:           transport,
	}, true
}

func hasCompleteAttemptMetadata(attempt domain.Attempt) bool {
	return attempt.Provider.IsValid() && attempt.ProviderAccountCode != "" && attempt.Transport.IsValid()
}

func hasRuntimeAttemptMetadata(channelCode string, runtime ProviderChannelRuntime, resolved bool) bool {
	return resolved &&
		channelCode != "" &&
		runtime.Account.Code != "" &&
		runtime.Channel.Code == channelCode &&
		runtime.Channel.Account == runtime.Account.Code &&
		runtime.Account.Provider.IsValid() &&
		runtime.Channel.Transport.IsValid()
}

func attemptWithStatus(attempt domain.Attempt, status domain.AttemptStatus, failureClass domain.FailureClass, errorCode domain.ErrorCode, errorMessage string, providerMessageID string, durationMS int) domain.Attempt {
	attempt.Status = status
	attempt.FailureClass = failureClass
	attempt.ErrorCode = errorCode
	attempt.ErrorMessage = safeErrorMessage(errorMessage)
	attempt.ProviderMessageID = providerMessageID
	attempt.DurationMS = durationMS
	return attempt
}

func messageWithStatus(message domain.Message, status domain.MessageStatus, errorCode domain.ErrorCode, errorMessage string) domain.Message {
	message.Status = status
	message.ErrorCode = errorCode
	message.ErrorMessage = safeErrorMessage(errorMessage)
	return message
}

func normalizedFailure(result provideradapter.SendResult) provideradapter.FailedResult {
	if result.Failed == nil || !result.Failed.FailureClass.IsValid() || result.Failed.FailureClass == domain.FailureClassNone {
		return provideradapter.FailedResult{
			FailureClass: domain.FailureClassTemporary,
			ErrorCode:    domain.ErrorCodeProviderUnavailable,
			ErrorMessage: "provider request failed",
		}
	}
	errorCode := result.Failed.ErrorCode
	if errorCode == "" {
		errorCode = domain.ErrorCodeProviderUnavailable
	}
	errorMessage := result.Failed.ErrorMessage
	if errorMessage == "" {
		errorMessage = "provider request failed"
	}

	return provideradapter.FailedResult{
		FailureClass: result.Failed.FailureClass,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
	}
}

func retryScheduleFailureCode(err error) domain.ErrorCode {
	var queueFull lite.QueueFullError
	if errors.As(err, &queueFull) {
		return domain.ErrorCodeQueueFull
	}

	return domain.ErrorCodeInternal
}

func roundedDurationMS(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}

	return int((duration + 500*time.Microsecond) / time.Millisecond)
}

func safeErrorMessage(message string) string {
	const maxBytes = 256
	message = strings.ToValidUTF8(message, "")
	if len(message) <= maxBytes {
		return message
	}

	end := maxBytes
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}

	return message[:end]
}

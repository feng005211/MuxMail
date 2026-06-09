package lite

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

const statsFilename = "mail-stats.jsonl"

const (
	// MetricMessagesQueued counts queued messages.
	MetricMessagesQueued = "messages_queued"
	// MetricMessagesSent counts messages accepted by a provider.
	MetricMessagesSent = "messages_sent"
	// MetricMessagesFailed counts messages that reached a final failure.
	MetricMessagesFailed = "messages_failed"
	// MetricAttemptsSent counts successful provider attempts.
	MetricAttemptsSent = "attempts_sent"
	// MetricAttemptsFailed counts failed provider attempts.
	MetricAttemptsFailed = "attempts_failed"
	// MetricRequestsRateLimited counts API requests rejected by rate limits.
	MetricRequestsRateLimited = "requests_rate_limited"
	// MetricRequestsQueueFull counts API requests rejected because the queue is full.
	MetricRequestsQueueFull = "requests_queue_full"
	// MetricRequestsIdempotentReplay counts idempotent replay requests.
	MetricRequestsIdempotentReplay = "requests_idempotent_replay"
	// MetricProviderEventsDelivered counts newly recorded provider delivered events.
	MetricProviderEventsDelivered = "provider_events_delivered"
	// MetricProviderEventsBounced counts newly recorded provider bounced events.
	MetricProviderEventsBounced = "provider_events_bounced"
	// MetricProviderEventsComplained counts newly recorded provider complained events.
	MetricProviderEventsComplained = "provider_events_complained"
	// MetricProviderDurationMS records provider attempt duration in milliseconds.
	MetricProviderDurationMS = "provider_duration_ms"
)

// StatsRecord is one Lite stats event.
type StatsRecord struct {
	Timestamp           time.Time
	AppCode             string
	SceneCode           string
	ProviderChannelCode string
	Transport           domain.Transport
	Metric              string
	Value               float64
}

// StatsSummary is an App-scoped aggregate view over Lite stats events.
type StatsSummary struct {
	AppCode           string
	Since             time.Time
	Until             time.Time
	Metrics           map[string]float64
	ProviderDurations map[string]ProviderDurationSummary
}

// ProviderDurationSummary aggregates provider duration samples for one channel.
type ProviderDurationSummary struct {
	ProviderChannelCode string
	Transport           domain.Transport
	Count               int
	TotalMS             float64
	AverageMS           float64
}

// StatsSink writes or discards Lite stats events.
type StatsSink interface {
	Record(record StatsRecord) error
	Summary(appCode string, since time.Time, until time.Time) (StatsSummary, error)
	Close() error
}

// NoopStatsSink discards all stats events.
type NoopStatsSink struct{}

// NewNoopStatsSink creates a stats sink for stats: off mode.
func NewNoopStatsSink() NoopStatsSink {
	return NoopStatsSink{}
}

// Record discards one stats event.
func (s NoopStatsSink) Record(record StatsRecord) error {
	return nil
}

// Summary returns an empty aggregate for stats: off mode.
func (s NoopStatsSink) Summary(appCode string, since time.Time, until time.Time) (StatsSummary, error) {
	return newStatsSummary(appCode, since, until), nil
}

// Close closes the no-op stats sink.
func (s NoopStatsSink) Close() error {
	return nil
}

// FileStatsSinkConfig contains file stats sink settings.
type FileStatsSinkConfig struct {
	Dir        string
	MaxBytes   int64
	MaxBackups int
	Now        func() time.Time
}

// FileStatsSink appends stats events to mail-stats.jsonl.
type FileStatsSink struct {
	writer *jsonlWriter
	now    func() time.Time
}

// NewFileStatsSink opens the file-backed stats sink.
func NewFileStatsSink(config FileStatsSinkConfig) (*FileStatsSink, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("log directory is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	writer, err := newJSONLWriter(filepath.Join(config.Dir, statsFilename), config.MaxBytes, config.MaxBackups)
	if err != nil {
		return nil, err
	}

	return &FileStatsSink{writer: writer, now: config.Now}, nil
}

// Record appends one stats event.
func (s *FileStatsSink) Record(record StatsRecord) error {
	if err := validateStatsRecord(record); err != nil {
		return err
	}

	timestamp := record.Timestamp
	if timestamp.IsZero() {
		timestamp = s.now()
	}

	return s.writer.appendLine(encodeStatsRecord(timestamp, record))
}

// Summary scans stats logs and aggregates events for one App and time window.
func (s *FileStatsSink) Summary(appCode string, since time.Time, until time.Time) (StatsSummary, error) {
	if s.writer == nil {
		return StatsSummary{}, fmt.Errorf("stats sink is closed")
	}

	s.writer.mu.Lock()
	defer s.writer.mu.Unlock()

	summary := newStatsSummary(appCode, since, until)
	for _, path := range s.statsQueryPaths() {
		if err := aggregateStatsPath(path, &summary); err != nil {
			return StatsSummary{}, err
		}
	}
	finalizeProviderDurations(&summary)

	return summary, nil
}

// Close flushes and closes the stats writer.
func (s *FileStatsSink) Close() error {
	if s.writer == nil {
		return nil
	}

	return s.writer.close()
}

func (s *FileStatsSink) statsQueryPaths() []string {
	paths := make([]string, 0, s.writer.maxBackups+1)
	for index := s.writer.maxBackups; index >= 1; index-- {
		paths = append(paths, fmt.Sprintf("%s.%d", s.writer.path, index))
	}
	paths = append(paths, s.writer.path)

	return paths
}

func encodeStatsRecord(ts time.Time, record StatsRecord) []byte {
	line := make([]byte, 0, 256)
	line = append(line, '{')
	appendJSONField(&line, "ts", ts.UTC().Format(time.RFC3339Nano), true)
	appendJSONField(&line, "app", record.AppCode, false)
	appendJSONField(&line, "scene", record.SceneCode, false)
	appendJSONField(&line, "provider_channel", record.ProviderChannelCode, false)
	appendJSONField(&line, "transport", string(record.Transport), false)
	appendJSONField(&line, "metric", record.Metric, false)
	appendJSONFloatField(&line, "value", record.Value, false)
	line = append(line, '}')

	return line
}

func newStatsSummary(appCode string, since time.Time, until time.Time) StatsSummary {
	return StatsSummary{
		AppCode:           appCode,
		Since:             since,
		Until:             until,
		Metrics:           make(map[string]float64),
		ProviderDurations: make(map[string]ProviderDurationSummary),
	}
}

func aggregateStatsPath(path string, summary *StatsSummary) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open stats log for query: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		record, err := decodeStatsRecord(scanner.Bytes())
		if err != nil {
			continue
		}
		if record.AppCode != summary.AppCode {
			continue
		}
		if record.Timestamp.Before(summary.Since) || !record.Timestamp.Before(summary.Until) {
			continue
		}
		if record.Metric == MetricProviderDurationMS {
			accumulateProviderDuration(summary, record)
			continue
		}
		summary.Metrics[record.Metric] += record.Value
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stats log: %w", err)
	}

	return nil
}

func decodeStatsRecord(line []byte) (StatsRecord, error) {
	var raw struct {
		Timestamp           string           `json:"ts"`
		AppCode             string           `json:"app"`
		SceneCode           string           `json:"scene"`
		ProviderChannelCode string           `json:"provider_channel"`
		Transport           domain.Transport `json:"transport"`
		Metric              string           `json:"metric"`
		Value               float64          `json:"value"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return StatsRecord{}, fmt.Errorf("decode stats log record: %w", err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if err != nil {
		return StatsRecord{}, fmt.Errorf("decode stats log timestamp: %w", err)
	}
	record := StatsRecord{
		Timestamp:           timestamp,
		AppCode:             raw.AppCode,
		SceneCode:           raw.SceneCode,
		ProviderChannelCode: raw.ProviderChannelCode,
		Transport:           raw.Transport,
		Metric:              raw.Metric,
		Value:               raw.Value,
	}
	if err := validateStatsRecord(record); err != nil {
		return StatsRecord{}, fmt.Errorf("decode stats log record: %w", err)
	}

	return record, nil
}

func accumulateProviderDuration(summary *StatsSummary, record StatsRecord) {
	if record.ProviderChannelCode == "" {
		return
	}
	duration := summary.ProviderDurations[record.ProviderChannelCode]
	duration.ProviderChannelCode = record.ProviderChannelCode
	duration.Transport = record.Transport
	duration.Count++
	duration.TotalMS += record.Value
	summary.ProviderDurations[record.ProviderChannelCode] = duration
}

func finalizeProviderDurations(summary *StatsSummary) {
	for channel, duration := range summary.ProviderDurations {
		if duration.Count > 0 {
			duration.AverageMS = duration.TotalMS / float64(duration.Count)
		}
		summary.ProviderDurations[channel] = duration
	}
}

func validateStatsRecord(record StatsRecord) error {
	if strings.TrimSpace(record.AppCode) == "" {
		return fmt.Errorf("stats record app is required")
	}
	if !isKnownStatsMetric(record.Metric) {
		return fmt.Errorf("stats record metric is invalid")
	}
	if math.IsNaN(record.Value) || math.IsInf(record.Value, 0) || record.Value < 0 {
		return fmt.Errorf("stats record value must be a finite non-negative number")
	}
	if record.Transport != "" && !record.Transport.IsValid() {
		return fmt.Errorf("stats record transport is invalid")
	}
	if isProviderAttemptStatsMetric(record.Metric) {
		if strings.TrimSpace(record.ProviderChannelCode) == "" {
			return fmt.Errorf("stats record provider channel is required")
		}
		if !record.Transport.IsValid() {
			return fmt.Errorf("stats record transport is required")
		}
	} else if strings.TrimSpace(record.ProviderChannelCode) != "" || record.Transport != "" {
		return fmt.Errorf("stats record provider fields are only allowed for provider attempt metrics")
	}

	return nil
}

func isKnownStatsMetric(metric string) bool {
	switch metric {
	case MetricMessagesQueued,
		MetricMessagesSent,
		MetricMessagesFailed,
		MetricAttemptsSent,
		MetricAttemptsFailed,
		MetricRequestsRateLimited,
		MetricRequestsQueueFull,
		MetricRequestsIdempotentReplay,
		MetricProviderEventsDelivered,
		MetricProviderEventsBounced,
		MetricProviderEventsComplained,
		MetricProviderDurationMS:
		return true
	default:
		return false
	}
}

func isProviderAttemptStatsMetric(metric string) bool {
	switch metric {
	case MetricAttemptsSent, MetricAttemptsFailed, MetricProviderDurationMS:
		return true
	default:
		return false
	}
}

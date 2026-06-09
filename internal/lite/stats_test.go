package lite

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestNoopStatsSinkDoesNotCreateStatsFile(t *testing.T) {
	dir := t.TempDir()
	sink := NewNoopStatsSink()

	if err := sink.Record(StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    MetricMessagesQueued,
		Value:     1,
	}); err != nil {
		t.Fatalf("record noop stat: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close noop stat sink: %v", err)
	}

	_, err := os.Stat(filepath.Join(dir, statsFilename))
	if !os.IsNotExist(err) {
		t.Fatalf("expected stats file to be absent, got %v", err)
	}
}

func TestFileStatsSinkWritesRequestLevelMetric(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	if err := sink.Record(StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    MetricMessagesQueued,
		Value:     1,
	}); err != nil {
		t.Fatalf("record stat: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, statsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","app":"project_a","scene":"register_code","provider_channel":"","transport":"","metric":"messages_queued","value":1}`
	if line != want {
		t.Fatalf("stats record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestFileStatsSinkRecordUsesExplicitTimestamp(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	explicit := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	if err := sink.Record(StatsRecord{
		Timestamp: explicit,
		AppCode:   "project_a",
		Metric:    MetricMessagesQueued,
		Value:     1,
	}); err != nil {
		t.Fatalf("record stat: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, statsFilename))
	if wantPrefix := `{"ts":"2026-05-29T01:02:03Z"`; len(line) < len(wantPrefix) || line[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("expected explicit timestamp, got %s", line)
	}
}

func TestFileStatsSinkWritesProviderDurationMetric(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	if err := sink.Record(StatsRecord{
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              MetricProviderDurationMS,
		Value:               42,
	}); err != nil {
		t.Fatalf("record stat: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, statsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","app":"project_a","scene":"register_code","provider_channel":"resend_auth_api","transport":"api","metric":"provider_duration_ms","value":42}`
	if line != want {
		t.Fatalf("stats record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestFileStatsSinkWritesProviderAttemptMetric(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	if err := sink.Record(StatsRecord{
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              MetricAttemptsSent,
		Value:               1,
	}); err != nil {
		t.Fatalf("record attempt stat: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, statsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","app":"project_a","scene":"register_code","provider_channel":"resend_auth_api","transport":"api","metric":"attempts_sent","value":1}`
	if line != want {
		t.Fatalf("stats record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestFileStatsSinkRejectsInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	tests := []StatsRecord{
		{Metric: MetricMessagesQueued, Value: 1},
		{AppCode: "project_a", Metric: "custom_metric", Value: 1},
		{AppCode: "project_a", Metric: MetricMessagesQueued, Value: -1},
		{AppCode: "project_a", Metric: MetricMessagesQueued, Value: math.NaN()},
		{AppCode: "project_a", Metric: MetricMessagesQueued, Value: math.Inf(1)},
		{AppCode: "project_a", Metric: MetricAttemptsSent, Value: 1},
		{AppCode: "project_a", ProviderChannelCode: "resend_auth_api", Metric: MetricMessagesQueued, Value: 1},
		{AppCode: "project_a", Transport: domain.TransportAPI, Metric: MetricMessagesQueued, Value: 1},
		{AppCode: "project_a", Metric: MetricProviderDurationMS, Value: 42},
		{
			AppCode:             "project_a",
			ProviderChannelCode: "resend_auth_api",
			Transport:           "http",
			Metric:              MetricProviderDurationMS,
			Value:               42,
		},
	}

	for index, record := range tests {
		if err := sink.Record(record); err == nil {
			t.Fatalf("case %d: expected invalid stats record to fail", index)
		}
	}

	info, err := os.Stat(filepath.Join(dir, statsFilename))
	if err != nil {
		t.Fatalf("stat stats file: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected rejected stats records not to be written, got size %d", info.Size())
	}
}

func TestNoopStatsSinkSummaryReturnsEmptyResult(t *testing.T) {
	sink := NewNoopStatsSink()
	since := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)

	summary, err := sink.Summary("project_a", since, until)
	if err != nil {
		t.Fatalf("summary noop stats: %v", err)
	}
	if summary.AppCode != "project_a" || !summary.Since.Equal(since) || !summary.Until.Equal(until) {
		t.Fatalf("unexpected summary identity: %+v", summary)
	}
	if len(summary.Metrics) != 0 || len(summary.ProviderDurations) != 0 {
		t.Fatalf("expected empty summary, got %+v", summary)
	}
}

func TestFileStatsSinkSummaryAggregatesAppWindow(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	writeStatsRecordAt(t, sink, time.Date(2026, 5, 27, 23, 59, 59, 0, time.UTC), StatsRecord{
		AppCode: "project_a",
		Metric:  MetricMessagesQueued,
		Value:   1,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    MetricMessagesQueued,
		Value:     1,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 4, 4, 5, 0, time.UTC), StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    MetricMessagesQueued,
		Value:     1,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 4, 34, 5, 0, time.UTC), StatsRecord{
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    MetricProviderEventsDelivered,
		Value:     1,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 5, 4, 5, 0, time.UTC), StatsRecord{
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              MetricProviderDurationMS,
		Value:               40,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 5, 4, 6, 0, time.UTC), StatsRecord{
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              MetricProviderDurationMS,
		Value:               60,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 5, 4, 7, 0, time.UTC), StatsRecord{
		AppCode: "project_b",
		Metric:  MetricMessagesQueued,
		Value:   99,
	})

	since := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	summary, err := sink.Summary("project_a", since, until)
	if err != nil {
		t.Fatalf("summary stats: %v", err)
	}

	if got := summary.Metrics[MetricMessagesQueued]; got != 2 {
		t.Fatalf("expected queued metric 2, got %g in %+v", got, summary.Metrics)
	}
	if got := summary.Metrics[MetricProviderEventsDelivered]; got != 1 {
		t.Fatalf("expected delivered event metric 1, got %g in %+v", got, summary.Metrics)
	}
	duration := summary.ProviderDurations["resend_auth_api"]
	if duration.Count != 2 || duration.TotalMS != 100 || duration.AverageMS != 50 {
		t.Fatalf("unexpected provider duration summary: %+v", duration)
	}
}

func TestFileStatsSinkSummarySkipsMalformedRecords(t *testing.T) {
	dir := t.TempDir()
	sink := openTestStatsSink(t, dir)
	defer sink.Close()

	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), StatsRecord{
		AppCode: "project_a",
		Metric:  MetricMessagesQueued,
		Value:   1,
	})
	if err := sink.writer.appendLine([]byte(`{"ts":"not-a-time","app":"project_a","metric":"messages_queued","value":99}`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	if err := sink.writer.appendLine([]byte(`not-json`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	if err := sink.writer.appendLine([]byte(`{"ts":"2026-05-28T03:04:06Z","app":"project_a","provider_channel":"broken","transport":"http","metric":"provider_duration_ms","value":99}`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	if err := sink.writer.appendLine([]byte(`{"ts":"2026-05-28T03:04:06Z","app":"project_a","metric":"custom_metric","value":99}`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	if err := sink.writer.appendLine([]byte(`{"ts":"2026-05-28T03:04:06Z","app":"project_a","metric":"messages_queued","value":-99}`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	if err := sink.writer.appendLine([]byte(`{"ts":"2026-05-28T03:04:06Z","app":"project_a","provider_channel":"","transport":"api","metric":"provider_duration_ms","value":99}`)); err != nil {
		t.Fatalf("write malformed stats record: %v", err)
	}
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 3, 4, 6, 0, time.UTC), StatsRecord{
		AppCode: "project_a",
		Metric:  MetricMessagesQueued,
		Value:   2,
	})
	writeStatsRecordAt(t, sink, time.Date(2026, 5, 28, 3, 4, 7, 0, time.UTC), StatsRecord{
		AppCode:             "project_a",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              MetricProviderDurationMS,
		Value:               10,
	})

	summary, err := sink.Summary(
		"project_a",
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("summary stats: %v", err)
	}
	if got := summary.Metrics[MetricMessagesQueued]; got != 3 {
		t.Fatalf("expected malformed records to be skipped, got queued metric %g", got)
	}
	if _, exists := summary.Metrics["custom_metric"]; exists {
		t.Fatalf("expected unknown metric to be skipped, got %+v", summary.Metrics)
	}
	if _, exists := summary.ProviderDurations["broken"]; exists {
		t.Fatalf("expected invalid provider duration transport to be skipped, got %+v", summary.ProviderDurations)
	}
	if got := summary.ProviderDurations["resend_auth_api"].Count; got != 1 {
		t.Fatalf("expected valid provider duration to remain, got %+v", summary.ProviderDurations)
	}
}

func openTestStatsSink(t *testing.T, dir string) *FileStatsSink {
	t.Helper()

	sink, err := NewFileStatsSink(FileStatsSinkConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now: func() time.Time {
			return time.Date(2026, 5, 28, 3, 4, 5, 123456789, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("open stats sink: %v", err)
	}

	return sink
}

func writeStatsRecordAt(t *testing.T, sink *FileStatsSink, ts time.Time, record StatsRecord) {
	t.Helper()

	if err := sink.writer.appendLine(encodeStatsRecord(ts, record)); err != nil {
		t.Fatalf("write stats record: %v", err)
	}
}

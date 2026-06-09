package lite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestMessageLogAppendMessageRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, messagesFilename))
	wantPrefix := `{"ts":"2026-05-28T03:04:05.123456789Z","request_id":"req_01ABC","business_request_id":"biz_123","message_id":"msg_01ABC","app":"project_a","api_key_name":"default","scene":"register_code","to_domain":"example.com","to_hash":"`
	if !strings.HasPrefix(line, wantPrefix) {
		t.Fatalf("message field order mismatch:\n%s", line)
	}
	if strings.Contains(line, `"to_email"`) ||
		strings.Contains(line, "user@example.com") ||
		strings.Contains(line, "Your code is 123456") ||
		strings.Contains(line, "<p>123456</p>") ||
		strings.Contains(line, "mk_live") {
		t.Fatalf("message record leaked sensitive value: %s", line)
	}
	if strings.Contains(line, "null") {
		t.Fatalf("message record contains null: %s", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("message record must be valid JSON: %v", err)
	}
	if decoded["status"] != string(domain.MessageStatusQueued) {
		t.Fatalf("expected queued status, got %v", decoded["status"])
	}
}

func TestMessageLogAppendAttemptRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	attempt := domain.Attempt{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ErrorCode:           "",
		ProviderMessageID:   "provider_123",
		DurationMS:          42,
	}
	if err := log.AppendAttempt(attempt); err != nil {
		t.Fatalf("append attempt: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, attemptsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","attempt_no":1,"provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","transport":"api","status":"sent","failure_class":"","error_code":"","error_message":"","provider_message_id":"provider_123","duration_ms":42}`
	if line != want {
		t.Fatalf("attempt record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestMessageLogAppendProviderEventRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := log.AppendProviderEvent(event); err != nil {
		t.Fatalf("append provider event: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, eventsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`
	if line != want {
		t.Fatalf("event record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestMessageLogAppendProviderEventFailsWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	err := log.AppendProviderEvent(domain.ProviderEvent{
		MessageID: "msg_01ABC",
		AppCode:   "project_a",
		EventType: domain.ProviderEventDelivered,
	})
	if err == nil {
		t.Fatal("expected disabled event log to fail")
	}
	if _, statErr := os.Stat(filepath.Join(dir, eventsFilename)); !os.IsNotExist(statErr) {
		t.Fatalf("expected event log file to be absent, got %v", statErr)
	}
}

func TestMessageLogHasProviderEvent(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := log.AppendProviderEvent(event); err != nil {
		t.Fatalf("append provider event: %v", err)
	}

	found, err := log.HasProviderEvent(event)
	if err != nil {
		t.Fatalf("check provider event duplicate: %v", err)
	}
	if !found {
		t.Fatal("expected provider event duplicate check to hit")
	}
}

func TestMessageLogAppendProviderEventOnceIsAtomic(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	appended := make(chan bool, 16)
	errs := make(chan error, 16)
	for index := 0; index < cap(appended); index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ok, err := log.AppendProviderEventOnce(event)
			if err != nil {
				errs <- err
				return
			}
			appended <- ok
		}()
	}
	close(start)
	workers.Wait()
	close(appended)
	close(errs)

	for err := range errs {
		t.Fatalf("append provider event once: %v", err)
	}
	appendedCount := 0
	for ok := range appended {
		if ok {
			appendedCount++
		}
	}
	if appendedCount != 1 {
		t.Fatalf("expected exactly one append, got %d", appendedCount)
	}
	events := readJSONLLines(t, filepath.Join(dir, eventsFilename))
	if len(events) != 1 {
		t.Fatalf("expected one provider event record, got %d", len(events))
	}
}

func TestMessageLogHasProviderEventReturnsErrorOnMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := os.WriteFile(filepath.Join(dir, eventsFilename), []byte("{bad json}\n"), filePerm); err != nil {
		t.Fatalf("write provider event log: %v", err)
	}

	_, err := log.HasProviderEvent(event)
	if err == nil {
		t.Fatal("expected malformed provider event log record to fail")
	}
	if !strings.Contains(err.Error(), "decode provider event log record") {
		t.Fatalf("expected provider event decode error, got %v", err)
	}
}

func TestMessageLogHasProviderEventReturnsErrorOnInvalidAuditFields(t *testing.T) {
	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}

	tests := []struct {
		name      string
		line      string
		wantError string
	}{
		{
			name:      "invalid timestamp",
			line:      `{"ts":"not-a-time","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`,
			wantError: "decode provider event log timestamp",
		},
		{
			name:      "missing provider message id",
			line:      `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`,
			wantError: "required fields are invalid",
		},
		{
			name:      "invalid occurred at",
			line:      `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28 03:00:00"}`,
			wantError: "required fields are invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			log := openTestMessageLogWithEvents(t, dir, 1<<20)
			defer log.Close()

			if err := os.WriteFile(filepath.Join(dir, eventsFilename), []byte(tt.line+"\n"), filePerm); err != nil {
				t.Fatalf("write provider event log: %v", err)
			}

			_, err := log.HasProviderEvent(event)
			if err == nil {
				t.Fatal("expected invalid provider event audit record to fail")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestMessageLogListProviderEvents(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	first := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	second := first
	second.Provider = domain.ProviderBrevo
	second.ProviderAccountCode = "brevo_main"
	second.ProviderChannelCode = "brevo_auth_api"
	second.ProviderMessageID = "provider_456"
	second.EventType = domain.ProviderEventBounced
	second.OccurredAt = "2026-05-28T03:10:00Z"
	if err := log.AppendProviderEvent(first); err != nil {
		t.Fatalf("append first provider event: %v", err)
	}
	if err := log.AppendProviderEvent(second); err != nil {
		t.Fatalf("append second provider event: %v", err)
	}

	events, err := log.ListProviderEvents("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("list provider events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two provider events, got %+v", events)
	}
	if events[0].EventType != domain.ProviderEventDelivered || events[1].EventType != domain.ProviderEventBounced {
		t.Fatalf("expected event order to be preserved, got %+v", events)
	}
	if events[0].Provider != domain.ProviderResend || events[1].Provider != domain.ProviderBrevo {
		t.Fatalf("unexpected provider event metadata: %+v", events)
	}
}

func TestMessageLogListProviderEventsReturnsErrorOnInvalidAuditFields(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	line := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, eventsFilename), []byte(line+"\n"), filePerm); err != nil {
		t.Fatalf("write provider event log: %v", err)
	}

	_, err := log.ListProviderEvents("project_a", "msg_01ABC")
	if err == nil {
		t.Fatal("expected invalid provider event audit record to fail")
	}
	if !strings.Contains(err.Error(), "required fields are invalid") {
		t.Fatalf("expected provider event required field error, got %v", err)
	}
}

func TestMessageLogListRecentProviderEvents(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	first := domain.ProviderEvent{
		MessageID:           "msg_01AAA",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_111",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	second := domain.ProviderEvent{
		MessageID:           "msg_01BBB",
		AppCode:             "project_a",
		Provider:            domain.ProviderBrevo,
		ProviderAccountCode: "brevo_main",
		ProviderChannelCode: "brevo_auth_api",
		ProviderMessageID:   "provider_222",
		EventType:           domain.ProviderEventBounced,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:10:00Z",
	}
	other := second
	other.AppCode = "project_b"
	other.ProviderMessageID = "provider_333"
	if err := log.AppendProviderEvent(first); err != nil {
		t.Fatalf("append first provider event: %v", err)
	}
	if err := log.AppendProviderEvent(second); err != nil {
		t.Fatalf("append second provider event: %v", err)
	}
	if err := log.AppendProviderEvent(other); err != nil {
		t.Fatalf("append other app provider event: %v", err)
	}

	events, err := log.ListRecentProviderEvents("project_a", ProviderEventListFilter{
		Limit:     1,
		Provider:  domain.ProviderBrevo,
		EventType: domain.ProviderEventBounced,
	})
	if err != nil {
		t.Fatalf("list recent provider events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one filtered event, got %+v", events)
	}
	if events[0].MessageID != "msg_01BBB" || events[0].Provider != domain.ProviderBrevo || events[0].EventType != domain.ProviderEventBounced {
		t.Fatalf("unexpected filtered event: %+v", events[0])
	}
}

func TestMessageLogListRecentProviderEventsReturnsErrorOnMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	validLine := encodeProviderEventRecord(time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), event)
	data := append([]byte("{bad json}\n"), append(validLine, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, eventsFilename), data, filePerm); err != nil {
		t.Fatalf("write provider event log: %v", err)
	}

	_, err := log.ListRecentProviderEvents("project_a", ProviderEventListFilter{Limit: 10})
	if err == nil {
		t.Fatal("expected malformed provider event log record to fail")
	}
	if !strings.Contains(err.Error(), "decode provider event log record") {
		t.Fatalf("expected provider event decode error, got %v", err)
	}
}

func TestMessageLogListAttempts(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := domain.Attempt{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSending,
		FailureClass:        domain.FailureClassNone,
	}
	second := first
	second.Status = domain.AttemptStatusSent
	second.ProviderMessageID = "provider_123"
	second.DurationMS = 42
	if err := log.AppendAttempt(first); err != nil {
		t.Fatalf("append first attempt record: %v", err)
	}
	if err := log.AppendAttempt(second); err != nil {
		t.Fatalf("append second attempt record: %v", err)
	}

	attempts, err := log.ListAttempts("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected two attempt records, got %+v", attempts)
	}
	if attempts[0].Status != domain.AttemptStatusSending || attempts[1].Status != domain.AttemptStatusSent {
		t.Fatalf("expected attempt order to be preserved, got %+v", attempts)
	}
	if attempts[1].ProviderMessageID != "provider_123" || attempts[1].DurationMS != 42 {
		t.Fatalf("unexpected attempt metadata: %+v", attempts[1])
	}
}

func TestMessageLogListAttemptsPreservesSafeErrorMessage(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	failed := domain.Attempt{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusFailed,
		FailureClass:        domain.FailureClassTemporary,
		ErrorCode:           domain.ErrorCodeProviderUnavailable,
		ErrorMessage:        "provider request failed",
		DurationMS:          42,
	}
	if err := log.AppendAttempt(failed); err != nil {
		t.Fatalf("append failed attempt record: %v", err)
	}

	attempts, err := log.ListAttempts("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected one attempt record, got %+v", attempts)
	}
	if attempts[0].ErrorMessage != "provider request failed" {
		t.Fatalf("expected failed attempt error message, got %+v", attempts[0])
	}
}

func TestMessageLogListAttemptsAllowsUnresolvedChannelFailure(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	failed := domain.Attempt{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		AttemptNo:           1,
		ProviderChannelCode: "missing_primary",
		Status:              domain.AttemptStatusFailed,
		FailureClass:        domain.FailureClassChannel,
		ErrorCode:           domain.ErrorCodeProviderUnavailable,
		ErrorMessage:        "provider channel unavailable",
	}
	if err := log.AppendAttempt(failed); err != nil {
		t.Fatalf("append unresolved channel attempt: %v", err)
	}

	attempts, err := log.ListAttempts("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected one attempt record, got %+v", attempts)
	}
	if attempts[0].Provider != "" || attempts[0].Transport != "" || attempts[0].ProviderChannelCode != "missing_primary" {
		t.Fatalf("unexpected unresolved channel metadata: %+v", attempts[0])
	}
}

func TestMessageLogListAttemptsRejectsIncompleteProviderIdentity(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "sent attempt missing account",
			line: `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","attempt_no":1,"provider":"resend","provider_account":"","provider_channel":"resend_auth_api","transport":"api","status":"sent","failure_class":"","error_code":"","error_message":"","provider_message_id":"provider_123","duration_ms":42}`,
		},
		{
			name: "sent attempt missing channel",
			line: `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","attempt_no":1,"provider":"resend","provider_account":"resend_main","provider_channel":"","transport":"api","status":"sent","failure_class":"","error_code":"","error_message":"","provider_message_id":"provider_123","duration_ms":42}`,
		},
		{
			name: "unresolved channel failure missing selected channel",
			line: `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","attempt_no":1,"provider":"","provider_account":"","provider_channel":"","transport":"","status":"failed","failure_class":"channel_failure","error_code":"provider_unavailable","error_message":"provider channel unavailable","provider_message_id":"","duration_ms":0}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			log := openTestMessageLog(t, dir, 1<<20)
			defer log.Close()

			if err := os.WriteFile(filepath.Join(dir, attemptsFilename), []byte(tt.line+"\n"), filePerm); err != nil {
				t.Fatalf("write attempt log: %v", err)
			}

			_, err := log.ListAttempts("project_a", "msg_01ABC")
			if err == nil {
				t.Fatal("expected incomplete provider identity to fail")
			}
			if !strings.Contains(err.Error(), "required fields are invalid") {
				t.Fatalf("expected required field error, got %v", err)
			}
		})
	}
}

func TestMessageLogListAttemptsIsAppScoped(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := domain.Attempt{
		MessageID:           "msg_shared",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_a",
	}
	second := first
	second.AppCode = "project_b"
	second.ProviderMessageID = "provider_b"
	if err := log.AppendAttempt(first); err != nil {
		t.Fatalf("append first app attempt: %v", err)
	}
	if err := log.AppendAttempt(second); err != nil {
		t.Fatalf("append second app attempt: %v", err)
	}

	attempts, err := log.ListAttempts("project_a", "msg_shared")
	if err != nil {
		t.Fatalf("list app-scoped attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].ProviderMessageID != "provider_a" {
		t.Fatalf("expected only project_a attempt, got %+v", attempts)
	}
}

func TestMessageLogListAttemptsIgnoresLegacyRecordsWithoutApp(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	legacyLine := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_legacy","attempt_no":1,"provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","transport":"api","status":"sent","failure_class":"","error_code":"","provider_message_id":"provider_legacy","duration_ms":42}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, attemptsFilename), []byte(legacyLine), 0o600); err != nil {
		t.Fatalf("write legacy attempt log: %v", err)
	}

	attempts, err := log.ListAttempts("project_a", "msg_legacy")
	if err != nil {
		t.Fatalf("list attempts with legacy records: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("expected legacy app-less attempts to be ignored, got %+v", attempts)
	}
}

func TestMessageLogListAttemptsReturnsErrorOnMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	attempt := domain.Attempt{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ProviderMessageID:   "provider_123",
	}
	validLine := encodeAttemptRecord(time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), attempt)
	data := append([]byte("{bad json}\n"), append(validLine, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, attemptsFilename), data, filePerm); err != nil {
		t.Fatalf("write attempt log: %v", err)
	}

	_, err := log.ListAttempts("project_a", "msg_01ABC")
	if err == nil {
		t.Fatal("expected malformed attempt log record to fail")
	}
	if !strings.Contains(err.Error(), "decode attempt log record") {
		t.Fatalf("expected attempt decode error, got %v", err)
	}
}

func TestMessageLogListLatestMessages(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := testMessage()
	first.MessageID = "msg_01AAA"
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusQueued
	second := testMessage()
	second.MessageID = "msg_01BBB"
	second.SceneCode = "reset_password"
	second.Status = domain.MessageStatusFailed
	if err := log.AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{})
	if err != nil {
		t.Fatalf("list latest messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected two latest messages, got %+v", messages)
	}
	if messages[0].MessageID != "msg_01BBB" || messages[1].MessageID != "msg_01AAA" {
		t.Fatalf("expected latest messages to be sorted descending, got %+v", messages)
	}
}

func TestMessageLogListLatestMessagesReturnsErrorOnMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	validLine := encodeMessageRecord(time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), testMessage())
	data := append([]byte("{bad json}\n"), append(validLine, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, messagesFilename), data, filePerm); err != nil {
		t.Fatalf("write message log: %v", err)
	}

	_, err := log.ListLatestMessages("project_a", MessageListFilter{})
	if err == nil {
		t.Fatal("expected malformed message log record to fail")
	}
	if !strings.Contains(err.Error(), "decode message log record") {
		t.Fatalf("expected message decode error, got %v", err)
	}
}

func TestMessageLogListLatestMessagesIgnoresLegacyRecordsWithoutApp(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	legacyLine := `{"ts":"2026-05-28T03:04:05.123456789Z","request_id":"req_legacy","message_id":"msg_legacy","api_key_name":"default","scene":"register_code","to_domain":"example.com","to_hash":"hash","locale":"en-US","status":"queued","error_code":"","error_message":""}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, messagesFilename), []byte(legacyLine), 0o600); err != nil {
		t.Fatalf("write legacy message log: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{})
	if err != nil {
		t.Fatalf("list latest messages with legacy records: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected app-less legacy messages to be ignored, got %+v", messages)
	}
}

func TestMessageLogListLatestMessagesFiltersAndLimits(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := testMessage()
	first.MessageID = "msg_01AAA"
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusQueued
	second := testMessage()
	second.MessageID = "msg_01BBB"
	second.SceneCode = "register_code"
	second.Status = domain.MessageStatusFailed
	third := testMessage()
	third.MessageID = "msg_01CCC"
	third.SceneCode = "reset_password"
	third.Status = domain.MessageStatusFailed
	if err := log.AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	if err := log.AppendMessage(third); err != nil {
		t.Fatalf("append third message: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{
		Limit:  1,
		Status: domain.MessageStatusFailed,
		Scene:  "register_code",
	})
	if err != nil {
		t.Fatalf("list filtered latest messages: %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "msg_01BBB" {
		t.Fatalf("expected one filtered message, got %+v", messages)
	}
}

func TestMessageLogListLatestMessagesUsesAppendOrderForSameMessage(t *testing.T) {
	dir := t.TempDir()
	times := []time.Time{
		time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC),
		time.Date(2026, 5, 28, 3, 4, 4, 0, time.UTC),
	}
	index := 0
	log, err := NewMessageLog(MessageLogConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now: func() time.Time {
			now := times[index]
			if index < len(times)-1 {
				index++
			}
			return now
		},
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}
	defer log.Close()

	message := testMessage()
	message.Status = domain.MessageStatusSent
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}
	message.Status = domain.MessageStatusDelivered
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append delivered message: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{})
	if err != nil {
		t.Fatalf("list latest messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one latest message, got %+v", messages)
	}
	if messages[0].Status != domain.MessageStatusDelivered {
		t.Fatalf("expected append-latest delivered status, got %+v", messages[0])
	}
}

func TestMessageLogRotatesBySize(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 650)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, messagesFilename+".1")); err != nil {
		t.Fatalf("expected rotated backup: %v", err)
	}
	current := readSingleLine(t, filepath.Join(dir, messagesFilename))
	backup := readSingleLine(t, filepath.Join(dir, messagesFilename+".1"))
	if current == "" || backup == "" {
		t.Fatalf("expected current and backup records")
	}
}

func TestMessageLogFindLatestMessage(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append queued message: %v", err)
	}
	message.Status = domain.MessageStatusSent
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	snapshot, found, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if !found {
		t.Fatal("expected message to be found")
	}
	if snapshot.Status != domain.MessageStatusSent {
		t.Fatalf("expected latest sent status, got %+v", snapshot)
	}
	if snapshot.ToDomain != "example.com" || snapshot.ToHash == "" || snapshot.BusinessRequestID != "biz_123" {
		t.Fatalf("expected safe message fields, got %+v", snapshot)
	}
}

func TestMessageLogFindLatestMessageIsAppScoped(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append message: %v", err)
	}

	_, found, err := log.FindLatestMessage("project_b", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if found {
		t.Fatal("expected app-scoped lookup to hide other app message")
	}
}

func TestMessageLogFindLatestMessageScansRotatedBackups(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 650)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	other := testMessage()
	other.MessageID = "msg_other"
	if err := log.AppendMessage(other); err != nil {
		t.Fatalf("append rotating message: %v", err)
	}

	snapshot, found, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if !found {
		t.Fatal("expected message from rotated backup to be found")
	}
	if snapshot.MessageID != "msg_01ABC" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestMessageLogFindLatestMessageReturnsErrorOnMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	validLine := encodeMessageRecord(time.Date(2026, 5, 28, 3, 4, 5, 0, time.UTC), testMessage())
	data := append([]byte("{bad json}\n"), append(validLine, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, messagesFilename), data, filePerm); err != nil {
		t.Fatalf("write message log: %v", err)
	}

	_, _, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err == nil {
		t.Fatal("expected malformed message log record to fail")
	}
	if !strings.Contains(err.Error(), "decode message log record") {
		t.Fatalf("expected message decode error, got %v", err)
	}
}

func TestMessageLogFindLatestMessageIgnoresLegacyRecordsWithoutApp(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	legacyLine := `{"ts":"2026-05-28T03:04:05.123456789Z","request_id":"req_legacy","message_id":"msg_legacy","api_key_name":"default","scene":"register_code","to_domain":"example.com","to_hash":"hash","locale":"en-US","status":"queued","error_code":"","error_message":""}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, messagesFilename), []byte(legacyLine), 0o600); err != nil {
		t.Fatalf("write legacy message log: %v", err)
	}

	_, found, err := log.FindLatestMessage("project_a", "msg_legacy")
	if err != nil {
		t.Fatalf("find latest message with legacy records: %v", err)
	}
	if found {
		t.Fatal("expected app-less legacy message not to be visible to app-scoped lookup")
	}
}

func TestMessageLogFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows permission bits do not reliably reflect POSIX modes")
	}

	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append message: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != directoryPerm {
		t.Fatalf("expected directory perm %o, got %o", directoryPerm, got)
	}

	fileInfo, err := os.Stat(filepath.Join(dir, messagesFilename))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != filePerm {
		t.Fatalf("expected file perm %o, got %o", filePerm, got)
	}
}

func openTestMessageLog(t *testing.T, dir string, maxBytes int64) *MessageLog {
	t.Helper()

	return openTestMessageLogConfigured(t, dir, maxBytes, false)
}

func openTestMessageLogWithEvents(t *testing.T, dir string, maxBytes int64) *MessageLog {
	t.Helper()

	return openTestMessageLogConfigured(t, dir, maxBytes, true)
}

func openTestMessageLogConfigured(t *testing.T, dir string, maxBytes int64, eventsEnabled bool) *MessageLog {
	t.Helper()

	log, err := NewMessageLog(MessageLogConfig{
		Dir:           dir,
		MaxBytes:      maxBytes,
		MaxBackups:    2,
		EventsEnabled: eventsEnabled,
		Now: func() time.Time {
			return time.Date(2026, 5, 28, 3, 4, 5, 123456789, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}

	return log
}

func testMessage() domain.Message {
	normalizedEmail := domain.NormalizeEmail("user@example.com")
	return domain.Message{
		RequestID:          "req_01ABC",
		BusinessRequestID:  "biz_123",
		MessageID:          "msg_01ABC",
		AppCode:            "project_a",
		APIKeyName:         "default",
		SceneCode:          "register_code",
		ToEmail:            "user@example.com",
		NormalizedToEmail:  normalizedEmail,
		ToDomain:           "example.com",
		ToHash:             domain.ToHash("project_a", normalizedEmail),
		Locale:             "en-US",
		Subject:            "Your code is 123456",
		HTMLBody:           "<p>123456</p>",
		TextBody:           "123456",
		Status:             domain.MessageStatusQueued,
		IdempotencyHash:    domain.IdempotencyHash("project_a", "register_code", "idem-123"),
		RequestFingerprint: "fingerprint_123",
		CallerIP:           "127.0.0.1",
		UserIP:             "1.2.3.4",
		UserIDHash:         domain.UserIDHash("project_a", "10001"),
		ErrorCode:          "",
		ErrorMessage:       "",
	}
}

func readSingleLine(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one line in %s, got %d: %q", path, len(lines), string(data))
	}

	return lines[0]
}

func readJSONLLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl %s: %v", path, err)
	}
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(rawLines))
	for _, line := range rawLines {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode jsonl line %q: %v", line, err)
		}
		records = append(records, record)
	}

	return records
}
